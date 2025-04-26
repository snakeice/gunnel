package connection

import (
	"sync/atomic"
	"time"

	"github.com/snakeice/gunnel/pkg/protocol"
)

func (c *Connection) handleMessage(msg *protocol.Message) {
	c.mu.Lock()
	c.lastActive = time.Now()
	c.mu.Unlock()

	switch msg.Type { //nolint:exhaustive // this switch not exhaustive
	case protocol.MessageHeartbeat:
		c.mu.Lock()
		c.heartbeatStats.last = time.Now()
		c.mu.Unlock()
		atomic.AddInt64(&c.heartbeatStats.received, 1)

		if !c.heartbeatEmitter {
			c.sendChannel <- &protocol.Heartbeat{}
		}
		atomic.AddInt64(&c.heartbeatStats.sent, 1)
	case protocol.MessageDisconnect:
		c.logger.Infof("Client %s disconnected", c.transp.Addr())
		c.disconnect()
		return
	case protocol.MessageError:
		errMsg := protocol.ErrorMessage{}
		errMsg.Unmarshal(msg.Payload)
		if errMsg.Message == "" {
			c.logger.Errorf("Error message from %s: %s", c.transp.Addr(), errMsg.Message)
			c.disconnect()
			return
		}

	default:
		if c.handler != nil {
			c.handler(c, msg)
		} else {
			c.logger.Warnf("No handler registered for message type: %s", msg.Type)
		}
	}
}
