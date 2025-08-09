package manager

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/sirupsen/logrus"
	"github.com/snakeice/gunnel/pkg/protocol"
	"github.com/snakeice/gunnel/pkg/transport"
)

// HandleHTTPConnection handles an HTTP connection.
func (r *Manager) HandleHTTPConnection(conn net.Conn) {
	// Reconstruct the request
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	defer func() {
		if err := writer.Flush(); err != nil {
			logrus.WithError(err).Warn("Failed to flush writer")
		}
	}()
	defer func() {
		if cerr := conn.Close(); cerr != nil && !errors.Is(cerr, net.ErrClosed) {
			logrus.WithError(cerr).Warn("Failed to close connection")
		}
	}()

	req, err := http.ReadRequest(reader)
	if err != nil {
		logrus.WithError(err).Error("Failed to read HTTP request")
		SendHttpResponse(conn, 400, "Failed to read request: %s", err)
		return
	}

	subdomain := extractSubdomain(req)
	if subdomain == "gunnel" {
		r.handleGunnel(conn, req)
		return
	}

	logger := logrus.WithFields(logrus.Fields{
		"subdomain": subdomain,
		"req":       fmt.Sprintf("%s %s", req.Method, req.URL),
	})

	logger.Infof("%s %s", req.Method, req.URL)

	if err := r.handleProxyFlow(conn, req, subdomain, logger); err != nil {
		logger.WithError(err).Error("Proxy flow failed")
		status := 500
		if errors.Is(err, ErrNoConnection) {
			status = 404
		}
		SendHttpResponse(conn, status, "%s", err)
		return
	}
	// logger.WithField("resp_size", respBuf.Len()).Debug("Received response from client")

	// // Send response to client
	// resp, err := http.ReadResponse(bufio.NewReader(&respBuf), req)
	// if err != nil {
	// 	logger.WithError(err).Error("Failed to read response")
	// 	SendHttpResponse(conn, 500, "Failed to read response: %s", err)
	// 	return
	// }

	// logger.WithField("status_code", resp.StatusCode).Debug("Received response from client")

	// if err := resp.Write(conn); err != nil {
	// 	logger.WithError(err).Error("Failed to write response to client")
	// 	SendHttpResponse(conn, 500, "Failed to write response: %s", err)
	// 	return
	// }

	// logger.Debug("Successfully wrote response to client")
}

func (m *Manager) handleGunnel(conn net.Conn, req *http.Request) {
	if m.gunnelSubdomainHandler == nil {
		SendHttpResponse(conn, 500, "Gunnel subdomain handler not set")
		return
	}

	resWriter := NewResponseWriterWrapper(conn)

	if !certmagic.DefaultACME.HandleHTTPChallenge(resWriter, req) {
		m.gunnelSubdomainHandler(resWriter, req)
	}

	resWriter.Flush()
}

// handleProxyFlow coordinates acquiring a stream, beginning the connection,
// waiting for readiness, and performing bidirectional proxying.
func (r *Manager) handleProxyFlow(
	conn net.Conn,
	req *http.Request,
	subdomain string,
	baseLogger *logrus.Entry,
) error {
	logger := baseLogger

	stream, err := r.Acquire(subdomain)
	if err != nil {
		if errors.Is(err, ErrNoConnection) {
			logger.Error("No service found for subdomain")
			return fmt.Errorf("no service found for subdomain %s", subdomain)
		}
		logger.WithError(err).Error("Failed to acquire transport")
		return fmt.Errorf("service temporarily unavailable: %w", err)
	}
	defer r.Release(subdomain, stream)

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
	go r.readClientMessagesAndProxy(conn, stream, readyChan, respChan, logger)

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
	defer resp.Body.Close()

	if err := resp.Write(conn); err != nil {
		logger.WithError(err).Error("Failed to write response to client")
		return fmt.Errorf("failed to write response to client: %w", err)
	}

	return nil
}

// readClientMessagesAndProxy waits for ConnectionReady, then signals readiness.
// Any error before readiness is sent on respChan.
func (r *Manager) readClientMessagesAndProxy(
	conn net.Conn,
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
			respChan <- fmt.Errorf("connection ended before ready")
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
