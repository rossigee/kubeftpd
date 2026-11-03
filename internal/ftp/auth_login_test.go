package ftp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

// loginAuth returns an auth whose user cache is loaded from the given users
// and secrets, as the controller does at start-up.
func loginAuth(t *testing.T, objs ...client.Object) *KubeAuth {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, ftpv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	auth := NewKubeAuth(fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build())
	require.NoError(t, auth.RefreshUserCache(context.Background()))
	return auth
}

func loginUser(name string, spec ftpv1.UserSpec) *ftpv1.User {
	return &ftpv1.User{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}, Spec: spec}
}

func TestKubeAuth_CheckPasswdLogin(t *testing.T) {
	regular := loginUser("alice", ftpv1.UserSpec{
		Username: "alice", Password: "alice-pw", HomeDirectory: "/alice", Enabled: true,
		Backend: ftpv1.BackendReference{Kind: "FilesystemBackend", Name: "fs"},
	})
	disabled := loginUser("bob", ftpv1.UserSpec{
		Username: "bob", Password: "bob-pw", HomeDirectory: "/bob", Enabled: false,
		Backend: ftpv1.BackendReference{Kind: "FilesystemBackend", Name: "fs"},
	})
	anon := loginUser("builtin-anonymous", ftpv1.UserSpec{
		Type: "anonymous", Username: "anonymous", HomeDirectory: "/pub", Enabled: true,
		Backend: ftpv1.BackendReference{Kind: "FilesystemBackend", Name: "fs"},
	})
	adminSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "admin-secret", Namespace: "default"},
		Data:       map[string][]byte{"password": []byte("admin-pw")},
	}
	admin := loginUser("builtin-admin", ftpv1.UserSpec{
		Type: "admin", Username: "admin", HomeDirectory: "/", Enabled: true,
		PasswordSecret: &ftpv1.UserSecretRef{Name: "admin-secret"},
		Backend:        ftpv1.BackendReference{Kind: "FilesystemBackend", Name: "fs"},
	})
	auth := loginAuth(t, regular, disabled, anon, admin, adminSecret)

	tests := []struct {
		name     string
		username string
		password string
		want     bool
	}{
		{"regular user, correct password", "alice", "alice-pw", true},
		{"regular user, wrong password", "alice", "guess", false},
		{"unknown user", "mallory", "anything", false},
		{"disabled user is refused even with the right password", "bob", "bob-pw", false},
		{"anonymous accepts any password", "anonymous", "name@example.org", true},
		{"admin, correct secret", "admin", "admin-pw", true},
		{"admin, wrong secret", "admin", "nope", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A nil session context is the connection-less path the tests can reach.
			ok, err := auth.CheckPasswd(nil, tt.username, tt.password)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, ok)
		})
	}
}

func TestKubeAuth_RefreshUserCacheReplacesAndReportsErrors(t *testing.T) {
	keep := loginUser("keep", ftpv1.UserSpec{Username: "keep", Password: "k", HomeDirectory: "/k", Enabled: true,
		Backend: ftpv1.BackendReference{Kind: "FilesystemBackend", Name: "fs"}})
	auth := loginAuth(t, keep)
	require.NotNil(t, auth.GetUser(context.Background(), "keep"))

	// A user that is no longer in the cluster must disappear from the cache on refresh.
	auth.userCache.Store("gone", loginUser("gone", ftpv1.UserSpec{Username: "gone"}))
	require.NoError(t, auth.RefreshUserCache(context.Background()))
	assert.Nil(t, auth.GetUser(context.Background(), "gone"))
	assert.NotNil(t, auth.GetUser(context.Background(), "keep"))

	// A failing list is returned, and the cache is left as it was.
	failing := &KubeAuth{client: failingListClient{Client: auth.client, err: errors.New("apiserver down")}}
	failing.userCache.Store("keep", loginUser("keep", ftpv1.UserSpec{Username: "keep"}))
	assert.ErrorContains(t, failing.RefreshUserCache(context.Background()), "apiserver down")
	assert.NotNil(t, failing.GetUser(context.Background(), "keep"))
}

type failingListClient struct {
	client.Client
	err error
}

func (f failingListClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return f.err
}

func TestKubeAuth_NilContextHelpers(t *testing.T) {
	auth := NewKubeAuth(nil)
	assert.Equal(t, "unknown", auth.clientIPFromCtx(nil), "no context means an unknown client")
	assert.Empty(t, auth.getSessionID(nil), "no context means no session identifier")
	assert.Empty(t, auth.GetSessionUser(""))
	auth.ClearSessionUser("") // must be a harmless no-op
}

// Regression: after the failure threshold, a correct password must still be
// refused for the locked-out username.
func TestKubeAuth_CheckPasswdLocksOutAfterRepeatedFailures(t *testing.T) {
	carol := loginUser("carol", ftpv1.UserSpec{
		Username: "carol", Password: "carol-pw", HomeDirectory: "/c", Enabled: true,
		Backend: ftpv1.BackendReference{Kind: "FilesystemBackend", Name: "fs"},
	})
	auth := loginAuth(t, carol)

	for i := 0; i < bruteForceMaxFails; i++ {
		ok, err := auth.CheckPasswd(nil, "carol", "wrong")
		require.NoError(t, err)
		require.False(t, ok)
	}

	ok, err := auth.CheckPasswd(nil, "carol", "carol-pw")
	assert.NoError(t, err)
	assert.False(t, ok, "a locked-out username is refused even with the correct password")
}

// A user whose password Secret is missing is refused, not allowed through and not
// reported as a server error to the client.
func TestKubeAuth_CheckPasswdMissingSecretIsRefused(t *testing.T) {
	admin := loginUser("builtin-admin", ftpv1.UserSpec{
		Type: "admin", Username: "admin", HomeDirectory: "/", Enabled: true,
		PasswordSecret: &ftpv1.UserSecretRef{Name: "no-such-secret"},
		Backend:        ftpv1.BackendReference{Kind: "FilesystemBackend", Name: "fs"},
	})
	regular := loginUser("dave", ftpv1.UserSpec{
		Username: "dave", HomeDirectory: "/d", Enabled: true,
		PasswordSecret: &ftpv1.UserSecretRef{Name: "no-such-secret"},
		Backend:        ftpv1.BackendReference{Kind: "FilesystemBackend", Name: "fs"},
	})
	auth := loginAuth(t, admin, regular)

	for _, name := range []string{"admin", "dave"} {
		ok, err := auth.CheckPasswd(nil, name, "anything")
		assert.NoError(t, err, name)
		assert.False(t, ok, name)
	}
}

// If the cluster cannot be listed, a cache miss is a plain "no such user".
func TestKubeAuth_GetUserReturnsNilWhenListFails(t *testing.T) {
	auth := &KubeAuth{client: failingListClient{Client: fake.NewClientBuilder().Build(), err: errors.New("down")}}
	assert.Nil(t, auth.GetUser(context.Background(), "anyone"))
}

func TestKubeAuth_GetUserLoadsOnMissAndCaches(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, ftpv1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		loginUser("late", ftpv1.UserSpec{Username: "late", Password: "p", HomeDirectory: "/late", Enabled: true}),
	).Build()
	auth := NewKubeAuth(c)
	ctx := context.Background()

	// Not in the cache yet: it is loaded from the cluster and then cached.
	u := auth.GetUser(ctx, "late")
	require.NotNil(t, u)
	assert.Equal(t, "late", u.Spec.Username)
	_, cached := auth.userCache.Load("late")
	assert.True(t, cached)

	assert.Nil(t, auth.GetUser(ctx, "nobody"), "unknown users are not found")
}

func TestKubeAuth_StartCacheRefreshPicksUpNewUsers(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, ftpv1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	auth := NewKubeAuth(c)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		auth.StartCacheRefresh(ctx, time.Millisecond)
		close(done)
	}()

	require.NoError(t, c.Create(context.Background(),
		loginUser("fresh", ftpv1.UserSpec{Username: "fresh", Password: "p", HomeDirectory: "/f", Enabled: true})))

	assert.Eventually(t, func() bool {
		_, ok := auth.userCache.Load("fresh")
		return ok
	}, 5*time.Second, time.Millisecond, "a refresh tick must load the new user")

	cancel()
	<-done
}

func TestKubeAuth_StartCacheRefreshReturnsWhenCancelled(t *testing.T) {
	auth := NewKubeAuth(fake.NewClientBuilder().Build())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		auth.StartCacheRefresh(ctx, time.Hour)
		close(done)
	}()
	<-done
}
