package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
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
	if config.Token != "" {
		m.SetTokenValidator(func(token string) bool { return token == config.Token })
	}

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

	s.startPprofIfEnabled(ctx)
	errChan := make(chan error)

	wg := &sync.WaitGroup{}
	wg.Add(1)

	httpServer := s.newHTTPServer()
	go func() {
		logrus.Infof("starting HTTP/S server on %s", httpServer.Addr)
		var err error
		if httpServer.TLSConfig != nil {
			// cert and key are provided by the TLSConfig.GetCertificate function
			err = httpServer.ListenAndServeTLS("", "")
		} else {
			err = httpServer.ListenAndServe()
		}

		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- fmt.Errorf("failed to start http server: %w", err)
		}
	}()

	go func() {
		<-ctx.Done()
		logrus.Info("Server context done, shutting down http server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logrus.WithError(err).Warn("http server shutdown error")
		}
	}()

	go s.StartQUICServer(ctx, errChan, wg)
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

func (s *Server) newHTTPServer() *http.Server {
	addr := portToAddr(s.config.ServerPort)
	server := &http.Server{
		Addr:    addr,
		Handler: s.connManager,
	}

	if s.config.Cert.Enabled {
		logrus.Infof("Setting up TLS for domain %s", s.config.Domain)
		certInfo := s.certInfo()

		tlsConfig, err := certmanager.GetTLSConfigWithLetsEncrypt(certInfo)
		if err != nil {
			logrus.WithError(err).Fatal("failed to get TLS config")
		}
		server.TLSConfig = tlsConfig
	}
	return server
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
	defer func() {
		if err := quicServer.Close(); err != nil {
			logrus.WithError(err).Warn("failed to close QUIC server")
		}
	}()

	go func() {
		<-ctx.Done()
		_ = quicServer.Close()
	}()

	logrus.Infof("QUIC server started on %s", quicServer.Addr())

	for {
		conn, err := quicServer.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
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

		go func(conn *quic.Conn) {
			defer func() {
				if err := conn.CloseWithError(0, ""); err != nil {
					logrus.WithError(err).Warn("failed to close QUIC connection")
				}
			}()
			s.connManager.HandleConnection(transp)
		}(conn)
	}
}

func (s *Server) startPprofIfEnabled(ctx context.Context) {
	addr := os.Getenv("GUNNEL_PPROF_ADDR")
	if addr == "" {
		if os.Getenv("GUNNEL_PPROF") == "" {
			return
		}
		addr = "127.0.0.1:6060"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logrus.Infof("pprof listener enabled on %s (set GUNNEL_PPROF_ADDR to change)", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logrus.WithError(err).Warn("pprof server exited")
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logrus.WithError(err).Warn("pprof server shutdown error")
		}
	}()
}

func portToAddr(port int) string {
	return fmt.Sprintf(":%d", port)
}
