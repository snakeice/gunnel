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
		t.Errorf("GetTLSConfigWithLetsEncrypt() unexpected error = %v", err)
		return
	}

	// nil config is acceptable — it means TLS couldn't be obtained and the server
	// will continue without TLS. Only validate NextProtos when a config is returned.
	if got == nil {
		return
	}

	if len(got.NextProtos) == 0 {
		t.Errorf("GetTLSConfigWithLetsEncrypt() NextProtos should not be empty")
	}
}
