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
	conn *tls.Conn

	recvMsgChan   chan *pb.CastMessage
	closeChanOnce sync.Once
	recvMsgMu     sync.RWMutex
	recvMsgClosed bool

	debug     bool
	connected bool

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

func (c *Connection) Start(addr string, port int) error {
	if !c.connected {
		err := c.connect(addr, port)
		if err != nil {
			return err
		}
		var ctx context.Context
		// TODO: Receive context through function params?
		ctx, c.cancel = context.WithCancel(context.Background())
		go c.receiveLoop(ctx)
	}
	return nil
}

func (c *Connection) Close() error {
	// TODO: nothing here is concurrent safe, fix?
	c.connected = false
	if c.cancel != nil {
		c.cancel()
	}
	c.closeChanOnce.Do(func() {
		c.recvMsgMu.Lock()
		close(c.recvMsgChan)
		c.recvMsgClosed = true
		c.recvMsgMu.Unlock()
	})
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Connection) SetDebug(debug bool) { c.debug = debug }

func (c *Connection) LocalAddr() (addr string, err error) {
	host, _, err := net.SplitHostPort(c.conn.LocalAddr().String())
	return host, err
}

func (c *Connection) RemoteAddr() (addr string, err error) {
	addr, _, err = net.SplitHostPort(c.conn.RemoteAddr().String())
	return addr, err
}

func (c *Connection) RemotePort() (port string, err error) {
	_, port, err = net.SplitHostPort(c.conn.RemoteAddr().String())
	return port, err
}

func (c *Connection) log(message string, args ...interface{}) {
	if c.debug {
		log.WithField("package", "cast").Debugf(message, args...)
	}
}

func (c *Connection) connect(addr string, port int) error {
	var err error
	dialer := &net.Dialer{
		Timeout:   dialerTimeout,
		KeepAlive: dialerKeepAlive,
	}
	c.conn, err = tls.DialWithDialer(dialer, "tcp", fmt.Sprintf("%s:%d", addr, port), &tls.Config{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return errors.Wrapf(err, "unable to connect to chromecast at '%s:%d'", addr, port)
	}
	c.connected = true
	return nil
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

	if err := binary.Write(c.conn, binary.BigEndian, uint32(len(data))); err != nil {
		return errors.Wrap(err, "unable to write binary format")
	}
	if _, err := c.conn.Write(data); err != nil {
		return errors.Wrap(err, "unable to send data")
	}

	return nil
}

func (c *Connection) receiveLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Fallthrough if not done
		}
		var length uint32
		if c.conn == nil {
			continue
		}
		if err := binary.Read(c.conn, binary.BigEndian, &length); err != nil {
			c.log("failed to binary read payload: %v", err)
			break
		}
		if length == 0 {
			c.log("empty payload received")
			continue
		}

		payload := make([]byte, length)
		i, err := io.ReadFull(c.conn, payload)
		if err != nil {
			c.log("failed to read payload: %v", err)
			continue
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
		closed := c.recvMsgClosed
		c.recvMsgMu.RUnlock()
		if closed {
			return
		}
		select {
		case c.recvMsgChan <- message:
		default:
			c.log("dropping message request_id=%d because receive channel is full", requestID)
		}
	}
}
