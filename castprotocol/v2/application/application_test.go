package application_test

import (
	"encoding/json"
	"errors"
	"fmt"
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

	// mediaUnreadable makes the media namespace answer with a body that cannot
	// be decoded, standing in for a receiver whose media session can't be read.
	// A garbled reply fails the same way a dropped one does, without making the
	// test wait out the 5s sendAndWait timeout.
	mediaUnreadable bool

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
				payloadBytes []byte
				err          error
			)
			switch {
			case frame.namespace == namespaceRecv:
				status := h.status
				status.PayloadHeader = cast.PayloadHeader{Type: "RECEIVER_STATUS", RequestId: args.Int(0)}
				payloadBytes, err = json.Marshal(&status)
			case frame.namespace == namespaceMedia && h.mediaUnreadable:
				// Routable (requestId parses) but not decodable as a status.
				payloadBytes = fmt.Appendf(nil, `{"requestId":%d,"type":"MEDIA_STATUS","status":"garbled"}`, args.Int(0))
			case frame.namespace == namespaceMedia:
				status := h.media
				status.PayloadHeader = cast.PayloadHeader{Type: "MEDIA_STATUS", RequestId: args.Int(0)}
				payloadBytes, err = json.Marshal(&status)
			default:
				return
			}
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

// A media session that cannot be read must surface as an error, not as a
// successful update that silently republishes the previous snapshot. The
// snapshot itself must survive: callers map a nil snapshot to IDLE, and the
// playback monitor ends playback on the first IDLE poll, so clearing here would
// turn one dropped response into a spurious "finished".
func TestUpdateReportsUnreadableMediaSessionWithoutClearingSnapshot(t *testing.T) {
	h := newStartHarness(t, nil)
	h.status.Status.Applications = []cast.Application{
		{AppId: "CC1AD845", DisplayName: "Default Media Receiver", TransportId: "transport-1"},
	}
	h.media.Status = []cast.Media{{MediaSessionId: 7, PlayerState: "PLAYING", CurrentTime: 30}}

	app := h.app()
	if err := app.Start(mockAddr, mockPort); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	h.mediaUnreadable = true
	if err := app.Update(); err == nil {
		t.Fatal("Update() = nil, want an error when the media session is unreadable")
	}
	media := app.Media()
	if media == nil || media.MediaSessionId != 7 || media.PlayerState != "PLAYING" {
		t.Fatalf("Media() = %+v, want the previous snapshot retained, not cleared to IDLE", media)
	}
	// The receiver-level read succeeded, so that part of the snapshot stands.
	if app.App() == nil {
		t.Fatal("App() = nil, want the receiver status that was read successfully")
	}
}

// updateMediaStatus clears the snapshot when the receiver reports no session.
// The seek paths refresh and then dereference it, so a cleared snapshot must be
// rejected rather than panicking.
func TestSeekPathsRejectClearedMediaSession(t *testing.T) {
	tests := []struct {
		name string
		call func(*application.Application) error
		want error
	}{
		{name: "Skip", call: (*application.Application).Skip, want: application.ErrNoMediaSkip},
		{
			name: "SeekFromStart",
			call: func(a *application.Application) error { return a.SeekFromStart(5) },
			want: application.ErrMediaNotYetInitialised,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newStartHarness(t, nil)
			h.status.Status.Applications = []cast.Application{
				{AppId: "CC1AD845", DisplayName: "Default Media Receiver", TransportId: "transport-1"},
			}
			h.media.Status = []cast.Media{{MediaSessionId: 7, PlayerState: "PLAYING", CurrentTime: 30}}

			app := h.app()
			if err := app.Start(mockAddr, mockPort); err != nil {
				t.Fatalf("Start() error = %v", err)
			}

			// The receiver tears the session down before the seek lands.
			h.media.Status = nil
			if err := tt.call(app); !errors.Is(err, tt.want) {
				t.Fatalf("%s() error = %v, want %v", tt.name, err, tt.want)
			}
		})
	}
}

func TestPlayableMediaTypeUppercaseExtension(t *testing.T) {
	app := application.NewApplication()

	if !app.PlayableMediaType("clip.AVI") {
		t.Fatal("expected uppercase AVI extension to be playable")
	}
}
