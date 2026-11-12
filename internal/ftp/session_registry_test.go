package ftp

import (
	"fmt"
	"testing"
	"time"
)

// goftp.io/server/v2 never calls Driver.Close - Session.Serve ends at
// sess.Close(), which only shuts the socket - so the session registry of the
// driver is the only thing that releases the storage of a session, and with it
// the transport of the backend it holds. It has to do that on the age of the
// session, not only once the registry has filled up: every retained session of
// a MinIO user keeps the connection pool, the TLS session cache and the read
// buffers of its backend alive.
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
		t.Error("a session idle for longer than the TTL survived the registry although the bound was not reached")
	}
	if _, ok := driver.sessions["live"]; !ok {
		t.Error("a session in use was pruned")
	}
	storage.AssertExpectations(t)
}

// A burst of connections must not be able to retain more backends than the
// bound, even while every session is still young enough to look alive.
func TestRegistryKeepsTheBound(t *testing.T) {
	driver := &KubeDriver{sessions: map[string]*driverSession{}}
	now := time.Now()
	storages := make([]*MockStorage, 0, maxDriverSessions+4)
	// A larger index is an older session; all of them are inside the TTL, so
	// only the bound can release anything here.
	for i := 0; i < maxDriverSessions+4; i++ {
		storage := &MockStorage{}
		storage.On("Close").Return(nil)
		storages = append(storages, storage)
		key := fmt.Sprintf("s%03d", i)
		driver.sessions[key] = &driverSession{
			username: key,
			storage:  storage,
			lastUsed: now.Add(-time.Duration(i) * time.Second),
		}
	}

	driver.evictStaleSessions()

	if len(driver.sessions) != maxDriverSessions {
		t.Fatalf("the registry holds %d sessions, want the bound of %d", len(driver.sessions), maxDriverSessions)
	}
	for i := maxDriverSessions; i < maxDriverSessions+4; i++ {
		key := fmt.Sprintf("s%03d", i)
		if _, ok := driver.sessions[key]; ok {
			t.Errorf("%s was retained although %d older sessions should have been released first", key, i)
		}
		storages[i].AssertCalled(t, "Close")
	}
	for i := 0; i < maxDriverSessions; i++ {
		storages[i].AssertNotCalled(t, "Close")
	}
}
