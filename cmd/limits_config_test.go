package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessEnvironmentOverrides_ConnectionLimits(t *testing.T) {
	config := &appConfig{}
	processEnvironmentOverrides(config)
	assert.Zero(t, config.ftpIdleTimeout, "unset means disabled")
	assert.Zero(t, config.ftpMaxConnections, "unset means no limit")

	t.Setenv("FTP_IDLE_TIMEOUT", "300")
	t.Setenv("FTP_MAX_CONNECTIONS", "100")
	processEnvironmentOverrides(config)
	assert.Equal(t, 300*time.Second, config.ftpIdleTimeout)
	assert.Equal(t, 100, config.ftpMaxConnections)

	t.Setenv("FTP_MAX_CONNECTIONS", "0")
	processEnvironmentOverrides(config)
	assert.Zero(t, config.ftpMaxConnections, "0 is an explicit no-limit")
}

func TestLogEncoderFor(t *testing.T) {
	tests := []struct {
		format  string
		wantNil bool
		wantErr bool
	}{
		{"", true, false},
		{"json", true, false},
		{"JSON", true, false},
		{"text", false, false},
		{"Text", false, false},
		{"yaml", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			enc, err := logEncoderFor(tt.format)
			if tt.wantErr {
				assert.ErrorContains(t, err, "unknown LOG_FORMAT")
			} else {
				require.NoError(t, err)
			}
			if tt.wantNil {
				assert.Nil(t, enc)
			} else {
				assert.NotNil(t, enc)
			}
		})
	}
}
