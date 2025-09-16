package manager

import (
	"bufio"
	"errors"
	"fmt"

	"net/http"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/sirupsen/logrus"
	"github.com/snakeice/gunnel/pkg/protocol"
	"github.com/snakeice/gunnel/pkg/transport"
)

// ServeHTTP handles an HTTP request and proxies it to the correct backend.
func (m *Manager) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	subdomain := extractSubdomain(req)
	if subdomain == "gunnel" {
		m.handleGunnel(w, req)
		return
	}

	logger := logrus.WithFields(logrus.Fields{
		"subdomain": subdomain,
		"req":       fmt.Sprintf("%s %s", req.Method, req.URL),
	})

	logger.Infof("%s %s", req.Method, req.URL)

	if err := m.handleProxyFlow(w, req, subdomain, logger); err != nil {
		logger.WithError(err).Error("Proxy flow failed")
		status := http.StatusInternalServerError
		if errors.Is(err, ErrNoConnection) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
	}
}

func (m *Manager) handleGunnel(w http.ResponseWriter, req *http.Request) {
	if m.gunnelSubdomainHandler == nil {
		http.Error(w, "Gunnel subdomain handler not set", http.StatusInternalServerError)
		return
	}

	if !certmagic.DefaultACME.HandleHTTPChallenge(w, req) {
		m.gunnelSubdomainHandler(w, req)
	}
}

// handleProxyFlow coordinates acquiring a stream, beginning the connection,
// waiting for readiness, and performing bidirectional proxying.
func (m *Manager) handleProxyFlow(
	w http.ResponseWriter,
	req *http.Request,
	subdomain string,
	baseLogger *logrus.Entry,
) error {
	logger := baseLogger

	stream, err := m.Acquire(subdomain)
	if err != nil {
		if errors.Is(err, ErrNoConnection) {
			logger.Error("No service found for subdomain")
			return fmt.Errorf("no service found for subdomain %s", subdomain)
		}
		logger.WithError(err).Error("Failed to acquire transport")
		return fmt.Errorf("service temporarily unavailable: %w", err)
	}
	defer m.Release(subdomain, stream)

	logger = logger.WithFields(logrus.Fields{
		"stream_id": stream.ID(),
	})

	// Send begin connection message
	beginMsg := &protocol.BeginConnection{Subdomain: subdomain}
	logger.Debug("Sending begin connection message")
	if err = stream.Send(beginMsg); err != nil {
		logger.WithError(err).Error("Failed to send begin connection message")
		return fmt.Errorf("failed to send begin connection message: %w", err)
	}

	readyChan := make(chan struct{})
	respChan := make(chan error)

	// Reader goroutine: wait only for ConnectionReady, then return.
	go m.readClientMessagesAndProxy(stream, readyChan, respChan, logger)

	// Wait for readiness or error/timeout
	select {
	case <-readyChan:
		logger.Debug("Client connection ready for proxying")
	case <-time.After(streamAcceptTimeout):
		logger.Error("Client connection not ready in time")
		return errors.New("client connection not ready in time")
	case err := <-respChan:
		if err != nil {
			logger.WithError(err).Error("Failed before proxy start")
			return fmt.Errorf("failed before proxy start: %w", err)
		}
	}

	// Write the HTTP request to the stream, half-close the write side,
	// then read the HTTP response back and write it to the client connection.
	if err := req.Write(stream); err != nil {
		logger.WithError(err).Error("Failed to write request to stream")
		return fmt.Errorf("failed to write request to stream: %w", err)
	}

	if err := stream.CloseWrite(); err != nil {
		logger.WithError(err).Warn("Failed to half-close stream write side")
	}

	resp, err := http.ReadResponse(bufio.NewReader(stream), req)
	if err != nil {
		logger.WithError(err).Error("Failed to read response from stream")
		return fmt.Errorf("failed to read response: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.WithError(err).Warn("Failed to close response body")
		}
	}()

	if err := resp.Write(w); err != nil {
		logger.WithError(err).Error("Failed to write response to client")
		// The response has already been partially sent, so we can't send a
		// different error. The connection will be closed by the server.
		return nil
	}

	return nil
}

// readClientMessagesAndProxy waits for ConnectionReady, then signals readiness.
// Any error before readiness is sent on respChan.
func (m *Manager) readClientMessagesAndProxy(
	stream transport.Stream,
	readyChan chan<- struct{},
	respChan chan<- error,
	logger *logrus.Entry,
) {
	for {
		msg, err := stream.Receive()
		if err != nil {
			logger.WithError(err).Error("Failed to read message from client")
			respChan <- fmt.Errorf("failed to read message: %w", err)
			return
		}

		switch msg.Type { //nolint:exhaustive // not all messages are handled here; only those relevant to proxy lifecycle
		case protocol.MessageEndStream:
			logger.Debug("Received end connection message before ready")
			respChan <- errors.New("connection ended before ready")
			return

		case protocol.MessageConnectionReady:
			readyMsg := protocol.ConnectionReady{}
			protocol.Unmarshal(&readyMsg, msg)
			logger.Debug("Received connection ready from proxying message")
			readyChan <- struct{}{}
			return

		case protocol.MessageError:
			errMsg := protocol.ErrorMessage{}
			protocol.Unmarshal(&errMsg, msg)
			logger.WithField("error", errMsg.Message).Error("Server sent error")
			respChan <- fmt.Errorf("server error: %s", errMsg.Message)
			return

		default:
			logger.WithField("type", msg.Type.String()).Warn("Unexpected message type before ready")
		}
	}
}
