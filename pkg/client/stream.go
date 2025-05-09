package client

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/snakeice/gunnel/pkg/protocol"
	"github.com/snakeice/gunnel/pkg/transport"
	"github.com/snakeice/gunnel/pkg/tunnel"
)

// handleStreams handles incoming messages from the server.
func (c *Client) handleStream(
	ctx context.Context,
	strm transport.Stream,
	logger *logrus.Entry,
) error {
	defer func() {
		if r := recover(); r != nil {
			logger.WithField("panic", r).Error("Stream handler panicked")
		}
		logger.Debug("Releasing stream")
		if err := strm.Close(); err != nil {
			logger.WithError(err).Error("Failed to close stream")
		}
	}()

	for {
		select {
		case <-ctx.Done():
			logger.Infof("Stopping stream %s handler", strm.ID())
		default:
		}

		// Read message
		msg, err := strm.Receive()
		if err != nil {
			return fmt.Errorf("failed to read message from server, closing connection: %w", err)
		}

		logger.WithField("msg_size", msg.Length).Debug("Received message from server")

		// Handle message
		switch msg.Type { //nolint:exhaustive // this switch not exhaustive
		case protocol.MessageBeginStream:
			beginMsg := protocol.BeginConnection{}
			protocol.Unmarshal(&beginMsg, msg)

			logger.Info("Received begin connection message")

			backend := c.getBackend(beginMsg.Subdomain)
			if backend == nil {
				logger.WithField("subdomain", beginMsg.Subdomain).
					Error("No backend found for subdomain")
				return fmt.Errorf("no backend found for subdomain: %s", beginMsg.Subdomain)
			}

			logger = logger.WithFields(logrus.Fields{
				"subdomain": beginMsg.Subdomain,
				"client_id": strm.ID(),
			})

			// Create tunnel for this connection
			t, err := tunnel.NewTunnel(backend.getAddr(), strm)
			if err != nil {
				return fmt.Errorf("failed to create tunnel: %w", err)
			}

			// Send connection ready message
			readyMsg := &protocol.ConnectionReady{
				Subdomain: beginMsg.Subdomain,
			}
			if err := strm.Send(readyMsg); err != nil {
				logger.Error("Failed to send connection ready message")
				return fmt.Errorf("failed to send connection ready message: %w", err)
			}

			logger.Info("Connection ready for proxying")

			if err := t.Proxy(); err != nil {
				return fmt.Errorf("proxy operation failed: %w", err)
			}

			logger.Debug("Proxy completed successfully")

			return nil
		case protocol.MessageEndStream:
			logger.Info("Received end stream message")
			return nil

		case protocol.MessageDisconnect:
			closeMsg := protocol.CloseConnection{}
			protocol.Unmarshal(&closeMsg, msg)

			logger.Info("Server closed connection")

			return nil

		case protocol.MessageError:
			errMsg := protocol.ErrorMessage{}
			protocol.Unmarshal(&errMsg, msg)

			logger.WithField("error", errMsg.Message).Error("Server sent error")
		default:
			err = strm.Send(protocol.NewErrorMessage("Unknown message type"))
			if err != nil {
				logger.WithError(err).Error("Failed to send error message")
			}

			return fmt.Errorf("unknown message type: %s", msg.Type)
		}
	}
}
