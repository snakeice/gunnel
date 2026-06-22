package certmanager

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/caddyserver/certmagic"
	"github.com/sirupsen/logrus"
)

type CertReqInfo struct {
	Domain         string
	WildcardDomain string
	Email          string
	// SubdomainChecker is called during OnDemand TLS to decide whether to issue
	// a certificate for a given subdomain. Only used when WildcardDomain is empty.
	// The full domain (e.g. "foo.example.com") is passed; return true to allow.
	SubdomainChecker func(subdomain string) bool
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

// GetTLSConfigWithLetsEncrypt resolves a TLS config using the following priority:
//  1. Wildcard domain (if configured) — covers all subdomains, OnDemand disabled.
//  2. Per-subdomain OnDemand — only issues certs for known subdomains via SubdomainChecker.
//
// Returns nil, nil when no TLS config could be obtained — caller should run without TLS.
func GetTLSConfigWithLetsEncrypt(req *CertReqInfo) (*tls.Config, error) {
	// Wildcard takes priority: one cert covers everything, no per-subdomain issuance needed.
	if req.WildcardDomain != "" {
		logrus.WithField("wildcard", req.WildcardDomain).
			Info("Attempting wildcard certificate (priority)")

		setupCertmagic(req.Email, nil) // no OnDemand for wildcard
		tlsConfig, err := manageDomain(req.WildcardDomain)
		if err == nil {
			logrus.WithField("wildcard", req.WildcardDomain).Info("Wildcard certificate obtained")
			return tlsConfig, nil
		}

		logrus.WithError(err).WithField("wildcard", req.WildcardDomain).
			Warn("Failed to obtain wildcard certificate, falling back to per-subdomain")
	}

	// Per-subdomain OnDemand: only allow cert issuance for known subdomains.
	logrus.WithField("domain", req.Domain).Info("Setting up per-subdomain OnDemand TLS")

	decisionFunc := buildDecisionFunc(req.Domain, req.SubdomainChecker)
	setupCertmagic(req.Email, decisionFunc)

	tlsConfig, err := manageDomain(req.Domain)
	if err != nil {
		logrus.WithError(err).WithField("domain", req.Domain).
			Warn("Failed to obtain base domain certificate, running without TLS")
		return nil, nil //nolint:nilnil // intentional: nil means "no TLS, continue without"
	}

	return tlsConfig, nil
}

// buildDecisionFunc returns an OnDemand DecisionFunc that only allows cert issuance
// for subdomains that exist in the manager (via checker). Unknown subdomains are denied,
// letting them fall through to the honeypot handler.
func buildDecisionFunc(
	baseDomain string,
	checker func(string) bool,
) func(context.Context, string) error {
	return func(_ context.Context, domain string) error {
		// Always allow the base domain itself.
		if domain == baseDomain {
			return nil
		}

		// Extract the subdomain label (e.g. "foo" from "foo.example.com").
		suffix := "." + baseDomain
		if !strings.HasSuffix(domain, suffix) {
			return fmt.Errorf("domain %q is not under base domain %q", domain, baseDomain)
		}
		subdomain := strings.TrimSuffix(domain, suffix)

		if checker == nil || !checker(subdomain) {
			logrus.WithFields(logrus.Fields{
				"domain":    domain,
				"subdomain": subdomain,
			}).Debug("OnDemand TLS denied: subdomain not registered")
			return fmt.Errorf("subdomain %q is not registered", subdomain)
		}

		return nil
	}
}

func setupCertmagic(email string, decisionFunc func(context.Context, string) error) {
	certmagic.DefaultACME.Agreed = true
	certmagic.DefaultACME.Email = email
	certmagic.DefaultACME.CA = certmagic.LetsEncryptProductionCA
	certmagic.DefaultACME.Profile = "classic"
	certmagic.DefaultACME.DisableHTTPChallenge = false

	if decisionFunc != nil {
		certmagic.Default.OnDemand = &certmagic.OnDemandConfig{
			DecisionFunc: decisionFunc,
		}
	} else {
		certmagic.Default.OnDemand = nil
	}
}

func manageDomain(domain string) (*tls.Config, error) {
	if !isValidDomain(domain) {
		return nil, errors.New("invalid domain: " + domain)
	}

	if err := certmagic.ManageSync(context.TODO(), []string{domain}); err != nil {
		return nil, err
	}

	tlsConfig, err := certmagic.TLS([]string{domain})
	if err != nil {
		return nil, err
	}

	tlsConfig.NextProtos = append([]string{"h2", "http/1.1"}, tlsConfig.NextProtos...)

	return tlsConfig, nil
}
