package cast

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gogo/protobuf/proto"
	pb "go2tv.app/go2tv/v2/castprotocol/v2/cast/proto"
)

func TestStartContextCancelsStalledTLSHandshake(t *testing.T) {
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connection := NewConnection()
	done := make(chan error, 1)
	port := listener.Addr().(*net.TCPAddr).Port
	go func() { done <- connection.StartContext(ctx, "127.0.0.1", port) }()
	var server net.Conn
	select {
	case server = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("dial did not reach TLS handshake")
	}
	defer server.Close()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stalled handshake succeeded after cancellation")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("canceled TLS handshake did not return promptly")
	}
	_ = connection.Close()
}

type observedConn struct {
	net.Conn
	observe atomic.Bool
	writing chan struct{}
	once    sync.Once
}

func (c *observedConn) Write(p []byte) (int, error) {
	if c.observe.Load() {
		c.once.Do(func() { close(c.writing) })
	}
	return c.Conn.Write(p)
}

func TestCloseInterruptsBlockedTLSWrite(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(1),
	}, &x509.Certificate{SerialNumber: big.NewInt(1)}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	clientPipe, serverPipe := net.Pipe()
	defer serverPipe.Close()
	observed := &observedConn{Conn: clientPipe, writing: make(chan struct{})}
	client := tls.Client(observed, &tls.Config{InsecureSkipVerify: true})
	server := tls.Server(serverPipe, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}})
	handshake := make(chan error, 1)
	go func() { handshake <- server.Handshake() }()
	if err := client.Handshake(); err != nil {
		t.Fatal(err)
	}
	if err := <-handshake; err != nil {
		t.Fatal(err)
	}
	connection := NewConnection()
	connection.conn = client
	connection.connected = true
	defer connection.Close()
	observed.observe.Store(true)
	done := make(chan error, 1)
	go func() {
		done <- connection.Send(1, &PayloadHeader{Type: "STOP"}, "sender-0", "receiver-0", "urn:x-cast:com.google.cast.receiver")
	}()
	select {
	case <-observed.writing:
	case <-time.After(time.Second):
		t.Fatal("STOP write did not start")
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("blocked write succeeded after close")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("socket close did not interrupt blocked STOP write")
	}
}

func TestSuccessfulStartOutlivesStartupContext(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(2)}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverReady := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		if err := conn.(*tls.Conn).Handshake(); err != nil {
			_ = conn.Close()
			return
		}
		serverReady <- conn
	}()
	ctx, cancel := context.WithCancel(context.Background())
	connection := NewConnection()
	defer connection.Close()
	if err := connection.StartContext(ctx, "127.0.0.1", listener.Addr().(*net.TCPAddr).Port); err != nil {
		t.Fatal(err)
	}
	var server net.Conn
	select {
	case server = <-serverReady:
	case <-time.After(time.Second):
		t.Fatal("server handshake did not finish")
	}
	defer server.Close()
	cancel()
	payload, source, destination, namespace := `{"type":"RECEIVER_STATUS"}`, "receiver-0", "sender-0", "urn:x-cast:com.google.cast.receiver"
	message := &pb.CastMessage{
		ProtocolVersion: pb.CastMessage_CASTV2_1_0.Enum(),
		SourceId:        &source, DestinationId: &destination, Namespace: &namespace,
		PayloadType: pb.CastMessage_STRING.Enum(), PayloadUtf8: &payload,
	}
	data, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(server, binary.BigEndian, uint32(len(data))); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Write(data); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-connection.MsgChan():
		if got == nil || got.PayloadUtf8 == nil || *got.PayloadUtf8 != payload {
			t.Fatalf("unexpected message: %#v", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("receive loop stopped when startup context ended")
	}
}

func testCastTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(3)}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
}

func TestCloseDoesNotWaitForTLSCloseNotify(t *testing.T) {
	clientPipe, serverPipe := net.Pipe()
	defer serverPipe.Close()
	client := tls.Client(clientPipe, &tls.Config{InsecureSkipVerify: true})
	server := tls.Server(serverPipe, testCastTLSConfig(t))
	handshake := make(chan error, 1)
	go func() { handshake <- server.Handshake() }()
	if err := client.Handshake(); err != nil {
		t.Fatal(err)
	}
	if err := <-handshake; err != nil {
		t.Fatal(err)
	}
	connection := NewConnection()
	connection.conn, connection.connected = client, true
	defer connection.Close()
	done := make(chan error, 1)
	go func() { done <- connection.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(200 * time.Millisecond):
		_ = serverPipe.Close()
		t.Fatal("shutdown waited for unread TLS close_notify")
	}
}

func TestConnectionReconnectsAfterPeerDrop(t *testing.T) {
	listener, err := tls.Listen("tcp", "127.0.0.1:0", testCastTLSConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan *tls.Conn, 2)
	go func() {
		for range 2 {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			tlsConn := conn.(*tls.Conn)
			if err := tlsConn.Handshake(); err != nil {
				_ = tlsConn.NetConn().Close()
				return
			}
			accepted <- tlsConn
		}
	}()
	connection := NewConnection()
	defer connection.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	if err := connection.StartContext(context.Background(), "127.0.0.1", port); err != nil {
		t.Fatal(err)
	}
	first := <-accepted
	_ = first.NetConn().Close()
	deadline := time.Now().Add(time.Second)
	for connection.IsConnected() {
		if time.Now().After(deadline) {
			t.Fatal("dropped socket remained connected")
		}
		time.Sleep(time.Millisecond)
	}
	if err := connection.StartContext(context.Background(), "127.0.0.1", port); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	second := <-accepted
	defer second.NetConn().Close()
	payload, source, destination, namespace := `{"type":"RECEIVER_STATUS"}`, "receiver-0", "sender-0", "urn:x-cast:com.google.cast.receiver"
	message := &pb.CastMessage{
		ProtocolVersion: pb.CastMessage_CASTV2_1_0.Enum(),
		SourceId:        &source, DestinationId: &destination, Namespace: &namespace,
		PayloadType: pb.CastMessage_STRING.Enum(), PayloadUtf8: &payload,
	}
	data, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(second, binary.BigEndian, uint32(len(data))); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Write(data); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-connection.MsgChan():
		if got == nil || got.PayloadUtf8 == nil || *got.PayloadUtf8 != payload {
			t.Fatalf("message after reconnect = %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("reconnected socket did not deliver messages")
	}
}
