package webui

import (
	"embed"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/snakeice/gunnel/pkg/connection"
	"github.com/snakeice/gunnel/pkg/manager"
	"github.com/snakeice/gunnel/pkg/metrics"
)

//go:embed templates
var templates embed.FS

// WebUI handles the web interface for displaying tunnel status and requests.
type WebUI struct {
	router    *manager.Manager
	server    *http.Server
	mu        sync.RWMutex
	startTime time.Time
	stats     map[string]any
	clients   []map[string]any
	streams   []map[string]any
}

// NewWebUI creates a new WebUI instance.
func NewWebUI(router *manager.Manager, addr string) *WebUI {
	webui := &WebUI{
		router:    router,
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

	webui.server = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return webui
}

// Start starts the web UI server.
func (ui *WebUI) Start() error {
	return ui.server.ListenAndServe()
}

// Stop stops the web UI server.
func (ui *WebUI) Stop() error {
	return ui.server.Close()
}

// Addr returns the address of the web UI server.
func (ui *WebUI) Addr() string {
	return ui.server.Addr
}

// handleIndex serves the main web interface.
func (ui *WebUI) handleIndex(w http.ResponseWriter, r *http.Request) {
	content, err := templates.ReadFile("templates/index.html")
	if err != nil {
		http.Error(w, "Failed to read template", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write(content)
}

// handleStats serves the stats API endpoint.
func (ui *WebUI) handleStats(w http.ResponseWriter, r *http.Request) {
	ui.mu.RLock()
	defer ui.mu.RUnlock()

	stats := metrics.GetStreamStats()
	stats["uptime"] = time.Since(ui.startTime).Round(time.Second).String()
	stats["total_clients"] = len(ui.clients)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// handleClients serves the clients API endpoint.
func (ui *WebUI) handleClients(w http.ResponseWriter, r *http.Request) {
	ui.mu.RLock()
	defer ui.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ui.clients)
}

// handleStreams serves the streams API endpoint.
func (ui *WebUI) handleStreams(w http.ResponseWriter, r *http.Request) {
	ui.mu.RLock()
	defer ui.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ui.streams)
}

// UpdateStats updates the stats with current information.
func (ui *WebUI) UpdateStats() {
	ui.mu.Lock()
	defer ui.mu.Unlock()

	const maxInactive = 5 * time.Minute
	const maxStreams = 15

	ui.clients = make([]map[string]any, 0)

	// Update streams
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

	// Update clients
	ui.router.ForEachClient(func(subdomain string, info *connection.Connection) {
		ui.clients = append(ui.clients, map[string]any{
			"subdomain":   subdomain,
			"connections": info.GetConnCount(subdomain),
			"last_active": info.GetLastActive(),
			"connected":   info.Connected(),
			"heartbeat":   info.GetHeartbeatStats(),
		})
	})
}
