package ftp

import (
	"net"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	bruteForceWindow   = 5 * time.Minute
	bruteForceMaxFails = 10
	bruteForceLockout  = 15 * time.Minute
)

var (
	authLockoutsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kubeftpd_auth_lockouts_total",
			Help: "Total number of brute-force lockout events",
		},
		[]string{"type"}, // "username" or "ip"
	)

	authLockedAccounts = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kubeftpd_auth_locked_accounts",
			Help: "Number of currently locked usernames or IPs",
		},
		[]string{"type"},
	)
)

// failureEntry tracks failed attempts for a single key (username or IP).
type failureEntry struct {
	mu          sync.Mutex
	count       int
	windowStart time.Time
	lockedUntil time.Time
}

// record increments the failure count; returns true if the key is now locked out.
func (e *failureEntry) record(now time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Reset window if expired
	if now.Sub(e.windowStart) > bruteForceWindow {
		e.count = 0
		e.windowStart = now
	}

	e.count++
	if e.count >= bruteForceMaxFails && e.lockedUntil.IsZero() {
		e.lockedUntil = now.Add(bruteForceLockout)
		return true
	}
	return false
}

// isLocked reports whether the key is currently locked out.
func (e *failureEntry) isLocked(now time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lockedUntil.IsZero() {
		return false
	}
	if now.After(e.lockedUntil) {
		e.lockedUntil = time.Time{}
		e.count = 0
		return false
	}
	return true
}

// reset clears failure state on successful authentication.
func (e *failureEntry) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.count = 0
	e.lockedUntil = time.Time{}
}

// BruteForceProtector guards against brute-force authentication attacks by
// tracking per-username and per-IP failed attempts with windowed lockouts.
type BruteForceProtector struct {
	byUsername sync.Map
	byIP       sync.Map
}

func newBruteForceProtector() *BruteForceProtector {
	return &BruteForceProtector{}
}

func (b *BruteForceProtector) entryFor(m *sync.Map, key string) *failureEntry {
	v, _ := m.LoadOrStore(key, &failureEntry{windowStart: time.Now()})
	return v.(*failureEntry)
}

// IsLockedOut returns true (and logs the reason) if the username or IP is
// currently locked out.
func (b *BruteForceProtector) IsLockedOut(username, clientIP string) bool {
	logger := ctrl.Log.WithName("bruteforce")
	now := time.Now()

	ip := extractIP(clientIP)

	if b.entryFor(&b.byUsername, username).isLocked(now) {
		logger.Info("Authentication blocked: username locked out", "username", username)
		return true
	}
	if ip != "" && b.entryFor(&b.byIP, ip).isLocked(now) {
		logger.Info("Authentication blocked: IP locked out", "ip", ip)
		return true
	}
	return false
}

// RecordFailure records a failed authentication attempt and locks out if threshold reached.
func (b *BruteForceProtector) RecordFailure(username, clientIP string) {
	logger := ctrl.Log.WithName("bruteforce")
	now := time.Now()
	ip := extractIP(clientIP)

	if b.entryFor(&b.byUsername, username).record(now) {
		logger.Info("Username locked out after repeated failures", "username", username,
			"threshold", bruteForceMaxFails, "lockout_duration", bruteForceLockout)
		authLockoutsTotal.WithLabelValues("username").Inc()
		authLockedAccounts.WithLabelValues("username").Inc()
	}
	if ip != "" {
		if b.entryFor(&b.byIP, ip).record(now) {
			logger.Info("IP locked out after repeated failures", "ip", ip,
				"threshold", bruteForceMaxFails, "lockout_duration", bruteForceLockout)
			authLockoutsTotal.WithLabelValues("ip").Inc()
			authLockedAccounts.WithLabelValues("ip").Inc()
		}
	}
}

// RecordSuccess clears failure state after a successful authentication.
func (b *BruteForceProtector) RecordSuccess(username, clientIP string) {
	ip := extractIP(clientIP)
	b.entryFor(&b.byUsername, username).reset()
	if ip != "" {
		b.entryFor(&b.byIP, ip).reset()
	}
}

// extractIP parses the host from an "ip:port" address string.
// Returns the raw string if it doesn't look like a host:port pair.
func extractIP(addr string) string {
	if addr == "" || addr == "unknown" {
		return ""
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
