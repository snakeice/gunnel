package manager

import (
	"net"
	"net/http"
	"strings"
)

// extractSubdomain extracts the subdomain from a host string.
func extractSubdomain(req *http.Request) string {
	// Prefer Host header; fallback to RemoteAddr
	hostPort := strings.TrimSpace(req.Host)
	if hostPort == "" {
		hostPort = strings.TrimSpace(req.RemoteAddr)
	}

	// Split host and port if present
	host := hostPort
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		host = h
	}

	// Strip IPv6 brackets if present
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") && len(host) > 2 {
		host = host[1 : len(host)-1]
	}

	// Remove any trailing dot
	host = strings.TrimSuffix(host, ".")

	// If it's an IP address, no subdomain
	if ip := net.ParseIP(host); ip != nil {
		return ""
	}

	parts := strings.Split(host, ".")
	if len(parts) > 1 {
		return parts[0]
	}
	return ""
}
