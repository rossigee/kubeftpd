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

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

// BuiltInUserConfig holds configuration for built-in users
type BuiltInUserConfig struct {
	// Anonymous user settings
	EnableAnonymous      bool
	AnonymousHomeDir     string
	AnonymousBackendKind string
	AnonymousBackendName string

	// Admin user settings
	EnableAdmin         bool
	AdminPasswordSecret string
	AdminHomeDir        string
	AdminBackendKind    string
	AdminBackendName    string

	// Common settings
	Namespace string // Namespace where built-in users should be created
}

// BuiltInUserManager manages built-in User CRs based on configuration
type BuiltInUserManager struct {
	client.Client
	Scheme *runtime.Scheme
	Config BuiltInUserConfig
}

// +kubebuilder:rbac:groups=ftp.golder.org,resources=users,verbs=get;list;watch;create;update;patch;delete

// Reconcile manages built-in users based on configuration
func (r *BuiltInUserManager) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Check if this is a built-in user by trying to get it and checking labels
	user := &ftpv1.User{}
	if err := r.Get(ctx, req.NamespacedName, user); err != nil {
		if errors.IsNotFound(err) {
			// User was deleted, ensure our built-in users are still present
			log.Info("User deleted, reconciling built-in users", "name", req.Name)
			return r.reconcileAndReturn(ctx)
		}
		log.Error(err, "Failed to get user", "name", req.Name)
		return ctrl.Result{}, err
	}

	// Only handle built-in users (those with our label)
	if user.Labels["kubeftpd.golder.org/builtin"] == "true" {
		log.Info("Reconciling built-in user", "name", req.Name)
		return r.reconcileAndReturn(ctx)
	}

	// Not a built-in user, ignore
	return ctrl.Result{}, nil
}

// reconcileAndReturn is a helper that reconciles built-in users and returns appropriate result
func (r *BuiltInUserManager) reconcileAndReturn(ctx context.Context) (ctrl.Result, error) {
	if err := r.reconcileBuiltInUsers(ctx); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// ReconcileBuiltInUsers creates, updates, or deletes built-in User CRs based on configuration
func (r *BuiltInUserManager) reconcileBuiltInUsers(ctx context.Context) error {
	log := logf.FromContext(ctx)

	// Handle anonymous user
	if err := r.reconcileAnonymousUser(ctx); err != nil {
		log.Error(err, "Failed to reconcile anonymous user")
		return err
	}

	// Handle admin user
	if err := r.reconcileAdminUser(ctx); err != nil {
		log.Error(err, "Failed to reconcile admin user")
		return err
	}

	return nil
}

// reconcileAnonymousUser manages the anonymous user CR
func (r *BuiltInUserManager) reconcileAnonymousUser(ctx context.Context) error {
	log := logf.FromContext(ctx)
	userName := "builtin-anonymous"

	user := &ftpv1.User{}
	userKey := client.ObjectKey{
		Name:      userName,
		Namespace: r.Config.Namespace,
	}

	err := r.Get(ctx, userKey, user)
	userExists := err == nil

	if r.Config.EnableAnonymous {
		// Create or update anonymous user
		desiredUser := r.createAnonymousUserSpec(userName)

		if !userExists {
			if errors.IsNotFound(err) {
				log.Info("Creating anonymous user CR", "name", userName)
				if err := r.Create(ctx, desiredUser); err != nil {
					return fmt.Errorf("failed to create anonymous user: %w", err)
				}
				return nil
			}
			return fmt.Errorf("failed to get anonymous user: %w", err)
		}

		// Update existing user if needed
		if r.needsUpdate(user, desiredUser) {
			log.Info("Updating anonymous user CR", "name", userName)
			user.Spec = desiredUser.Spec
			if err := r.Update(ctx, user); err != nil {
				return fmt.Errorf("failed to update anonymous user: %w", err)
			}
		}
	} else {
		// Delete anonymous user if it exists
		if userExists {
			log.Info("Deleting anonymous user CR", "name", userName)
			if err := r.Delete(ctx, user); err != nil {
				return fmt.Errorf("failed to delete anonymous user: %w", err)
			}
		}
	}

	return nil
}

// reconcileAdminUser manages the admin user CR
func (r *BuiltInUserManager) reconcileAdminUser(ctx context.Context) error {
	log := logf.FromContext(ctx)
	userName := "builtin-admin"

	user := &ftpv1.User{}
	userKey := client.ObjectKey{
		Name:      userName,
		Namespace: r.Config.Namespace,
	}

	err := r.Get(ctx, userKey, user)
	userExists := err == nil

	if r.Config.EnableAdmin {
		// Validate admin configuration
		if r.Config.AdminPasswordSecret == "" {
			return fmt.Errorf("admin user enabled but no password secret specified")
		}

		// Create or update admin user
		desiredUser := r.createAdminUserSpec(userName)

		if !userExists {
			if errors.IsNotFound(err) {
				log.Info("Creating admin user CR", "name", userName)
				if err := r.Create(ctx, desiredUser); err != nil {
					return fmt.Errorf("failed to create admin user: %w", err)
				}
				return nil
			}
			return fmt.Errorf("failed to get admin user: %w", err)
		}

		// Update existing user if needed
		if r.needsUpdate(user, desiredUser) {
			log.Info("Updating admin user CR", "name", userName)
			user.Spec = desiredUser.Spec
			if err := r.Update(ctx, user); err != nil {
				return fmt.Errorf("failed to update admin user: %w", err)
			}
		}
	} else {
		// Delete admin user if it exists
		if userExists {
			log.Info("Deleting admin user CR", "name", userName)
			if err := r.Delete(ctx, user); err != nil {
				return fmt.Errorf("failed to delete admin user: %w", err)
			}
		}
	}

	return nil
}

// createAnonymousUserSpec creates the desired anonymous User CR spec
func (r *BuiltInUserManager) createAnonymousUserSpec(name string) *ftpv1.User {
	return &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: r.Config.Namespace,
			Labels: map[string]string{
				"kubeftpd.golder.org/builtin": "true",
				"kubeftpd.golder.org/type":    "anonymous",
			},
		},
		Spec: ftpv1.UserSpec{
			Type:          "anonymous",
			Username:      "anonymous",
			HomeDirectory: r.Config.AnonymousHomeDir,
			Enabled:       true,
			Backend: ftpv1.BackendReference{
				Kind: r.Config.AnonymousBackendKind,
				Name: r.Config.AnonymousBackendName,
			},
			Permissions: ftpv1.UserPermissions{
				Read:   true,
				Write:  false, // RFC 1635: anonymous is read-only
				Delete: false,
				List:   true,
			},
		},
	}
}

// createAdminUserSpec creates the desired admin User CR spec
func (r *BuiltInUserManager) createAdminUserSpec(name string) *ftpv1.User {
	return &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: r.Config.Namespace,
			Labels: map[string]string{
				"kubeftpd.golder.org/builtin": "true",
				"kubeftpd.golder.org/type":    "admin",
			},
		},
		Spec: ftpv1.UserSpec{
			Type:          "admin",
			Username:      "admin",
			HomeDirectory: r.Config.AdminHomeDir,
			Enabled:       true,
			PasswordSecret: &ftpv1.UserSecretRef{
				Name: r.Config.AdminPasswordSecret,
				Key:  "password",
			},
			Backend: ftpv1.BackendReference{
				Kind: r.Config.AdminBackendKind,
				Name: r.Config.AdminBackendName,
			},
			Permissions: ftpv1.UserPermissions{
				Read:   true,
				Write:  true, // Admin has full permissions
				Delete: true,
				List:   true,
			},
		},
	}
}

// needsUpdate determines if the existing user needs to be updated
func (r *BuiltInUserManager) needsUpdate(existing, desired *ftpv1.User) bool {
	return r.basicFieldsNeedUpdate(existing, desired) ||
		r.permissionsNeedUpdate(existing, desired) ||
		r.passwordSecretNeedsUpdate(existing, desired)
}

// basicFieldsNeedUpdate checks if basic user fields need update
func (r *BuiltInUserManager) basicFieldsNeedUpdate(existing, desired *ftpv1.User) bool {
	return existing.Spec.Type != desired.Spec.Type ||
		existing.Spec.Username != desired.Spec.Username ||
		existing.Spec.HomeDirectory != desired.Spec.HomeDirectory ||
		existing.Spec.Enabled != desired.Spec.Enabled ||
		existing.Spec.Backend.Kind != desired.Spec.Backend.Kind ||
		existing.Spec.Backend.Name != desired.Spec.Backend.Name
}

// permissionsNeedUpdate checks if permissions need update
func (r *BuiltInUserManager) permissionsNeedUpdate(existing, desired *ftpv1.User) bool {
	return existing.Spec.Permissions.Read != desired.Spec.Permissions.Read ||
		existing.Spec.Permissions.Write != desired.Spec.Permissions.Write ||
		existing.Spec.Permissions.Delete != desired.Spec.Permissions.Delete ||
		existing.Spec.Permissions.List != desired.Spec.Permissions.List
}

// passwordSecretNeedsUpdate checks if password secret needs update
func (r *BuiltInUserManager) passwordSecretNeedsUpdate(existing, desired *ftpv1.User) bool {
	if desired.Spec.PasswordSecret != nil {
		return existing.Spec.PasswordSecret == nil ||
			existing.Spec.PasswordSecret.Name != desired.Spec.PasswordSecret.Name ||
			existing.Spec.PasswordSecret.Key != desired.Spec.PasswordSecret.Key
	}
	return existing.Spec.PasswordSecret != nil
}

// UpdateConfig updates the controller configuration and triggers reconciliation
func (r *BuiltInUserManager) UpdateConfig(ctx context.Context, config BuiltInUserConfig) error {
	r.Config = config
	return r.reconcileBuiltInUsers(ctx)
}

// SetupWithManager sets up the controller with the Manager
func (r *BuiltInUserManager) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("builtin-user-manager").
		For(&ftpv1.User{}).
		Complete(r)
}
