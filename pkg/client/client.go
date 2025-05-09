package client

import (
	"context"
	"fmt"
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
	mu             sync.Mutex
	reconnectDelay time.Duration
	protocol       protocol.Protocol
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

	go c.reconnectLoop()

	return c.worker(ctx)
}

func (c *Client) register() error {
	if c.conn == nil || c.conn.IsClosed() {
		return nil
	}

	for _, backend := range c.config.Backend {
		if err := c.registryBackend(backend); err != nil {
			c.logger.WithError(err).Error("Failed to register backend")
			continue
		}
	}

	c.logger.Info("All backends registered successfully")

	connection.New(c.conn).Start()

	return nil
}

// registerClient creates a new connection to the server.
func (c *Client) registryBackend(backend *BackendConfig) error {
	stream := c.conn.Root()
	reg := protocol.ConnectionRegister{
		Subdomain: backend.Subdomain,
		Host:      backend.Host,
		Port:      backend.Port,
		Protocol:  c.protocol,
	}

	c.logger.Debug("Registering client with server")

	if err := stream.Send(&reg); err != nil {
		c.disconnect()
		return fmt.Errorf("failed to send registration message: %w", err)
	}

	msg, err := stream.Receive()
	if err != nil {
		c.disconnect()
		return fmt.Errorf("failed to receive registration response: %w", err)
	}

	if msg.Type == protocol.MessageError {
		errMsg := protocol.ErrorMessage{}
		protocol.Unmarshal(&errMsg, msg)

		c.disconnect()
		return fmt.Errorf("server sent error during registration: %s", errMsg.Message)
	}

	if msg.Type != protocol.MessageConnectionRegisterResp {
		c.disconnect()
		return fmt.Errorf("unexpected response type during registration: %s != %s",
			protocol.MessageConnectionRegisterResp.String(),
			msg.Type.String())
	}

	connectionResponse := protocol.ConnectionRegisterResp{}
	protocol.Unmarshal(&connectionResponse, msg)
	if !connectionResponse.Success {
		c.disconnect()
		return fmt.Errorf("server rejected connection: %s", connectionResponse.Message)
	}

	c.mu.Lock()
	if backend.Subdomain == "" {
		backend.Subdomain = connectionResponse.Subdomain
	}
	c.mu.Unlock()

	c.logger.WithFields(logrus.Fields{
		"subdomain": backend.Subdomain,
	}).Info("Successfully registered with server")
	return nil
}

func (c *Client) worker(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Stopping connection manager worker")
			return nil
		default:
			if c.conn == nil || c.conn.IsClosed() {
				c.logger.Warn("Connection is closed, waiting for reconnection")
				time.Sleep(c.reconnectDelay)
				continue
			}

			strm, err := c.conn.AcceptStream(ctx)
			if err != nil {
				c.logger.Error("Failed to accept stream from server, closing connection")
				c.disconnect()
				continue
			}

			strmLogger := c.logger.WithFields(logrus.Fields{
				"client_id": strm.ID(),
			})

			strmLogger.Debug("Accepted new stream from server")

			go func() {
				if err := c.handleStream(ctx, strm, strmLogger); err != nil {
					strmLogger.WithError(err).Error("Failed to handle stream")
				}
			}()
		}
	}
}

// reconnectLoop handles reconnection attempts.
func (c *Client) reconnectLoop() {
	for {
		if c.conn == nil || c.conn.IsClosed() {
			c.mu.Lock()
			c.logger.Info("No active connections, attempting to reconnect")

			transp, err := transport.New(c.config.ServerAddr)
			if err != nil {
				c.logger.WithError(err).Error("Failed to create transport")
				time.Sleep(c.reconnectDelay)
				continue
			}

			c.conn = transp

			if err := c.register(); err != nil {
				c.logger.WithError(err).Error("Failed to reconnect")
			}
			c.mu.Unlock()
		}

		time.Sleep(c.reconnectDelay)
	}
}

// disconnect closes all connections.
func (c *Client) disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return
	}
	c.logger.Info("Closing connection manager")
	if err := c.conn.Close(); err != nil {
		c.logger.WithError(err).Error("Failed to close connection")
	}

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
