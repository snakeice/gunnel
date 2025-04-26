package certmanager

import (
	"context"
	"crypto/tls"
	"log"

	"github.com/caddyserver/certmagic"
)

type CertReqInfo struct {
	Domain string
	Email  string
}

// GetTLSConfigWithLetsEncrypt generates a TLS configuration using Let's Encrypt.
func GetTLSConfigWithLetsEncrypt(req *CertReqInfo) (*tls.Config, error) {
	certmagic.DefaultACME.Agreed = true
	certmagic.DefaultACME.Email = req.Email
	certmagic.DefaultACME.CA = certmagic.LetsEncryptProductionCA
	certmagic.DefaultACME.DisableHTTPChallenge = false
	certmagic.Default.OnDemand = new(certmagic.OnDemandConfig)
	certmagic.Default.OnDemand.DecisionFunc = func(ctx context.Context, name string) error {
		return nil
	}

	domain := req.Domain

	err := certmagic.ManageSync(context.TODO(), []string{domain})
	if err != nil {
		log.Printf("Failed to listen for domain %s: %v", domain, err)
		return nil, err
	}

	tlsConfig, err := certmagic.TLS([]string{domain})
	if err != nil {
		log.Printf("Failed to get TLS config: %v", err)
		return nil, err
	}

	tlsConfig.NextProtos = append([]string{"h2", "http/1.1"}, tlsConfig.NextProtos...)

	return tlsConfig, nil
}
