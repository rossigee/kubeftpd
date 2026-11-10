package ftp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

// Regression: goftp.io/server/v2 hands every FTP session the same Driver
// value and has no per-session driver factory, so a single KubeDriver serves
// all sessions of the process. The user and the storage implementation of a
// session therefore have to live in the session registry of the driver.
// Kept in the driver struct, the first session that completes a command pins
// its backend for every later session and one user's files are written to
// another user's backend.
func TestKubeDriver_SessionIsolation(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, ftpv1.AddToScheme(scheme))

	newUser := func(name, backend string) *ftpv1.User {
		return &ftpv1.User{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: ftpv1.UserSpec{
				Username:      name,
				Enabled:       true,
				HomeDirectory: "/" + name,
				Backend: ftpv1.BackendReference{
					Kind: "FilesystemBackend",
					Name: backend,
				},
				Permissions: ftpv1.UserPermissions{Read: true, Write: true},
			},
		}
	}

	newBackend := func(name, basePath string) *ftpv1.FilesystemBackend {
		return &ftpv1.FilesystemBackend{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec:       ftpv1.FilesystemBackendSpec{BasePath: basePath},
		}
	}

	alice := newUser("alice", "fs-alice")
	bob := newUser("bob", "fs-bob")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			alice,
			bob,
			newBackend("fs-alice", t.TempDir()),
			newBackend("fs-bob", t.TempDir()),
		).
		Build()

	driver := &KubeDriver{auth: NewKubeAuth(fakeClient), client: fakeClient}

	t.Run("each session gets the backend of its own user", func(t *testing.T) {
		sessionA, err := driver.sessionFor("sess-a", "alice", "192.0.2.1:50000")
		require.NoError(t, err)
		sessionB, err := driver.sessionFor("sess-b", "bob", "192.0.2.1:50001")
		require.NoError(t, err)

		assert.Equal(t, "alice", sessionA.username)
		assert.Equal(t, "bob", sessionB.username)
		assert.Equal(t, "fs-alice", sessionA.user.Spec.Backend.Name)
		assert.Equal(t, "fs-bob", sessionB.user.Spec.Backend.Name)
		assert.NotSame(t, sessionA.storage, sessionB.storage)
	})

	t.Run("a session keeps its storage", func(t *testing.T) {
		first, err := driver.sessionFor("sess-c", "alice", "192.0.2.2:50000")
		require.NoError(t, err)

		second, err := driver.sessionFor("sess-c", "alice", "192.0.2.2:50000")
		require.NoError(t, err)

		assert.Same(t, first.storage, second.storage)
	})

	t.Run("a session that authenticates as another user gets that backend", func(t *testing.T) {
		first, err := driver.sessionFor("sess-d", "alice", "192.0.2.3:50000")
		require.NoError(t, err)

		second, err := driver.sessionFor("sess-d", "bob", "192.0.2.3:50000")
		require.NoError(t, err)

		assert.Equal(t, "bob", second.username)
		assert.Equal(t, "fs-bob", second.user.Spec.Backend.Name)
		assert.NotSame(t, first.storage, second.storage)
	})
}

// A call without a session context falls back to the driver level session, so
// the operations that carry no context keep working.
func TestKubeDriver_FallbackSession(t *testing.T) {
	driver := &KubeDriver{
		authenticatedUser: "alice",
		user: &ftpv1.User{
			ObjectMeta: metav1.ObjectMeta{Name: "alice", Namespace: "default"},
			Spec:       ftpv1.UserSpec{Username: "alice", HomeDirectory: "/alice"},
		},
		storageImpl: &MockStorage{},
	}

	sess, err := driver.sessionForContext(nil)
	require.NoError(t, err)
	assert.Equal(t, "alice", sess.username)
	assert.Equal(t, "/alice", sess.user.Spec.HomeDirectory)
}
