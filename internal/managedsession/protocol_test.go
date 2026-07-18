package managedsession

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestParentFrameRoundTrip(t *testing.T) {
	t.Parallel()
	line, err := EncodeParentFrame(ParentFrame{
		Type:     TypeDiscoverySnapshot,
		Revision: 7,
		Devices: []Device{{
			Name: "TV", Protocol: "DLNA",
			Endpoint: "http://192.168.1.20:1400/device.xml",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(line, []byte("GO2TV_PARENT ")) || !bytes.HasSuffix(line, []byte("\n")) {
		t.Fatalf("frame line = %q", line)
	}
	frame, err := DecodeParentFrame(bytes.TrimSuffix(line, []byte("\n")))
	if err != nil {
		t.Fatal(err)
	}
	if frame.Revision != 7 || len(frame.Devices) != 1 || frame.Devices[0].Name != "TV" {
		t.Fatalf("frame = %+v", frame)
	}
}

func TestParentFrameEmptySnapshotValid(t *testing.T) {
	t.Parallel()
	line, err := EncodeParentFrame(ParentFrame{Type: TypeDiscoverySnapshot, Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := DecodeParentFrame(bytes.TrimSuffix(line, []byte("\n")))
	if err != nil {
		t.Fatal(err)
	}
	if len(frame.Devices) != 0 {
		t.Fatalf("devices = %v, want empty", frame.Devices)
	}
}

func TestDecodeParentFrameRejections(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		line string
	}{
		{"missing marker", `{"protocol_version":1,"type":"discovery.snapshot","revision":1,"devices":[]}`},
		{"wrong version", `GO2TV_PARENT {"protocol_version":2,"type":"discovery.snapshot","revision":1,"devices":[]}`},
		{"unknown type", `GO2TV_PARENT {"protocol_version":1,"type":"discovery.evil","revision":1,"devices":[]}`},
		{"unknown field", `GO2TV_PARENT {"protocol_version":1,"type":"discovery.snapshot","revision":1,"devices":[],"pid":42}`},
		{"trailing data", `GO2TV_PARENT {"protocol_version":1,"type":"discovery.snapshot","revision":1,"devices":[]} {}`},
		{"bad device protocol", `GO2TV_PARENT {"protocol_version":1,"type":"discovery.snapshot","revision":1,"devices":[{"name":"x","protocol":"UPNP","endpoint":"http://a:1/x","audio_only":false}]}`},
		{"bad endpoint", `GO2TV_PARENT {"protocol_version":1,"type":"discovery.snapshot","revision":1,"devices":[{"name":"x","protocol":"DLNA","endpoint":"file:///etc/passwd","audio_only":false}]}`},
		{"duplicate endpoint", `GO2TV_PARENT {"protocol_version":1,"type":"discovery.snapshot","revision":1,"devices":[{"name":"a","protocol":"DLNA","endpoint":"http://a:1/x","audio_only":false},{"name":"b","protocol":"DLNA","endpoint":"http://a:1/x","audio_only":true}]}`},
		{"refresh result without id", `GO2TV_PARENT {"protocol_version":1,"type":"discovery.refresh_result","revision":1,"devices":[]}`},
		{"not json", `GO2TV_PARENT hello`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeParentFrame([]byte(tt.line)); err == nil {
				t.Fatalf("DecodeParentFrame(%q) succeeded, want error", tt.line)
			}
		})
	}
}

func TestValidateDevicesLimit(t *testing.T) {
	t.Parallel()
	devices := make([]Device, MaxDevices+1)
	for i := range devices {
		devices[i] = Device{Name: "d", Protocol: "DLNA", Endpoint: "http://192.0.2.1:1400/" + strings.Repeat("a", i%7+1)}
	}
	devices = devices[:MaxDevices+1]
	// Ensure endpoints are unique so only the limit triggers.
	for i := range devices {
		devices[i].Endpoint = "http://192.0.2.1:1400/dev" + string(rune('a'+i%26)) + strings.Repeat("x", i/26)
	}
	if err := ValidateDevices(devices); !errors.Is(err, ErrTooManyDevices) {
		t.Fatalf("ValidateDevices error = %v, want ErrTooManyDevices", err)
	}
}

func TestManagedLineClassification(t *testing.T) {
	t.Parallel()
	line, err := EncodeManagedFrame(ManagedFrame{Type: TypeReady, URL: "http://127.0.0.1:9666/"})
	if err != nil {
		t.Fatal(err)
	}
	frame, isFrame, err := DecodeManagedLine(bytes.TrimSuffix(line, []byte("\n")))
	if err != nil || !isFrame || frame.URL != "http://127.0.0.1:9666/" {
		t.Fatalf("frame = %+v isFrame=%v err=%v", frame, isFrame, err)
	}

	_, isFrame, err = DecodeManagedLine([]byte("[2026-07-15 10:00:00] INFO  Web server listening"))
	if err != nil || isFrame {
		t.Fatalf("log line classified as frame err=%v", err)
	}

	// Marker mid-line must not be treated as a frame.
	_, isFrame, err = DecodeManagedLine([]byte("device name GO2TV_MANAGED {}"))
	if err != nil || isFrame {
		t.Fatalf("mid-line marker classified as frame err=%v", err)
	}

	// Marker at line start with malformed payload is a frame error.
	_, isFrame, err = DecodeManagedLine([]byte(`GO2TV_MANAGED {"protocol_version":1,"type":"ready"}`))
	if !isFrame || err == nil {
		t.Fatalf("malformed ready: isFrame=%v err=%v", isFrame, err)
	}
}

func TestManagedFrameRefreshValidation(t *testing.T) {
	t.Parallel()
	if _, err := EncodeManagedFrame(ManagedFrame{Type: TypeDiscoveryRefresh}); err == nil {
		t.Fatal("refresh without request_id encoded")
	}
	line, err := EncodeManagedFrame(ManagedFrame{Type: TypeDiscoveryRefresh, RequestID: "4"})
	if err != nil {
		t.Fatal(err)
	}
	frame, isFrame, err := DecodeManagedLine(bytes.TrimSuffix(line, []byte("\n")))
	if err != nil || !isFrame || frame.RequestID != "4" {
		t.Fatalf("frame = %+v err=%v", frame, err)
	}
}

func TestLineReaderCRLFAndBounds(t *testing.T) {
	t.Parallel()
	input := "first\r\nsecond\n"
	reader := NewLineReader(strings.NewReader(input))
	line, err := reader.ReadLine()
	if err != nil || string(line) != "first" {
		t.Fatalf("line = %q err = %v", line, err)
	}
	line, err = reader.ReadLine()
	if err != nil || string(line) != "second" {
		t.Fatalf("line = %q err = %v", line, err)
	}
	if _, err = reader.ReadLine(); err != io.EOF {
		t.Fatalf("err = %v, want io.EOF", err)
	}

	oversized := strings.Repeat("a", MaxFrameBytes+10) + "\n"
	reader = NewLineReader(strings.NewReader(oversized))
	if _, err := reader.ReadLine(); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversized line err = %v, want ErrFrameTooLarge", err)
	}
}
