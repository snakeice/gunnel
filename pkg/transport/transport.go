package transport

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/sirupsen/logrus"
	gunnelquic "github.com/snakeice/gunnel/pkg/quic"
)

type StreamHandler func(stream *quic.Stream) error

type Transport interface {
	Addr() string
	Close()
	Acquire() (Stream, error)
	Release(stream Stream) error
	AcceptStream(ctx context.Context) (Stream, error)
	Len() int
	LenActive(subdomain ...string) int
	Root() Stream
	IsClosed() bool

	ImServer() bool
}

// PoolConfig holds stream pool configuration.
type PoolConfig struct {
	MaxIdle     int           // maximum idle streams to keep in pool
	IdleTimeout time.Duration // max time a stream can be idle before eviction
	Enabled     bool          // whether pool is enabled
}

// connectionTransport represents a transport connection.
type connectionTransport struct {
	root       *streamClient
	closed     bool
	client     *gunnelquic.Client
	streams    []*streamClient
	mu         sync.RWMutex
	server     bool
	ctx        context.Context
	cancelFunc context.CancelFunc

	// stream pool
	pool       chan *streamClient
	poolConfig PoolConfig
	poolHits   int64
	poolMisses int64
}

func New(addr string) (Transport, error) {
	client, err := gunnelquic.NewClient(addr)
	if err != nil {
		return nil, fmt.Errorf("failed to create QUIC client: %w", err)
	}

	return newWrapper(client, false)
}

func newWrapper(client *gunnelquic.Client, isServer bool) (*connectionTransport, error) {
	ctx, cancel := context.WithCancel(context.Background())

	transp := &connectionTransport{
		client:     client,
		streams:    []*streamClient{},
		closed:     false,
		server:     isServer,
		ctx:        ctx,
		cancelFunc: cancel,
		pool:       make(chan *streamClient, 100),
		poolConfig: PoolConfig{
			MaxIdle:     100,
			IdleTimeout: 30 * time.Second,
			Enabled:     true,
		},
	}

	if !isServer {
		stream, err := client.OpenStream()
		if err != nil {
			cancel()
			return nil, fmt.Errorf("failed to open stream: %w", err)
		}

		handled := newStreamHandler(stream)
		transp.streams = append(transp.streams, handled)
		transp.root = handled
	}

	go transp.cleanupLoop()

	return transp, nil
}

func NewFromServer(ctx context.Context, client *quic.Conn) (Transport, error) {
	conn := gunnelquic.NewClientFromConn(client)

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
	if t.poolConfig.Enabled {
		select {
		case pooledStream := <-t.pool:
			if pooledStream.isValid() {
				t.mu.Lock()
				t.streams = append(t.streams, pooledStream)
				t.mu.Unlock()
				atomic.AddInt64(&t.poolHits, 1)
				return pooledStream, nil
			}
			atomic.AddInt64(&t.poolMisses, 1)
		default:
		}
	}

	stream, err := t.client.OpenStream()
	if err != nil {
		return nil, fmt.Errorf("failed to open stream: %w", err)
	}

	streamHandler := newStreamHandler(stream)
	if streamHandler == nil {
		return nil, errors.New("failed to create stream handler")
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.streams = append(t.streams, streamHandler)

	return streamHandler, nil
}

func (t *connectionTransport) AcceptStream(ctx context.Context) (Stream, error) {
	stream, err := t.client.AcceptStream(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("accept stream timed out: %w", err)
		}
		return nil, fmt.Errorf("failed to accept stream: %w", err)
	}

	streamHandler := newStreamHandler(stream)

	t.mu.Lock()
	defer t.mu.Unlock()
	t.streams = append(t.streams, streamHandler)

	return streamHandler, nil
}

func (t *connectionTransport) Release(stream Stream) error {
	sc, ok := stream.(*streamClient)
	if !ok {
		return stream.Close()
	}

	if !sc.isValid() {
		return sc.Close()
	}

	sc.markIdle()

	select {
	case t.pool <- sc:
		return nil
	default:
	}

	return sc.Close()
}

func (t *connectionTransport) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true

	if t.cancelFunc != nil {
		t.cancelFunc()
	}

	if t.root != nil {
		if err := t.root.Close(); err != nil {
			logrus.WithError(err).Errorf("Failed to close root stream: %s", t.root.ID())
		}
	}

	close(t.pool)
	for range t.pool {
	}

	if t.client == nil {
		return
	}

	if err := t.client.Close(); err != nil {
		logrus.WithError(err).Errorf("Failed to close client: %s", t.client.Addr())
		return
	}

	for _, stream := range t.streams {
		if err := stream.Close(); err != nil {
			logrus.WithError(err).Errorf("Failed to close stream: %s", stream.ID())
		}
	}
	t.streams = nil

	logrus.Infof("Closed transport connection: %s", t.client.Addr())
	if t.server {
		logrus.Infof("Server transport connection closed: %s", t.client.Addr())
	} else {
		logrus.Infof("Client transport connection closed: %s", t.client.Addr())
	}
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
func (t *connectionTransport) findInactiveStreamIDs(maxInactive time.Duration) []int {
	var ids []int

	t.mu.RLock()
	for id, stream := range t.streams {
		if !stream.metricsInfo.IsActive &&
			time.Since(stream.metricsInfo.LastActive) >= maxInactive {
			ids = append(ids, id)
			logrus.Infof("Marking inactive stream %s for removal", stream.ID())
		}
	}
	t.mu.RUnlock()

	return ids
}

// removeStreams removes streams by index, closing them first.
// Indices must be sorted in reverse order for safe removal.
func (t *connectionTransport) removeStreams(indices []int) {
	if len(indices) == 0 {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Close all streams first
	for _, id := range indices {
		if id < len(t.streams) {
			stream := t.streams[id]
			if err := stream.Close(); err != nil {
				logrus.WithError(err).Warnf("Failed to close stream %s", stream.ID())
			}
		}
	}

	// Then remove them (in reverse order to maintain indices)
	slices.Sort(indices)
	slices.Reverse(indices)

	for _, id := range indices {
		if id < len(t.streams) {
			t.streams = slices.Delete(t.streams, id, id+1)
		}
	}
}

func (t *connectionTransport) cleanupInactiveStreams(maxInactive time.Duration) {
	timer := time.NewTicker(maxInactive)
	defer timer.Stop()

	for range timer.C {
		if len(t.streams) == 0 {
			continue
		}

		streamsToRemove := t.findInactiveStreamIDs(maxInactive)
		t.removeStreams(streamsToRemove)
	}
}

func (t *connectionTransport) cleanupClosedStreams() {
	t.mu.Lock()
	defer t.mu.Unlock()

	var active []*streamClient
	for _, stream := range t.streams {
		if stream.metricsInfo.IsActive {
			active = append(active, stream)
		} else {
			if stream.stream != nil {
				stream.stream.Close()
				stream.stream = nil
			}
		}
	}
	t.streams = active
}

func (t *connectionTransport) cleanupLoop() {
	cleanupTicker := time.NewTicker(30 * time.Second)
	defer cleanupTicker.Stop()

	oldStreamsTicker := time.NewTicker(5 * time.Minute)
	defer oldStreamsTicker.Stop()

	poolCleanupTicker := time.NewTicker(t.poolConfig.IdleTimeout)
	defer poolCleanupTicker.Stop()

	for {
		select {
		case <-t.ctx.Done():
			return
		case <-cleanupTicker.C:
			t.cleanupClosedStreams()
		case <-oldStreamsTicker.C:
			streamsToRemove := t.findInactiveStreamIDs(5 * time.Minute)
			t.removeStreams(streamsToRemove)
		case <-poolCleanupTicker.C:
			t.cleanupIdlePool()
		}
	}
}

func (t *connectionTransport) cleanupIdlePool() {
	if !t.poolConfig.Enabled {
		return
	}

	var valid []*streamClient
drain:
	for {
		select {
		case sc := <-t.pool:
			if sc.isValid() {
				valid = append(valid, sc)
			}
		default:
			break drain
		}
	}

	for _, sc := range valid {
		if !sc.isValid() {
			sc.Close()
			continue
		}
		select {
		case t.pool <- sc:
		default:
			sc.Close()
		}
	}
}

func (t *connectionTransport) Addr() string {
	if t.client == nil {
		return ""
	}
	return t.client.Addr()
}

func (t *connectionTransport) IsClosed() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.closed
}

func (t *connectionTransport) Root() Stream {
	if t == nil {
		return nil
	}
	return t.root
}

func (t *connectionTransport) ImServer() bool {
	return t.server
}

func (t *connectionTransport) PoolConfig() PoolConfig {
	return t.poolConfig
}

func (t *connectionTransport) PoolSize() int {
	return len(t.pool)
}

func (t *connectionTransport) PoolHits() int64 {
	return atomic.LoadInt64(&t.poolHits)
}

func (t *connectionTransport) PoolMisses() int64 {
	return atomic.LoadInt64(&t.poolMisses)
}
