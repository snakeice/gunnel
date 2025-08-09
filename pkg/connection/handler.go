package connection

import (
	"sync/atomic"
	"time"

	"github.com/snakeice/gunnel/pkg/protocol"
)

func (c *Connection) handleMessage(msg *protocol.Message) {
	c.markActive()

	switch msg.Type { //nolint:exhaustive // this switch not exhaustive
	case protocol.MessageHeartbeat:
		c.heartbeatStats.last = time.Now()
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
			if err := c.handler(c, msg); err != nil {
				c.logger.WithError(err).Errorf("Handler error for message type: %s", msg.Type)
			}
		} else {
			c.logger.Warnf("No handler registered for message type: %s", msg.Type)
		}
	}
}
