package castprotocol

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/castprotocol/v2/application"
	"go2tv.app/go2tv/v2/castprotocol/v2/cast"
	pb "go2tv.app/go2tv/v2/castprotocol/v2/cast/proto"
)

type blockedStopConn struct {
	cast.Conn
	writing  chan struct{}
	closed   chan struct{}
	messages chan *pb.CastMessage
	once     sync.Once
}

func (c *blockedStopConn) Send(int, cast.Payload, string, string, string) error {
	c.once.Do(func() { close(c.writing) })
	<-c.closed
	return net.ErrClosed
}

func (c *blockedStopConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}

func (c *blockedStopConn) MsgChan() chan *pb.CastMessage { return c.messages }

func TestStopAndCloseInterruptsBlockedStop(t *testing.T) {
	client, err := NewCastClient("http://127.0.0.1:8009")
	if err != nil {
		t.Fatal(err)
	}
	_ = client.app.ForceClose()
	conn := &blockedStopConn{writing: make(chan struct{}), closed: make(chan struct{}), messages: make(chan *pb.CastMessage)}
	client.app = application.NewApplication(application.WithConnection(conn))
	done := make(chan struct{})
	go func() { client.StopAndClose(50 * time.Millisecond); close(done) }()
	select {
	case <-conn.writing:
	case <-time.After(time.Second):
		t.Fatal("STOP write did not start")
	}
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("blocked STOP held cancellation teardown")
	}
	select {
	case <-conn.closed:
	default:
		t.Fatal("transport was not closed")
	}
}

func TestConnectContextCancelsStalledTLSHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	client, err := NewCastClient("http://" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.ConnectContext(ctx) }()
	var server net.Conn
	select {
	case server = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("connect did not reach TLS handshake")
	}
	defer server.Close()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled connect succeeded")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("canceled connect waited for TLS timeout")
	}
	_ = client.ForceClose()
}
