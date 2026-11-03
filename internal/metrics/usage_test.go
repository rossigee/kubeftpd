package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoUsernameLabels guards against usernames becoming metric label values.
// Labels such as these would let anyone who can scrape /metrics enumerate accounts.
func TestNoUsernameLabels(t *testing.T) {
	RecordConnection("192.0.2.1")
	RecordConnectionClosed(time.Second)
	RecordFileOperation("upload", "FilesystemBackend", "success")
	RecordFileTransfer("upload", "FilesystemBackend", 10, time.Second)
	RecordUserSession(time.Minute)

	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	gathered := make(map[string]bool)
	for _, f := range families {
		gathered[f.GetName()] = true
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				assert.NotEqualf(t, "username", l.GetName(),
					"%s must not carry a username label", f.GetName())
			}
		}
	}

	for _, name := range []string{
		"kubeftpd_connections_total",
		"kubeftpd_connection_duration_seconds",
		"kubeftpd_file_operations_total",
		"kubeftpd_file_transfer_bytes_total",
		"kubeftpd_file_transfer_duration_seconds",
		"kubeftpd_user_session_duration_seconds",
	} {
		assert.Truef(t, gathered[name], "%s must be gathered after recording", name)
	}
}
