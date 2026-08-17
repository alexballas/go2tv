package utils

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/h2non/filetype"
)

var (
	ErrInvalidSeekFlag    = errors.New("invalid seek flag")
	ErrInvalidClockFormat = errors.New("invalid clock format")
)

// DLNAContentFeaturesOptions describes only capabilities that are true for
// the bytes served by the HTTP endpoint. Profile names and FLAGS are omitted
// unless a future classifier can verify them from the actual media stream.
type DLNAContentFeaturesOptions struct {
	TimeSeek  bool
	ByteSeek  bool
	Converted bool
}

// BuildDLNAContentFeatures builds contentFeatures.dlna.org for a resource.
func BuildDLNAContentFeatures(opts DLNAContentFeaturesOptions) string {
	features := make([]string, 0, 2)
	switch {
	case opts.TimeSeek && opts.ByteSeek:
		features = append(features, "DLNA.ORG_OP=11")
	case opts.TimeSeek:
		features = append(features, "DLNA.ORG_OP=10")
	case opts.ByteSeek:
		features = append(features, "DLNA.ORG_OP=01")
	}
	if opts.Converted {
		features = append(features, "DLNA.ORG_CI=1")
	} else {
		features = append(features, "DLNA.ORG_CI=0")
	}
	return strings.Join(features, ";")
}

// DLNAResourceMediaType returns the MIME type of the bytes on the wire.
func DLNAResourceMediaType(sourceMediaType string, transcode bool) string {
	mediaType := strings.TrimSpace(sourceMediaType)
	if transcode && strings.HasPrefix(strings.ToLower(mediaType), "video/") {
		return "video/mpeg"
	}
	return mediaType
}

// DLNATransferMode selects the UPnP transfer mode for a media class.
func DLNATransferMode(mediaType string) string {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if strings.HasPrefix(mediaType, "audio/") || strings.HasPrefix(mediaType, "video/") {
		return "Streaming"
	}
	return "Interactive"
}

// BuildContentFeatures is kept for callers that still pass the legacy seek
// string. It no longer infers profiles or emits OP=00/FLAGS.
// Deprecated: use BuildDLNAContentFeatures.
func BuildContentFeatures(_ string, seek string, transcode bool) (string, error) {
	opts := DLNAContentFeaturesOptions{Converted: transcode}
	switch seek {
	case "00":
	case "01":
		opts.ByteSeek = true
	case "10":
		opts.TimeSeek = true
	case "11":
		opts.TimeSeek = true
		opts.ByteSeek = true
	default:
		return "", ErrInvalidSeekFlag
	}
	return BuildDLNAContentFeatures(opts), nil
}

// GetMimeDetailsFromPath returns the media mime details from a local file path.
func GetMimeDetailsFromPath(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("getMimeDetailsFromPath: %w", err)
	}
	defer f.Close()

	head := make([]byte, 261)
	read, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", fmt.Errorf("getMimeDetailsFromPath error #2: %w", err)
	}

	kind, err := filetype.Match(head[:read])
	if err != nil {
		return "", fmt.Errorf("getMimeDetailsFromPath error #3: %w", err)
	}

	return fmt.Sprintf("%s/%s", kind.MIME.Type, kind.MIME.Subtype), nil
}

// GetMimeDetailsFromBytes returns the media mime details from a byte slice.
func GetMimeDetailsFromBytes(data []byte) (string, error) {
	kind, err := filetype.Match(data)
	if err != nil {
		return "", fmt.Errorf("getMimeDetailsFromBytes error: %w", err)
	}

	return fmt.Sprintf("%s/%s", kind.MIME.Type, kind.MIME.Subtype), nil
}

// GetMimeDetailsFromStream returns the media URL mime details.
func GetMimeDetailsFromStream(s io.ReadCloser) (string, error) {
	defer s.Close()

	// The whole header has to be collected before matching. A single Read is free
	// to return less than it was asked for, and the streams behind an Android
	// content:// URI do exactly that when the provider serves them over a pipe -
	// which is what an app sharing a file to us typically does. A short header
	// matches nothing, and the unknown type that comes out is worse than useless:
	// it is passed on to the renderer as the media's own type.
	head := make([]byte, 261)
	read, err := io.ReadFull(s, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", fmt.Errorf("getMimeDetailsFromStream error: %w", err)
	}

	kind, err := filetype.Match(head[:read])
	if err != nil {
		return "", fmt.Errorf("getMimeDetailsFromStream error  #2: %w", err)
	}

	return fmt.Sprintf("%s/%s", kind.MIME.Type, kind.MIME.Subtype), nil
}

// ClockTimeToSeconds converts relative time to seconds.
func ClockTimeToSeconds(strtime string) (int, error) {
	var out int
	v := make([]int, 0, 3)

	s := strings.Split(strtime, ":")
	if len(s) != 3 {
		return 0, ErrInvalidClockFormat
	}

	num, err := strconv.Atoi(s[0])
	if err != nil {
		return 0, ErrInvalidClockFormat
	}
	v = append(v, num)

	num, err = strconv.Atoi(s[1])
	if err != nil {
		return 0, ErrInvalidClockFormat
	}
	v = append(v, num)

	f, err := strconv.ParseFloat(s[2], 32)
	if err != nil {
		return 0, ErrInvalidClockFormat
	}
	f = math.Round(f)
	v = append(v, int(f))

	for n, i := range v {
		switch n {
		case 0:
			out += i * 3600
		case 1:
			out += i * 60
		case 2:
			out += i
		}
	}

	return out, nil
}

// SecondsToClockTime converts seconds to seconds relative time.
func SecondsToClockTime(secs int) string {
	hours := secs / 3600
	secs %= 3600
	minutes := secs / 60
	secs %= 60

	str := fmt.Sprintf("%02d:%02d:%02d", hours, minutes, secs)
	return str
}

// FormatClockTime converts clock time to a more expected format of clock time.
func FormatClockTime(strtime string) (string, error) {
	sec, err := ClockTimeToSeconds(strtime)
	if err != nil {
		return "", ErrInvalidClockFormat
	}

	return SecondsToClockTime(sec), nil
}
