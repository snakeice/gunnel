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
	mngr      *manager.Manager
	Mux       *http.ServeMux
	mu        sync.RWMutex
	startTime time.Time
	stats     map[string]any
	clients   []map[string]any
	streams   []map[string]any
}

// NewWebUI creates a new WebUI instance.
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

// handleIndex serves the main web interface.
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

// handleStats serves the stats API endpoint.
func (ui *WebUI) handleStats(w http.ResponseWriter, _ *http.Request) {
	ui.mu.RLock()
	defer ui.mu.RUnlock()

	stats := metrics.GetStreamStats()
	stats["uptime"] = time.Since(ui.startTime).Round(time.Second).String()
	stats["total_clients"] = len(ui.clients)

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(stats); err != nil {
		http.Error(w, "Failed to encode JSON", http.StatusInternalServerError)
	}
}

// handleClients serves the clients API endpoint.
func (ui *WebUI) handleClients(w http.ResponseWriter, r *http.Request) {
	ui.mu.RLock()
	defer ui.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(ui.clients); err != nil {
		http.Error(w, "Failed to encode JSON", http.StatusInternalServerError)
		return
	}
}

// handleStreams serves the streams API endpoint.
func (ui *WebUI) handleStreams(w http.ResponseWriter, _ *http.Request) {
	ui.mu.RLock()
	defer ui.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(ui.streams); err != nil {
		http.Error(w, "Failed to encode JSON", http.StatusInternalServerError)
		return
	}
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
