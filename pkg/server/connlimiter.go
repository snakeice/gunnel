package server

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type ConnectionLimiter struct {
	maxConns  int
	maxPerIP  int
	rateLimit int

	activeConns atomic.Int64
	ipConns     sync.Map
	rateTracker sync.Map

	cleanupTicker *time.Ticker
	stopCleanup   chan struct{}
}

type ipConnCount struct {
	count atomic.Int64
}

type rateEntry struct {
	timestamps []time.Time
	mu         sync.Mutex
}

func NewConnectionLimiter(maxConns, maxPerIP, rateLimit int) *ConnectionLimiter {
	cl := &ConnectionLimiter{
		maxConns:    maxConns,
		maxPerIP:    maxPerIP,
		rateLimit:   rateLimit,
		stopCleanup: make(chan struct{}),
	}

	if rateLimit > 0 {
		cl.cleanupTicker = time.NewTicker(time.Minute)
		go cl.cleanupLoop()
	}

	return cl
}

func (cl *ConnectionLimiter) Allow(remoteAddr string) bool {
	ip, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		ip = remoteAddr
	}

	if cl.maxConns > 0 {
		current := cl.activeConns.Load()
		if int(current) >= cl.maxConns {
			return false
		}
	}

	if cl.maxPerIP > 0 {
		count := cl.getIPCount(ip)
		if count >= cl.maxPerIP {
			return false
		}
	}

	if cl.rateLimit > 0 {
		if !cl.checkRateLimit(ip) {
			return false
		}
	}

	return true
}

func (cl *ConnectionLimiter) Acquire(remoteAddr string) bool {
	if !cl.Allow(remoteAddr) {
		return false
	}

	ip, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		ip = remoteAddr
	}

	cl.activeConns.Add(1)
	cl.incrementIPCount(ip)

	return true
}

func (cl *ConnectionLimiter) Release(remoteAddr string) {
	ip, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		ip = remoteAddr
	}

	cl.activeConns.Add(-1)
	cl.decrementIPCount(ip)
}

func (cl *ConnectionLimiter) ActiveConnections() int64 {
	return cl.activeConns.Load()
}

func (cl *ConnectionLimiter) Stop() {
	if cl.cleanupTicker != nil {
		cl.cleanupTicker.Stop()
	}
	close(cl.stopCleanup)
}

func (cl *ConnectionLimiter) getIPCount(ip string) int {
	val, ok := cl.ipConns.Load(ip)
	if !ok {
		return 0
	}
	return int(val.(*ipConnCount).count.Load())
}

func (cl *ConnectionLimiter) incrementIPCount(ip string) {
	val, _ := cl.ipConns.LoadOrStore(ip, &ipConnCount{})
	val.(*ipConnCount).count.Add(1)
}

func (cl *ConnectionLimiter) decrementIPCount(ip string) {
	val, ok := cl.ipConns.Load(ip)
	if !ok {
		return
	}
	newCount := val.(*ipConnCount).count.Add(-1)
	if newCount <= 0 {
		cl.ipConns.Delete(ip)
	}
}

func (cl *ConnectionLimiter) checkRateLimit(ip string) bool {
	val, _ := cl.rateTracker.LoadOrStore(ip, &rateEntry{timestamps: make([]time.Time, 0)})
	entry := val.(*rateEntry)

	entry.mu.Lock()
	defer entry.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-time.Minute)

	valid := make([]time.Time, 0, len(entry.timestamps))
	for _, t := range entry.timestamps {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= cl.rateLimit {
		entry.timestamps = valid
		return false
	}

	entry.timestamps = append(valid, now)
	return true
}

func (cl *ConnectionLimiter) cleanupLoop() {
	for {
		select {
		case <-cl.stopCleanup:
			return
		case <-cl.cleanupTicker.C:
			cl.cleanupOldRateEntries()
		}
	}
}

func (cl *ConnectionLimiter) cleanupOldRateEntries() {
	cutoff := time.Now().Add(-2 * time.Minute)
	cl.rateTracker.Range(func(key, value any) bool {
		entry := value.(*rateEntry)
		entry.mu.Lock()
		defer entry.mu.Unlock()

		valid := make([]time.Time, 0)
		for _, t := range entry.timestamps {
			if t.After(cutoff) {
				valid = append(valid, t)
			}
		}

		if len(valid) == 0 {
			cl.rateTracker.Delete(key)
		} else {
			entry.timestamps = valid
		}
		return true
	})
}
