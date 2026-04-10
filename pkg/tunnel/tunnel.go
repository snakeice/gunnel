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

const nilString = "nil"

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
//

//nolint:gocognit // This function handles bidirectional proxying logic
func (t *Tunnel) Proxy() error {
	// Capture current ends to avoid racing with Close() mutating t.local/t.remote
	local := t.local
	remote := t.remote

	var wg sync.WaitGroup
	wg.Add(2)

	// Start bidirectional copying
	go func() {
		defer wg.Done()

		laddr := nilString
		if local != nil {
			laddr = local.LocalAddr().String()
		}
		rid := nilString
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

		laddr := nilString
		if local != nil {
			laddr = local.LocalAddr().String()
		}
		rid := nilString
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
//
//nolint:gocognit // Bidirectional IO transfer with error handling
func (t *Tunnel) copy(dst io.Writer, src io.Reader) error {
	buf := make([]byte, 32*1024)
	totalBytes := 0
	lastLogTime := time.Now()
	lastLogBytes := 0

	for {
		if src == nil {
			return nil
		}

		n, err := src.Read(buf)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				if totalBytes > 0 {
					logrus.WithFields(logrus.Fields{
						"total_bytes": totalBytes,
						"duration":    time.Since(lastLogTime),
					}).Debug("Copy completed")
				}
				return nil
			}
			logrus.WithError(err).Error("Failed to read data")
			return fmt.Errorf("failed to read data: %w", err)
		}

		if n > 0 {
			totalBytes += n

			if _, err := dst.Write(buf[:n]); err != nil {
				logrus.WithError(err).Error("Failed to write data")
				return fmt.Errorf("failed to write data: %w", err)
			}

			elapsed := time.Since(lastLogTime)
			if totalBytes-lastLogBytes >= 1024*1024 || elapsed > 10*time.Second {
				bytesTransferred := totalBytes - lastLogBytes
				ratePerSecond := float64(bytesTransferred) / elapsed.Seconds()
				rateMBps := ratePerSecond / 1024 / 1024
				logrus.WithFields(logrus.Fields{
					"bytes_transferred": totalBytes,
					"rate":              fmt.Sprintf("%.2f MB/s", rateMBps),
				}).Debug("Copy progress")
				lastLogBytes = totalBytes
				lastLogTime = time.Now()
			}
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
