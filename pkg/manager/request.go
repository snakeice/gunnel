package manager

import (
	"net/http"
	"strings"
)

// extractSubdomain extracts the subdomain from a host string.
func extractSubdomain(req *http.Request) string {
	// Extract subdomain from Host header
	host := req.Host
	if host == "" {
		host = req.RemoteAddr
	}

	parts := strings.Split(host, ".")
	if len(parts) > 1 {
		return parts[0]
	}
	return ""
}
