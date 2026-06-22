package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const namespace = "gunnel"

const unknownLabel = "unknown"

//nolint:gochecknoglobals // prometheus metrics are package-level by convention
var (
	// BytesReceivedTotal tracks total bytes received from clients (per subdomain).
	BytesReceivedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "bytes_received_total",
			Help:      "Total bytes received from tunnel clients by subdomain.",
		},
		[]string{"subdomain"},
	)

	// BytesSentTotal tracks total bytes sent to clients (per subdomain).
	BytesSentTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "bytes_sent_total",
			Help:      "Total bytes sent to tunnel clients by subdomain.",
		},
		[]string{"subdomain"},
	)

	// RequestsTotal tracks total HTTP requests processed (per subdomain and status).
	RequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "requests_total",
			Help:      "Total HTTP requests processed by subdomain and HTTP status code.",
		},
		[]string{"subdomain", "method", "status"},
	)

	// RequestDuration tracks request processing time in seconds.
	RequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "request_duration_seconds",
			Help:      "HTTP request duration in seconds by subdomain.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"subdomain", "method"},
	)

	// ActiveStreams tracks currently active tunnel streams.
	ActiveStreams = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "active_streams",
			Help:      "Number of currently active tunnel streams by subdomain.",
		},
		[]string{"subdomain"},
	)

	// StreamConnections tracks total stream connections (not individual requests).
	StreamConnections = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "stream_connections_total",
			Help:      "Total stream connections established by subdomain.",
		},
		[]string{"subdomain"},
	)

	// TunnelErrors tracks tunnel-related errors.
	TunnelErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "tunnel_errors_total",
			Help:      "Total tunnel errors by subdomain and error type.",
		},
		[]string{"subdomain", "error_type"},
	)
)

// RecordBytesReceived increments the bytes received counter for a subdomain.
func RecordBytesReceived(subdomain string, bytes int) {
	if subdomain == "" {
		subdomain = unknownLabel
	}
	BytesReceivedTotal.WithLabelValues(subdomain).Add(float64(bytes))
}

// RecordBytesSent increments the bytes sent counter for a subdomain.
func RecordBytesSent(subdomain string, bytes int) {
	if subdomain == "" {
		subdomain = unknownLabel
	}
	BytesSentTotal.WithLabelValues(subdomain).Add(float64(bytes))
}

// RecordRequest records a completed HTTP request with its duration and status.
func RecordRequest(subdomain string, method string, statusCode int, durationSeconds float64) {
	if subdomain == "" {
		subdomain = unknownLabel
	}
	RequestsTotal.WithLabelValues(subdomain, method, statusCodeString(statusCode)).Inc()
	RequestDuration.WithLabelValues(subdomain, method).Observe(durationSeconds)
}

// IncActiveStream increments the active streams gauge for a subdomain.
func IncActiveStream(subdomain string) {
	if subdomain == "" {
		subdomain = unknownLabel
	}
	ActiveStreams.WithLabelValues(subdomain).Inc()
}

// DecActiveStream decrements the active streams gauge for a subdomain.
func DecActiveStream(subdomain string) {
	if subdomain == "" {
		subdomain = unknownLabel
	}
	ActiveStreams.WithLabelValues(subdomain).Dec()
}

// RecordStreamConnection records a new stream connection.
func RecordStreamConnection(subdomain string) {
	if subdomain == "" {
		subdomain = unknownLabel
	}
	StreamConnections.WithLabelValues(subdomain).Inc()
}

// RecordTunnelError records a tunnel error.
func RecordTunnelError(subdomain string, errorType string) {
	if subdomain == "" {
		subdomain = unknownLabel
	}
	TunnelErrors.WithLabelValues(subdomain, errorType).Inc()
}

// statusCodeString converts an HTTP status code to a string label.
func statusCodeString(code int) string {
	// Group status codes by hundreds for better cardinality
	switch {
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500:
		return "5xx"
	default:
		return unknownLabel
	}
}
