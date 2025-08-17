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
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
	"github.com/rossigee/kubeftpd/internal/backends"
)

// MinioBackendReconciler reconciles a MinioBackend object
type MinioBackendReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=ftp.golder.org,resources=miniobackends,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ftp.golder.org,resources=miniobackends/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ftp.golder.org,resources=miniobackends/finalizers,verbs=update

// Reconcile handles MinioBackend CRD changes and tests connectivity
func (r *MinioBackendReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the MinioBackend instance
	backend := &ftpv1.MinioBackend{}
	err := r.Get(ctx, req.NamespacedName, backend)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("MinioBackend resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get MinioBackend")
		return ctrl.Result{}, err
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(backend, "ftp.golder.org/finalizer") {
		controllerutil.AddFinalizer(backend, "ftp.golder.org/finalizer")
		err := r.Update(ctx, backend)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	// Handle deletion
	if backend.DeletionTimestamp != nil {
		return r.handleMinioBackendDeletion(ctx, backend)
	}

	// Test connectivity to MinIO
	if err := r.testMinioConnectivity(ctx, backend); err != nil {
		log.Error(err, "MinIO connectivity test failed", "backend", backend.Name)
		r.updateMinioBackendStatus(ctx, backend, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "ConnectionFailed",
			Message:            err.Error(),
			LastTransitionTime: metav1.Now(),
		})
		return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
	}

	// Update status to ready
	r.updateMinioBackendStatus(ctx, backend, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "ConnectionSuccessful",
		Message:            "Successfully connected to MinIO backend",
		LastTransitionTime: metav1.Now(),
	})

	log.Info("MinioBackend reconciliation completed", "backend", backend.Name)
	return ctrl.Result{RequeueAfter: time.Minute * 10}, nil
}

// testMinioConnectivity tests connectivity to the MinIO backend
func (r *MinioBackendReconciler) testMinioConnectivity(ctx context.Context, backend *ftpv1.MinioBackend) error {
	// Create a MinIO backend instance to test connectivity
	_, err := backends.NewMinioBackend(backend, r.Client)
	if err != nil {
		return fmt.Errorf("failed to create MinIO backend: %w", err)
	}

	// If we get here, the connection was successful
	return nil
}

// updateMinioBackendStatus updates the backend status with the given condition
func (r *MinioBackendReconciler) updateMinioBackendStatus(ctx context.Context, backend *ftpv1.MinioBackend, condition metav1.Condition) {
	backend.Status.Conditions = []metav1.Condition{condition}
	if err := r.Status().Update(ctx, backend); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to update MinioBackend status")
	}
}

// handleMinioBackendDeletion handles cleanup when a backend is being deleted
func (r *MinioBackendReconciler) handleMinioBackendDeletion(ctx context.Context, backend *ftpv1.MinioBackend) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Handling MinioBackend deletion", "backend", backend.Name)

	// Perform any cleanup operations here
	// For now, we just remove the finalizer

	controllerutil.RemoveFinalizer(backend, "ftp.golder.org/finalizer")
	err := r.Update(ctx, backend)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MinioBackendReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ftpv1.MinioBackend{}).
		Named("miniobackend").
		Complete(r)
}
