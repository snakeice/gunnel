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
			logrus.WithFields(logrus.Fields{
				"stream_id": stream.ID(),
				"addr":      transp.Addr(),
			}).Debug("Stream received but no handler assigned (expected - handled by connection)")
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

func (m *Manager) HandleStream(client *connection.Connection, msg *protocol.Message) error {
	regMsg := protocol.ConnectionRegister{}
	protocol.Unmarshal(&regMsg, msg)

	subdomain := regMsg.Subdomain
	if subdomain == "" {
		subdomain = "default"
	}

	logrus.WithFields(logrus.Fields{
		"subdomain": subdomain,
		"host":      regMsg.Host,
		"port":      regMsg.Port,
		"protocol":  regMsg.Protocol,
	}).Info("Client requested registration")

	reason := "success"

	canAccept := true

	if !m.IsAuthorized(regMsg.Token) {
		reason = "unauthorized"
		canAccept = false
	}

	if canAccept {
		m.addClient(subdomain, client)
	}

	regRespMsg := protocol.ConnectionRegisterResp{
		Success:   canAccept,
		Subdomain: subdomain,
		Message:   reason,
	}
	client.Send(&regRespMsg)

	logrus.WithFields(logrus.Fields{
		"subdomain": subdomain,
		"accepted":  canAccept,
		"reason":    reason,
	}).Info("Client registration result")

	return nil
}
