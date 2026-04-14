package manager

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/sirupsen/logrus"
	"github.com/snakeice/gunnel/pkg/protocol"
	"github.com/snakeice/gunnel/pkg/transport"
)

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
		m.handleProxyError(w, req, subdomain, logger, err)
	}
}

func (m *Manager) handleProxyError(
	w http.ResponseWriter,
	req *http.Request,
	subdomain string,
	logger *logrus.Entry,
	err error,
) {
	logger.WithError(err).Error("Proxy flow failed")
	status := http.StatusInternalServerError

	if errors.Is(err, ErrNoConnection) || errors.Is(err, ErrSubdomainNotFound) {
		status = http.StatusNotFound
		if m.honeypot != nil && subdomain != "" {
			m.serveHoneypotResponse(w, req, subdomain, logger)
			return
		}
	}
	http.Error(w, err.Error(), status)
}

func (m *Manager) serveHoneypotResponse(
	w http.ResponseWriter,
	req *http.Request,
	subdomain string,
	logger *logrus.Entry,
) {
	ip := extractClientIP(req)
	m.honeypot.RecordRequest(req, subdomain)

	if m.honeypot.IsSuspicious(ip) {
		delay := m.honeypot.GetDelay(ip)
		if delay > 0 {
			time.Sleep(delay)
		}

		body, contentType := m.honeypot.GetFakeResponse(req)
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)

		if _, writeErr := w.Write(body); writeErr != nil { //nolint:gosec // honeypot
			logger.WithError(writeErr).Warn("Failed to write fake response")
		}

		return
	}
}

func extractClientIP(req *http.Request) string {
	xff := req.Header.Get("X-Forwarded-For")
	if xff != "" {
		parts := splitCSV(xff)
		if len(parts) > 0 {
			return trimSpace(parts[0])
		}
	}

	xri := req.Header.Get("X-Real-IP")
	if xri != "" {
		return trimSpace(xri)
	}

	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
}

func splitCSV(s string) []string {
	var result []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			part := trimSpace(s[start:i])
			if part != "" {
				result = append(result, part)
			}
			start = i + 1
		}
	}
	return result
}

func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
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

	beginMsg := &protocol.BeginConnection{Subdomain: subdomain}
	logger.Debug("Sending begin connection message")
	if err = stream.Send(beginMsg); err != nil {
		logger.WithError(err).Error("Failed to send begin connection message")
		return fmt.Errorf("failed to send begin connection message: %w", err)
	}

	readyChan := make(chan struct{})
	respChan := make(chan error)

	go m.readClientMessagesAndProxy(stream, readyChan, respChan, logger)

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

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)

	if _, err := io.Copy(w, resp.Body); err != nil {
		logger.WithError(err).Error("Failed to write response body to client")
		return nil
	}

	return nil
}

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
