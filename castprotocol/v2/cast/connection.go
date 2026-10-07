package cast

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/buger/jsonparser"
	"github.com/gogo/protobuf/proto"
	"github.com/pkg/errors"

	pb "go2tv.app/go2tv/v2/castprotocol/v2/cast/proto"
)

const (
	dialerTimeout   = time.Second * 3
	dialerKeepAlive = time.Second * 30
	writeTimeout    = time.Second * 2
)

type Conn interface {
	Start(addr string, port int) error
	MsgChan() chan *pb.CastMessage
	Close() error
	SetDebug(debug bool)
	LocalAddr() (addr string, err error)
	RemoteAddr() (addr string, err error)
	RemotePort() (addr string, err error)
	Send(requestID int, payload Payload, sourceID, destinationID, namespace string) error
}

type Connection struct {
	mu     sync.RWMutex
	sendMu sync.Mutex
	conn   *tls.Conn

	recvMsgChan     chan *pb.CastMessage
	closeChanOnce   sync.Once
	closeSocketOnce sync.Once
	closeSocketErr  error
	recvMsgMu       sync.RWMutex
	recvMsgClosed   bool

	debug     bool
	connected bool
	closed    bool

	cancel context.CancelFunc
}

func NewConnection() *Connection {
	c := &Connection{
		recvMsgChan: make(chan *pb.CastMessage, 5),
		connected:   false,
	}
	return c
}

func (c *Connection) MsgChan() chan *pb.CastMessage { return c.recvMsgChan }

// IsConnected reports whether the current socket can carry a new cast request.
// An unexpected peer drop leaves the Connection reusable.
func (c *Connection) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected && !c.closed
}

func (c *Connection) Start(addr string, port int) error {
	return c.StartContext(context.Background(), addr, port)
}

// StartContext bounds both the TCP dial and TLS handshake and allows Close to
// interrupt either operation before a socket is installed.
func (c *Connection) StartContext(ctx context.Context, addr string, port int) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return net.ErrClosed
	}
	if c.connected {
		c.mu.Unlock()
		return nil
	}
	startCtx, cancelSetup := context.WithCancel(ctx)
	c.cancel = cancelSetup
	c.mu.Unlock()

	conn, err := c.connect(startCtx, addr, port)
	if err != nil {
		cancelSetup()
		return err
	}
	c.mu.Lock()
	if c.closed || startCtx.Err() != nil {
		c.mu.Unlock()
		cancelSetup()
		_ = conn.Close()
		return context.Canceled
	}
	// The startup context ends when playback preparation completes. Once the
	// socket is ready, its receive loop belongs to the connection lifecycle.
	receiveCtx, cancelReceive := context.WithCancel(context.Background())
	c.cancel = cancelReceive
	c.conn = conn
	c.connected = true
	c.mu.Unlock()
	cancelSetup()
	go c.receiveLoop(receiveCtx, conn)
	return nil
}

func (c *Connection) Close() error {
	c.mu.Lock()
	c.closed = true
	c.connected = false
	if c.cancel != nil {
		c.cancel()
	}
	conn := c.conn
	c.mu.Unlock()
	c.closeChanOnce.Do(func() {
		c.recvMsgMu.Lock()
		close(c.recvMsgChan)
		c.recvMsgClosed = true
		c.recvMsgMu.Unlock()
	})
	c.closeSocketOnce.Do(func() {
		if conn != nil {
			// tls.Conn.Close may wait for close_notify to be written. Shut the
			// underlying socket first so cancellation never waits on that write.
			c.closeSocketErr = conn.NetConn().Close()
			_ = conn.Close()
		}
	})
	return c.closeSocketErr
}

// disconnect releases a failed socket but keeps the receive channel and
// application listeners alive for a later StartContext call.
func (c *Connection) disconnect(conn *tls.Conn) {
	c.mu.Lock()
	if c.conn != conn || c.closed {
		c.mu.Unlock()
		return
	}
	c.conn = nil
	c.connected = false
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	c.mu.Unlock()
	_ = conn.NetConn().Close()
	_ = conn.Close()
}

func (c *Connection) SetDebug(debug bool) { c.debug = debug }

func (c *Connection) LocalAddr() (addr string, err error) {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()
	if conn == nil {
		return "", net.ErrClosed
	}
	host, _, err := net.SplitHostPort(conn.LocalAddr().String())
	return host, err
}

func (c *Connection) RemoteAddr() (addr string, err error) {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()
	if conn == nil {
		return "", net.ErrClosed
	}
	addr, _, err = net.SplitHostPort(conn.RemoteAddr().String())
	return addr, err
}

func (c *Connection) RemotePort() (port string, err error) {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()
	if conn == nil {
		return "", net.ErrClosed
	}
	_, port, err = net.SplitHostPort(conn.RemoteAddr().String())
	return port, err
}

func (c *Connection) log(message string, args ...any) {
	if c.debug {
		log.WithField("package", "cast").Debugf(message, args...)
	}
}

func (c *Connection) connect(ctx context.Context, addr string, port int) (*tls.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, dialerTimeout)
	defer cancel()
	dialer := &net.Dialer{
		KeepAlive: dialerKeepAlive,
	}
	raw, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(addr, fmt.Sprint(port)))
	if err != nil {
		return nil, errors.Wrapf(err, "unable to connect to chromecast at '%s:%d'", addr, port)
	}
	conn := tls.Client(raw, &tls.Config{
		InsecureSkipVerify: true,
	})
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, errors.Wrapf(err, "unable to connect to chromecast at '%s:%d'", addr, port)
	}
	return conn, nil
}

func (c *Connection) Send(requestID int, payload Payload, sourceID, destinationID, namespace string) error {
	payloadJson, err := json.Marshal(payload)
	if err != nil {
		return errors.Wrap(err, "unable to marshal json payload")
	}
	payloadUtf8 := string(payloadJson)
	message := &pb.CastMessage{
		ProtocolVersion: pb.CastMessage_CASTV2_1_0.Enum(),
		SourceId:        &sourceID,
		DestinationId:   &destinationID,
		Namespace:       &namespace,
		PayloadType:     pb.CastMessage_STRING.Enum(),
		PayloadUtf8:     &payloadUtf8,
	}
	proto.SetDefaults(message)
	data, err := proto.Marshal(message)
	if err != nil {
		return errors.Wrap(err, "unable to marshal proto payload")
	}

	c.log("(%d)%s -> %s [%s]: %s", requestID, sourceID, destinationID, namespace, payloadJson)

	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.mu.RLock()
	conn := c.conn
	closed := c.closed
	c.mu.RUnlock()
	if closed || conn == nil {
		return net.ErrClosed
	}
	if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	defer conn.SetWriteDeadline(time.Time{})
	if err := binary.Write(conn, binary.BigEndian, uint32(len(data))); err != nil {
		return errors.Wrap(err, "unable to write binary format")
	}
	if _, err := conn.Write(data); err != nil {
		return errors.Wrap(err, "unable to send data")
	}

	return nil
}

func (c *Connection) receiveLoop(ctx context.Context, conn *tls.Conn) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Fallthrough if not done
		}
		var length uint32
		if err := binary.Read(conn, binary.BigEndian, &length); err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			c.log("failed to binary read payload: %v", err)
			// This loop is also the PING responder: once reads fail the
			// receiver will drop the session anyway. Close the socket so
			// later writes fail immediately instead of feeding a half-open
			// connection until a write finally hits a broken pipe.
			c.disconnect(conn)
			return
		}
		if length == 0 {
			c.log("empty payload received")
			continue
		}

		payload := make([]byte, length)
		i, err := io.ReadFull(conn, payload)
		if err != nil {
			c.log("failed to read payload: %v", err)
			c.disconnect(conn)
			return
		}

		if i != int(length) {
			c.log("invalid payload, wanted: %d but read: %d", length, i)
			continue
		}

		message := &pb.CastMessage{}
		if err := proto.Unmarshal(payload, message); err != nil {
			c.log("failed to unmarshal proto cast message '%s': %v", payload, err)
			continue
		}
		// Get the requestID from the message to use in the log. We don't really
		// care if this fails.
		requestID, _ := jsonparser.GetInt([]byte(*message.PayloadUtf8), "requestId")
		if requestID == 0 {
			requestID = -1
		}
		// Cast to int, losing information, but unlilely we will
		// ever send that many messages in a single run.
		requestIDi := int(requestID)

		c.log("(%d)%s <- %s [%s]: %s", requestIDi, *message.DestinationId, *message.SourceId, *message.Namespace, *message.PayloadUtf8)

		var headers PayloadHeader
		if err := json.Unmarshal([]byte(*message.PayloadUtf8), &headers); err != nil {
			c.log("failed to unmarshal proto message header: %v", err)
			continue
		}

		c.handleMessage(requestIDi, message, &headers)
	}
}

func (c *Connection) handleMessage(requestID int, message *pb.CastMessage, headers *PayloadHeader) {
	messageType, err := jsonparser.GetString([]byte(*message.PayloadUtf8), "type")
	if err != nil {
		c.log("could not find 'type' key in response message request_id=%d %q: %s", requestID, *message.PayloadUtf8, err)
		return
	}

	switch messageType {
	case "PING":
		if err := c.Send(-1, &PongHeader, *message.SourceId, *message.DestinationId, *message.Namespace); err != nil {
			c.log("unable to respond to 'PING': %v", err)
		}
	default:
		c.recvMsgMu.RLock()
		defer c.recvMsgMu.RUnlock()
		if c.recvMsgClosed {
			return
		}
		select {
		case c.recvMsgChan <- message:
		default:
			c.log("dropping message request_id=%d because receive channel is full", requestID)
		}
	}
}
