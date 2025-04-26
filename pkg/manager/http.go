package manager

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/sirupsen/logrus"
	"github.com/snakeice/gunnel/pkg/protocol"
	"github.com/snakeice/gunnel/pkg/tunnel"
)

// HandleHTTPConnection handles an HTTP connection.
func (r *Manager) HandleHTTPConnection(conn net.Conn) {
	// Reconstruct the request
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	defer writer.Flush()

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

	logger.Debug("Processing HTTP request")

	stream, err := r.Acquire(subdomain)
	if err != nil {
		if errors.Is(err, ErrNoConnection) {
			logger.Error("No service found for subdomain")
			SendHttpResponse(conn, 404, "No service found for subdomain %s", subdomain)
			return
		}

		logger.WithError(err).Error("Failed to acquire transport")

		SendHttpResponse(conn, 503, "Service temporarily unavailable: %s", err)
		return
	}

	logger = logger.WithFields(logrus.Fields{
		"stream_id": stream.ID(),
	})

	defer r.Release(subdomain, stream)

	// Send begin connection message
	beginMsg := &protocol.BeginConnection{
		Subdomain: subdomain,
	}

	logger.Debug("Sending begin connection message")

	if err = stream.Send(beginMsg); err != nil {
		logger.WithError(err).Error("Failed to send begin connection message")
		SendHttpResponse(conn, 500, "Failed to send begin connection message: %s", err)
		return
	}

	// Send request data directly through transport
	reqBytes, err := httputil.DumpRequest(req, true)
	if err != nil {
		logger.WithError(err).Error("Failed to dump request")
		SendHttpResponse(conn, 500, "Failed to dump request: %s", err)
		return
	}

	// Create a buffer to store the response
	var respBuf bytes.Buffer

	// Start a goroutine to read the response from the client
	respChan := make(chan error)
	readyChan := make(chan struct{})

	go func() {
		// Read until we get an end connection message
		for {
			msg, err := stream.Receive()
			if err != nil {
				logger.WithError(err).Error("Failed to read message from client")
				respChan <- fmt.Errorf("failed to read message: %w", err)
				return
			}

			switch msg.Type { //nolint:exhaustive // this switch not exhaustive
			case protocol.MessageEndStream:
				logger.WithError(err).Debug("Received end connection message")
				// We're done receiving data
				respChan <- nil
				return

			case protocol.MessageConnectionReady:
				readyMsg := protocol.ConnectionReady{}
				protocol.Unmarshal(&readyMsg, msg)
				logger.Debug("Received connection ready from proxing message")
				readyChan <- struct{}{}

				// If it's not an end message, it's data
				if _, err = respBuf.Write(msg.Payload); err != nil {
					logger.WithError(err).Error("Failed to write to buffer")
					respChan <- fmt.Errorf("failed to write to buffer: %w", err)
					return
				}

				tun := tunnel.NewTunnelWithLocal(conn, stream)

				if err = tun.Proxy(); err != nil {
					logger.WithError(err).Error("Failed to proxy data")
					respChan <- fmt.Errorf("failed to proxy data: %w", err)
					return
				}

				if err = tun.Close(); err != nil {
					logger.WithError(err).Error("Failed to close tunnel")
					respChan <- fmt.Errorf("failed to close tunnel: %w", err)
					return
				}

				logger.WithFields(logrus.Fields{
					"data_size":  len(msg.Payload),
					"total_size": respBuf.Len(),
				}).Debug("Received data from client")
				respChan <- nil
				return
			}
		}
	}()

	select {
	case <-readyChan:
		logger.Debug("Client connection ready for proxying")
	case <-time.After(streamAcceptTimeout):
		logger.Error("Client connection not ready in time")
	case err := <-respChan:
		logger.WithError(err).Error("Failed to receive response from client")
		SendHttpResponse(conn, 500, "Failed to receive response: %s", err)
		return
	}

	// Send the request data
	logger.WithField("req_size", len(reqBytes)).Debug("Sending request data to client")

	if _, err := stream.Write(reqBytes); err != nil {
		logger.WithError(err).Error("Failed to send request data to client")
		SendHttpResponse(conn, 500, "Failed to send request data: %s", err)
		return
	}

	// Wait for the response to be fully received
	if err := <-respChan; err != nil {
		logger.WithError(err).Error("Failed to receive response from client")
		SendHttpResponse(conn, 500, "Failed to receive response: %s", err)
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
