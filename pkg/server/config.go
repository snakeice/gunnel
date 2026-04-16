package server

import (
	"errors"
	"os"
	"path/filepath"

	yaml "github.com/goccy/go-yaml"
	"github.com/sirupsen/logrus"
)

// Config represents the configuration for the client.
// It contains the server address and a map of backend configurations.
// Each backend configuration includes the host, port, subdomain, and protocol.
// The server address is the address of the gunnel server.
type Config struct {
	Domain     string            `yaml:"domain"`
	Token      string            `yaml:"token"`
	ServerPort int               `yaml:"server_port"`
	QuicPort   int               `yaml:"quic_port"`
	Cert       *CertConfig       `yaml:"cert"`
	Limits     *ConnectionLimits `yaml:"limits"`
}

type CertConfig struct {
	Enabled bool   `yaml:"enabled"`
	Email   string `yaml:"email"`
}

// ConnectionLimits holds connection limiting configuration.
type ConnectionLimits struct {
	// MaxConnections is the global maximum number of concurrent connections (0 = unlimited)
	MaxConnections int `yaml:"max_connections"`
	// MaxConnectionsPerIP is the maximum connections per IP address (0 = unlimited)
	MaxConnectionsPerIP int `yaml:"max_connections_per_ip"`
	// ConnectionRateLimit is the max new connections per minute per IP (0 = unlimited)
	ConnectionRateLimit int `yaml:"connection_rate_limit"`
}

func DefaultConfig() *Config {
	return &Config{
		Domain:     "",
		Token:      "",
		ServerPort: 8080,
		QuicPort:   8081,
		Cert: &CertConfig{
			Enabled: false,
			Email:   "",
		},
		Limits: &ConnectionLimits{
			MaxConnections:      0,
			MaxConnectionsPerIP: 0,
			ConnectionRateLimit: 0,
		},
	}
}

func (c *Config) LoadConfig(configPath string) error {
	// Clean the path to prevent directory traversal
	configPath = filepath.Clean(configPath)

	file, err := os.Open(configPath)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			logrus.WithError(cerr).WithField("path", configPath).Warn("Failed to close config file")
		}
	}()

	err = yaml.NewDecoder(file).Decode(c)
	if err != nil {
		return err
	}

	return c.validate()
}

func (c *Config) validate() error {
	if c.Domain == "" {
		return errors.New("domain is required")
	}

	return nil
}
