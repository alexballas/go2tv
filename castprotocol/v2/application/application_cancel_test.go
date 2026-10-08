package application

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/castprotocol/v2/cast"
	pb "go2tv.app/go2tv/v2/castprotocol/v2/cast/proto"
)

type timeoutStatusConn struct {
	cast.Conn
	messages  chan *pb.CastMessage
	requested chan struct{}
	once      sync.Once
}

func (c *timeoutStatusConn) MsgChan() chan *pb.CastMessage { return c.messages }
func (c *timeoutStatusConn) Send(int, cast.Payload, string, string, string) error {
	c.once.Do(func() { close(c.requested) })
	return context.DeadlineExceeded
}
func (c *timeoutStatusConn) Close() error { return nil }

func TestUpdateContextCancelsStatusRetryWait(t *testing.T) {
	conn := &timeoutStatusConn{messages: make(chan *pb.CastMessage), requested: make(chan struct{})}
	app := NewApplication(WithConnection(conn), WithConnectionRetries(5))
	defer app.Close(false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- app.UpdateContext(ctx) }()
	select {
	case <-conn.requested:
	case <-time.After(time.Second):
		t.Fatal("status request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled retry returned %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("canceled status retry kept sleeping")
	}
}
