package manager

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/snakeice/gunnel/pkg/connection"
	"github.com/snakeice/gunnel/pkg/honeypot"
	"github.com/snakeice/gunnel/pkg/transport"
)

const streamAcceptTimeout = 5 * time.Second

var (
	ErrNoConnection      = errors.New("no connection available")
	ErrSubdomainNotFound = errors.New("subdomain not found")
)

type Manager struct {
	subdomains sync.Map

	gunnelSubdomainHandler http.HandlerFunc

	tokenValidator func(string) bool

	honeypot *honeypot.Honeypot
}

func New() *Manager {
	return &Manager{
		honeypot: honeypot.New(honeypot.DefaultConfig()),
	}
}

func (m *Manager) SetHoneypot(h *honeypot.Honeypot) {
	m.honeypot = h
}

func (m *Manager) Honeypot() *honeypot.Honeypot {
	return m.honeypot
}

func (m *Manager) SetGunnelSubdomainHandler(handler http.HandlerFunc) {
	m.gunnelSubdomainHandler = handler
}

func (m *Manager) SetTokenValidator(validator func(string) bool) {
	m.tokenValidator = validator
}

func (m *Manager) IsAuthorized(token string) bool {
	if m.tokenValidator == nil {
		return true
	}
	return m.tokenValidator(token)
}

func (m *Manager) ForEachClient(fn func(subdomain string, info *connection.Connection)) {
	m.subdomains.Range(func(key, value any) bool {
		subdomain, ok := key.(string)
		if !ok {
			return true
		}
		conn, ok := value.(*connection.Connection)
		if !ok {
			return true
		}
		fn(subdomain, conn)
		return true
	})
}

func (m *Manager) Acquire(subdomain string) (transport.Stream, error) {
	client, ok := m.getClient(subdomain)
	if !ok {
		return nil, ErrSubdomainNotFound
	}

	stream, err := client.Acquire()
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"subdomain": subdomain,
		}).Errorf("Failed to acquire transport stream: %s", err)
		return nil, ErrNoConnection
	}

	stream.SetSubdomain(subdomain)
	return stream, nil
}

func (m *Manager) getClient(subdomain string) (*connection.Connection, bool) {
	value, ok := m.subdomains.Load(subdomain)
	if !ok {
		return nil, false
	}
	conn, ok := value.(*connection.Connection)
	if !ok {
		return nil, false
	}
	return conn, true
}

func (m *Manager) Release(subdomain string, stream transport.Stream) {
	if client, ok := m.getClient(subdomain); ok {
		client.Release(stream)
	}
}

func (m *Manager) addClient(subdomain string, client *connection.Connection) {
	if oldClient, exists := m.getClient(subdomain); exists {
		if !oldClient.Connected() {
			m.subdomains.Store(subdomain, client)
			return
		}
		if oldClient != client {
			logrus.WithField("subdomain", subdomain).
				Info("Replacing existing client with new connection")
			oldClient.Close()
			m.subdomains.Store(subdomain, client)
		}
		return
	}

	m.subdomains.Store(subdomain, client)
}

func (m *Manager) removeClient(subdomain string) {
	m.subdomains.Delete(subdomain)
	logrus.WithField("subdomain", subdomain).Debug("Removed client from registry")
}
