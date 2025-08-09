package quic

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	"github.com/quic-go/quic-go"
)

const (
	handshakeTimeout = 60 * time.Second
	keepAlivePeriod  = 15 * time.Second
	maxIdleTimeout   = 120 * time.Second
)

// Server represents a QUIC server.
type Server struct {
	listener *quic.Listener
}

// Client represents a QUIC client.
type Client struct {
	conn quic.Connection
}

// NewServer creates a new QUIC server.
func NewServer(addr string) (*Server, error) {
	tlsConfig, err := generateTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to generate TLS config: %w", err)
	}

	config := generateQuicConfig()

	listener, err := quic.ListenAddr(addr, tlsConfig, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create QUIC listener: %w", err)
	}

	return &Server{
		listener: listener,
	}, nil
}

// NewClient creates a new QUIC client.
func NewClient(addr string) (*Client, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // For testing purposes only
		MinVersion:         tls.VersionTLS13,
	}

	config := generateQuicConfig()

	conn, err := quic.DialAddr(context.Background(), addr, tlsConfig, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create QUIC connection: %w", err)
	}

	return &Client{
		conn: conn,
	}, nil
}

func NewClientWrapper(conn quic.Connection) *Client {
	return &Client{
		conn: conn,
	}
}

// Accept accepts a new QUIC connection.
func (s *Server) Accept() (quic.Connection, error) {
	ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
	defer cancel()
	return s.listener.Accept(ctx)
}

// Close closes the server.
func (s *Server) Close() error {
	return s.listener.Close()
}

// Addr returns the address of the server.
func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

// OpenStream opens a new QUIC stream.
func (c *Client) OpenStream() (quic.Stream, error) {
	return c.conn.OpenStream()
}

// AcceptStream accepts a new QUIC stream.
func (c *Client) AcceptStream(ctx context.Context) (quic.Stream, error) {
	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	return c.conn.AcceptStream(ctx)
}

// Close closes the client connection.
func (c *Client) Close() error {
	return c.conn.CloseWithError(0, "")
}

// GetStreamID returns the stream ID of the client connection.
func (c *Client) Addr() string {
	return c.conn.LocalAddr().String()
}

// generateTLSConfig generates a self-signed TLS certificate for QUIC.
func generateTLSConfig() (*tls.Config, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}

	keyPEM := pem.EncodeToMemory(
		&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)},
	)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{tlsCert},
	}, nil
}

func generateQuicConfig() *quic.Config {
	return &quic.Config{
		HandshakeIdleTimeout:  handshakeTimeout,
		KeepAlivePeriod:       keepAlivePeriod,
		MaxIdleTimeout:        maxIdleTimeout,
		MaxIncomingStreams:    5000,
		MaxIncomingUniStreams: 5000,
		Allow0RTT:             true,
	}
}
