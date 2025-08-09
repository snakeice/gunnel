package manager

import (
	"errors"
	"net/http"
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

	gunnelSubdomainHandler http.HandlerFunc

	// tokenValidator, when set, is used to authorize client registrations.
	// If nil, all registrations are allowed.
	tokenValidator func(string) bool
}

// New creates a new router.
func New() *Manager {
	return &Manager{
		clients:    make([]clientInfo, 0),
		clientsMux: sync.RWMutex{},
	}
}

func (m *Manager) SetGunnelSubdomainHandler(handler http.HandlerFunc) {
	m.gunnelSubdomainHandler = handler
}

// SetTokenValidator defines a callback used to authorize client registration tokens.
// If not set, all registrations are allowed.
func (m *Manager) SetTokenValidator(validator func(string) bool) {
	m.tokenValidator = validator
}

// IsAuthorized evaluates the provided token using the installed validator.
// When no validator is configured, it returns true (allow).
func (m *Manager) IsAuthorized(token string) bool {
	if m.tokenValidator == nil {
		return true
	}
	return m.tokenValidator(token)
}

// ForEachClient iterates over all clients and calls the provided function for each one.
func (m *Manager) ForEachClient(fn func(subdomain string, info *connection.Connection)) {
	m.clientsMux.RLock()
	defer m.clientsMux.RUnlock()

	for _, info := range m.clients {
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
			return nil, ErrNoConnection
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
		defer m.clientsMux.Unlock()

		m.clients = append(m.clients, clientInfo{
			subdomains: []string{subdomain},
			client:     client,
		})

		return nil
	}

	m.clientsMux.Lock()
	defer m.clientsMux.Unlock()

	for i := range m.clients {
		if m.clients[i].client == oldClient.client {
			m.clients[i].subdomains = append(m.clients[i].subdomains, subdomain)
			break
		}
	}

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
