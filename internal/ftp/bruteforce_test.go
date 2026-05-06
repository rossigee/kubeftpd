package ftp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBruteForce_NoLockoutBelowThreshold(t *testing.T) {
	b := newBruteForceProtector()
	for i := 0; i < bruteForceMaxFails-1; i++ {
		b.RecordFailure("alice", "10.0.0.1:1234")
	}
	assert.False(t, b.IsLockedOut("alice", "10.0.0.1:1234"))
}

func TestBruteForce_LocksOutAtThreshold(t *testing.T) {
	b := newBruteForceProtector()
	for i := 0; i < bruteForceMaxFails; i++ {
		b.RecordFailure("alice", "10.0.0.1:1234")
	}
	assert.True(t, b.IsLockedOut("alice", "10.0.0.2:5678")) // different port/IP, same username
}

func TestBruteForce_IPLockout(t *testing.T) {
	b := newBruteForceProtector()
	for i := 0; i < bruteForceMaxFails; i++ {
		b.RecordFailure("bob", "192.168.1.1:9000")
	}
	// Different username, same IP — IP-based lockout fires
	assert.True(t, b.IsLockedOut("carol", "192.168.1.1:9000"))
}

func TestBruteForce_SuccessClearsLockout(t *testing.T) {
	b := newBruteForceProtector()
	for i := 0; i < bruteForceMaxFails; i++ {
		b.RecordFailure("alice", "10.0.0.1:1234")
	}
	assert.True(t, b.IsLockedOut("alice", "10.0.0.1:1234"))

	b.RecordSuccess("alice", "10.0.0.1:1234")
	assert.False(t, b.IsLockedOut("alice", "10.0.0.1:1234"))
}

func TestBruteForce_WindowExpiry(t *testing.T) {
	b := newBruteForceProtector()
	// Manually place an entry whose window has already expired
	e := b.entryFor(&b.byUsername, "dave")
	e.mu.Lock()
	e.count = bruteForceMaxFails - 1
	e.windowStart = time.Now().Add(-(bruteForceWindow + time.Second))
	e.mu.Unlock()

	// One more failure — but since the window expired, count resets to 1 (< threshold)
	b.RecordFailure("dave", "10.0.0.3:1234")
	assert.False(t, b.IsLockedOut("dave", "10.0.0.3:1234"))
}

func TestBruteForce_LockoutExpiry(t *testing.T) {
	b := newBruteForceProtector()
	e := b.entryFor(&b.byUsername, "eve")
	e.mu.Lock()
	e.count = bruteForceMaxFails
	e.lockedUntil = time.Now().Add(-time.Second) // already expired
	e.mu.Unlock()

	assert.False(t, b.IsLockedOut("eve", "10.0.0.4:1234"))
}

func TestBruteForce_UnknownIPIgnored(t *testing.T) {
	b := newBruteForceProtector()
	for i := 0; i < bruteForceMaxFails; i++ {
		b.RecordFailure("frank", "unknown")
	}
	// IP-based tracking is skipped for "unknown"; only username is checked
	assert.True(t, b.IsLockedOut("frank", "unknown"))
	// Different username should not be affected
	assert.False(t, b.IsLockedOut("grace", "unknown"))
}

func TestExtractIP(t *testing.T) {
	assert.Equal(t, "10.0.0.1", extractIP("10.0.0.1:1234"))
	assert.Equal(t, "::1", extractIP("[::1]:1234"))
	assert.Equal(t, "", extractIP("unknown"))
	assert.Equal(t, "", extractIP(""))
	assert.Equal(t, "192.168.1.1", extractIP("192.168.1.1:80"))
}
