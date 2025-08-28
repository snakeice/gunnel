package client

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
	"github.com/snakeice/gunnel/pkg/protocol"
	"gopkg.in/yaml.v3"
)

// Config represents the configuration for the client.
// It contains the server address and a map of backend configurations.
// Each backend configuration includes the host, port, subdomain, and protocol.
// The server address is the address of the gunnel server.
type Config struct {
	ServerAddr string                    `yaml:"server_addr"`
	Backend    map[string]*BackendConfig `yaml:"backend"`
}

type BackendConfig struct {
	Host      string            `yaml:"host"`
	Port      uint32            `yaml:"port"`
	Subdomain string            `yaml:"subdomain"`
	Protocol  protocol.Protocol `yaml:"protocol"`
}

func LoadConfig(configPath string) (*Config, error) {
	// Clean the path to prevent directory traversal
	configPath = filepath.Clean(configPath)

	file, err := os.Open(configPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			logrus.WithError(cerr).WithField("path", configPath).Warn("Failed to close config file")
		}
	}()

	config := &Config{
		ServerAddr: "localhost:8081",
		Backend:    make(map[string]*BackendConfig),
	}

	err = yaml.NewDecoder(file).Decode(config)
	if err != nil {
		return nil, err
	}

	return config, config.validate()
}

func (c *Config) validate() error {
	if c.ServerAddr == "" {
		return errors.New("server address is required")
	}
	if len(c.Backend) == 0 {
		return errors.New("at least one backend is required")
	}
	for name, backend := range c.Backend {
		if err := backend.validate(); err != nil {
			return fmt.Errorf("backend %s: %w", name, err)
		}
	}

	return nil
}

func (b *BackendConfig) validate() error {
	if b == nil {
		return errors.New("is nil")
	}

	if b.Host == "" {
		b.Host = "localhost"
	}

	if b.Port == 0 {
		return errors.New("port is required")
	}

	if b.Subdomain == "" {
		return errors.New("subdomain is required")
	}

	if b.Protocol != "" && !b.Protocol.Valid() {
		return fmt.Errorf("protocol is invalid: %s", b.Protocol)
	}

	if b.Protocol == "" {
		b.Protocol = protocol.HTTP
	}

	return nil
}

func (b *BackendConfig) getAddr() string {
	return fmt.Sprintf("%s:%d", b.Host, b.Port)
}
