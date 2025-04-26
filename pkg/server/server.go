package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/sirupsen/logrus"
	"github.com/snakeice/gunnel/pkg/certmanager"
	"github.com/snakeice/gunnel/pkg/manager"
	gunnelquic "github.com/snakeice/gunnel/pkg/quic"
	"github.com/snakeice/gunnel/pkg/signal"
	"github.com/snakeice/gunnel/pkg/transport"
	"github.com/snakeice/gunnel/pkg/webui"
)

const (
	connectionTimeout = 120 * time.Second
)

type Server struct {
	config *Config

	connManager *manager.Manager

	webUI *webui.WebUI
}

func NewServer(config *Config) *Server {
	m := manager.New()

	webUI := webui.NewWebUI(m)

	m.SetGunnelSubdomainHandler(webUI.HandleRequest)

	s := &Server{
		config:      config,
		webUI:       webUI,
		connManager: m,
	}

	return s
}

func (s *Server) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		signal.WaitInterruptSignal()

		logrus.Info("Received interrupt signal, shutting down")
		cancel()
	}()

	errChan := make(chan error)

	wg := &sync.WaitGroup{}
	wg.Add(2)

	go s.StartQUICServer(ctx, errChan, wg)
	go s.StartHTTPServer(ctx, errChan, wg)
	go s.updater(ctx, errChan)

	wg.Wait()
	logrus.Info("Server stopped")
	return nil
}

func (s *Server) certInfo() *certmanager.CertReqInfo {
	return &certmanager.CertReqInfo{
		Domain: s.config.Domain,
		Email:  s.config.Cert.Email,
	}
}

func (s *Server) StartHTTPServer(ctx context.Context, errChan chan error, wg *sync.WaitGroup) {
	defer wg.Done()

	var listener net.Listener
	var err error

	addr := portToAddr(s.config.ServerPort)

	if s.config.Cert.Enabled {
		logrus.Infof("Setting up TLS for domain %s", s.config.Domain)
		certInfo := s.certInfo()

		tlsConfig, err := certmanager.GetTLSConfigWithLetsEncrypt(certInfo)
		if err != nil {
			errChan <- fmt.Errorf("failed to get TLS config: %w", err)
			return
		}

		listener, err = tls.Listen("tcp", addr, tlsConfig)
		if err != nil {
			errChan <- fmt.Errorf("failed to start TLS user server: %w", err)
			return
		}
		logrus.Infof(
			"HTTPS server started on %s with TLS for domain %s",
			listener.Addr(),
			s.config.Domain,
		)
	} else {
		listener, err = net.Listen("tcp", addr)
		if err != nil {
			errChan <- fmt.Errorf("failed to start user server: %w", err)
			return
		}
		logrus.Infof("HTTP server started on %s", listener.Addr())
	}
	defer listener.Close()

	for {
		select {
		case <-ctx.Done():
			logrus.Info("Server context done, shutting down http server")
			return
		default:
			conn, err := listener.Accept()
			if err != nil {
				logrus.WithError(err).Error("Failed to accept user connection")
				continue
			}

			conn.SetDeadline(time.Now().Add(connectionTimeout))

			go func(conn net.Conn) {
				defer conn.Close()
				s.connManager.HandleHTTPConnection(conn)
			}(conn)
		}
	}
}

func (s *Server) updater(ctx context.Context, errChan chan error) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.webUI.UpdateStats()
		case err := <-errChan:
			if err != nil {
				logrus.WithError(err).Error("Failed to server")
			}

		case <-ctx.Done():
			logrus.Info("Server context done, shutting down")
			return

		default:
			time.Sleep(1 * time.Second)
		}
	}
}

func (s *Server) StartQUICServer(ctx context.Context, errChan chan error, wg *sync.WaitGroup) {
	defer wg.Done()

	quicServer, err := gunnelquic.NewServer(portToAddr(s.config.QuicPort))
	if err != nil {
		errChan <- fmt.Errorf("failed to start QUIC server: %w", err)
		return
	}
	defer quicServer.Close()

	logrus.Infof("QUIC server started on %s", quicServer.Addr())

	for {
		conn, err := quicServer.Accept()
		if err != nil {
			if !errors.Is(err, context.DeadlineExceeded) {
				logrus.WithError(err).Error("Failed to accept client connection")
			}
			continue
		}

		transp, err := transport.NewFromServer(ctx, conn)
		if err != nil {
			logrus.WithError(err).Error("Failed to create transport wrapper")
			continue
		}

		go func(conn quic.Connection) {
			defer conn.CloseWithError(0, "")
			s.connManager.HandleConnection(transp)
		}(conn)
	}
}

func portToAddr(port int) string {
	return fmt.Sprintf(":%d", port)
}
