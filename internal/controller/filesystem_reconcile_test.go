package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

func fsBackendWithPath(basePath string, readOnly bool, pvc *ftpv1.VolumeClaimReference) *ftpv1.FilesystemBackend {
	return &ftpv1.FilesystemBackend{
		ObjectMeta: metav1.ObjectMeta{Name: "fs", Namespace: "default"},
		Spec: ftpv1.FilesystemBackendSpec{
			BasePath:       basePath,
			ReadOnly:       readOnly,
			VolumeClaimRef: pvc,
		},
	}
}

// reconcileFS runs one reconcile of the backend and returns its stored status.
func reconcileFS(t *testing.T, objs []client.Object, backend *ftpv1.FilesystemBackend) ftpv1.FilesystemBackendStatus {
	t.Helper()
	scheme := backendTestScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))
	all := append([]client.Object{backend}, objs...)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(all...).
		WithStatusSubresource(&ftpv1.FilesystemBackend{}).Build()
	r := &FilesystemBackendReconciler{Client: c, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: reconcileKey("fs")})
	require.NoError(t, err)

	got := &ftpv1.FilesystemBackend{}
	require.NoError(t, c.Get(context.Background(), reconcileKey("fs"), got))
	return got.Status
}

func TestFilesystemBackendReconcile_MissingAndDeleting(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(backendTestScheme(t)).Build()
	r := &FilesystemBackendReconciler{Client: c, Scheme: backendTestScheme(t)}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: reconcileKey("gone")})
	assert.NoError(t, err)
	assert.Zero(t, res)

	now := metav1.Now()
	deleting := fsBackendWithPath(t.TempDir(), false, nil)
	deleting.DeletionTimestamp = &now
	deleting.Finalizers = []string{backendFinalizer}
	res, err = (&FilesystemBackendReconciler{
		Client: fake.NewClientBuilder().WithScheme(backendTestScheme(t)).WithObjects(deleting).Build(),
		Scheme: backendTestScheme(t),
	}).Reconcile(context.Background(), ctrl.Request{NamespacedName: reconcileKey("fs")})
	assert.NoError(t, err)
	assert.Zero(t, res)
}

func TestFilesystemBackendReconcile_ValidatesBasePath(t *testing.T) {
	t.Run("missing path is not ready", func(t *testing.T) {
		st := reconcileFS(t, nil, fsBackendWithPath(filepath.Join(t.TempDir(), "absent"), false, nil))
		assert.False(t, st.Ready)
		assert.Equal(t, "Base path does not exist", st.Message)
		assert.Nil(t, st.AvailableSpace)
	})

	t.Run("a file is not a directory", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "plain")
		require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
		st := reconcileFS(t, nil, fsBackendWithPath(file, false, nil))
		assert.False(t, st.Ready)
		assert.Equal(t, "Base path is not a directory", st.Message)
	})

	t.Run("writable directory is ready and reports space", func(t *testing.T) {
		st := reconcileFS(t, nil, fsBackendWithPath(t.TempDir(), false, nil))
		assert.True(t, st.Ready)
		assert.Equal(t, "Filesystem backend is ready", st.Message)
		require.NotNil(t, st.AvailableSpace)
		require.NotNil(t, st.TotalSpace)
		assert.Positive(t, *st.TotalSpace)
		assert.NotNil(t, st.LastChecked)
	})

	t.Run("read-only directory is ready without a write probe", func(t *testing.T) {
		dir := t.TempDir()
		st := reconcileFS(t, nil, fsBackendWithPath(dir, true, nil))
		assert.True(t, st.Ready)
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Empty(t, entries, "a read-only backend must not create its write-probe file")
	})

	t.Run("writable backend leaves no probe file behind", func(t *testing.T) {
		dir := t.TempDir()
		st := reconcileFS(t, nil, fsBackendWithPath(dir, false, nil))
		assert.True(t, st.Ready)
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Empty(t, entries)
	})
}

func TestFilesystemBackendReconcile_VolumeClaim(t *testing.T) {
	dir := t.TempDir()
	ref := &ftpv1.VolumeClaimReference{Name: "ftp-data"}

	t.Run("missing claim", func(t *testing.T) {
		st := reconcileFS(t, nil, fsBackendWithPath(dir, false, ref))
		assert.False(t, st.Ready)
		assert.Equal(t, "Referenced PVC not found or not accessible", st.Message)
	})

	t.Run("unbound claim", func(t *testing.T) {
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "ftp-data", Namespace: "default"},
			Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
		}
		st := reconcileFS(t, []client.Object{pvc}, fsBackendWithPath(dir, false, ref))
		assert.False(t, st.Ready)
		assert.Equal(t, "Referenced PVC is not bound", st.Message)
	})

	t.Run("bound claim in another namespace", func(t *testing.T) {
		other := "storage"
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "ftp-data", Namespace: other},
			Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
		}
		withNS := &ftpv1.VolumeClaimReference{Name: "ftp-data", Namespace: &other}
		st := reconcileFS(t, []client.Object{pvc}, fsBackendWithPath(dir, false, withNS))
		assert.True(t, st.Ready)
	})
}

func TestFilesystemBackendReconcile_StatsAreUnknownForMissingPath(t *testing.T) {
	r := &FilesystemBackendReconciler{}
	avail, total := r.getStorageStats(filepath.Join(t.TempDir(), "absent"))
	assert.Equal(t, int64(-1), avail)
	assert.Equal(t, int64(-1), total)

	avail, total = r.getStorageStats(t.TempDir())
	assert.Positive(t, avail)
	assert.Positive(t, total)
}
