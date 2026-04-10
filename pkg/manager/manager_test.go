package manager_test

import (
	"testing"

	"github.com/snakeice/gunnel/pkg/manager"
)

// TestManagerCreation tests that manager can be created successfully.
func TestManagerCreation(t *testing.T) {
	mgr := manager.New()
	if mgr == nil {
		t.Fatal("Manager should not be nil")
	}
	t.Log("✓ Manager created successfully")
}
