package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/snakeice/gunnel/pkg/connection"
	"github.com/snakeice/gunnel/pkg/protocol"
	"github.com/snakeice/gunnel/pkg/transport"
)

// Client manages client connections to the server.
type Client struct {
	config         *Config
	conn           transport.Transport
	connWrapper    *connection.Connection
	mu             sync.Mutex
	reconnectDelay time.Duration
	token          string
	logger         *logrus.Entry
}

// New creates a new connection manager.
func New(config *Config) (*Client, error) {
	transp, err := transport.New(config.ServerAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport: %w", err)
	}

	c := &Client{
		config:         config,
		reconnectDelay: 5 * time.Second,
		conn:           transp,
		token:          os.Getenv("GUNNEL_TOKEN"),
		logger: logrus.WithFields(
			logrus.Fields{
				"server_addr": config.ServerAddr,
			},
		),
	}

	return c, nil
}

// Start starts the connection manager.
func (c *Client) Start(ctx context.Context) error {
	c.logger.Info("Starting registration process")

	err := c.register()
	if err != nil {
		c.logger.WithError(err).Error("Failed to register client")
		return err
	}

	go c.reconnectLoop(ctx)

	return c.worker(ctx)
}

func (c *Client) register() error {
	if c.conn == nil || c.conn.IsClosed() {
		return nil
	}

	for _, backend := range c.config.Backend {
		if err := c.registryBackendWithTransport(c.conn, backend); err != nil {
			c.logger.WithError(err).Error("Failed to register backend")
			continue
		}
	}

	c.logger.Info("Backends registered")

	if c.conn != nil && !c.conn.IsClosed() {
		c.connWrapper = connection.New(c.conn)
		c.connWrapper.Start()
	}

	return nil
}

func (c *Client) registerWithTransport(transp transport.Transport) {
	for _, backend := range c.config.Backend {
		if err := c.registryBackendWithTransport(transp, backend); err != nil {
			c.logger.WithError(err).Error("Failed to register backend")
			continue
		}
	}

	c.logger.Info("Backends registered")
	if c.connWrapper != nil {
		c.connWrapper.Close()
	}
	c.connWrapper = connection.New(transp)
	c.connWrapper.Start()
}

func (c *Client) registryBackendWithTransport(
	transp transport.Transport,
	backend *BackendConfig,
) error {
	stream := transp.Root()
	reg := protocol.ConnectionRegister{
		Subdomain: backend.Subdomain,
		Host:      backend.Host,
		Port:      backend.Port,
		Protocol:  backend.Protocol,
		Token:     c.token,
	}

	c.logger.Debug("Registering client with server")

	if err := stream.Send(&reg); err != nil {
		transp.Close()
		return fmt.Errorf("failed to send registration message: %w", err)
	}

	msg, err := stream.Receive()
	if err != nil {
		transp.Close()
		return fmt.Errorf("failed to receive registration response: %w", err)
	}

	if msg.Type == protocol.MessageError {
		errMsg := protocol.ErrorMessage{}
		protocol.Unmarshal(&errMsg, msg)

		transp.Close()
		return fmt.Errorf("server sent error during registration: %s", errMsg.Message)
	}

	if msg.Type != protocol.MessageConnectionRegisterResp {
		transp.Close()
		return fmt.Errorf("unexpected response type during registration: %s != %s",
			protocol.MessageConnectionRegisterResp.String(),
			msg.Type.String())
	}

	connectionResponse := protocol.ConnectionRegisterResp{}
	protocol.Unmarshal(&connectionResponse, msg)
	if !connectionResponse.Success {
		transp.Close()
		return fmt.Errorf("server rejected connection: %s", connectionResponse.Message)
	}

	backend.Subdomain = connectionResponse.Subdomain

	c.logger.WithFields(logrus.Fields{
		"subdomain": backend.Subdomain,
	}).Info("Registered with server")
	return nil
}

func (c *Client) worker(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Stopping connection manager worker")
			return nil
		default:
		}

		if c.shouldWaitForReconnection(ctx) {
			continue
		}

		strm, err := c.conn.AcceptStream(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			c.logger.WithError(err).Error("Failed to accept stream from server")
			c.disconnect()
			continue
		}

		c.handleAcceptedStream(ctx, strm)
	}
}

func (c *Client) shouldWaitForReconnection(ctx context.Context) bool {
	if c.conn == nil || c.conn.IsClosed() {
		c.logger.Warn("Connection is closed, waiting for reconnection")
		select {
		case <-ctx.Done():
			return false
		case <-time.After(c.reconnectDelay):
			return true
		}
	}
	return false
}

func (c *Client) handleAcceptedStream(ctx context.Context, strm transport.Stream) {
	strmLogger := c.logger.WithFields(logrus.Fields{
		"client_id": strm.ID(),
	})

	strmLogger.Debug("Accepted new stream from server")

	go func() {
		if err := c.handleStream(ctx, strm, strmLogger); err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
				strmLogger.WithError(err).Error("Failed to handle stream")
			}
		}
	}()
}

func (c *Client) reconnectLoop(ctx context.Context) {
	attemptCount := 0
	reconnectTimer := time.NewTimer(0)
	defer reconnectTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Stopping reconnect loop")
			return
		case <-reconnectTimer.C:
		}

		if c.conn == nil || c.conn.IsClosed() {
			attemptCount++
			exponentialFactor := math.Pow(2, float64(attemptCount-1))
			maxRetry := 300 * time.Second
			nextRetry := time.Duration(math.Min(
				float64(c.reconnectDelay)*exponentialFactor,
				float64(maxRetry),
			))

			c.mu.Lock()
			c.logger.Warnf(
				"No active connections. Reconnecting in %v (attempt %d)",
				nextRetry, attemptCount,
			)

			transp, err := transport.New(c.config.ServerAddr)
			if err != nil {
				c.mu.Unlock()
				c.logger.WithError(err).Warnf(
					"Failed to create transport (attempt %d)",
					attemptCount,
				)
				reconnectTimer.Reset(nextRetry)
				continue
			}

			// Don't assign c.conn until register succeeds to avoid orphan transports
			c.registerWithTransport(transp)
			c.mu.Lock()
			c.conn = transp
			c.mu.Unlock()

			c.logger.Info("Reconnected")
			attemptCount = 0
		}
	}
}

// Stop gracefully stops the client.
func (c *Client) Stop() {
	c.disconnect()
}

// disconnect closes all connections.
func (c *Client) disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connWrapper != nil {
		c.connWrapper.Close()
		c.connWrapper = nil
	}
	if c.conn == nil {
		return
	}
	c.logger.Info("Closing connection manager")
	c.conn.Close()

	c.conn = nil
}

func (c *Client) getBackend(subdomain string) *BackendConfig {
	for _, backend := range c.config.Backend {
		if backend.Subdomain == subdomain {
			return backend
		}
	}
	return nil
}
