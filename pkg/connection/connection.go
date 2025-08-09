package connection

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/snakeice/gunnel/pkg/protocol"
	"github.com/snakeice/gunnel/pkg/transport"
)

type MessageHandlerFunc func(*Connection, *protocol.Message) error

type Connection struct {
	transp transport.Transport
	stream transport.Stream

	connected        bool
	heartbeatEmitter bool
	lastActive       time.Time
	mu               sync.RWMutex

	sendChannel    chan protocol.Parsable
	receiveChannel chan *protocol.Message
	handler        MessageHandlerFunc

	heartbeatInterval time.Duration
	heartbeatTimeout  time.Duration
	heartbeatStats    struct {
		last     time.Time
		sent     int64
		received int64
		missed   int64
	}

	logger *logrus.Entry
}

func New(transp transport.Transport, messageHandler ...MessageHandlerFunc) *Connection {
	conn := &Connection{
		stream:         transp.Root(),
		sendChannel:    make(chan protocol.Parsable, 50),
		receiveChannel: make(chan *protocol.Message, 50),
		transp:         transp,
		connected:      true,
		lastActive:     time.Now(),
		heartbeatStats: struct {
			last                   time.Time
			sent, received, missed int64
		}{last: time.Now()},
		heartbeatEmitter:  !transp.ImServer(),
		heartbeatInterval: 5 * time.Second,
		heartbeatTimeout:  25 * time.Second,
		logger: logrus.WithFields(
			logrus.Fields{
				"addr": transp.Addr(),
			},
		),
	}
	if len(messageHandler) > 0 {
		conn.handler = messageHandler[0]
	}

	return conn
}

func (c *Connection) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()

	ctx := c.stream.Context()

	go c.watchReceive(ctx)
	go c.watchSend(ctx)
	go c.observeConnection(ctx)

	logrus.Infof("Client connected: %s", c.transp.Addr())
}

func (c *Connection) watchReceive(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Client context done, shutting down")
			return
		default:
			msg, err := c.stream.Receive()
			if err != nil {
				c.logger.WithError(err).Errorf("Failed to read message from %s", c.transp.Addr())
				c.connected = false
				c.markActive()
				c.transp.Close()
				return
			}

			c.receiveChannel <- msg
		}
	}
}

func (c *Connection) watchSend(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Client context done, shutting down")
			return
		case msg := <-c.sendChannel:
			if err := c.stream.Send(msg); err != nil {
				c.logger.WithError(err).Errorf("Failed to send message to %s", c.transp.Addr())
				c.connected = false
				c.lastActive = time.Now()
				c.transp.Close()
				return
			}
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func (c *Connection) observeConnection(ctx context.Context) {
	ticker := time.NewTicker(c.heartbeatInterval)
	defer ticker.Stop()

	timeoutTicker := time.NewTicker(c.heartbeatTimeout)
	defer timeoutTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Heartbeat context done, shutting down")
			c.transp.Close()
			return
		case <-ticker.C:
			if c.heartbeatEmitter {
				c.sendChannel <- &protocol.Heartbeat{}
				atomic.AddInt64(&c.heartbeatStats.sent, 1)
			}
		case <-timeoutTicker.C:
			c.mu.RLock()
			timeSinceLastHeartbeat := time.Since(c.heartbeatStats.last)
			c.mu.RUnlock()

			if timeSinceLastHeartbeat > c.heartbeatTimeout {
				atomic.AddInt64(&c.heartbeatStats.missed, 1)
				c.logger.Warnf(
					"No heartbeat received for %v, connection may be stale",
					timeSinceLastHeartbeat,
				)
				c.disconnect()
			}
		case msg := <-c.receiveChannel:
			c.handleMessage(msg)
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func (c *Connection) Send(msg protocol.Parsable) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		c.logger.Warn("Client is not connected, cannot send message")
		return
	}

	c.sendChannel <- msg
}

func (c *Connection) Acquire() (transport.Stream, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.lastActive = time.Now()

	return c.transp.Acquire()
}

func (c *Connection) Release(stream transport.Stream) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastActive = time.Now()
	if err := c.transp.Release(stream); err != nil {
		c.logger.WithError(err).Errorf("Failed to release stream %s", stream.ID())
	}
	c.logger.Debugf("Released stream %s", stream.ID())
}

func (c *Connection) disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.connected = false
	c.lastActive = time.Now()
	c.transp.Close()
	logrus.Debugf("Client %s disconnected", c.transp.Addr())
}

// GetConnCount returns the client's connections.
func (c *Connection) GetConnCount(subdomain ...string) int {
	return c.transp.LenActive(subdomain...)
}

// GetLastActive returns the client's last active timestamp.
func (c *Connection) GetLastActive() time.Time {
	return c.lastActive
}

// Connected returns true if the client is connected.
func (c *Connection) Connected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.connected
}

// GetHeartbeatStats returns the current heartbeat statistics.
func (c *Connection) GetHeartbeatStats() map[string]any {
	return map[string]any{
		"last":     c.heartbeatStats.last,
		"sent":     atomic.LoadInt64(&c.heartbeatStats.sent),
		"received": atomic.LoadInt64(&c.heartbeatStats.received),
		"missed":   atomic.LoadInt64(&c.heartbeatStats.missed),
	}
}

// SetHeartbeatConfig updates the heartbeat configuration.
func (c *Connection) SetHeartbeatConfig(interval, timeout time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if interval > 0 {
		c.heartbeatInterval = interval
	}
	if timeout > 0 {
		c.heartbeatTimeout = timeout
	}
}

func (c *Connection) markActive() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.lastActive = time.Now()
}
