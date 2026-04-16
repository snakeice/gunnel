package transport

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/sirupsen/logrus"
	"github.com/snakeice/gunnel/pkg/metrics"
	"github.com/snakeice/gunnel/pkg/protocol"
)

const deadlineDefault = 60 * time.Second

type Stream interface {
	io.ReadWriteCloser
	ID() string
	SetID(id string)
	Send(msg protocol.Parsable) error
	Receive() (*protocol.Message, error)

	SetSubdomain(subdomain string)

	Read(p []byte) (n int, err error)
	Write(p []byte) (n int, err error)
	CloseWrite() error
	Context() context.Context
	BufferedReader() *bufio.Reader
}

// Transport represents a transport connection.
type streamClient struct {
	id          string
	stream      *quic.Stream
	metricsInfo *metrics.StreamInfo
	reader      *bufio.Reader

	mu sync.RWMutex
}

func GenerateID(strmID quic.StreamID) string {
	return fmt.Sprintf("strm-%s-%d", strmID.InitiatedBy().String(), strmID.StreamNum())
}

func newStreamHandler(stream *quic.Stream) *streamClient {
	if stream == nil {
		logrus.WithFields(logrus.Fields{
			"stream_id": "nil",
		}).Debug("Stream is nil, cannot create streamClient")
		return nil
	}

	strm := &streamClient{
		stream: stream,
		id:     GenerateID(stream.StreamID()),
		reader: bufio.NewReader(stream),
	}

	strm.watchClose()
	strm.metricsInfo = metrics.NewInfo(strm.ID())

	return strm
}

func (t *streamClient) watchClose() {
	if t == nil || t.stream == nil {
		return
	}

	ctx := t.stream.Context()
	if ctx == nil {
		return
	}

	go func(stream *quic.Stream) {
		<-ctx.Done()

		t.mu.Lock()
		defer t.mu.Unlock()

		if t.stream != nil && t.stream == stream {
			if err := t.stream.Close(); err != nil {
				logrus.WithError(err).Warn("Failed to close stream on context done")
			}
		}
	}(t.stream)
}

func (t *streamClient) ID() string {
	if t == nil {
		return "nil"
	}

	return t.id
}

func (t *streamClient) Send(msg protocol.Parsable) error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.stream == nil {
		return errors.New("stream is closed")
	}

	streamPayload := msg.Marshal()

	n, err := streamPayload.Write(t)
	if err != nil {
		return fmt.Errorf("failed to write packet: %w", err)
	}

	t.metricsInfo.UpdateOut(n)

	logrus.WithFields(logrus.Fields{
		"stream_id": t.ID(),
		"size":      n,
		"type":      streamPayload.Type.String(),
	}).Trace("sent message")

	return nil
}

func (t *streamClient) Receive() (*protocol.Message, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.stream == nil {
		return nil, errors.New("stream is closed")
	}

	if err := t.stream.SetReadDeadline(time.Now().Add(deadlineDefault)); err != nil {
		logrus.WithFields(logrus.Fields{
			"error":     err,
			"stream_id": t.ID(),
		}).Error("Failed to set read deadline")
		return nil, err
	}

	n, msg, err := protocol.ReadMessage(t.reader)
	if err != nil {
		if errors.Is(err, io.EOF) {
			logrus.WithFields(logrus.Fields{
				"stream_id": t.ID(),
			}).Trace("EOF reached in transport receive")
			return nil, err
		}
		logrus.WithFields(logrus.Fields{
			"error":     err,
			"stream_id": t.ID(),
		}).Error("Failed to read message")
		return nil, fmt.Errorf("failed to read message: %w", err)
	}

	t.metricsInfo.UpdateIn(n)

	logrus.WithFields(logrus.Fields{
		"size":      n,
		"stream_id": t.ID(),
		"type":      msg.Type.String(),
	}).Trace("received message")

	return msg, nil
}

func (t *streamClient) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.stream == nil {
		logrus.WithFields(logrus.Fields{
			"stream_id": t.ID(),
		}).Debug("Stream is nil, nothing to close")
		return nil
	}

	t.metricsInfo.IsActive = false
	t.metricsInfo.LastActive = time.Now()

	if err := t.stream.Close(); err != nil {
		return fmt.Errorf("failed to close streamClient: %w", err)
	}

	t.stream = nil

	return nil
}

func (t *streamClient) Read(p []byte) (int, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.stream == nil {
		logrus.WithFields(logrus.Fields{
			"stream_id": t.ID(),
		}).Debug("Stream is nil, nothing to read")
		return 0, errors.New("stream is nil")
	}

	if err := t.stream.SetReadDeadline(time.Now().Add(deadlineDefault)); err != nil {
		logrus.WithFields(logrus.Fields{
			"error":     err,
			"stream_id": t.ID(),
		}).Error("Failed to set read deadline")
		return 0, err
	}

	n, err := t.reader.Read(p)

	t.metricsInfo.UpdateIn(n)
	if err != nil {
		if errors.Is(err, io.EOF) {
			logrus.WithFields(logrus.Fields{
				"stream_id": t.ID(),
			}).Trace("EOF reached in transport read")
			return n, err
		}

		logrus.WithFields(logrus.Fields{
			"error":     err,
			"stream_id": t.ID(),
		}).Error("Error reading from transport")

		return n, err
	}

	logrus.WithFields(logrus.Fields{
		"bytes_read": n,
		"stream_id":  t.ID(),
	}).Trace("Read from transport")
	return n, nil
}

func (t *streamClient) Write(p []byte) (int, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.stream == nil {
		logrus.WithFields(logrus.Fields{
			"stream_id": t.ID(),
		}).Debug("Stream is nil, nothing to write")
		return 0, errors.New("stream is nil")
	}

	if err := t.stream.SetWriteDeadline(time.Now().Add(deadlineDefault)); err != nil {
		logrus.WithFields(logrus.Fields{
			"error":     err,
			"stream_id": t.ID(),
		}).Error("Failed to set write deadline")
		return 0, err
	}

	n, err := t.stream.Write(p)

	t.metricsInfo.UpdateOut(n)

	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error":     err,
			"stream_id": t.ID(),
		}).Error("Error writing to transport")

		return n, err
	}
	logrus.WithFields(logrus.Fields{
		"bytes_written": n,
		"stream_id":     t.ID(),
	}).Trace("Wrote to transport")
	return n, nil
}

func (t *streamClient) SetID(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.id = id
}

func (t *streamClient) SetSubdomain(subdomain string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.metricsInfo.SetSubdomain(subdomain)
}

func (t *streamClient) CloseWrite() error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.stream == nil {
		logrus.WithFields(logrus.Fields{
			"stream_id": t.ID(),
		}).Debug("Stream is nil, nothing to close write")
		return nil
	}

	if err := t.stream.Close(); err != nil {
		return fmt.Errorf("failed to close write side: %w", err)
	}
	return nil
}

func (t *streamClient) Context() context.Context {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.stream == nil {
		return context.Background()
	}
	return t.stream.Context()
}

func (t *streamClient) isValid() bool {
	if t == nil {
		return false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.stream == nil {
		return false
	}
	ctx := t.stream.Context()
	return ctx != nil && ctx.Err() == nil
}

func (t *streamClient) markIdle() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.metricsInfo != nil {
		t.metricsInfo.IsActive = false
		t.metricsInfo.LastActive = time.Now()
	}
}

func (t *streamClient) BufferedReader() *bufio.Reader {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.reader
}
