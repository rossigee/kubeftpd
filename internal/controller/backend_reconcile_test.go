package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

const backendFinalizer = "ftp.golder.org/finalizer"

// unreachableURL returns the address of a server that has already been closed,
// so connection attempts fail immediately without leaving the machine.
func unreachableURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	return url
}

// webdavOKServer answers the PROPFIND sent when a WebDAV backend is constructed.
func webdavOKServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// bucketOKServer answers the bucket check made when a MinIO backend is constructed.
func bucketOKServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func backendTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, ftpv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	return scheme
}

func reconcileKey(name string) types.NamespacedName {
	return types.NamespacedName{Name: name, Namespace: "default"}
}

func TestShouldTestConnectivity(t *testing.T) {
	ctx := context.Background()
	r := &WebDavBackendReconciler{}
	backend := &ftpv1.WebDavBackend{ObjectMeta: metav1.ObjectMeta{Generation: 2}}

	assert.True(t, r.shouldTestConnectivity(ctx, backend), "never tested before")

	backend.Status.LastConnectivityTest = metav1.Now()
	backend.Status.ObservedGeneration = 1
	assert.True(t, r.shouldTestConnectivity(ctx, backend), "spec changed since last test")

	backend.Status.ObservedGeneration = 2
	assert.False(t, r.shouldTestConnectivity(ctx, backend), "already tested at this generation")
}

// The credentials of a backend live in a secret, so a change of the secret
// has to trigger a new connectivity test even when the spec of the backend
// did not change.
func TestShouldTestConnectivity_SecretChanged(t *testing.T) {
	ctx := context.Background()
	scheme := backendTestScheme(t)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "default", ResourceVersion: "1"},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
	r := &WebDavBackendReconciler{Client: c, Scheme: scheme}

	backend := &ftpv1.WebDavBackend{
		ObjectMeta: metav1.ObjectMeta{Name: "dav", Namespace: "default", Generation: 1},
		Spec: ftpv1.WebDavBackendSpec{
			Endpoint: "https://dav.example.invalid",
			Credentials: ftpv1.WebDavCredentials{
				UseSecret: &ftpv1.WebDavSecretRef{Name: "creds"},
			},
		},
	}
	backend.Status.LastConnectivityTest = metav1.Now()
	backend.Status.ObservedGeneration = 1

	assert.True(t, r.shouldTestConnectivity(ctx, backend), "the tested secret version is not recorded")

	backend.Annotations = map[string]string{ObservedSecretVersionAnnotation: "1"}
	assert.False(t, r.shouldTestConnectivity(ctx, backend), "the secret did not change")

	backend.Annotations[ObservedSecretVersionAnnotation] = "2"
	assert.True(t, r.shouldTestConnectivity(ctx, backend), "the secret changed")
}

func TestMarkConnectivityTested_RecordsGeneration(t *testing.T) {
	r := &WebDavBackendReconciler{}
	backend := &ftpv1.WebDavBackend{ObjectMeta: metav1.ObjectMeta{Generation: 5}}
	r.markConnectivityTested(backend)
	assert.False(t, backend.Status.LastConnectivityTest.IsZero())
	assert.Equal(t, int64(5), backend.Status.ObservedGeneration)
}

func TestWebDavBackendReconcile(t *testing.T) {
	ctx := context.Background()

	t.Run("missing object is ignored", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(backendTestScheme(t)).Build()
		r := &WebDavBackendReconciler{Client: c, Scheme: backendTestScheme(t)}

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: reconcileKey("gone")})
		assert.NoError(t, err)
		assert.Zero(t, res)
	})

	t.Run("first reconcile adds the finalizer and requeues", func(t *testing.T) {
		backend := &ftpv1.WebDavBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "dav", Namespace: "default"},
			Spec:       ftpv1.WebDavBackendSpec{Endpoint: "http://127.0.0.1:1"},
		}
		c := fake.NewClientBuilder().WithScheme(backendTestScheme(t)).WithObjects(backend).
			WithStatusSubresource(&ftpv1.WebDavBackend{}).Build()
		r := &WebDavBackendReconciler{Client: c, Scheme: backendTestScheme(t)}

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: reconcileKey("dav")})
		require.NoError(t, err)
		assert.Equal(t, time.Second, res.RequeueAfter)

		got := &ftpv1.WebDavBackend{}
		require.NoError(t, c.Get(ctx, reconcileKey("dav"), got))
		assert.Contains(t, got.Finalizers, backendFinalizer)
	})

	t.Run("reachable endpoint is marked Ready", func(t *testing.T) {
		srv := webdavOKServer(t)
		backend := &ftpv1.WebDavBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "dav", Namespace: "default", Finalizers: []string{backendFinalizer}},
			Spec:       ftpv1.WebDavBackendSpec{Endpoint: srv.URL},
		}
		c := fake.NewClientBuilder().WithScheme(backendTestScheme(t)).WithObjects(backend).
			WithStatusSubresource(&ftpv1.WebDavBackend{}).Build()
		r := &WebDavBackendReconciler{Client: c, Scheme: backendTestScheme(t)}

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: reconcileKey("dav")})
		require.NoError(t, err)
		assert.Zero(t, res)

		got := &ftpv1.WebDavBackend{}
		require.NoError(t, c.Get(ctx, reconcileKey("dav"), got))
		require.Len(t, got.Status.Conditions, 1)
		assert.Equal(t, "Ready", got.Status.Conditions[0].Type)
		assert.Equal(t, metav1.ConditionTrue, got.Status.Conditions[0].Status)
		assert.Equal(t, "ConnectionSuccessful", got.Status.Conditions[0].Reason)
		assert.False(t, got.Status.LastConnectivityTest.IsZero())
	})

	t.Run("unreachable endpoint is marked not Ready and requeued", func(t *testing.T) {
		backend := &ftpv1.WebDavBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "dav", Namespace: "default", Finalizers: []string{backendFinalizer}},
			Spec:       ftpv1.WebDavBackendSpec{Endpoint: unreachableURL(t)},
		}
		c := fake.NewClientBuilder().WithScheme(backendTestScheme(t)).WithObjects(backend).
			WithStatusSubresource(&ftpv1.WebDavBackend{}).Build()
		r := &WebDavBackendReconciler{Client: c, Scheme: backendTestScheme(t)}

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: reconcileKey("dav")})
		require.NoError(t, err)
		assert.Equal(t, 5*time.Minute, res.RequeueAfter)

		got := &ftpv1.WebDavBackend{}
		require.NoError(t, c.Get(ctx, reconcileKey("dav"), got))
		require.Len(t, got.Status.Conditions, 1)
		assert.Equal(t, metav1.ConditionFalse, got.Status.Conditions[0].Status)
		assert.Equal(t, "ConnectionFailed", got.Status.Conditions[0].Reason)
	})

	t.Run("up-to-date backend is not re-tested", func(t *testing.T) {
		backend := &ftpv1.WebDavBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "dav", Namespace: "default", Finalizers: []string{backendFinalizer}, Generation: 3},
			Spec:       ftpv1.WebDavBackendSpec{Endpoint: unreachableURL(t)},
			Status: ftpv1.WebDavBackendStatus{
				LastConnectivityTest: metav1.Now(),
				ObservedGeneration:   3,
			},
		}
		c := fake.NewClientBuilder().WithScheme(backendTestScheme(t)).WithObjects(backend).
			WithStatusSubresource(&ftpv1.WebDavBackend{}).Build()
		r := &WebDavBackendReconciler{Client: c, Scheme: backendTestScheme(t)}

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: reconcileKey("dav")})
		require.NoError(t, err)
		assert.Zero(t, res, "no connectivity test, so no requeue")

		got := &ftpv1.WebDavBackend{}
		require.NoError(t, c.Get(ctx, reconcileKey("dav"), got))
		assert.Empty(t, got.Status.Conditions, "the unreachable endpoint was not contacted")
	})

	t.Run("deletion removes the finalizer", func(t *testing.T) {
		now := metav1.Now()
		backend := &ftpv1.WebDavBackend{
			ObjectMeta: metav1.ObjectMeta{
				Name: "dav", Namespace: "default",
				Finalizers:        []string{backendFinalizer},
				DeletionTimestamp: &now,
			},
			Spec: ftpv1.WebDavBackendSpec{Endpoint: "http://127.0.0.1:1"},
		}
		c := fake.NewClientBuilder().WithScheme(backendTestScheme(t)).WithObjects(backend).Build()
		r := &WebDavBackendReconciler{Client: c, Scheme: backendTestScheme(t)}

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: reconcileKey("dav")})
		require.NoError(t, err)

		err = c.Get(ctx, reconcileKey("dav"), &ftpv1.WebDavBackend{})
		assert.Error(t, err, "the object is gone once its last finalizer is removed")
	})
}

func TestMinioBackendReconcile(t *testing.T) {
	ctx := context.Background()

	t.Run("missing object is ignored", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(backendTestScheme(t)).Build()
		r := &MinioBackendReconciler{Client: c, Scheme: backendTestScheme(t)}

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: reconcileKey("gone")})
		assert.NoError(t, err)
		assert.Zero(t, res)
	})

	t.Run("reachable bucket is marked Ready", func(t *testing.T) {
		srv := bucketOKServer(t)
		backend := &ftpv1.MinioBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "s3", Namespace: "default", Finalizers: []string{backendFinalizer}},
			Spec: ftpv1.MinioBackendSpec{
				Endpoint:    srv.URL,
				Bucket:      "ftp-bucket",
				Region:      "us-east-1",
				Credentials: ftpv1.MinioCredentials{AccessKeyID: "k", SecretAccessKey: "s"},
			},
		}
		c := fake.NewClientBuilder().WithScheme(backendTestScheme(t)).WithObjects(backend).
			WithStatusSubresource(&ftpv1.MinioBackend{}).Build()
		r := &MinioBackendReconciler{Client: c, Scheme: backendTestScheme(t)}

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: reconcileKey("s3")})
		require.NoError(t, err)
		assert.Zero(t, res)

		got := &ftpv1.MinioBackend{}
		require.NoError(t, c.Get(ctx, reconcileKey("s3"), got))
		require.Len(t, got.Status.Conditions, 1)
		assert.Equal(t, metav1.ConditionTrue, got.Status.Conditions[0].Status)
		assert.Equal(t, "ConnectionSuccessful", got.Status.Conditions[0].Reason)
	})

	t.Run("unreachable endpoint is marked not Ready", func(t *testing.T) {
		backend := &ftpv1.MinioBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "s3", Namespace: "default", Finalizers: []string{backendFinalizer}},
			Spec: ftpv1.MinioBackendSpec{
				Endpoint:    unreachableURL(t),
				Bucket:      "ftp-bucket",
				Region:      "us-east-1",
				Credentials: ftpv1.MinioCredentials{AccessKeyID: "k", SecretAccessKey: "s"},
			},
		}
		c := fake.NewClientBuilder().WithScheme(backendTestScheme(t)).WithObjects(backend).
			WithStatusSubresource(&ftpv1.MinioBackend{}).Build()
		r := &MinioBackendReconciler{Client: c, Scheme: backendTestScheme(t)}

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: reconcileKey("s3")})
		require.NoError(t, err)
		assert.Equal(t, 5*time.Minute, res.RequeueAfter)

		got := &ftpv1.MinioBackend{}
		require.NoError(t, c.Get(ctx, reconcileKey("s3"), got))
		require.Len(t, got.Status.Conditions, 1)
		assert.Equal(t, metav1.ConditionFalse, got.Status.Conditions[0].Status)
		assert.Equal(t, "ConnectionFailed", got.Status.Conditions[0].Reason)
	})

	t.Run("deletion removes the finalizer", func(t *testing.T) {
		now := metav1.Now()
		backend := &ftpv1.MinioBackend{
			ObjectMeta: metav1.ObjectMeta{
				Name: "s3", Namespace: "default",
				Finalizers:        []string{backendFinalizer},
				DeletionTimestamp: &now,
			},
		}
		c := fake.NewClientBuilder().WithScheme(backendTestScheme(t)).WithObjects(backend).Build()
		r := &MinioBackendReconciler{Client: c, Scheme: backendTestScheme(t)}

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: reconcileKey("s3")})
		require.NoError(t, err)
		assert.Error(t, c.Get(ctx, reconcileKey("s3"), &ftpv1.MinioBackend{}))
	})
}
