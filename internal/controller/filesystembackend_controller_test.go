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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

func createTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = ftpv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	return scheme
}

func createTestDir(t *testing.T) string {
	tmpDir, err := os.MkdirTemp("", "kubeftpd-controller-test-*")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.RemoveAll(tmpDir)
	})
	return tmpDir
}

func TestFilesystemBackendReconciler_ReconcileNormal(t *testing.T) {
	testDir := createTestDir(t)
	scheme := createTestScheme()

	// Create test FilesystemBackend
	backend := &ftpv1.FilesystemBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-backend",
			Namespace: "default",
		},
		Spec: ftpv1.FilesystemBackendSpec{
			BasePath:    testDir,
			ReadOnly:    false,
			FileMode:    "0644",
			DirMode:     "0755",
			MaxFileSize: 1024 * 1024,
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backend).
		WithStatusSubresource(&ftpv1.FilesystemBackend{}).
		Build()

	reconciler := &FilesystemBackendReconciler{
		Client: client,
		Scheme: scheme,
	}

	ctx := context.Background()
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-backend",
			Namespace: "default",
		},
	}

	// Perform reconciliation
	result, err := reconciler.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Check that status was updated
	var updatedBackend ftpv1.FilesystemBackend
	err = client.Get(ctx, req.NamespacedName, &updatedBackend)
	assert.NoError(t, err)
	assert.True(t, updatedBackend.Status.Ready)
	assert.Equal(t, "Filesystem backend is ready", updatedBackend.Status.Message)
	assert.NotNil(t, updatedBackend.Status.LastChecked)
}

func TestFilesystemBackendReconciler_ReconcileWithPVC(t *testing.T) {
	testDir := createTestDir(t)
	scheme := createTestScheme()

	// Create test PVC
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pvc",
			Namespace: "default",
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimBound,
		},
	}

	// Create test FilesystemBackend with PVC reference
	backend := &ftpv1.FilesystemBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-backend",
			Namespace: "default",
		},
		Spec: ftpv1.FilesystemBackendSpec{
			BasePath:    testDir,
			ReadOnly:    false,
			FileMode:    "0644",
			DirMode:     "0755",
			MaxFileSize: 1024 * 1024,
			VolumeClaimRef: &ftpv1.VolumeClaimReference{
				Name: "test-pvc",
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backend, pvc).
		WithStatusSubresource(&ftpv1.FilesystemBackend{}).
		Build()

	reconciler := &FilesystemBackendReconciler{
		Client: client,
		Scheme: scheme,
	}

	ctx := context.Background()
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-backend",
			Namespace: "default",
		},
	}

	// Perform reconciliation
	result, err := reconciler.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Check that status was updated
	var updatedBackend ftpv1.FilesystemBackend
	err = client.Get(ctx, req.NamespacedName, &updatedBackend)
	assert.NoError(t, err)
	assert.True(t, updatedBackend.Status.Ready)
	assert.Equal(t, "Filesystem backend is ready", updatedBackend.Status.Message)
}

func TestFilesystemBackendReconciler_ReconcileInvalidPath(t *testing.T) {
	scheme := createTestScheme()

	// Create test FilesystemBackend with invalid path
	backend := &ftpv1.FilesystemBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-backend",
			Namespace: "default",
		},
		Spec: ftpv1.FilesystemBackendSpec{
			BasePath:    "/nonexistent/path",
			ReadOnly:    false,
			FileMode:    "0644",
			DirMode:     "0755",
			MaxFileSize: 1024 * 1024,
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backend).
		WithStatusSubresource(&ftpv1.FilesystemBackend{}).
		Build()

	reconciler := &FilesystemBackendReconciler{
		Client: client,
		Scheme: scheme,
	}

	ctx := context.Background()
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-backend",
			Namespace: "default",
		},
	}

	// Perform reconciliation
	result, err := reconciler.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Check that status shows not ready
	var updatedBackend ftpv1.FilesystemBackend
	err = client.Get(ctx, req.NamespacedName, &updatedBackend)
	assert.NoError(t, err)
	assert.False(t, updatedBackend.Status.Ready)
	assert.Equal(t, "Base path does not exist", updatedBackend.Status.Message)
}

func TestFilesystemBackendReconciler_ReconcileReadOnlyMode(t *testing.T) {
	testDir := createTestDir(t)
	scheme := createTestScheme()

	// Create test FilesystemBackend in read-only mode
	backend := &ftpv1.FilesystemBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-backend",
			Namespace: "default",
		},
		Spec: ftpv1.FilesystemBackendSpec{
			BasePath:    testDir,
			ReadOnly:    true,
			FileMode:    "0644",
			DirMode:     "0755",
			MaxFileSize: 1024 * 1024,
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backend).
		WithStatusSubresource(&ftpv1.FilesystemBackend{}).
		Build()

	reconciler := &FilesystemBackendReconciler{
		Client: client,
		Scheme: scheme,
	}

	ctx := context.Background()
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-backend",
			Namespace: "default",
		},
	}

	// Perform reconciliation
	result, err := reconciler.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Check that status shows ready (read-only mode should still be valid)
	var updatedBackend ftpv1.FilesystemBackend
	err = client.Get(ctx, req.NamespacedName, &updatedBackend)
	assert.NoError(t, err)
	assert.True(t, updatedBackend.Status.Ready)
	assert.Equal(t, "Filesystem backend is ready", updatedBackend.Status.Message)
}

func TestFilesystemBackendReconciler_ReconcileWithNotBoundPVC(t *testing.T) {
	testDir := createTestDir(t)
	scheme := createTestScheme()

	// Create test PVC that is not bound
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pvc",
			Namespace: "default",
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimPending, // Not bound
		},
	}

	// Create test FilesystemBackend with PVC reference
	backend := &ftpv1.FilesystemBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-backend",
			Namespace: "default",
		},
		Spec: ftpv1.FilesystemBackendSpec{
			BasePath:    testDir,
			ReadOnly:    false,
			FileMode:    "0644",
			DirMode:     "0755",
			MaxFileSize: 1024 * 1024,
			VolumeClaimRef: &ftpv1.VolumeClaimReference{
				Name: "test-pvc",
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backend, pvc).
		WithStatusSubresource(&ftpv1.FilesystemBackend{}).
		Build()

	reconciler := &FilesystemBackendReconciler{
		Client: client,
		Scheme: scheme,
	}

	ctx := context.Background()
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-backend",
			Namespace: "default",
		},
	}

	// Perform reconciliation
	result, err := reconciler.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Check that status shows not ready due to unbound PVC
	var updatedBackend ftpv1.FilesystemBackend
	err = client.Get(ctx, req.NamespacedName, &updatedBackend)
	assert.NoError(t, err)
	assert.False(t, updatedBackend.Status.Ready)
	assert.Equal(t, "Referenced PVC is not bound", updatedBackend.Status.Message)
}

func TestFilesystemBackendReconciler_ReconcileWithMissingPVC(t *testing.T) {
	testDir := createTestDir(t)
	scheme := createTestScheme()

	// Create test FilesystemBackend with PVC reference but no actual PVC
	backend := &ftpv1.FilesystemBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-backend",
			Namespace: "default",
		},
		Spec: ftpv1.FilesystemBackendSpec{
			BasePath:    testDir,
			ReadOnly:    false,
			FileMode:    "0644",
			DirMode:     "0755",
			MaxFileSize: 1024 * 1024,
			VolumeClaimRef: &ftpv1.VolumeClaimReference{
				Name: "nonexistent-pvc",
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backend).
		WithStatusSubresource(&ftpv1.FilesystemBackend{}).
		Build()

	reconciler := &FilesystemBackendReconciler{
		Client: client,
		Scheme: scheme,
	}

	ctx := context.Background()
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-backend",
			Namespace: "default",
		},
	}

	// Perform reconciliation
	result, err := reconciler.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Check that status shows not ready due to missing PVC
	var updatedBackend ftpv1.FilesystemBackend
	err = client.Get(ctx, req.NamespacedName, &updatedBackend)
	assert.NoError(t, err)
	assert.False(t, updatedBackend.Status.Ready)
	assert.Contains(t, updatedBackend.Status.Message, "Referenced PVC not found or not accessible")
}

func TestFilesystemBackendReconciler_ReconcileNotFound(t *testing.T) {
	scheme := createTestScheme()

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&ftpv1.FilesystemBackend{}).
		Build()

	reconciler := &FilesystemBackendReconciler{
		Client: client,
		Scheme: scheme,
	}

	ctx := context.Background()
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "nonexistent-backend",
			Namespace: "default",
		},
	}

	// Perform reconciliation
	result, err := reconciler.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestFilesystemBackendReconciler_ReconcileDelete(t *testing.T) {
	testDir := createTestDir(t)
	scheme := createTestScheme()

	// Create test FilesystemBackend with deletion timestamp
	now := metav1.Now()
	backend := &ftpv1.FilesystemBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-backend",
			Namespace:         "default",
			Finalizers:        []string{"test-finalizer"}, // Add finalizer to prevent deletion panic
			DeletionTimestamp: &now,
		},
		Spec: ftpv1.FilesystemBackendSpec{
			BasePath: testDir,
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backend).
		WithStatusSubresource(&ftpv1.FilesystemBackend{}).
		Build()

	reconciler := &FilesystemBackendReconciler{
		Client: client,
		Scheme: scheme,
	}

	ctx := context.Background()
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-backend",
			Namespace: "default",
		},
	}

	// Perform reconciliation
	result, err := reconciler.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestFilesystemBackendReconciler_ValidateBackend(t *testing.T) {
	testDir := createTestDir(t)
	scheme := createTestScheme()

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&ftpv1.FilesystemBackend{}).
		Build()

	reconciler := &FilesystemBackendReconciler{
		Client: client,
		Scheme: scheme,
	}

	ctx := context.Background()

	tests := []struct {
		name          string
		backend       *ftpv1.FilesystemBackend
		setupFunc     func()
		expectedReady bool
		expectedMsg   string
	}{
		{
			name: "valid directory",
			backend: &ftpv1.FilesystemBackend{
				Spec: ftpv1.FilesystemBackendSpec{
					BasePath: testDir,
					ReadOnly: false,
				},
			},
			expectedReady: true,
			expectedMsg:   "Filesystem backend is ready",
		},
		{
			name: "nonexistent path",
			backend: &ftpv1.FilesystemBackend{
				Spec: ftpv1.FilesystemBackendSpec{
					BasePath: "/nonexistent/path",
					ReadOnly: false,
				},
			},
			expectedReady: false,
			expectedMsg:   "Base path does not exist",
		},
		{
			name: "path is a file",
			backend: &ftpv1.FilesystemBackend{
				Spec: ftpv1.FilesystemBackendSpec{
					BasePath: filepath.Join(testDir, "testfile"),
					ReadOnly: false,
				},
			},
			setupFunc: func() {
				// Create a file instead of directory
				err := os.WriteFile(filepath.Join(testDir, "testfile"), []byte("content"), 0644)
				require.NoError(t, err)
			},
			expectedReady: false,
			expectedMsg:   "Base path is not a directory",
		},
		{
			name: "read-only valid",
			backend: &ftpv1.FilesystemBackend{
				Spec: ftpv1.FilesystemBackendSpec{
					BasePath: testDir,
					ReadOnly: true,
				},
			},
			expectedReady: true,
			expectedMsg:   "Filesystem backend is ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupFunc != nil {
				tt.setupFunc()
			}

			ready, message, err := reconciler.validateBackend(ctx, tt.backend)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedReady, ready)
			assert.Equal(t, tt.expectedMsg, message)
		})
	}
}

func TestFilesystemBackendReconciler_GetStorageStats(t *testing.T) {
	testDir := createTestDir(t)
	scheme := createTestScheme()

	reconciler := &FilesystemBackendReconciler{
		Scheme: scheme,
	}

	// Test with valid directory
	available, total := reconciler.getStorageStats(testDir)

	// Should return valid statistics (positive values)
	assert.True(t, available >= 0, "Available space should be non-negative")
	assert.True(t, total >= 0, "Total space should be non-negative")
	assert.True(t, total >= available, "Total space should be >= available space")

	// Test with invalid directory
	available, total = reconciler.getStorageStats("/nonexistent/path")
	assert.Equal(t, int64(-1), available)
	assert.Equal(t, int64(-1), total)
}
