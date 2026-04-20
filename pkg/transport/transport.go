package transport

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
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

type PoolConfig struct {
	MaxIdle     int
	IdleTimeout time.Duration
	Enabled     bool
}

var (
	metricsPoolSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "gunnel",
		Name:      "stream_pool_size",
		Help:      "Current number of streams in the reuse pool",
	})
	metricsPoolHits = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "gunnel",
		Name:      "stream_pool_hits_total",
		Help:      "Total number of times a stream was reused from pool",
	})
	metricsPoolMisses = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "gunnel",
		Name:      "stream_pool_misses_total",
		Help:      "Total number of times a new stream was created",
	})
)

func init() {
	prometheus.MustRegister(metricsPoolSize, metricsPoolHits, metricsPoolMisses)
}

type connectionTransport struct {
	root       *streamClient
	closed     bool
	client     *gunnelquic.Client
	streams    []*streamClient
	mu         sync.RWMutex
	server     bool
	ctx        context.Context
	cancelFunc context.CancelFunc

	pool       chan *streamClient
	poolConfig PoolConfig
	poolHits   atomic.Int64
	poolMisses atomic.Int64
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
		pool:       make(chan *streamClient, 50),
		poolConfig: PoolConfig{
			MaxIdle:     50,
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
			if pooledStream != nil && pooledStream.isValid() {
				t.mu.Lock()
				t.streams = append(t.streams, pooledStream)
				t.mu.Unlock()
				t.poolHits.Add(1)
				metricsPoolHits.Inc()
				return pooledStream, nil
			}
			t.poolMisses.Add(1)
			metricsPoolMisses.Inc()
			if pooledStream != nil {
				pooledStream.Close()
			}
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

	if sc == nil || !sc.isValid() {
		if sc != nil {
			return sc.Close()
		}
		return nil
	}

	sc.markIdle()

	select {
	case t.pool <- sc:
		metricsPoolSize.Set(float64(len(t.pool)))
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

func (t *connectionTransport) cleanupClosedStreams() {
	t.mu.Lock()
	defer t.mu.Unlock()

	var active []*streamClient
	for _, stream := range t.streams {
		if stream.metricsInfo.IsActive {
			active = append(active, stream)
		} else if stream.stream != nil {
			if err := stream.stream.Close(); err != nil {
				logrus.WithError(err).Warn("Failed to close stream")
			}
			stream.stream = nil
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
			if err := sc.Close(); err != nil {
				logrus.WithError(err).Warn("Failed to close stream in pool cleanup")
			}
			continue
		}
		select {
		case t.pool <- sc:
		default:
			if err := sc.Close(); err != nil {
				logrus.WithError(err).Warn("Failed to close stream in pool cleanup")
			}
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
	return t.poolHits.Load()
}

func (t *connectionTransport) PoolMisses() int64 {
	return t.poolMisses.Load()
}
