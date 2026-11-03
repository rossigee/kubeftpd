package storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

func storageTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, ftpv1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func storageTestUser(kind, name string, ns *string) *ftpv1.User {
	return &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "alice", Namespace: "default"},
		Spec: ftpv1.UserSpec{
			Username:      "alice",
			HomeDirectory: "/home/alice",
			Backend:       ftpv1.BackendReference{Kind: kind, Name: name, Namespace: ns},
			Permissions:   ftpv1.UserPermissions{Read: true, Write: true, List: true},
		},
	}
}

// newWebDAVTestServer answers the PROPFIND the WebDAV backend sends when it is
// constructed, so the constructor can succeed without any external network.
func newWebDAVTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNewStorage_DispatchesOnBackendKind(t *testing.T) {
	dav := newWebDAVTestServer(t)
	// The MinIO constructor checks the bucket exists; answer that locally.
	minioSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(minioSrv.Close)
	c := storageTestClient(t,
		&ftpv1.FilesystemBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "fs", Namespace: "default"},
			Spec:       ftpv1.FilesystemBackendSpec{BasePath: t.TempDir()},
		},
		&ftpv1.WebDavBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "dav", Namespace: "default"},
			Spec: ftpv1.WebDavBackendSpec{
				Endpoint:    dav.URL,
				Credentials: ftpv1.WebDavCredentials{Username: "u", Password: "p"},
			},
		},
		&ftpv1.MinioBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "s3", Namespace: "default"},
			Spec: ftpv1.MinioBackendSpec{
				Endpoint: minioSrv.URL,
				Bucket:   "ftp-bucket",
				Region:   "us-east-1", // skips the bucket-location lookup
				Credentials: ftpv1.MinioCredentials{
					AccessKeyID:     "k",
					SecretAccessKey: "s",
				},
			},
		},
	)
	ctx := context.Background()

	st, err := NewStorage(ctx, storageTestUser("FilesystemBackend", "fs", nil), c)
	require.NoError(t, err)
	fsSt, ok := st.(*filesystemStorage)
	require.True(t, ok, "got %T", st)
	assert.Equal(t, "/home/alice", fsSt.currentDir)

	st, err = NewStorage(ctx, storageTestUser("WebDavBackend", "dav", nil), c)
	require.NoError(t, err)
	_, ok = st.(*webdavStorage)
	assert.True(t, ok, "got %T", st)

	st, err = NewStorage(ctx, storageTestUser("MinioBackend", "s3", nil), c)
	require.NoError(t, err)
	minioSt, ok := st.(*minioStorage)
	require.True(t, ok, "got %T", st)
	assert.Equal(t, "s3", minioSt.backendName)
}

func TestNewStorage_UnsupportedKind(t *testing.T) {
	c := storageTestClient(t)
	_, err := NewStorage(context.Background(), storageTestUser("SftpBackend", "x", nil), c)
	assert.EqualError(t, err, "unsupported backend kind: SftpBackend")
}

func TestNewStorage_BackendNotFound(t *testing.T) {
	c := storageTestClient(t)
	ctx := context.Background()

	_, err := NewStorage(ctx, storageTestUser("MinioBackend", "missing", nil), c)
	assert.ErrorContains(t, err, "failed to get MinioBackend default/missing")

	_, err = NewStorage(ctx, storageTestUser("WebDavBackend", "missing", nil), c)
	assert.ErrorContains(t, err, "failed to get WebDavBackend default/missing")

	_, err = NewStorage(ctx, storageTestUser("FilesystemBackend", "missing", nil), c)
	assert.ErrorContains(t, err, "failed to get FilesystemBackend default/missing")
}

// A backend reference with an explicit namespace must be looked up there, not
// in the User's namespace.
func TestNewStorage_ExplicitBackendNamespace(t *testing.T) {
	c := storageTestClient(t,
		&ftpv1.FilesystemBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "fs", Namespace: "shared"},
			Spec:       ftpv1.FilesystemBackendSpec{BasePath: t.TempDir()},
		},
	)
	shared := "shared"
	_, err := NewStorage(context.Background(), storageTestUser("FilesystemBackend", "fs", &shared), c)
	assert.NoError(t, err)

	_, err = NewStorage(context.Background(), storageTestUser("FilesystemBackend", "fs", nil), c)
	assert.ErrorContains(t, err, "failed to get FilesystemBackend default/fs")
}
