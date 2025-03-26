package tunnel

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/snakeice/gunnel/pkg/transport"
)

// Tunnel represents a bidirectional tunnel between two connections.
type Tunnel struct {
	local  io.ReadWriteCloser
	remote transport.Stream
	mu     sync.Mutex
}

// NewTunnel creates a new tunnel instance.
func NewTunnel(addr string, remote transport.Stream) (*Tunnel, error) {
	local, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to local service: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"local_addr":  local.LocalAddr().String(),
		"remote_addr": addr,
	}).Info("Connected to local service")

	return &Tunnel{
		local:  local,
		remote: remote,
	}, nil
}

func NewTunnelWithLocal(local io.ReadWriteCloser, remote transport.Stream) *Tunnel {
	return &Tunnel{
		local:  local,
		remote: remote,
	}
}

// Proxy starts bidirectional tunneling.
func (t *Tunnel) Proxy() error {
	waitCh := make(chan struct{}, 1)

	// Start bidirectional copying
	go func() {
		defer func() { waitCh <- struct{}{} }()

		logrus.WithFields(logrus.Fields{
			"direction": "remote_to_local",
			"local":     t.local.(net.Conn).LocalAddr().String(),
			"remote":    t.remote.ID(),
		}).Debug("Starting remote to local copy")
		if err := t.copy(t.remote, t.local); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":     err,
				"direction": "remote_to_local",
			}).Error("Error copying from remote to local")
		}
		// Send EOF to local service
		if localConn, ok := t.local.(net.Conn); ok {
			if err := localConn.Close(); err != nil {
				logrus.WithFields(logrus.Fields{
					"error":     err,
					"direction": "remote_to_local",
				}).Error("Failed to close local connection")
			} else {
				logrus.WithFields(logrus.Fields{
					"direction": "remote_to_local",
				}).Debug("Closed local connection")
			}
		}
	}()

	go func() {
		defer func() { waitCh <- struct{}{} }()
		logrus.WithFields(logrus.Fields{
			"direction": "local_to_remote",
			"local":     t.local.(net.Conn).LocalAddr().String(),
			"remote":    t.remote.ID(),
		}).Debug("Starting local to remote copy")
		if err := t.copy(t.local, t.remote); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":     err,
				"direction": "local_to_remote",
			}).Error("Error copying from local to remote")
		}
		// Send EOF to remote
		if err := t.remote.Close(); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":     err,
				"direction": "local_to_remote",
			}).Error("Failed to close remote connection")
		} else {
			logrus.WithFields(logrus.Fields{
				"direction": "local_to_remote",
			}).Debug("Closed remote connection")
		}
	}()

	<-waitCh

	// Only close the local connection
	t.CloseLocal()
	return nil
}

// copy handles the actual data transfer between connections.
func (t *Tunnel) copy(dst io.Writer, src io.Reader) error {
	buf := make([]byte, 32*1024) // 32KB buffer
	totalBytes := 0
	lastReadTime := time.Now()
	readCount := 0
	writeCount := 0

	for {
		if src == nil {
			return nil
		}

		n, err := src.Read(buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				logrus.WithFields(logrus.Fields{
					"total_bytes": totalBytes,
					"read_count":  readCount,
					"write_count": writeCount,
					"last_read":   lastReadTime,
					"duration":    time.Since(lastReadTime),
				}).Trace("EOF reached, copy complete")
				return nil
			}
			logrus.WithFields(logrus.Fields{
				"error":       err,
				"total_bytes": totalBytes,
				"read_count":  readCount,
				"write_count": writeCount,
			}).Error("Failed to read data")
			return fmt.Errorf("failed to read data: %w", err)
		}

		if n > 0 {
			totalBytes += n
			readCount++
			lastReadTime = time.Now()
			logrus.WithFields(logrus.Fields{
				"bytes_read": n,
				"total":      totalBytes,
				"read_count": readCount,
			}).Trace("Read data from source")

			if _, err := dst.Write(buf[:n]); err != nil {
				logrus.WithFields(logrus.Fields{
					"error":       err,
					"bytes_read":  n,
					"total":       totalBytes,
					"write_count": writeCount,
				}).Error("Failed to write data to destination")
				return fmt.Errorf("failed to write data: %w", err)
			}
			writeCount++
			logrus.WithFields(logrus.Fields{
				"bytes_written": n,
				"total":         totalBytes,
				"write_count":   writeCount,
			}).Trace("Wrote data to destination")
		} else {
			logrus.WithFields(logrus.Fields{
				"total_bytes": totalBytes,
				"read_count":  readCount,
				"write_count": writeCount,
			}).Debug("No data read, continuing")
		}
	}
}

// Close closes both connections.
func (t *Tunnel) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.local != nil {
		t.local.Close()
	}
	if t.remote != nil {
		t.remote.Close()
	}
}

// CloseLocal closes only the local connection.
func (t *Tunnel) CloseLocal() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.local != nil {
		t.local.Close()
	}
}
