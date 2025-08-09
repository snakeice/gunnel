package tunnel

import (
	"context"
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
	local  net.Conn
	remote transport.Stream
	mu     sync.Mutex
}

// NewTunnel creates a new tunnel instance.
func NewTunnel(addr string, remote transport.Stream) (*Tunnel, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	local, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to local service: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"local_addr":  local.LocalAddr().String(),
		"remote_addr": addr,
	}).Trace("Connected to local service")

	return &Tunnel{
		local:  local,
		remote: remote,
	}, nil
}

func NewTunnelWithLocal(local net.Conn, remote transport.Stream) *Tunnel {
	return &Tunnel{
		local:  local,
		remote: remote,
	}
}

// Proxy starts bidirectional tunneling.
func (t *Tunnel) Proxy() error {
	// Capture current ends to avoid racing with Close() mutating t.local/t.remote
	local := t.local
	remote := t.remote

	var wg sync.WaitGroup
	wg.Add(2)

	// Start bidirectional copying
	go func() {
		defer wg.Done()

		laddr := "nil"
		if local != nil {
			laddr = local.LocalAddr().String()
		}
		rid := "nil"
		if remote != nil {
			rid = remote.ID()
		}

		logrus.WithFields(logrus.Fields{
			"direction": "remote_to_local",
			"local":     laddr,
			"remote":    rid,
		}).Debug("Starting remote to local copy")

		if err := t.copy(remote, local); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":     err,
				"direction": "remote_to_local",
			}).Error("Error copying from remote to local")
		}

		// Half-close local write side if supported to signal end-of-request,
		// but keep the connection open for reading the response.
		if local != nil {
			if cw, ok := local.(interface{ CloseWrite() error }); ok {
				if err := cw.CloseWrite(); err != nil && !errors.Is(err, net.ErrClosed) {
					logrus.WithFields(logrus.Fields{
						"error":     err,
						"direction": "remote_to_local",
					}).Warn("Failed to half-close local write side")
				} else {
					logrus.WithFields(logrus.Fields{
						"direction": "remote_to_local",
					}).Debug("Half-closed local write side")
				}
			}
		}
	}()

	go func() {
		defer wg.Done()

		laddr := "nil"
		if local != nil {
			laddr = local.LocalAddr().String()
		}
		rid := "nil"
		if remote != nil {
			rid = remote.ID()
		}

		logrus.WithFields(logrus.Fields{
			"direction": "local_to_remote",
			"local":     laddr,
			"remote":    rid,
		}).Debug("Starting local to remote copy")

		if err := t.copy(local, remote); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":     err,
				"direction": "local_to_remote",
			}).Error("Error copying from local to remote")
		}

		// After finishing local->remote copy, half-close the write side of the remote
		// stream to signal end-of-request and allow the response to flush back.
		if remote != nil {
			if err := remote.CloseWrite(); err != nil {
				logrus.WithFields(logrus.Fields{
					"error":     err,
					"direction": "local_to_remote",
				}).Warn("Failed to close write side of remote stream")
			} else {
				logrus.WithFields(logrus.Fields{
					"direction": "local_to_remote",
				}).Debug("Closed write side of remote stream")
			}
		}
	}()

	// Wait for both directions to complete to avoid races with Close()
	wg.Wait()
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
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
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
func (t *Tunnel) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.local != nil {
		if err := t.local.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			return fmt.Errorf("failed to close local connection: %w", err)
		}

		t.local = nil
	}

	if t.remote != nil {
		if err := t.remote.Close(); err != nil {
			return fmt.Errorf("failed to close remote connection: %w", err)
		}

		t.remote = nil
	}

	return nil
}
