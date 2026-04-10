package transport_test

import (
	"testing"

	"github.com/snakeice/gunnel/pkg/transport"
)

// TestTransportInterface verifies the Transport interface is properly defined.
func TestTransportInterface(t *testing.T) {
	var _ transport.Transport

	t.Log("✓ Transport interface verified")
}
