package honeypot

import (
	"math/rand"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

type SuspiciousRequest struct {
	Timestamp   time.Time `json:"timestamp"`
	Subdomain   string    `json:"subdomain"`
	Method      string    `json:"method"`
	Path        string    `json:"path"`
	UserAgent   string    `json:"user_agent"`
	ContentType string    `json:"content_type"`
	Referer     string    `json:"referer"`
}

type IPStats struct {
	FirstSeen       time.Time           `json:"first_seen"`
	LastSeen        time.Time           `json:"last_seen"`
	RequestCount    int                 `json:"request_count"`
	SubdomainsTried map[string]int      `json:"subdomains_tried"`
	Requests        []SuspiciousRequest `json:"requests"`
}

type Honeypot struct {
	mu               sync.RWMutex
	ipStats          map[string]*IPStats
	enabled          bool
	threshold        int
	maxRequestsPerIP int
	minDelay         time.Duration
	maxDelay         time.Duration
	cleanupInterval  time.Duration
	ipTTL            time.Duration
	stopCleanup      chan struct{}
	logger           *logrus.Entry
}

type Config struct {
	Enabled          bool
	Threshold        int
	MaxRequestsPerIP int
	MinDelay         time.Duration
	MaxDelay         time.Duration
	CleanupInterval  time.Duration
	IPTTL            time.Duration
}

func DefaultConfig() Config {
	return Config{
		Enabled:          true,
		Threshold:        3,
		MaxRequestsPerIP: 50,
		MinDelay:         1 * time.Second,
		MaxDelay:         5 * time.Minute,
		CleanupInterval:  30 * time.Minute,
		IPTTL:            3 * time.Hour,
	}
}

func New(config Config) *Honeypot {
	h := &Honeypot{
		ipStats:          make(map[string]*IPStats),
		enabled:          config.Enabled,
		threshold:        config.Threshold,
		maxRequestsPerIP: config.MaxRequestsPerIP,
		minDelay:         config.MinDelay,
		maxDelay:         config.MaxDelay,
		cleanupInterval:  config.CleanupInterval,
		ipTTL:            config.IPTTL,
		stopCleanup:      make(chan struct{}),
		logger:           logrus.WithField("component", "honeypot"),
	}

	if h.enabled && h.cleanupInterval > 0 {
		go h.startCleanup()
	}

	return h
}

func (h *Honeypot) RecordRequest(req *http.Request, subdomain string) {
	if !h.enabled {
		return
	}

	ip := extractIP(req)
	if ip == "" {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	stats, exists := h.ipStats[ip]
	if !exists {
		stats = &IPStats{
			FirstSeen:       time.Now(),
			SubdomainsTried: make(map[string]int),
			Requests:        make([]SuspiciousRequest, 0),
		}
		h.ipStats[ip] = stats
	}

	stats.LastSeen = time.Now()
	stats.RequestCount++
	stats.SubdomainsTried[subdomain]++

	if len(stats.Requests) < h.maxRequestsPerIP {
		stats.Requests = append(stats.Requests, SuspiciousRequest{
			Timestamp:   time.Now(),
			Subdomain:   subdomain,
			Method:      req.Method,
			Path:        req.URL.Path,
			UserAgent:   req.UserAgent(),
			ContentType: req.Header.Get("Content-Type"),
			Referer:     req.Header.Get("Referer"),
		})
	}

	if stats.RequestCount == h.threshold {
		h.logger.WithFields(logrus.Fields{
			"ip":            ip,
			"request_count": stats.RequestCount,
			"subdomains":    stats.SubdomainsTried,
		}).Warn("IP reached suspicious threshold - will troll")
	}
}

func (h *Honeypot) IsSuspicious(ip string) bool {
	if !h.enabled {
		return false
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	stats, exists := h.ipStats[ip]
	if !exists {
		return false
	}

	return stats.RequestCount >= h.threshold
}

func (h *Honeypot) GetDelay(ip string) time.Duration {
	if !h.enabled {
		return 0
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	stats, exists := h.ipStats[ip]
	if !exists || stats.RequestCount < h.threshold {
		return 0
	}

	excessRequests := stats.RequestCount - h.threshold
	delay := h.minDelay * time.Duration(1<<excessRequests)
	if delay > h.maxDelay || delay < h.minDelay {
		delay = h.maxDelay
	}

	h.logger.WithFields(logrus.Fields{
		"ip":              ip,
		"request_count":   stats.RequestCount,
		"delay_seconds":   delay.Seconds(),
		"excess_requests": excessRequests,
	}).Debug("Applying exponential delay")

	return delay
}

func (h *Honeypot) GetFakeResponse(req *http.Request) ([]byte, string) {
	path := req.URL.Path
	userAgent := req.UserAgent()

	responses := h.getFakeResponses(path, userAgent)
	idx := rand.Intn(len(responses))
	return []byte(responses[idx].body), responses[idx].contentType
}

type fakeResponse struct {
	body        string
	contentType string
}

func (h *Honeypot) getFakeResponses(path, userAgent string) []fakeResponse {
	if contains(path, "admin") || contains(path, "login") || contains(path, "wp-") {
		return h.getAdminFakes()
	}
	if contains(path, "api") || contains(path, "v1") || contains(path, "graphql") {
		return h.getAPIFakes()
	}
	if contains(userAgent, "sqlmap") || contains(userAgent, "nikto") || contains(userAgent, "nmap") {
		return h.getScannerFakes()
	}
	return h.getGenericFakes()
}

func (h *Honeypot) getAdminFakes() []fakeResponse {
	return []fakeResponse{
		{
			contentType: "text/html",
			body: `<!DOCTYPE html>
<html>
<head><title>Admin Login</title></head>
<body>
<form action="/admin/login" method="post">
<input type="text" name="username" placeholder="admin">
<input type="password" name="password" placeholder="password">
<button type="submit">Login</button>
</form>
</body>
</html>`,
		},
		{
			contentType: "application/json",
			body:        `{"status":"error","message":"Invalid credentials","attempts_remaining":3}`,
		},
		{
			contentType: "text/html",
			body: `<!DOCTYPE html>
<html>
<head><title>phpMyAdmin</title></head>
<body>
<h1>Welcome to phpMyAdmin</h1>
<form method="post">
Server: <input type="text" name="pma_server" value="localhost"><br>
Username: <input type="text" name="pma_username"><br>
Password: <input type="password" name="pma_password"><br>
<input type="submit" value="Go">
</form>
</body>
</html>`,
		},
	}
}

func (h *Honeypot) getAPIFakes() []fakeResponse {
	return []fakeResponse{
		{
			contentType: "application/json",
			body:        `{"error":"rate_limit_exceeded","retry_after":3600,"message":"Too many requests"}`,
		},
		{
			contentType: "application/json",
			body:        `{"status":"maintenance","message":"API temporarily unavailable"}`,
		},
		{
			contentType: "application/json",
			body:        `{"data":[],"meta":{"total":0,"page":1,"limit":100}}`,
		},
		{
			contentType: "application/json",
			body:        `{"token":"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.fake.signature","expires_in":3600}`,
		},
	}
}

func (h *Honeypot) getScannerFakes() []fakeResponse {
	return []fakeResponse{
		{
			contentType: "text/plain",
			body:        "SSH-2.0-OpenSSH_8.9p1 Ubuntu-3ubuntu0.1\n",
		},
		{
			contentType: "text/html",
			body:        `<html><body><h1>It works!</h1><p>This is the default web page for this server.</p></body></html>`,
		},
		{
			contentType: "application/json",
			body:        `{"version":"1.0.0","endpoints":["/api/v1/users","/api/v1/admin","/api/v1/config"]}`,
		},
		{
			contentType: "text/xml",
			body:        `<?xml version="1.0"?><error>Invalid request</error>`,
		},
	}
}

func (h *Honeypot) getGenericFakes() []fakeResponse {
	return []fakeResponse{
		{
			contentType: "text/html",
			body:        `<!DOCTYPE html><html><head><title>Loading...</title></head><body><script>setTimeout(function(){location.reload()},5000)</script><h1>Please wait...</h1></body></html>`,
		},
		{
			contentType: "text/html",
			body:        `<!DOCTYPE html><html><body><h1>404 Not Found</h1><p>The requested URL was not found on this server.</p></body></html>`,
		},
		{
			contentType: "application/json",
			body:        `{"status":"ok","timestamp":"` + time.Now().Format(time.RFC3339) + `"}`,
		},
	}
}

func (h *Honeypot) GetStats(ip string) (*IPStats, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	stats, exists := h.ipStats[ip]
	if !exists {
		return nil, false
	}

	copy := &IPStats{
		FirstSeen:       stats.FirstSeen,
		LastSeen:        stats.LastSeen,
		RequestCount:    stats.RequestCount,
		SubdomainsTried: make(map[string]int, len(stats.SubdomainsTried)),
		Requests:        make([]SuspiciousRequest, len(stats.Requests)),
	}
	for k, v := range stats.SubdomainsTried {
		copy.SubdomainsTried[k] = v
	}
	copy.Requests = append(copy.Requests, stats.Requests...)

	return copy, true
}

func (h *Honeypot) GetSuspiciousIPs() map[string]*IPStats {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make(map[string]*IPStats)
	for ip, stats := range h.ipStats {
		if stats.RequestCount >= h.threshold {
			result[ip] = stats
		}
	}

	return result
}

func (h *Honeypot) GetAllIPs() map[string]*IPStats {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make(map[string]*IPStats, len(h.ipStats))
	for ip, stats := range h.ipStats {
		result[ip] = stats
	}

	return result
}

func (h *Honeypot) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.ipStats = make(map[string]*IPStats)
}

func (h *Honeypot) SetEnabled(enabled bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.enabled = enabled
}

func (h *Honeypot) startCleanup() {
	ticker := time.NewTicker(h.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			h.cleanupStaleIPs()
		case <-h.stopCleanup:
			return
		}
	}
}

func (h *Honeypot) cleanupStaleIPs() {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()
	removed := 0

	for ip, stats := range h.ipStats {
		if now.Sub(stats.LastSeen) > h.ipTTL {
			delete(h.ipStats, ip)
			removed++
		}
	}

	if removed > 0 {
		h.logger.WithFields(logrus.Fields{
			"removed_count": removed,
			"remaining_ips": len(h.ipStats),
		}).Info("Cleaned up stale IPs from honeypot")
	}
}

func (h *Honeypot) Stop() {
	close(h.stopCleanup)
}

func extractIP(req *http.Request) string {
	xff := req.Header.Get("X-Forwarded-For")
	if xff != "" {
		ips := splitCSV(xff)
		if len(ips) > 0 {
			ip := trimSpace(ips[0])
			if net.ParseIP(ip) != nil {
				return ip
			}
		}
	}

	xri := req.Header.Get("X-Real-IP")
	if xri != "" {
		ip := trimSpace(xri)
		if net.ParseIP(ip) != nil {
			return ip
		}
	}

	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}

	return host
}

func splitCSV(s string) []string {
	var result []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			part := trimSpace(s[start:i])
			if part != "" {
				result = append(result, part)
			}
			start = i + 1
		}
	}
	return result
}

func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && isSpace(s[start]) {
		start++
	}
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
