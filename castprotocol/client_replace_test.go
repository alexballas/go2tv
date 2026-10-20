package castprotocol

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/castprotocol/v2/application"
	"go2tv.app/go2tv/v2/castprotocol/v2/cast"
	pb "go2tv.app/go2tv/v2/castprotocol/v2/cast/proto"
	"go2tv.app/go2tv/v2/metadata"
)

// replacingReceiverConn plays session 1 and keeps reporting it for a few
// status polls after LOAD, as a busy receiver does while replacing media.
type replacingReceiverConn struct {
	cast.Conn
	mu       sync.Mutex
	messages chan *pb.CastMessage
	session  int
	loaded   int
	lagging  int
	plays    []int
}

func (c *replacingReceiverConn) MsgChan() chan *pb.CastMessage { return c.messages }
func (c *replacingReceiverConn) IsConnected() bool             { return true }
func (c *replacingReceiverConn) Close() error                  { return nil }

func (c *replacingReceiverConn) Send(id int, payload cast.Payload, source, destination, namespace string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	payload.SetRequestId(id)
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var command struct {
		Type           string `json:"type"`
		MediaSessionID int    `json:"mediaSessionId"`
	}
	if err := json.Unmarshal(data, &command); err != nil {
		return err
	}
	var response any
	switch command.Type {
	case "LOAD":
		c.loaded = c.session + 1
	case "PLAY":
		c.plays = append(c.plays, command.MediaSessionID)
	case "GET_STATUS":
		if !strings.HasSuffix(namespace, ".media") {
			response = map[string]any{"type": "RECEIVER_STATUS", "requestId": id, "status": map[string]any{"applications": []any{map[string]any{"appId": cast.DefaultMediaReceiverAppID, "transportId": "web-1", "sessionId": "session"}}}}
			break
		}
		if c.loaded != 0 {
			if c.lagging > 0 {
				c.lagging--
			} else {
				c.session, c.loaded = c.loaded, 0
			}
		}
		response = map[string]any{"type": "MEDIA_STATUS", "requestId": id, "status": []any{map[string]any{"mediaSessionId": c.session, "playerState": "PLAYING", "media": map[string]any{"contentId": "http://radio.test/old"}}}}
	}
	if response == nil {
		return nil
	}
	body, err := json.Marshal(response)
	if err != nil {
		return err
	}
	message := string(body)
	c.messages <- &pb.CastMessage{PayloadUtf8: &message, Namespace: &namespace, SourceId: &destination, DestinationId: &source}
	return nil
}

func TestLiveLoadOnPlayingReceiverPlaysReplacementSession(t *testing.T) {
	timeout := replacementSessionTimeout
	replacementSessionTimeout = time.Second
	t.Cleanup(func() { replacementSessionTimeout = timeout })
	tt := []struct {
		name    string
		lagging int
		wantErr error
		want    []int
	}{
		{name: "next status reports replacement", lagging: 1, want: []int{2}},
		{name: "slow receiver still gets PLAY", lagging: 3, want: []int{2}},
		{name: "replacement never reported", lagging: 1000, wantErr: ErrMediaNotReplaced},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			conn := &replacingReceiverConn{Conn: cast.NewConnection(), messages: make(chan *pb.CastMessage, 8), session: 1, lagging: tc.lagging}
			client := &CastClient{connected: true, conn: conn}
			client.app = application.NewApplication(application.WithConnection(conn), application.WithConnectionRetries(1))
			t.Cleanup(func() { _ = client.app.ForceClose() })

			err := client.LoadMedia(LoadRequest{
				MediaURL:    "http://radio.test/next",
				ContentType: "audio/mpeg",
				Metadata:    metadata.Media{Title: "Next"},
				Live:        true,
			})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("LoadMedia() error = %v, want %v", err, tc.wantErr)
			}
			conn.mu.Lock()
			defer conn.mu.Unlock()
			if !slices.Equal(conn.plays, tc.want) {
				t.Fatalf("PLAY sessions = %v, want %v", conn.plays, tc.want)
			}
		})
	}
}
