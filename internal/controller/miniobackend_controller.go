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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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

	// Only test connectivity if this is a new backend, if the spec has changed
	// or if the credentials have changed
	shouldTestConnectivity := r.shouldTestConnectivity(ctx, backend)

	if shouldTestConnectivity {
		// Test connectivity to MinIO
		if err := r.testMinioConnectivity(ctx, backend); err != nil {
			log.Error(err, "MinIO connectivity test failed", "backend", backend.Name)
			backend.Status.Conditions = []metav1.Condition{{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				Reason:             "ConnectionFailed",
				Message:            err.Error(),
				LastTransitionTime: metav1.Now(),
			}}
			backend.Status.LastConnectivityTest = metav1.Now()
			backend.Status.ObservedGeneration = backend.Generation
			if err := r.Status().Update(ctx, backend); err != nil {
				return ctrl.Result{}, fmt.Errorf("updating MinioBackend status: %w", err)
			}
			r.recordTestedSecretVersion(ctx, backend)
			return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
		}

		// Update status to ready
		backend.Status.Conditions = []metav1.Condition{{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "ConnectionSuccessful",
			Message:            "Successfully connected to MinIO backend",
			LastTransitionTime: metav1.Now(),
		}}
		backend.Status.LastConnectivityTest = metav1.Now()
		backend.Status.ObservedGeneration = backend.Generation
		if err := r.Status().Update(ctx, backend); err != nil {
			return ctrl.Result{}, fmt.Errorf("updating MinioBackend status: %w", err)
		}
		r.recordTestedSecretVersion(ctx, backend)
	}

	log.Info("MinioBackend reconciliation completed", "backend", backend.Name)
	// Don't requeue - only reconcile when the resource changes
	return ctrl.Result{}, nil
}

// testMinioConnectivity tests connectivity to the MinIO backend
func (r *MinioBackendReconciler) testMinioConnectivity(ctx context.Context, backend *ftpv1.MinioBackend) error {
	// Create a MinIO backend instance to test connectivity
	_, err := backends.NewMinioBackend(ctx, backend, r.Client)
	if err != nil {
		return fmt.Errorf("failed to create MinIO backend: %w", err)
	}

	// If we get here, the connection was successful
	return nil
}

// shouldTestConnectivity determines if we should test connectivity for this backend
func (r *MinioBackendReconciler) shouldTestConnectivity(ctx context.Context, backend *ftpv1.MinioBackend) bool {
	// Always test if we haven't tested before
	if backend.Status.LastConnectivityTest.IsZero() {
		return true
	}

	// Test if the spec has changed since last test
	// Compare resource version or generation
	if backend.Status.ObservedGeneration != backend.Generation {
		return true
	}

	// Test again when the credentials changed: they live in a secret, so a
	// change of the secret does not change the generation of the backend and
	// would otherwise keep the result of the first test forever.
	ref, ok := r.credentialsSecret(backend)
	if !ok {
		return false
	}

	version, err := credentialsSecretVersion(ctx, r.Client, backend.Namespace, ref)
	if err != nil {
		// The secret cannot be read. Test again, the test reports the error.
		return true
	}

	return backend.Annotations[ObservedSecretVersionAnnotation] != version
}

// credentialsSecret returns the secret that holds the credentials of the backend.
func (r *MinioBackendReconciler) credentialsSecret(backend *ftpv1.MinioBackend) (secretRef, bool) {
	if backend.Spec.Credentials.UseSecret == nil {
		return secretRef{}, false
	}

	ref := secretRef{name: backend.Spec.Credentials.UseSecret.Name}
	if backend.Spec.Credentials.UseSecret.Namespace != nil {
		ref.namespace = *backend.Spec.Credentials.UseSecret.Namespace
	}

	return ref, true
}

// recordTestedSecretVersion stores the version of the credentials secret that
// the connectivity test used.
func (r *MinioBackendReconciler) recordTestedSecretVersion(ctx context.Context, backend *ftpv1.MinioBackend) {
	ref, ok := r.credentialsSecret(backend)
	if !ok {
		return
	}

	version, err := credentialsSecretVersion(ctx, r.Client, backend.Namespace, ref)
	if err != nil {
		return
	}

	_ = recordSecretVersion(ctx, r.Client, backend, version)
}

// backendsForSecret maps a credentials secret to the backends that use it.
func (r *MinioBackendReconciler) backendsForSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	var list ftpv1.MinioBackendList
	if err := r.List(ctx, &list); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for i := range list.Items {
		backend := &list.Items[i]
		ref, ok := r.credentialsSecret(backend)
		if !ok || ref.name != obj.GetName() {
			continue
		}
		ns := ref.namespace
		if ns == "" {
			ns = backend.Namespace
		}
		if ns != obj.GetNamespace() {
			continue
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: backend.Name, Namespace: backend.Namespace},
		})
	}

	return requests
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
		// A change of the credentials secret has to trigger a new
		// connectivity test: the credentials are not part of the spec of the
		// backend, so the generation of the backend does not change.
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.backendsForSecret)).
		Named("miniobackend").
		Complete(r)
}
