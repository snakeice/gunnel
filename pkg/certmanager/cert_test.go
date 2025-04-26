package certmanager_test

import (
	"testing"

	"github.com/snakeice/gunnel/pkg/certmanager"
)

func TestGetTLSConfigWithLetsEncrypt(t *testing.T) {
	req := &certmanager.CertReqInfo{
		Domain: "saw.hashload.com",
		Email:  "snakeice@rb.dev",
	}

	got, err := certmanager.GetTLSConfigWithLetsEncrypt(req)
	if err != nil {
		t.Errorf("GetTLSConfigWithLetsEncrypt() error = %v", err)
		return
	}

	// Check if the returned config is not nil
	if got == nil {
		t.Errorf("GetTLSConfigWithLetsEncrypt() got = nil, want non-nil")
		return
	}
	// Check if the config has the expected fields
	if got.MinVersion != 0 {
		t.Errorf("GetTLSConfigWithLetsEncrypt() got.MinVersion = %v, want 0", got.MinVersion)
	}
}
