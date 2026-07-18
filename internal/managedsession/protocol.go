// Package managedsession defines the bounded line-framed protocol spoken over
// the anonymous stdin/stdout pipes between the desktop GUI parent and a
// GUI-managed -server child. It must stay free of Fyne and network transports.
package managedsession

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

const (
	ProtocolVersion = 1

	// ParentMarker prefixes frames written by the GUI parent to child stdin.
	ParentMarker = "GO2TV_PARENT"
	// ManagedMarker prefixes frames written by the managed child to stdout.
	ManagedMarker = "GO2TV_MANAGED"

	// MaxFrameBytes bounds one framed line, marker included.
	MaxFrameBytes = 256 * 1024
	// MaxDevices bounds the device count in one discovery frame.
	MaxDevices = 128
)

// Parent frame types.
const (
	TypeDiscoverySnapshot      = "discovery.snapshot"
	TypeDiscoveryRefreshResult = "discovery.refresh_result"
)

// Managed (child) frame types.
const (
	TypeReady            = "ready"
	TypeDiscoveryRefresh = "discovery.refresh"
)

// Stable refresh error codes; never raw network or device data.
const (
	RefreshErrorTimeout     = "refresh_timeout"
	RefreshErrorUnavailable = "refresh_unavailable"
)

var (
	ErrFrameTooLarge   = errors.New("managed frame exceeds size bound")
	ErrInvalidFrame    = errors.New("invalid managed frame")
	ErrInvalidDevice   = errors.New("invalid managed device")
	ErrTooManyDevices  = errors.New("too many managed devices")
	ErrUnexpectedFrame = errors.New("unexpected managed frame type")
)

// Device carries the only renderer fields allowed on the wire. Endpoint is
// transport-only inside the child and must never reach WebUI DTOs or logs.
type Device struct {
	Name      string `json:"name"`
	Protocol  string `json:"protocol"`
	Endpoint  string `json:"endpoint"`
	AudioOnly bool   `json:"audio_only"`
}

// ParentFrame is one control/discovery frame from the GUI parent.
type ParentFrame struct {
	ProtocolVersion int      `json:"protocol_version"`
	Type            string   `json:"type"`
	RequestID       string   `json:"request_id,omitempty"`
	Revision        uint64   `json:"revision"`
	Devices         []Device `json:"devices"`
	ErrorCode       string   `json:"error_code,omitempty"`
}

// ManagedFrame is one event frame from the managed child.
type ManagedFrame struct {
	ProtocolVersion int    `json:"protocol_version"`
	Type            string `json:"type"`
	RequestID       string `json:"request_id,omitempty"`
	URL             string `json:"url,omitempty"`
}

// ValidateDevices enforces the wire device rules shared by both directions.
func ValidateDevices(devices []Device) error {
	if len(devices) > MaxDevices {
		return fmt.Errorf("%w: %d", ErrTooManyDevices, len(devices))
	}
	seen := make(map[string]struct{}, len(devices))
	for _, device := range devices {
		if device.Protocol != "DLNA" && device.Protocol != "Chromecast" {
			return fmt.Errorf("%w: protocol %q", ErrInvalidDevice, device.Protocol)
		}
		if device.Name == "" {
			return fmt.Errorf("%w: empty name", ErrInvalidDevice)
		}
		u, err := url.Parse(device.Endpoint)
		if err != nil || u.Scheme != "http" || u.Host == "" {
			return fmt.Errorf("%w: bad endpoint", ErrInvalidDevice)
		}
		key := device.Protocol + "\x00" + device.Endpoint
		if _, dup := seen[key]; dup {
			return fmt.Errorf("%w: duplicate endpoint", ErrInvalidDevice)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (f ParentFrame) validate() error {
	if f.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("%w: protocol version %d", ErrInvalidFrame, f.ProtocolVersion)
	}
	switch f.Type {
	case TypeDiscoverySnapshot:
		if f.RequestID != "" || f.ErrorCode != "" {
			return fmt.Errorf("%w: snapshot with request fields", ErrInvalidFrame)
		}
	case TypeDiscoveryRefreshResult:
		if f.RequestID == "" {
			return fmt.Errorf("%w: refresh result without request_id", ErrInvalidFrame)
		}
	default:
		return fmt.Errorf("%w: %q", ErrUnexpectedFrame, f.Type)
	}
	return ValidateDevices(f.Devices)
}

func (f ManagedFrame) validate() error {
	if f.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("%w: protocol version %d", ErrInvalidFrame, f.ProtocolVersion)
	}
	switch f.Type {
	case TypeReady:
		if f.URL == "" || f.RequestID != "" {
			return fmt.Errorf("%w: malformed ready frame", ErrInvalidFrame)
		}
	case TypeDiscoveryRefresh:
		if f.RequestID == "" || f.URL != "" {
			return fmt.Errorf("%w: refresh without request_id", ErrInvalidFrame)
		}
	default:
		return fmt.Errorf("%w: %q", ErrUnexpectedFrame, f.Type)
	}
	return nil
}

// EncodeParentFrame renders one marker-prefixed, newline-terminated frame.
func EncodeParentFrame(frame ParentFrame) ([]byte, error) {
	frame.ProtocolVersion = ProtocolVersion
	if frame.Devices == nil {
		frame.Devices = []Device{}
	}
	if err := frame.validate(); err != nil {
		return nil, err
	}
	return encodeFrame(ParentMarker, frame)
}

// EncodeManagedFrame renders one marker-prefixed, newline-terminated frame.
func EncodeManagedFrame(frame ManagedFrame) ([]byte, error) {
	frame.ProtocolVersion = ProtocolVersion
	if err := frame.validate(); err != nil {
		return nil, err
	}
	return encodeFrame(ManagedMarker, frame)
}

func encodeFrame(marker string, payload any) ([]byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	line := make([]byte, 0, len(marker)+len(encoded)+2)
	line = append(line, marker...)
	line = append(line, ' ')
	line = append(line, encoded...)
	line = append(line, '\n')
	if len(line) > MaxFrameBytes {
		return nil, ErrFrameTooLarge
	}
	return line, nil
}

// DecodeParentFrame parses one line that must be a parent frame.
func DecodeParentFrame(line []byte) (ParentFrame, error) {
	payload, ok := framePayload(line, ParentMarker)
	if !ok {
		return ParentFrame{}, fmt.Errorf("%w: missing parent marker", ErrInvalidFrame)
	}
	var frame ParentFrame
	if err := strictUnmarshal(payload, &frame); err != nil {
		return ParentFrame{}, err
	}
	if err := frame.validate(); err != nil {
		return ParentFrame{}, err
	}
	return frame, nil
}

// DecodeManagedLine classifies one child stdout line. When the line does not
// start with the managed marker it is a human log line and isFrame is false.
func DecodeManagedLine(line []byte) (frame ManagedFrame, isFrame bool, err error) {
	payload, ok := framePayload(line, ManagedMarker)
	if !ok {
		return ManagedFrame{}, false, nil
	}
	if err := strictUnmarshal(payload, &frame); err != nil {
		return ManagedFrame{}, true, err
	}
	if err := frame.validate(); err != nil {
		return ManagedFrame{}, true, err
	}
	return frame, true, nil
}

func framePayload(line []byte, marker string) ([]byte, bool) {
	if !bytes.HasPrefix(line, []byte(marker+" ")) {
		return nil, false
	}
	return line[len(marker)+1:], true
}

func strictUnmarshal(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidFrame, sanitizeJSONError(err))
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("%w: trailing data", ErrInvalidFrame)
	}
	return nil
}

func sanitizeJSONError(err error) string {
	text := err.Error()
	if len(text) > 120 {
		text = text[:120]
	}
	return strings.ToValidUTF8(text, "")
}

// LineReader yields newline-delimited lines with the frame size bound applied.
// It accepts both \n and \r\n endings and fails on oversized lines.
type LineReader struct {
	scanner *bufio.Scanner
}

func NewLineReader(r io.Reader) *LineReader {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), MaxFrameBytes)
	return &LineReader{scanner: scanner}
}

// ReadLine returns the next line without its terminator, io.EOF at end of
// stream, or ErrFrameTooLarge when a line exceeds the bound.
func (r *LineReader) ReadLine() ([]byte, error) {
	if !r.scanner.Scan() {
		if err := r.scanner.Err(); err != nil {
			if errors.Is(err, bufio.ErrTooLong) {
				return nil, ErrFrameTooLarge
			}
			return nil, err
		}
		return nil, io.EOF
	}
	line := r.scanner.Bytes()
	line = bytes.TrimSuffix(line, []byte("\r"))
	return line, nil
}
