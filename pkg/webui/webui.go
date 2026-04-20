package webui

import (
	"embed"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/snakeice/gunnel/pkg/connection"
	"github.com/snakeice/gunnel/pkg/manager"
	"github.com/snakeice/gunnel/pkg/metrics"
)

//go:embed templates
var templates embed.FS

type WebUI struct {
	mngr      *manager.Manager
	Mux       *http.ServeMux
	mu        sync.RWMutex
	startTime time.Time
	stats     map[string]any
	clients   []map[string]any
	streams   []map[string]any
}

func NewWebUI(router *manager.Manager) *WebUI {
	webui := &WebUI{
		mngr:      router,
		startTime: time.Now(),
		stats:     make(map[string]any),
		clients:   make([]map[string]any, 0),
		streams:   make([]map[string]any, 0),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", webui.handleIndex)
	mux.HandleFunc("/api/stats", webui.handleStats)
	mux.HandleFunc("/api/clients", webui.handleClients)
	mux.HandleFunc("/api/streams", webui.handleStreams)
	mux.HandleFunc("/api/honeypot", webui.handleHoneypot)
	mux.HandleFunc("/api/prometheus", webui.handlePrometheusMetrics)

	webui.Mux = mux

	return webui
}

func (ui *WebUI) HandleRequest(w http.ResponseWriter, r *http.Request) {
	ui.mu.RLock()
	defer ui.mu.RUnlock()

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ui.Mux.ServeHTTP(w, r)
}

func (ui *WebUI) handleIndex(w http.ResponseWriter, _ *http.Request) {
	content, err := templates.ReadFile("templates/index.html")
	if err != nil {
		http.Error(w, "Failed to read template", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write(content); err != nil {
		http.Error(w, "Failed to write response", http.StatusInternalServerError)
	}
}

func (ui *WebUI) handleStats(w http.ResponseWriter, _ *http.Request) {
	ui.mu.RLock()
	defer ui.mu.RUnlock()

	stats := metrics.GetStreamStats()
	stats["uptime"] = time.Since(ui.startTime).Round(time.Second).String()
	stats["total_clients"] = len(ui.clients)

	promMetrics := ui.getPrometheusMetrics()
	stats["requests_total"] = promMetrics["requests_total"]
	stats["requests_per_second"] = promMetrics["requests_per_second"]
	stats["pool_hits"] = promMetrics["pool_hits"]
	stats["pool_misses"] = promMetrics["pool_misses"]
	stats["pool_size"] = promMetrics["pool_size"]
	stats["pool_efficiency"] = promMetrics["pool_efficiency"]
	stats["tunnel_errors"] = promMetrics["tunnel_errors"]

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(stats); err != nil {
		http.Error(w, "Failed to encode JSON", http.StatusInternalServerError)
	}
}

func (ui *WebUI) handleClients(w http.ResponseWriter, r *http.Request) {
	ui.mu.RLock()
	defer ui.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(ui.clients); err != nil {
		http.Error(w, "Failed to encode JSON", http.StatusInternalServerError)
		return
	}
}

func (ui *WebUI) handleStreams(w http.ResponseWriter, _ *http.Request) {
	ui.mu.RLock()
	defer ui.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(ui.streams); err != nil {
		http.Error(w, "Failed to encode JSON", http.StatusInternalServerError)
		return
	}
}

func (ui *WebUI) handleHoneypot(w http.ResponseWriter, _ *http.Request) {
	hp := ui.mngr.Honeypot()
	if hp == nil {
		http.Error(w, "Honeypot not enabled", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(hp.GetSuspiciousIPs()); err != nil {
		http.Error(w, "Failed to encode JSON", http.StatusInternalServerError)
		return
	}
}

func (ui *WebUI) handlePrometheusMetrics(w http.ResponseWriter, _ *http.Request) {
	ui.mu.RLock()
	defer ui.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(ui.getPrometheusMetrics()); err != nil {
		http.Error(w, "Failed to encode JSON", http.StatusInternalServerError)
	}
}

func (ui *WebUI) getPrometheusMetrics() map[string]any {
	result := make(map[string]any)

	metricFamilies, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		return result
	}

	requestsTotal := int64(0)
	poolHits := int64(0)
	poolMisses := int64(0)
	poolSize := int64(0)
	tunnelErrors := int64(0)

	for _, mf := range metricFamilies {
		switch *mf.Name {
		case "gunnel_requests_total":
			for _, m := range mf.GetMetric() {
				requestsTotal += int64(m.GetCounter().GetValue())
			}
		case "gunnel_stream_pool_hits_total":
			for _, m := range mf.GetMetric() {
				poolHits = int64(m.GetCounter().GetValue())
			}
		case "gunnel_stream_pool_misses_total":
			for _, m := range mf.GetMetric() {
				poolMisses = int64(m.GetCounter().GetValue())
			}
		case "gunnel_stream_pool_size":
			for _, m := range mf.GetMetric() {
				poolSize = int64(m.GetGauge().GetValue())
			}
		case "gunnel_tunnel_errors_total":
			for _, m := range mf.GetMetric() {
				tunnelErrors += int64(m.GetCounter().GetValue())
			}
		}
	}

	uptimeSeconds := time.Since(ui.startTime).Seconds()
	requestsPerSecond := float64(0)
	if uptimeSeconds > 0 {
		requestsPerSecond = float64(requestsTotal) / uptimeSeconds
	}

	result["requests_total"] = requestsTotal
	result["requests_per_second"] = requestsPerSecond
	result["pool_hits"] = poolHits
	result["pool_misses"] = poolMisses
	result["pool_size"] = poolSize
	result["tunnel_errors"] = tunnelErrors
	result["pool_efficiency"] = calculateEfficiency(poolHits, poolMisses)

	return result
}

func calculateEfficiency(hits, misses int64) float64 {
	total := hits + misses
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total) * 100
}

func (ui *WebUI) UpdateStats() {
	ui.mu.Lock()
	defer ui.mu.Unlock()

	const maxInactive = 5 * time.Minute
	const maxStreams = 15

	removed := metrics.CleanupOldStreams(10 * time.Minute)
	if removed > 0 {
		ui.stats["cleaned_streams"] = removed
	}

	ui.clients = make([]map[string]any, 0)

	ui.streams = make([]map[string]any, 0)
	for _, stream := range metrics.GetActiveStreams() {
		ui.streams = append(ui.streams, map[string]any{
			"id":         stream.ID,
			"subdomain":  stream.Subdomain,
			"start_time": stream.StartTime,
			"bytes_in":   stream.BytesReceived.Load(),
			"bytes_out":  stream.BytesSent.Load(),
			"is_active":  stream.IsActive,
		})
	}

	if len(ui.streams) < maxStreams {
		for _, stream := range metrics.GetInactiveStreams() {
			if time.Since(stream.StartTime) > maxInactive {
				continue
			}

			ui.streams = append(ui.streams, map[string]any{
				"id":         stream.ID,
				"subdomain":  stream.Subdomain,
				"start_time": stream.StartTime,
				"bytes_in":   stream.BytesReceived.Load(),
				"bytes_out":  stream.BytesSent.Load(),
				"is_active":  stream.IsActive,
			})

			if len(ui.streams) >= maxStreams {
				break
			}
		}
	}

	ui.mngr.ForEachClient(func(subdomain string, info *connection.Connection) {
		ui.clients = append(ui.clients, map[string]any{
			"subdomain":   subdomain,
			"connections": info.GetConnCount(subdomain),
			"last_active": info.GetLastActive(),
			"connected":   info.Connected(),
			"heartbeat":   info.GetHeartbeatStats(),
		})
	})
}
