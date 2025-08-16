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
)

// UserReconciler reconciles a User object
type UserReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=ftp.golder.org,resources=users,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ftp.golder.org,resources=users/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ftp.golder.org,resources=users/finalizers,verbs=update
// +kubebuilder:rbac:groups=ftp.golder.org,resources=miniobackends,verbs=get;list;watch
// +kubebuilder:rbac:groups=ftp.golder.org,resources=webdavbackends,verbs=get;list;watch

// Reconcile handles User CRD changes and validates user configuration
func (r *UserReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the User instance
	user := &ftpv1.User{}
	err := r.Get(ctx, req.NamespacedName, user)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("User resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get User")
		return ctrl.Result{}, err
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(user, "ftp.golder.org/finalizer") {
		controllerutil.AddFinalizer(user, "ftp.golder.org/finalizer")
		err := r.Update(ctx, user)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Handle deletion
	if user.DeletionTimestamp != nil {
		return r.handleUserDeletion(ctx, user)
	}

	// Validate user configuration
	if err := r.validateUser(ctx, user); err != nil {
		log.Error(err, "User validation failed", "user", user.Name)
		r.updateUserStatus(ctx, user, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "ValidationFailed",
			Message:            err.Error(),
			LastTransitionTime: metav1.Now(),
		})
		return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
	}

	// Update status to ready
	r.updateUserStatus(ctx, user, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "UserValid",
		Message:            "User configuration is valid",
		LastTransitionTime: metav1.Now(),
	})

	log.Info("User reconciliation completed", "user", user.Name)
	return ctrl.Result{RequeueAfter: time.Minute * 10}, nil
}

// validateUser validates the user configuration and backend references
func (r *UserReconciler) validateUser(ctx context.Context, user *ftpv1.User) error {
	// Validate required fields
	if user.Spec.Username == "" {
		return fmt.Errorf("username is required")
	}
	// Validate that either password or passwordSecret is provided
	if user.Spec.Password == "" && user.Spec.PasswordSecret == nil {
		return fmt.Errorf("either password or passwordSecret is required")
	}
	if user.Spec.Password != "" && user.Spec.PasswordSecret != nil {
		return fmt.Errorf("cannot specify both password and passwordSecret")
	}
	if user.Spec.HomeDirectory == "" {
		return fmt.Errorf("homeDirectory is required")
	}

	// Validate backend reference
	backendNamespace := user.Namespace
	if user.Spec.Backend.Namespace != nil {
		backendNamespace = *user.Spec.Backend.Namespace
	}

	switch user.Spec.Backend.Kind {
	case "MinioBackend":
		backend := &ftpv1.MinioBackend{}
		err := r.Get(ctx, client.ObjectKey{
			Name:      user.Spec.Backend.Name,
			Namespace: backendNamespace,
		}, backend)
		if err != nil {
			return fmt.Errorf("failed to find MinioBackend %s/%s: %w", backendNamespace, user.Spec.Backend.Name, err)
		}
	case "WebDavBackend":
		backend := &ftpv1.WebDavBackend{}
		err := r.Get(ctx, client.ObjectKey{
			Name:      user.Spec.Backend.Name,
			Namespace: backendNamespace,
		}, backend)
		if err != nil {
			return fmt.Errorf("failed to find WebDavBackend %s/%s: %w", backendNamespace, user.Spec.Backend.Name, err)
		}
	default:
		return fmt.Errorf("unsupported backend kind: %s", user.Spec.Backend.Kind)
	}

	return nil
}

// updateUserStatus updates the user status with the given condition
func (r *UserReconciler) updateUserStatus(ctx context.Context, user *ftpv1.User, condition metav1.Condition) {
	user.Status.Conditions = []metav1.Condition{condition}
	if err := r.Status().Update(ctx, user); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to update User status")
	}
}

// handleUserDeletion handles cleanup when a user is being deleted
func (r *UserReconciler) handleUserDeletion(ctx context.Context, user *ftpv1.User) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Handling user deletion", "user", user.Name)

	// Perform any cleanup operations here
	// For now, we just remove the finalizer

	controllerutil.RemoveFinalizer(user, "ftp.golder.org/finalizer")
	err := r.Update(ctx, user)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *UserReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ftpv1.User{}).
		Named("user").
		Complete(r)
}
