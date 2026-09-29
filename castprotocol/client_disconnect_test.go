package castprotocol

import (
	"errors"
	"testing"

	"go2tv.app/go2tv/v2/castprotocol/v2/application"
	"go2tv.app/go2tv/v2/castprotocol/v2/cast"
	"go2tv.app/go2tv/v2/metadata"
)

type disconnectOnStatusConn struct {
	cast.Conn
	client *CastClient
}

func (c *disconnectOnStatusConn) Send(int, cast.Payload, string, string, string) error {
	c.client.mu.Lock()
	c.client.connected = false
	c.client.mu.Unlock()
	return errors.New("connection lost")
}

func TestLoadMediaReportsConnectionClosedDuringReceiverLaunch(t *testing.T) {
	client := &CastClient{connected: true}
	conn := &disconnectOnStatusConn{Conn: cast.NewConnection(), client: client}
	client.conn = conn
	client.app = application.NewApplication(
		application.WithConnection(conn),
		application.WithConnectionRetries(1),
	)
	t.Cleanup(func() { _ = client.Close(false) })

	err := client.LoadMedia(LoadRequest{
		MediaURL:    "http://example.test/song.mp3",
		ContentType: "audio/mpeg",
		Metadata:    metadata.Media{Title: "Song"},
	})
	if !errors.Is(err, ErrCastDisconnected) {
		t.Fatalf("LoadMedia() error = %v, want ErrCastDisconnected", err)
	}
}

var _ cast.Conn = (*disconnectOnStatusConn)(nil)
