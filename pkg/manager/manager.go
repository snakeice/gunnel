package manager

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/snakeice/gunnel/pkg/connection"
	"github.com/snakeice/gunnel/pkg/transport"
)

const streamAcceptTimeout = 30 * time.Second

var (
	ErrNoConnection      = errors.New("no connection available")
	ErrSubdomainNotFound = errors.New("subdomain not found")
)

type clientInfo struct {
	subdomains []string
	client     *connection.Connection
}

// Manager handles routing of connections between clients and local services.
type Manager struct {
	clients []clientInfo

	clientsMux sync.RWMutex
}

// New creates a new router.
func New() *Manager {
	return &Manager{
		clients:    make([]clientInfo, 0),
		clientsMux: sync.RWMutex{},
	}
}

func (m *Manager) Start(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				logrus.Info("Router context done, shutting down")
				return
			default:
				// Sleep for a short duration to avoid busy waiting
				time.Sleep(500 * time.Millisecond)
			}
		}
	}()
}

// ForEachClient iterates over all clients and calls the provided function for each one.
func (r *Manager) ForEachClient(fn func(subdomain string, info *connection.Connection)) {
	r.clientsMux.RLock()
	defer r.clientsMux.RUnlock()

	for _, info := range r.clients {
		for _, subdomain := range info.subdomains {
			fn(subdomain, info.client)
		}
	}
}

func (m *Manager) Acquire(subdomain string) (transport.Stream, error) {
	if client, ok := m.getClient(subdomain); ok {
		if stream, err := client.client.Acquire(); err == nil {
			stream.SetSubdomain(subdomain)
			return stream, nil
		} else {
			logrus.WithFields(logrus.Fields{
				"subdomain": subdomain,
			}).Errorf("Failed to acquire transport stream: %s", err)
		}
	}

	return nil, ErrSubdomainNotFound
}

func (m *Manager) getClient(subdomain string) (*clientInfo, bool) {
	m.clientsMux.RLock()
	defer m.clientsMux.RUnlock()

	for _, c := range m.clients {
		if slices.Contains(c.subdomains, subdomain) {
			return &c, true
		}
	}

	return nil, false

}

func (m *Manager) Release(subdomain string, stream transport.Stream) {
	if client, ok := m.getClient(subdomain); ok {
		client.client.Release(stream)
	}
}

func (m *Manager) addClient(subdomain string, client *connection.Connection) error {

	oldClient, exists := m.getClient(subdomain)

	canAccept := true

	if exists {
		canAccept = oldClient.client.Connected()
		canAccept = canAccept || oldClient.client == client
	}

	if !canAccept {
		return errors.New("client already exists for subdomain " + subdomain)
	}

	needReplace := exists && canAccept && oldClient.client != client

	if needReplace {
		logrus.WithField("subdomain", subdomain).Error("Client already exists, removing old client")
		m.removeClient(oldClient.client)
	}

	if !exists || needReplace {
		m.clientsMux.Lock()
		m.clients = append(m.clients, clientInfo{
			subdomains: []string{subdomain},
			client:     client,
		})
		m.clientsMux.Unlock()

		return nil
	}

	oldClient.subdomains = append(oldClient.subdomains, subdomain)

	return nil
}

func (m *Manager) removeClient(client *connection.Connection) {
	m.clientsMux.Lock()
	defer m.clientsMux.Unlock()

	for i, c := range m.clients {
		if c.client == client {
			m.clients = slices.Delete(m.clients, i, i+1)
			return
		}
	}
}
