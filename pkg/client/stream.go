package client

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/snakeice/gunnel/pkg/protocol"
	"github.com/snakeice/gunnel/pkg/transport"
)

func (c *Client) handleStream(
	ctx context.Context,
	strm transport.Stream,
	logger *logrus.Entry,
) error {
	defer func() {
		if r := recover(); r != nil {
			logger.WithField("panic", r).Error("Stream handler panicked")
		}
		logger.Trace("Releasing stream")
		if err := strm.Close(); err != nil {
			logger.WithError(err).Error("Failed to close stream")
		}
	}()

	for {
		select {
		case <-ctx.Done():
			logger.Infof("Stopping stream %s handler", strm.ID())
			return nil
		default:
		}

		if err := c.waitOrReceiveAndHandle(ctx, strm, logger); err != nil {
			return err
		}

		logger.Debug("Request processed, closing stream")
	}
}

func (c *Client) waitOrReceiveAndHandle(
	ctx context.Context,
	strm transport.Stream,
	logger *logrus.Entry,
) error {
	// Read message
	msg, err := strm.Receive()
	if err != nil {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			return nil // Context cancelled, exit gracefully
		default:
		}

		// EOF is expected when stream ends normally
		if errors.Is(err, io.EOF) {
			logger.Debug("Stream ended normally")
			return err // Return EOF to exit the loop and close the stream
		}
		return fmt.Errorf("failed to read message from server, closing connection: %w", err)
	}

	logger.WithField("msg_size", msg.Length).Debug("Received message from server")

	return c.dispatchMessage(strm, logger, msg)
}

func (c *Client) dispatchMessage(
	strm transport.Stream,
	logger *logrus.Entry,
	msg *protocol.Message,
) error {
	switch msg.Type { //nolint:exhaustive // not all message types need handling here
	case protocol.MessageBeginStream:
		return c.handleBeginStream(strm, logger, msg)

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
		return nil

	default:
		if err := strm.Send(protocol.NewErrorMessage("Unknown message type")); err != nil {
			logger.WithError(err).Error("Failed to send error message")
		}
		return fmt.Errorf("unknown message type: %s", msg.Type)
	}
}

func (c *Client) handleBeginStream(
	strm transport.Stream,
	baseLogger *logrus.Entry,
	msg *protocol.Message,
) error {
	beginMsg := protocol.BeginConnection{}
	protocol.Unmarshal(&beginMsg, msg)

	baseLogger.Debug("Received begin connection message")

	backend := c.getBackend(beginMsg.Subdomain)
	if backend == nil {
		baseLogger.WithField("subdomain", beginMsg.Subdomain).
			Error("No backend found for subdomain")
		return fmt.Errorf("no backend found for subdomain: %s", beginMsg.Subdomain)
	}

	logger := baseLogger.WithFields(logrus.Fields{
		"subdomain": beginMsg.Subdomain,
		"client_id": strm.ID(),
	})

	readyMsg := &protocol.ConnectionReady{
		Subdomain: beginMsg.Subdomain,
	}
	if err := strm.Send(readyMsg); err != nil {
		logger.Error("Failed to send connection ready message")
		return fmt.Errorf("failed to send connection ready message: %w", err)
	}

	req, err := http.ReadRequest(strm.BufferedReader())
	if err != nil {
		return fmt.Errorf("failed to read request from stream: %w", err)
	}

	if !backend.IsPathAllowed(req.URL.Path) {
		logger.WithField("path", req.URL.Path).Warn("Path not allowed")
		forbiddenResp := &http.Response{
			StatusCode: http.StatusForbidden,
			Status:     "403 Forbidden",
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("403 Forbidden: path not allowed")),
		}
		forbiddenResp.Header.Set("Content-Type", "text/plain")
		if err := forbiddenResp.Write(strm); err != nil {
			logger.WithError(err).Error("Failed to write forbidden response")
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d := &net.Dialer{Timeout: 10 * time.Second}
	backendConn, err := d.DialContext(ctx, "tcp", backend.getAddr())
	if err != nil {
		return fmt.Errorf("failed to connect to backend: %w", err)
	}
	defer func() {
		if err := backendConn.Close(); err != nil {
			logger.WithError(err).Warn("Failed to close backend connection")
		}
	}()

	// Write request to backend
	if err := req.Write(backendConn); err != nil {
		return fmt.Errorf("failed to write request to backend: %w", err)
	}

	// Read response from backend
	resp, err := http.ReadResponse(bufio.NewReader(backendConn), req)
	if err != nil {
		return fmt.Errorf("failed to read response from backend: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.WithError(err).Warn("Failed to close response body")
		}
	}()

	// Write response back to stream
	if err := resp.Write(strm); err != nil {
		return fmt.Errorf("failed to write response to stream: %w", err)
	}

	return nil
}
