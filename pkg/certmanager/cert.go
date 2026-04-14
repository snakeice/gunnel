package certmanager

import (
	"context"
	"crypto/tls"
	"errors"
	"net/url"

	"github.com/caddyserver/certmagic"
	"github.com/sirupsen/logrus"
)

type CertReqInfo struct {
	Domain string
	Email  string
}

func isValidDomain(domain string) bool {
	if domain == "" {
		return false
	}

	if u, err := url.Parse(domain); err == nil && u.Host != "" {
		return true
	}

	if u, err := url.Parse("https://" + domain); err == nil && u.Host != "" {
		return u.Host == domain || u.Hostname() == domain
	}

	return false
}

func GetTLSConfigWithLetsEncrypt(req *CertReqInfo) (*tls.Config, error) {
	domain := req.Domain

	if !isValidDomain(domain) {
		logrus.WithField("domain", domain).
			Warn("Invalid domain, skipping certificate generation")
		return nil, errors.New("invalid domain")
	}

	certmagic.DefaultACME.Agreed = true
	certmagic.DefaultACME.Email = req.Email
	certmagic.DefaultACME.CA = certmagic.LetsEncryptProductionCA
	certmagic.DefaultACME.Profile = "classic"
	certmagic.DefaultACME.DisableHTTPChallenge = false
	certmagic.Default.OnDemand = new(certmagic.OnDemandConfig)
	certmagic.Default.OnDemand.DecisionFunc = func(ctx context.Context, name string) error {
		return nil
	}

	err := certmagic.ManageSync(context.TODO(), []string{domain})
	if err != nil {
		logrus.WithError(err).
			WithField("domain", domain).
			Error("Failed to manage certificate for domain")
		return nil, err
	}

	tlsConfig, err := certmagic.TLS([]string{domain})
	if err != nil {
		logrus.WithError(err).Error("Failed to get TLS config")
		return nil, err
	}

	tlsConfig.NextProtos = append([]string{"h2", "http/1.1"}, tlsConfig.NextProtos...)

	return tlsConfig, nil
}
