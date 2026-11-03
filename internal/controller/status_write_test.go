package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

// failingStatusClient makes every status write fail, as a conflict would.
func failingStatusClient(t *testing.T, base client.WithWatch) client.Client {
	t.Helper()
	return interceptor.NewClient(base, interceptor.Funcs{
		SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
			return errors.New("the object has been modified")
		},
	})
}

// Regression: a status write that fails must fail the reconcile, so that it is
// retried. Logging it and reporting success left the condition stale.
func TestStatusWriteFailureFailsTheReconcile(t *testing.T) {
	ctx := context.Background()

	t.Run("webdav reachable endpoint", func(t *testing.T) {
		srv := webdavOKServer(t)
		backend := &ftpv1.WebDavBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "dav", Namespace: "default", Finalizers: []string{backendFinalizer}},
			Spec:       ftpv1.WebDavBackendSpec{Endpoint: srv.URL},
		}
		base := fake.NewClientBuilder().WithScheme(backendTestScheme(t)).WithObjects(backend).
			WithStatusSubresource(&ftpv1.WebDavBackend{}).Build()
		r := &WebDavBackendReconciler{Client: failingStatusClient(t, base), Scheme: backendTestScheme(t)}

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: reconcileKey("dav")})
		assert.ErrorContains(t, err, "the object has been modified")
	})

	t.Run("webdav unreachable endpoint", func(t *testing.T) {
		backend := &ftpv1.WebDavBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "dav", Namespace: "default", Finalizers: []string{backendFinalizer}},
			Spec:       ftpv1.WebDavBackendSpec{Endpoint: unreachableURL(t)},
		}
		base := fake.NewClientBuilder().WithScheme(backendTestScheme(t)).WithObjects(backend).
			WithStatusSubresource(&ftpv1.WebDavBackend{}).Build()
		r := &WebDavBackendReconciler{Client: failingStatusClient(t, base), Scheme: backendTestScheme(t)}

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: reconcileKey("dav")})
		assert.ErrorContains(t, err, "the object has been modified")
	})

	t.Run("minio reachable bucket", func(t *testing.T) {
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
		base := fake.NewClientBuilder().WithScheme(backendTestScheme(t)).WithObjects(backend).
			WithStatusSubresource(&ftpv1.MinioBackend{}).Build()
		r := &MinioBackendReconciler{Client: failingStatusClient(t, base), Scheme: backendTestScheme(t)}

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: reconcileKey("s3")})
		assert.ErrorContains(t, err, "the object has been modified")
	})

	t.Run("user validation result", func(t *testing.T) {
		backend := &ftpv1.FilesystemBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "fs", Namespace: "default"},
			Spec:       ftpv1.FilesystemBackendSpec{BasePath: "/data"},
		}
		u := &ftpv1.User{
			ObjectMeta: metav1.ObjectMeta{Name: "alice", Namespace: "default", Finalizers: []string{backendFinalizer}},
			Spec:       userTestSpec("fs"),
		}
		base := fake.NewClientBuilder().WithScheme(backendTestScheme(t)).WithObjects(backend, u).
			WithStatusSubresource(&ftpv1.User{}).Build()
		r := &UserReconciler{Client: failingStatusClient(t, base), Scheme: backendTestScheme(t)}

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: reconcileKey("alice")})
		assert.ErrorContains(t, err, "the object has been modified")
	})
}
