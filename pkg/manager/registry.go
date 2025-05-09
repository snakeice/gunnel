package manager

import (
	"context"
	"errors"

	"github.com/sirupsen/logrus"
	"github.com/snakeice/gunnel/pkg/connection"
	"github.com/snakeice/gunnel/pkg/protocol"
	"github.com/snakeice/gunnel/pkg/transport"
)

// HandleConnection handles a new connection.
func (m *Manager) HandleConnection(transp transport.Transport) {
	client := connection.New(transp, m.HandleStream)
	client.Start()

	streamChan := make(chan transport.Stream)
	go m.acceptStreams(transp, streamChan)

	for {
		select {
		case stream := <-streamChan:
			go m.HandleStreamDude(stream)
		case <-transp.Root().Context().Done():
			logrus.Info("Transport context done, stopping stream handling")
			return
		}
	}
}

func (m *Manager) acceptStreams(transp transport.Transport, streamChan chan transport.Stream) {
	defer close(streamChan)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), streamAcceptTimeout)
		stream, err := transp.AcceptStream(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				cancel()
				continue
			}
			logrus.WithError(err).Error("Failed to accept stream")
			cancel()
			return
		}

		logrus.WithFields(logrus.Fields{
			"stream_id": stream.ID(),
			"addr":      transp.Addr(),
		}).Debug("Accepted new stream")

		streamChan <- stream
		cancel()
	}
}

func (m *Manager) HandleStreamDude(stream transport.Stream) {
	for {
		buf := make([]byte, 4)
		_, err := stream.Read(buf)
		if err != nil {
			logrus.WithError(err).Error("Failed to receive message")
			return
		}
		logrus.WithFields(logrus.Fields{
			"stream_id": stream.ID(),
			"buf":       buf,
		}).Debug("Received message")
	}
}

func (m *Manager) HandleStream(client *connection.Connection, msg *protocol.Message) error {
	regMsg := protocol.ConnectionRegister{}
	protocol.Unmarshal(&regMsg, msg)

	subdomain := regMsg.Subdomain
	if subdomain == "" {
		subdomain = "default"
	}

	reason := "success"

	canAccept := true

	if err := m.addClient(subdomain, client); err != nil {
		reason = "failed to add client: " + err.Error()
		canAccept = false
	}

	regRespMsg := protocol.ConnectionRegisterResp{
		Success:   canAccept,
		Subdomain: subdomain,
		Message:   reason,
	}
	client.Send(&regRespMsg)

	return nil
}
