package transport

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/sirupsen/logrus"
	gunnelquic "github.com/snakeice/gunnel/pkg/quic"
)

type StreamHandler func(stream quic.Stream) error

type Transport interface {
	Addr() string
	Close() error
	Acquire() (Stream, error)
	Release(stream Stream) error
	AcceptStream(ctx context.Context) (Stream, error)
	Len() int
	LenActive(subdomain ...string) int
	Root() Stream
	IsClosed() bool

	ImServer() bool
}

// connectionTransport represents a transport connection.
type connectionTransport struct {
	root    *streamClient // Primary stream
	closed  bool
	client  *gunnelquic.Client
	streams []*streamClient
	mu      sync.RWMutex

	server bool
}

func New(addr string) (Transport, error) {
	client, err := gunnelquic.NewClient(addr)
	if err != nil {
		return nil, fmt.Errorf("failed to create QUIC client: %w", err)
	}

	return newWrapper(client, false)
}

func newWrapper(client *gunnelquic.Client, isServer bool) (*connectionTransport, error) {
	transp := &connectionTransport{
		client:  client,
		streams: []*streamClient{},
		closed:  false,
		server:  isServer,
	}

	if !isServer {
		stream, err := client.OpenStream()
		if err != nil {
			return nil, fmt.Errorf("failed to open stream: %w", err)
		}

		handled := newStreamHandler(stream)
		transp.streams = append(transp.streams, handled)
		transp.root = handled
	}

	go transp.cleanupInactiveStreams(5 * time.Minute)

	return transp, nil
}

func NewFromServer(ctx context.Context, client quic.Connection) (Transport, error) {
	conn := gunnelquic.NewClientWrapper(client)

	transp, err := newWrapper(conn, true)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport wrapper: %w", err)
	}

	strm, err := transp.client.AcceptStream(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to accept stream: %w", err)
	}

	handler := newStreamHandler(strm)
	transp.root = handler
	transp.streams = append(transp.streams, handler)

	return transp, nil
}

func (t *connectionTransport) Acquire() (Stream, error) {
	stream, err := t.client.OpenStream()
	if err != nil {
		return nil, fmt.Errorf("failed to open stream: %w", err)
	}

	streamHandler := newStreamHandler(stream)
	if streamHandler == nil {
		return nil, errors.New("failed to create stream handler")
	}

	t.mu.Lock()
	t.streams = append(t.streams, streamHandler)
	t.mu.Unlock()

	return streamHandler, nil
}

func (t *connectionTransport) AcceptStream(ctx context.Context) (Stream, error) {
	stream, err := t.client.AcceptStream(ctx)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("failed to accept stream: %w", err)
	} else if errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("accept stream timed out: %w", err)
	}

	streamHandler := newStreamHandler(stream)

	t.mu.Lock()
	t.streams = append(t.streams, streamHandler)
	t.mu.Unlock()

	return streamHandler, nil
}

func (t *connectionTransport) Release(stream Stream) error {
	if err := stream.Close(); err != nil {
		return fmt.Errorf("failed to close stream: %w", err)
	}

	return nil
}

func (t *connectionTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true

	if err := t.root.Close(); err != nil {
		return fmt.Errorf("failed to close stream: %w", err)
	}

	if t.client == nil {
		return nil
	}

	if err := t.client.Close(); err != nil {
		return fmt.Errorf("failed to close client: %w", err)
	}

	return t.client.Close()
}

func (t *connectionTransport) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.streams)
}

func (t *connectionTransport) LenActive(subdomain ...string) int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var count = 0
	sub := ""
	if len(subdomain) > 0 {
		sub = subdomain[0]
	}
	for _, stream := range t.streams {
		if stream.metricsInfo.IsActive && (sub == "" || stream.metricsInfo.Subdomain == sub) {
			count++
		}
	}

	return count
}

// cleanupInactiveStreams removes streams that have been inactive for too long.
func (t *connectionTransport) cleanupInactiveStreams(maxInactive time.Duration) {
	timer := time.NewTicker(maxInactive)
	defer timer.Stop()

	for range timer.C {
		if len(t.streams) == 0 {
			continue
		}

		t.mu.RLock()
		for id, stream := range t.streams {
			if !stream.metricsInfo.IsActive &&
				time.Since(stream.metricsInfo.LastActive) >= maxInactive {
				logrus.Infof("Removing inactive stream %s", stream.ID())

				if err := stream.Close(); err != nil {
					logrus.WithError(err).Errorf("Failed to close stream %s", stream.ID())
				}

				t.streams = slices.Delete(t.streams, id, id+1)
			}
		}
		t.mu.RUnlock()
	}
}

// Addr implements TransportServer.
func (t *connectionTransport) Addr() string {
	if t.client == nil {
		return ""
	}
	return t.client.Addr()
}

func (t *connectionTransport) IsClosed() bool {
	return t.closed
}

func (t *connectionTransport) Root() Stream {
	if t.root == nil {
		return nil
	}
	return t.root
}

func (t *connectionTransport) ImServer() bool {
	return t.server
}
