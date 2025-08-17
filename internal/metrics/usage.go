package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Connection metrics
	ActiveConnections = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "kubeftpd_active_connections",
			Help: "Number of active FTP connections",
		},
	)

	ConnectionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kubeftpd_connections_total",
			Help: "Total number of FTP connections",
		},
		[]string{"username", "client_ip"},
	)

	ConnectionDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "kubeftpd_connection_duration_seconds",
			Help:    "Duration of FTP connections in seconds",
			Buckets: []float64{1, 5, 10, 30, 60, 300, 600, 1800, 3600},
		},
		[]string{"username"},
	)

	// File operation metrics
	FileOperationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kubeftpd_file_operations_total",
			Help: "Total number of file operations",
		},
		[]string{"username", "operation", "backend_type", "result"},
	)

	FileTransferBytes = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kubeftpd_file_transfer_bytes_total",
			Help: "Total bytes transferred",
		},
		[]string{"username", "direction", "backend_type"},
	)

	FileTransferDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "kubeftpd_file_transfer_duration_seconds",
			Help:    "Duration of file transfers in seconds",
			Buckets: []float64{0.1, 0.5, 1, 5, 10, 30, 60, 300},
		},
		[]string{"username", "operation", "backend_type"},
	)

	// User activity metrics
	UserLoginTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kubeftpd_user_logins_total",
			Help: "Total user login attempts",
		},
		[]string{"username", "result"},
	)

	UserSessionDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "kubeftpd_user_session_duration_seconds",
			Help:    "Duration of user sessions in seconds",
			Buckets: []float64{60, 300, 600, 1800, 3600, 7200, 14400},
		},
		[]string{"username"},
	)

	// Backend metrics
	BackendOperationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kubeftpd_backend_operations_total",
			Help: "Total backend operations",
		},
		[]string{"backend_name", "backend_type", "operation", "result"},
	)

	BackendResponseTime = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "kubeftpd_backend_response_time_seconds",
			Help:    "Backend operation response time in seconds",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5},
		},
		[]string{"backend_name", "backend_type", "operation"},
	)

	// System metrics
	ErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kubeftpd_errors_total",
			Help: "Total errors by type",
		},
		[]string{"error_type", "component"},
	)

	ConfigReloads = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kubeftpd_config_reloads_total",
			Help: "Total configuration reloads",
		},
		[]string{"resource_type", "result"},
	)
)

// RecordConnection records a new connection
func RecordConnection(username, clientIP string) {
	ActiveConnections.Inc()
	ConnectionsTotal.WithLabelValues(username, clientIP).Inc()
}

// RecordConnectionClosed records a connection closure
func RecordConnectionClosed(username string, duration time.Duration) {
	ActiveConnections.Dec()
	ConnectionDuration.WithLabelValues(username).Observe(duration.Seconds())
}

// RecordFileOperation records a file operation
func RecordFileOperation(username, operation, backendType, result string) {
	FileOperationsTotal.WithLabelValues(username, operation, backendType, result).Inc()
}

// RecordFileTransfer records file transfer metrics
func RecordFileTransfer(username, direction, backendType string, bytes int64, duration time.Duration) {
	FileTransferBytes.WithLabelValues(username, direction, backendType).Add(float64(bytes))
	FileTransferDuration.WithLabelValues(username, direction, backendType).Observe(duration.Seconds())
}

// RecordUserLogin records a user login attempt
func RecordUserLogin(username, result string) {
	UserLoginTotal.WithLabelValues(username, result).Inc()
}

// RecordUserSession records user session metrics
func RecordUserSession(username string, duration time.Duration) {
	UserSessionDuration.WithLabelValues(username).Observe(duration.Seconds())
}

// RecordBackendOperation records backend operation metrics
func RecordBackendOperation(backendName, backendType, operation, result string, duration time.Duration) {
	BackendOperationsTotal.WithLabelValues(backendName, backendType, operation, result).Inc()
	BackendResponseTime.WithLabelValues(backendName, backendType, operation).Observe(duration.Seconds())
}

// RecordError records an error
func RecordError(errorType, component string) {
	ErrorsTotal.WithLabelValues(errorType, component).Inc()
}

// RecordConfigReload records a configuration reload
func RecordConfigReload(resourceType, result string) {
	ConfigReloads.WithLabelValues(resourceType, result).Inc()
}
