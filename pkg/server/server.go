package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/sirupsen/logrus"
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
	serverPort int
	clientPort int

	serverRouter *manager.Manager

	webUI *webui.WebUI
}

func NewServer(serverPort, clientPort int, serverProtocol string, webUIPort int) *Server {
	r := manager.New()

	webUI := webui.NewWebUI(r, portToAddr(webUIPort))

	return &Server{
		serverPort:   serverPort,
		clientPort:   clientPort,
		webUI:        webUI,
		serverRouter: r,
	}
}

func (s *Server) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		signal.WaitInterruptSignal()

		logrus.Info("Received interrupt signal, shutting down")
		cancel()
		s.webUI.Stop()
	}()

	errChan := make(chan error)

	wg := &sync.WaitGroup{}
	wg.Add(3)

	go s.StartWebUI(errChan, wg)
	go s.StartQUICServer(ctx, errChan, wg)
	go s.StartHTTPServer(errChan, wg)
	go s.updater(ctx, errChan)
	go s.serverRouter.Start(ctx)

	wg.Wait()
	logrus.Info("Server stopped")
	return nil
}

func (s *Server) StartHTTPServer(errChan chan error, wg *sync.WaitGroup) {
	defer wg.Done()

	userServer, err := net.Listen("tcp", portToAddr(s.serverPort))
	if err != nil {
		errChan <- fmt.Errorf("failed to start user server: %w", err)
		return
	}
	defer userServer.Close()

	logrus.Infof("User server started on %s", userServer.Addr())

	for {
		conn, err := userServer.Accept()
		if err != nil {
			logrus.WithError(err).Error("Failed to accept user connection")
			continue
		}

		conn.SetDeadline(time.Now().Add(connectionTimeout))

		go func(conn net.Conn) {
			defer conn.Close()
			s.serverRouter.HandleHTTPConnection(conn)
		}(conn)
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

	quicServer, err := gunnelquic.NewServer(portToAddr(s.clientPort))
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
			s.serverRouter.HandleConnection(transp)
		}(conn)
	}
}

func (s *Server) StartWebUI(errChan chan error, wg *sync.WaitGroup) {
	defer wg.Done()

	logrus.Infof("WebUI server started on [::]%s", s.webUI.Addr())

	if err := s.webUI.Start(); err != nil {
		logrus.WithError(err).Error("Failed to start web UI")
		errChan <- err
		return
	}
}

func portToAddr(port int) string {
	return fmt.Sprintf(":%d", port)
}
