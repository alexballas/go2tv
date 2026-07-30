package application_test

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/mock"
	"go2tv.app/go2tv/v2/castprotocol/v2/application"
	"go2tv.app/go2tv/v2/castprotocol/v2/cast"
	mockCast "go2tv.app/go2tv/v2/castprotocol/v2/cast/mocks"
	pb "go2tv.app/go2tv/v2/castprotocol/v2/cast/proto"
)

var (
	mockAddr = "foo.bar"
	mockPort = 42
)

const (
	namespaceConn  = "urn:x-cast:com.google.cast.tp.connection"
	namespaceRecv  = "urn:x-cast:com.google.cast.receiver"
	namespaceMedia = "urn:x-cast:com.google.cast.media"
	defaultRecv    = "receiver-0"
)

// sentFrame records what Start actually put on the wire.
type sentFrame struct {
	payloadType string
	destination string
	namespace   string
}

// startHarness wires a mock Conn that answers the receiver and media GET_STATUS
// requests with the supplied snapshots and records every frame in order.
type startHarness struct {
	conn   *mockCast.Conn
	status cast.ReceiverStatusResponse
	media  cast.MediaStatusResponse

	mu   sync.Mutex
	sent []sentFrame
}

func newStartHarness(t *testing.T, startErr error) *startHarness {
	t.Helper()

	h := &startHarness{conn: &mockCast.Conn{}}
	recvChan := make(chan *pb.CastMessage, 5)
	h.conn.On("MsgChan").Return(recvChan)
	h.conn.On("Start", mockAddr, mockPort).Return(startErr)
	h.conn.On("Send", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			payload, _ := args.Get(1).(*cast.PayloadHeader)
			frame := sentFrame{destination: args.String(3), namespace: args.String(4)}
			if payload != nil {
				frame.payloadType = payload.Type
			}
			h.mu.Lock()
			h.sent = append(h.sent, frame)
			h.mu.Unlock()

			var (
				response any
				err      error
			)
			switch frame.namespace {
			case namespaceRecv:
				status := h.status
				status.PayloadHeader = cast.PayloadHeader{Type: "RECEIVER_STATUS", RequestId: args.Int(0)}
				response = &status
			case namespaceMedia:
				status := h.media
				status.PayloadHeader = cast.PayloadHeader{Type: "MEDIA_STATUS", RequestId: args.Int(0)}
				response = &status
			default:
				return
			}
			payloadBytes, err := json.Marshal(response)
			if err != nil {
				t.Errorf("marshal %s status: %v", frame.namespace, err)
				return
			}
			payloadString := string(payloadBytes)
			protocolVersion := pb.CastMessage_CASTV2_1_0
			payloadType := pb.CastMessage_STRING
			recvChan <- &pb.CastMessage{
				ProtocolVersion: &protocolVersion,
				PayloadType:     &payloadType,
				PayloadUtf8:     &payloadString,
				PayloadBinary:   payloadBytes,
			}
		}).Return(nil)
	return h
}

func (h *startHarness) frames() []sentFrame {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]sentFrame(nil), h.sent...)
}

func (h *startHarness) app() *application.Application {
	app := application.NewApplication(application.WithConnection(h.conn))
	app.SetCacheDisabled(true)
	return app
}

// A sender that talks to the receiver before sending CONNECT gets its requests
// dropped, so the ordering here is a protocol requirement, not an artifact.
func TestApplicationStartConnectsBeforeQueryingStatus(t *testing.T) {
	h := newStartHarness(t, nil)
	h.status.Status.Volume = cast.Volume{Level: 0.42, Muted: true}
	h.status.Status.Applications = []cast.Application{
		{AppId: "CC1AD845", DisplayName: "Default Media Receiver", TransportId: "transport-1", SessionId: "session-1"},
	}
	h.media.Status = []cast.Media{
		{MediaSessionId: 7, PlayerState: "PLAYING", CurrentTime: 12.5},
	}

	app := h.app()
	if err := app.Start(mockAddr, mockPort); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	frames := h.frames()
	if len(frames) < 2 {
		t.Fatalf("Start() sent %d frames, want at least 2: %+v", len(frames), frames)
	}
	if frames[0] != (sentFrame{payloadType: "CONNECT", destination: defaultRecv, namespace: namespaceConn}) {
		t.Fatalf("first frame = %+v, want CONNECT to %s on %s", frames[0], defaultRecv, namespaceConn)
	}
	if frames[1].namespace != namespaceRecv || frames[1].payloadType != "GET_STATUS" {
		t.Fatalf("second frame = %+v, want GET_STATUS on %s", frames[1], namespaceRecv)
	}

	// Start must leave the receiver snapshot readable by callers.
	got := app.App()
	if got == nil || got.TransportId != "transport-1" || got.AppId != "CC1AD845" {
		t.Fatalf("App() = %+v, want the receiver's running application", got)
	}
	if volume := app.Volume(); volume == nil || volume.Level != 0.42 || !volume.Muted {
		t.Fatalf("Volume() = %+v, want level 0.42 muted", volume)
	}
	media := app.Media()
	if media == nil || media.MediaSessionId != 7 || media.PlayerState != "PLAYING" {
		t.Fatalf("Media() = %+v, want the receiver's PLAYING session", media)
	}
}

// An idle screen means the receiver tore the media session down. Reconnecting to
// one must drop the cached snapshot, otherwise callers keep reporting the last
// known PLAYING state for media that is gone.
func TestApplicationStartIdleScreenClearsStaleMedia(t *testing.T) {
	h := newStartHarness(t, nil)
	h.status.Status.Applications = []cast.Application{
		{AppId: "CC1AD845", DisplayName: "Default Media Receiver", TransportId: "transport-1"},
	}
	h.media.Status = []cast.Media{{MediaSessionId: 7, PlayerState: "PLAYING"}}

	app := h.app()
	if err := app.Start(mockAddr, mockPort); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if app.Media() == nil {
		t.Fatal("Media() = nil, want the PLAYING session before the receiver goes idle")
	}

	// The receiver drops back to the backdrop and we reconnect.
	h.status.Status.Applications = []cast.Application{
		{AppId: "E8C28D3C", DisplayName: "Backdrop", IsIdleScreen: true, TransportId: "transport-idle"},
	}
	if err := app.Start(mockAddr, mockPort); err != nil {
		t.Fatalf("Start() after idle error = %v", err)
	}
	if media := app.Media(); media != nil {
		t.Fatalf("Media() = %+v, want nil for an idle screen", media)
	}
	for _, frame := range h.frames() {
		if frame.destination == "transport-idle" {
			t.Fatalf("Start() opened a media session against the idle screen: %+v", frame)
		}
	}
}

func TestApplicationStartPropagatesConnectionFailure(t *testing.T) {
	wantErr := errors.New("dial tcp: connection refused")
	h := newStartHarness(t, wantErr)

	err := h.app().Start(mockAddr, mockPort)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Start() error = %v, want %v", err, wantErr)
	}
	if frames := h.frames(); len(frames) != 0 {
		t.Fatalf("Start() sent %d frames over a failed connection: %+v", len(frames), frames)
	}
}

func TestPlayableMediaTypeUppercaseExtension(t *testing.T) {
	app := application.NewApplication()

	if !app.PlayableMediaType("clip.AVI") {
		t.Fatal("expected uppercase AVI extension to be playable")
	}
}
