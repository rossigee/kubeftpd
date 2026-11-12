package ftp

import (
	"testing"
	"time"
)

// goftp.io/server/v2 never calls Driver.Close - Session.Serve ends at
// sess.Close(), which only shuts the socket - so the session registry of the
// driver is the only thing that releases the storage of a session, and with it
// the transport of the backend it holds. stabler's overhead of keeping the
// stale entry is bounded by the 30-minute TTL here.
func TestStaleSessionsArePrunedBeforeTheBound(t *testing.T) {
	storage := &MockStorage{}
	storage.On("Close").Return(nil).Once()

	driver := &KubeDriver{sessions: map[string]*driverSession{
		"gone": {
			username: "scanner-archive",
			storage:  storage,
			lastUsed: time.Now().Add(-driverSessionTTL - time.Minute),
		},
		"live": {username: "scanner-general", storage: &MockStorage{}, lastUsed: time.Now()},
	}}

	driver.evictStaleSessions()

	if _, ok := driver.sessions["gone"]; ok {
		t.Error("a session idle for longer than the TTL survived the registry")
	}
	if _, ok := driver.sessions["live"]; !ok {
		t.Error("a session in use was pruned")
	}
	storage.AssertExpectations(t)
}
