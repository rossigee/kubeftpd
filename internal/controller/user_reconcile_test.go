package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

func userTestSpec(backendName string) ftpv1.UserSpec {
	return ftpv1.UserSpec{
		Username:      "alice",
		Password:      "plain-dev-password",
		HomeDirectory: "/alice",
		Enabled:       true,
		Backend:       ftpv1.BackendReference{Kind: "FilesystemBackend", Name: backendName},
		Permissions:   ftpv1.UserPermissions{Read: true, List: true},
	}
}

func TestUserReconcilerLifecycle(t *testing.T) {
	ctx := context.Background()

	t.Run("missing user is ignored", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(backendTestScheme(t)).Build()
		r := &UserReconciler{Client: c, Scheme: backendTestScheme(t)}

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: reconcileKey("gone")})
		assert.NoError(t, err)
		assert.Zero(t, res)
	})

	t.Run("first reconcile adds the finalizer and requeues", func(t *testing.T) {
		u := &ftpv1.User{
			ObjectMeta: metav1.ObjectMeta{Name: "alice", Namespace: "default"},
			Spec:       userTestSpec("fs"),
		}
		c := fake.NewClientBuilder().WithScheme(backendTestScheme(t)).WithObjects(u).
			WithStatusSubresource(&ftpv1.User{}).Build()
		r := &UserReconciler{Client: c, Scheme: backendTestScheme(t)}

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: reconcileKey("alice")})
		require.NoError(t, err)
		assert.Equal(t, time.Second, res.RequeueAfter)

		got := &ftpv1.User{}
		require.NoError(t, c.Get(ctx, reconcileKey("alice"), got))
		assert.Contains(t, got.Finalizers, backendFinalizer)
	})

	t.Run("valid user with an existing backend is Ready", func(t *testing.T) {
		backend := &ftpv1.FilesystemBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "fs", Namespace: "default"},
			Spec:       ftpv1.FilesystemBackendSpec{BasePath: "/data"},
		}
		u := &ftpv1.User{
			ObjectMeta: metav1.ObjectMeta{Name: "alice", Namespace: "default", Finalizers: []string{backendFinalizer}},
			Spec:       userTestSpec("fs"),
		}
		c := fake.NewClientBuilder().WithScheme(backendTestScheme(t)).WithObjects(backend, u).
			WithStatusSubresource(&ftpv1.User{}).Build()
		r := &UserReconciler{Client: c, Scheme: backendTestScheme(t)}

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: reconcileKey("alice")})
		require.NoError(t, err)
		assert.Zero(t, res)

		got := &ftpv1.User{}
		require.NoError(t, c.Get(ctx, reconcileKey("alice"), got))
		require.Len(t, got.Status.Conditions, 1)
		assert.Equal(t, metav1.ConditionTrue, got.Status.Conditions[0].Status)
		assert.Equal(t, "UserValid", got.Status.Conditions[0].Reason)
	})

	t.Run("user pointing at a missing backend is not Ready and requeued", func(t *testing.T) {
		u := &ftpv1.User{
			ObjectMeta: metav1.ObjectMeta{Name: "alice", Namespace: "default", Finalizers: []string{backendFinalizer}},
			Spec:       userTestSpec("does-not-exist"),
		}
		c := fake.NewClientBuilder().WithScheme(backendTestScheme(t)).WithObjects(u).
			WithStatusSubresource(&ftpv1.User{}).Build()
		r := &UserReconciler{Client: c, Scheme: backendTestScheme(t)}

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: reconcileKey("alice")})
		require.NoError(t, err)
		assert.Equal(t, 5*time.Minute, res.RequeueAfter)

		got := &ftpv1.User{}
		require.NoError(t, c.Get(ctx, reconcileKey("alice"), got))
		require.Len(t, got.Status.Conditions, 1)
		assert.Equal(t, metav1.ConditionFalse, got.Status.Conditions[0].Status)
		assert.Equal(t, "ValidationFailed", got.Status.Conditions[0].Reason)
		assert.NotEmpty(t, got.Status.Conditions[0].Message)
	})

	t.Run("deletion removes the finalizer", func(t *testing.T) {
		now := metav1.Now()
		u := &ftpv1.User{
			ObjectMeta: metav1.ObjectMeta{
				Name: "alice", Namespace: "default",
				Finalizers:        []string{backendFinalizer},
				DeletionTimestamp: &now,
			},
			Spec: userTestSpec("fs"),
		}
		c := fake.NewClientBuilder().WithScheme(backendTestScheme(t)).WithObjects(u).Build()
		r := &UserReconciler{Client: c, Scheme: backendTestScheme(t)}

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: reconcileKey("alice")})
		require.NoError(t, err)
		assert.Error(t, c.Get(ctx, reconcileKey("alice"), &ftpv1.User{}))
	})
}
