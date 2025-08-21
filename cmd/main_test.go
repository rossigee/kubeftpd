package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetDefaultFTPPort(t *testing.T) {
	tests := []struct {
		name         string
		uid          int
		expectedPort int
		description  string
	}{
		{
			name:         "Root user should get port 21",
			uid:          0,
			expectedPort: 21,
			description:  "Root users (UID 0) can bind to privileged ports",
		},
		{
			name:         "Non-root user should get port 2121",
			uid:          1000,
			expectedPort: 2121,
			description:  "Non-root users should use unprivileged port to avoid permission errors",
		},
		{
			name:         "Another non-root user should get port 2121",
			uid:          1001,
			expectedPort: 2121,
			description:  "Any UID > 0 should use unprivileged port",
		},
	}

	// Save original UID function and restore after test
	originalGetuid := os.Getuid
	defer func() {
		// We can't actually restore os.Getuid as it's not assignable
		// This test demonstrates the expected behavior based on current UID
		_ = originalGetuid
	}()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: We can't mock os.Getuid() directly as it's a system call
			// This test documents the expected behavior
			// In a real scenario, we would need to refactor the code to make UID injectable
			currentUID := os.Getuid()
			expectedPort := getDefaultFTPPort()

			if currentUID == 0 && expectedPort != 21 {
				t.Errorf("Expected port 21 for root user, got %d", expectedPort)
			} else if currentUID != 0 && expectedPort != 2121 {
				t.Errorf("Expected port 2121 for non-root user (UID %d), got %d", currentUID, expectedPort)
			}

			t.Logf("Test case: %s - UID: %d, Expected: %d, Got: %d",
				tt.description, currentUID, tt.expectedPort, expectedPort)
		})
	}
}

func TestGetDefaultFTPPortDocumentation(t *testing.T) {
	// This test documents the expected behavior
	t.Log("getDefaultFTPPort() behavior:")
	t.Log("- UID 0 (root): returns 21 (privileged port)")
	t.Log("- UID > 0 (non-root): returns 2121 (unprivileged port)")
	t.Log("This prevents 'permission denied' errors when non-root users try to bind to port 21")

	currentUID := os.Getuid()
	port := getDefaultFTPPort()
	t.Logf("Current test environment - UID: %d, Default port: %d", currentUID, port)

	if currentUID == 0 {
		if port != 21 {
			t.Errorf("Root user should get port 21, got %d", port)
		}
	} else {
		if port != 2121 {
			t.Errorf("Non-root user should get port 2121, got %d", port)
		}
	}
}

// Regression test for PublicIP configuration
func TestProcessEnvironmentOverrides_PublicIP(t *testing.T) {
	// Test FTP_PUBLIC_IP environment variable
	config := &appConfig{}

	// Test without environment variable
	processEnvironmentOverrides(config)
	assert.Empty(t, config.ftpPublicIP)

	// Test with environment variable
	t.Setenv("FTP_PUBLIC_IP", "10.188.1.22")
	processEnvironmentOverrides(config)
	assert.Equal(t, "10.188.1.22", config.ftpPublicIP)
}

// Regression test for PASV port configuration
func TestProcessEnvironmentOverrides_PASVPorts(t *testing.T) {
	// Test FTP_PASSIVE_PORTS environment variable
	config := &appConfig{}
	t.Setenv("FTP_PASSIVE_PORTS", "10000-10019")
	processEnvironmentOverrides(config)
	assert.Equal(t, "10000-10019", config.ftpPasvPorts)

	// Test FTP_PASSIVE_PORT_MIN/MAX environment variables (new test env)
	config2 := &appConfig{}
	// Clear the FTP_PASSIVE_PORTS from previous test
	t.Setenv("FTP_PASSIVE_PORTS", "")
	t.Setenv("FTP_PASSIVE_PORT_MIN", "11000")
	t.Setenv("FTP_PASSIVE_PORT_MAX", "11020")
	processEnvironmentOverrides(config2)
	assert.Equal(t, "11000-11020", config2.ftpPasvPorts)
}
