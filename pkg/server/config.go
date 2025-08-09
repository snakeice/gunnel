package server

import (
	"errors"
	"log"
	"os"

	yaml "github.com/goccy/go-yaml"
)

// Config represents the configuration for the client.
// It contains the server address and a map of backend configurations.
// Each backend configuration includes the host, port, subdomain, and protocol.
// The server address is the address of the gunnel server.
type Config struct {
	// Domain base for HTTP routing (e.g., example.com)
	Domain string `yaml:"domain"`
	// Optional shared token required from clients for registration/auth
	Token      string      `yaml:"token"`
	ServerPort int         `yaml:"server_port"`
	QuicPort   int         `yaml:"quic_port"`
	Cert       *CertConfig `yaml:"cert"`
}

type CertConfig struct {
	Enabled bool   `yaml:"enabled"`
	Email   string `yaml:"email"`
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
	}
}

func (c *Config) LoadConfig(configPath string) error {
	file, err := os.Open(configPath) //nolint:gosec // It's ok to use os.Open here
	if err != nil {
		return err
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			log.Printf("failed to close config file %s: %v", configPath, cerr)
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
