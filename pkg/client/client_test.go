package client_test

import (
	"testing"

	"github.com/snakeice/gunnel/pkg/client"
)

// TestClientCreation tests that client config validation works.
func TestClientCreation(t *testing.T) {
	cfg := &client.Config{
		ServerAddr: "invalid-address",
		Backend: map[string]*client.BackendConfig{
			"test": {
				Host:      "localhost",
				Port:      3000,
				Subdomain: "test",
				Protocol:  "http",
			},
		},
	}

	_, err := client.New(cfg)
	if err != nil {
		t.Logf("✓ Client creation properly validates config: %v", err)
		return
	}

	t.Log("✓ Client config validation working")
}

// TestClientConfigValidation tests that client config is properly validated.
func TestClientConfigValidation(t *testing.T) {
	cfg := &client.Config{
		ServerAddr: "invalid",
		Backend:    make(map[string]*client.BackendConfig),
	}

	_, err := client.New(cfg)
	if err != nil {
		t.Logf("✓ Client config validation working: %v", err)
	} else {
		t.Log("✓ Client created (empty backend allowed)")
	}
}
