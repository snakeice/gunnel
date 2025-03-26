package metrics

import (
	"slices"
	"sync/atomic"
	"time"
)

type StreamInfo struct {
	ID            string
	Subdomain     string
	StartTime     time.Time
	LastActive    time.Time
	IsActive      bool
	BytesReceived atomic.Int64
	BytesSent     atomic.Int64
}

type streamMetrics struct {
	streams []*StreamInfo

	totalIn  atomic.Int64
	totalOut atomic.Int64
}

var metricsCollector *streamMetrics = &streamMetrics{
	streams: make([]*StreamInfo, 0),
}

func NewInfo(id string) *StreamInfo {
	info := &StreamInfo{
		ID:            id,
		StartTime:     time.Now(),
		LastActive:    time.Now(),
		IsActive:      true,
		BytesReceived: atomic.Int64{},
		BytesSent:     atomic.Int64{},
	}

	metricsCollector.streams = append(metricsCollector.streams, info)
	return info
}

func (s *StreamInfo) SetSubdomain(subdomain string) {
	s.Subdomain = subdomain
}

func (s *StreamInfo) UpdateIn(in int) {
	s.BytesReceived.Add(int64(in))
	metricsCollector.totalIn.Add(int64(in))
	s.LastActive = time.Now()
}

func (s *StreamInfo) UpdateOut(out int) {
	s.BytesSent.Add(int64(out))
	metricsCollector.totalOut.Add(int64(out))
	s.LastActive = time.Now()
}

func (s *StreamInfo) Inactive() {
	s.IsActive = false
	s.LastActive = time.Now()
}

// GetActiveStreams returns all active streams.
func GetActiveStreams() []*StreamInfo {
	active := make([]*StreamInfo, 0)
	for _, stream := range metricsCollector.streams {
		if stream.IsActive {
			active = append(active, stream)
		}
	}

	slices.SortFunc(active, func(i, j *StreamInfo) int {
		if i.StartTime.Before(j.StartTime) {
			return 1
		}

		if i.StartTime.After(j.StartTime) {
			return -1
		}

		return 0
	})

	return active
}

// GetInactiveStreams returns all inactive streams.
func GetInactiveStreams() []*StreamInfo {
	inactiveStreams := make([]*StreamInfo, 0)
	for _, stream := range metricsCollector.streams {
		if !stream.IsActive {
			inactiveStreams = append(inactiveStreams, stream)
		}
	}

	slices.SortFunc(inactiveStreams, func(i, j *StreamInfo) int {
		if i.StartTime.Before(j.StartTime) {
			return 1
		}

		if i.StartTime.After(j.StartTime) {
			return -1
		}

		return 0
	})

	return inactiveStreams
}

// GetStreamStats returns statistics about all streams.
func GetStreamStats() map[string]any {
	stats := make(map[string]any)
	stats["total_streams"] = len(metricsCollector.streams)

	activeStreams := 0
	totalBytesIn := int64(0)
	totalBytesOut := int64(0)

	for _, stream := range metricsCollector.streams {
		if stream.IsActive {
			activeStreams++
		}
		totalBytesIn += stream.BytesReceived.Load()
		totalBytesOut += stream.BytesSent.Load()
	}

	stats["active_streams"] = activeStreams
	stats["total_bytes_in"] = totalBytesIn
	stats["total_bytes_out"] = totalBytesOut

	return stats
}
