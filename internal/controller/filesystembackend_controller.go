/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"os"
	"syscall"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FilesystemBackendReconciler reconciles a FilesystemBackend object
type FilesystemBackendReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=ftp.golder.org,resources=filesystembackends,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ftp.golder.org,resources=filesystembackends/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ftp.golder.org,resources=filesystembackends/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *FilesystemBackendReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the FilesystemBackend instance
	var backend ftpv1.FilesystemBackend
	if err := r.Get(ctx, req.NamespacedName, &backend); err != nil {
		log.Error(err, "unable to fetch FilesystemBackend")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check if the backend is being deleted
	if !backend.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &backend)
	}

	// Reconcile the backend
	return r.reconcileNormal(ctx, &backend)
}

// reconcileNormal handles the normal reconciliation logic
func (r *FilesystemBackendReconciler) reconcileNormal(ctx context.Context, backend *ftpv1.FilesystemBackend) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Validate the backend configuration
	ready, message, err := r.validateBackend(ctx, backend)
	if err != nil {
		log.Error(err, "failed to validate filesystem backend")
		ready = false
		message = err.Error()
	}

	// Get storage statistics if available
	var availableSpace, totalSpace *int64
	if ready {
		avail, total := r.getStorageStats(backend.Spec.BasePath)
		if avail >= 0 {
			availableSpace = &avail
		}
		if total >= 0 {
			totalSpace = &total
		}
	}

	// Update status
	now := metav1.Now()
	backend.Status = ftpv1.FilesystemBackendStatus{
		Ready:          ready,
		Message:        message,
		LastChecked:    &now,
		AvailableSpace: availableSpace,
		TotalSpace:     totalSpace,
	}

	if err := r.Status().Update(ctx, backend); err != nil {
		log.Error(err, "failed to update FilesystemBackend status")
		return ctrl.Result{}, err
	}

	// Requeue for periodic health checks
	return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
}

// reconcileDelete handles deletion of the backend
func (r *FilesystemBackendReconciler) reconcileDelete(ctx context.Context, backend *ftpv1.FilesystemBackend) (ctrl.Result, error) {
	// No cleanup needed for filesystem backends
	return ctrl.Result{}, nil
}

// validateBackend validates the filesystem backend configuration
func (r *FilesystemBackendReconciler) validateBackend(ctx context.Context, backend *ftpv1.FilesystemBackend) (bool, string, error) {
	// Check if base path exists
	if _, err := os.Stat(backend.Spec.BasePath); err != nil {
		if os.IsNotExist(err) {
			return false, "Base path does not exist", nil
		}
		return false, "Base path is not accessible", err
	}

	// Check if path is a directory
	info, err := os.Stat(backend.Spec.BasePath)
	if err != nil {
		return false, "Cannot stat base path", err
	}

	if !info.IsDir() {
		return false, "Base path is not a directory", nil
	}

	// Check permissions
	if !backend.Spec.ReadOnly {
		// Test write permissions by creating a temporary file
		testFile := backend.Spec.BasePath + "/.kubeftpd-write-test"
		file, err := os.Create(testFile)
		if err != nil {
			return false, "Base path is not writable", nil
		}
		_ = file.Close()
		_ = os.Remove(testFile)
	}

	// Validate referenced PVC if specified
	if backend.Spec.VolumeClaimRef != nil {
		pvcNamespace := backend.Namespace
		if backend.Spec.VolumeClaimRef.Namespace != nil {
			pvcNamespace = *backend.Spec.VolumeClaimRef.Namespace
		}

		var pvc corev1.PersistentVolumeClaim
		err := r.Get(ctx, types.NamespacedName{
			Name:      backend.Spec.VolumeClaimRef.Name,
			Namespace: pvcNamespace,
		}, &pvc)
		if err != nil {
			return false, "Referenced PVC not found or not accessible", nil
		}

		// Check if PVC is bound
		if pvc.Status.Phase != corev1.ClaimBound {
			return false, "Referenced PVC is not bound", nil
		}
	}

	return true, "Filesystem backend is ready", nil
}

// getStorageStats returns storage statistics for the given path
func (r *FilesystemBackendReconciler) getStorageStats(path string) (available int64, total int64) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return -1, -1
	}

	// Calculate available and total space in bytes
	available = int64(stat.Bavail) * int64(stat.Bsize)
	total = int64(stat.Blocks) * int64(stat.Bsize)

	return available, total
}

// SetupWithManager sets up the controller with the Manager.
func (r *FilesystemBackendReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ftpv1.FilesystemBackend{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(r)
}
