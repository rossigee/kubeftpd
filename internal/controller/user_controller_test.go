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
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

// TestUserTypeValidation tests the validation of different user types
func TestUserTypeValidation(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = ftpv1.AddToScheme(scheme)

	tests := []struct {
		name        string
		user        *ftpv1.User
		expectError bool
		errorMsg    string
	}{
		{
			name: "Valid regular user with password",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{Name: "test-user", Namespace: "default"},
				Spec: ftpv1.UserSpec{
					Type:          "regular",
					Username:      "testuser",
					Password:      "testpass",
					HomeDirectory: "/home/testuser",
					Backend: ftpv1.BackendReference{
						Kind: "FilesystemBackend",
						Name: "test-backend",
					},
				},
			},
			expectError: false,
		},
		{
			name: "Valid regular user with password secret",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{Name: "test-user", Namespace: "default"},
				Spec: ftpv1.UserSpec{
					Type:     "regular",
					Username: "testuser",
					PasswordSecret: &ftpv1.UserSecretRef{
						Name: "test-secret",
						Key:  "password",
					},
					HomeDirectory: "/home/testuser",
					Backend: ftpv1.BackendReference{
						Kind: "FilesystemBackend",
						Name: "test-backend",
					},
				},
			},
			expectError: false,
		},
		{
			name: "Valid anonymous user",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{Name: "anonymous-user", Namespace: "default"},
				Spec: ftpv1.UserSpec{
					Type:          "anonymous",
					Username:      "anonymous",
					HomeDirectory: "/pub",
					Backend: ftpv1.BackendReference{
						Kind: "FilesystemBackend",
						Name: "anonymous-backend",
					},
					Permissions: ftpv1.UserPermissions{
						Read:   true,
						Write:  false,
						Delete: false,
						List:   true,
					},
				},
			},
			expectError: false,
		},
		{
			name: "Valid admin user",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{Name: "admin-user", Namespace: "default"},
				Spec: ftpv1.UserSpec{
					Type:     "admin",
					Username: "admin",
					PasswordSecret: &ftpv1.UserSecretRef{
						Name: "admin-secret",
						Key:  "password",
					},
					HomeDirectory: "/",
					Backend: ftpv1.BackendReference{
						Kind: "FilesystemBackend",
						Name: "admin-backend",
					},
				},
			},
			expectError: false,
		},
		{
			name: "Invalid anonymous user with password",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{Name: "invalid-anonymous", Namespace: "default"},
				Spec: ftpv1.UserSpec{
					Type:          "anonymous",
					Username:      "anonymous",
					Password:      "shouldnotbeset",
					HomeDirectory: "/pub",
					Backend: ftpv1.BackendReference{
						Kind: "FilesystemBackend",
						Name: "anonymous-backend",
					},
				},
			},
			expectError: true,
			errorMsg:    "anonymous users should not have password or passwordSecret specified",
		},
		{
			name: "Invalid admin user with plaintext password",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{Name: "invalid-admin", Namespace: "default"},
				Spec: ftpv1.UserSpec{
					Type:          "admin",
					Username:      "admin",
					Password:      "plaintext",
					HomeDirectory: "/",
					Backend: ftpv1.BackendReference{
						Kind: "FilesystemBackend",
						Name: "admin-backend",
					},
				},
			},
			expectError: true,
			errorMsg:    "admin users must use passwordSecret, not plaintext password",
		},
		{
			name: "Invalid anonymous user with wrong username",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{Name: "invalid-anon-username", Namespace: "default"},
				Spec: ftpv1.UserSpec{
					Type:          "anonymous",
					Username:      "wrongname",
					HomeDirectory: "/pub",
					Backend: ftpv1.BackendReference{
						Kind: "FilesystemBackend",
						Name: "anonymous-backend",
					},
				},
			},
			expectError: true,
			errorMsg:    "anonymous type users must have username 'anonymous'",
		},
		{
			name: "Invalid anonymous user with write permissions",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{Name: "invalid-anon-write", Namespace: "default"},
				Spec: ftpv1.UserSpec{
					Type:          "anonymous",
					Username:      "anonymous",
					HomeDirectory: "/pub",
					Backend: ftpv1.BackendReference{
						Kind: "FilesystemBackend",
						Name: "anonymous-backend",
					},
					Permissions: ftpv1.UserPermissions{
						Read:   true,
						Write:  true, // Should not be allowed for anonymous
						Delete: false,
						List:   true,
					},
				},
			},
			expectError: true,
			errorMsg:    "anonymous users must have read-only permissions (RFC 1635)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake backend for validation
			backend := &ftpv1.FilesystemBackend{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.user.Spec.Backend.Name,
					Namespace: "default",
				},
				Spec: ftpv1.FilesystemBackendSpec{
					BasePath: "/tmp/test",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(backend).
				Build()

			reconciler := &UserReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			ctx := context.Background()
			err := reconciler.validateUser(ctx, tt.user)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestBuiltInUserManager tests the BuiltInUserManager functionality
func TestBuiltInUserManager(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = ftpv1.AddToScheme(scheme)

	tests := []struct {
		name            string
		config          BuiltInUserConfig
		expectAnonymous bool
		expectAdmin     bool
	}{
		{
			name: "Both anonymous and admin enabled",
			config: BuiltInUserConfig{
				EnableAnonymous:      true,
				AnonymousHomeDir:     "/pub",
				AnonymousBackendKind: "FilesystemBackend",
				AnonymousBackendName: "anonymous-backend",
				EnableAdmin:          true,
				AdminPasswordSecret:  "admin-secret",
				AdminHomeDir:         "/",
				AdminBackendKind:     "FilesystemBackend",
				AdminBackendName:     "admin-backend",
				Namespace:            "default",
			},
			expectAnonymous: true,
			expectAdmin:     true,
		},
		{
			name: "Only anonymous enabled",
			config: BuiltInUserConfig{
				EnableAnonymous:      true,
				AnonymousHomeDir:     "/pub",
				AnonymousBackendKind: "FilesystemBackend",
				AnonymousBackendName: "anonymous-backend",
				EnableAdmin:          false,
				Namespace:            "default",
			},
			expectAnonymous: true,
			expectAdmin:     false,
		},
		{
			name: "Only admin enabled",
			config: BuiltInUserConfig{
				EnableAnonymous:     false,
				EnableAdmin:         true,
				AdminPasswordSecret: "admin-secret",
				AdminHomeDir:        "/",
				AdminBackendKind:    "FilesystemBackend",
				AdminBackendName:    "admin-backend",
				Namespace:           "default",
			},
			expectAnonymous: false,
			expectAdmin:     true,
		},
		{
			name: "Both disabled",
			config: BuiltInUserConfig{
				EnableAnonymous: false,
				EnableAdmin:     false,
				Namespace:       "default",
			},
			expectAnonymous: false,
			expectAdmin:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				Build()

			manager := &BuiltInUserManager{
				Client: fakeClient,
				Scheme: scheme,
				Config: tt.config,
			}

			ctx := context.Background()
			err := manager.reconcileBuiltInUsers(ctx)
			assert.NoError(t, err)

			// Check if anonymous user was created/deleted as expected
			anonymousUser := &ftpv1.User{}
			err = fakeClient.Get(ctx, types.NamespacedName{
				Name:      "builtin-anonymous",
				Namespace: tt.config.Namespace,
			}, anonymousUser)

			if tt.expectAnonymous {
				assert.NoError(t, err, "Anonymous user should exist")
				assert.Equal(t, "anonymous", anonymousUser.Spec.Type)
				assert.Equal(t, "anonymous", anonymousUser.Spec.Username)
				assert.False(t, anonymousUser.Spec.Permissions.Write, "Anonymous should be read-only")
			} else {
				assert.True(t, errors.IsNotFound(err), "Anonymous user should not exist")
			}

			// Check if admin user was created/deleted as expected
			adminUser := &ftpv1.User{}
			err = fakeClient.Get(ctx, types.NamespacedName{
				Name:      "builtin-admin",
				Namespace: tt.config.Namespace,
			}, adminUser)

			if tt.expectAdmin {
				assert.NoError(t, err, "Admin user should exist")
				assert.Equal(t, "admin", adminUser.Spec.Type)
				assert.Equal(t, "admin", adminUser.Spec.Username)
				assert.True(t, adminUser.Spec.Permissions.Write, "Admin should have write permissions")
				assert.NotNil(t, adminUser.Spec.PasswordSecret, "Admin should have password secret")
			} else {
				assert.True(t, errors.IsNotFound(err), "Admin user should not exist")
			}
		})
	}
}

var _ = Describe("User Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		user := &ftpv1.User{}

		BeforeEach(func() {
			By("creating the MinioBackend resource first")
			backendName := types.NamespacedName{
				Name:      "test-backend",
				Namespace: "default",
			}
			backend := &ftpv1.MinioBackend{}
			err := k8sClient.Get(ctx, backendName, backend)
			if err != nil && errors.IsNotFound(err) {
				backendResource := &ftpv1.MinioBackend{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-backend",
						Namespace: "default",
					},
					Spec: ftpv1.MinioBackendSpec{
						Endpoint: "http://minio:9000",
						Bucket:   "test-bucket",
						Credentials: ftpv1.MinioCredentials{
							AccessKeyID:     "testkey",
							SecretAccessKey: "testsecret",
						},
					},
				}
				Expect(k8sClient.Create(ctx, backendResource)).To(Succeed())
			}

			By("creating the custom resource for the Kind User")
			err = k8sClient.Get(ctx, typeNamespacedName, user)
			if err != nil && errors.IsNotFound(err) {
				resource := &ftpv1.User{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: ftpv1.UserSpec{
						Username:      "testuser",
						Password:      "testpass",
						HomeDirectory: "/home/testuser",
						Enabled:       true,
						Backend: ftpv1.BackendReference{
							Kind: "MinioBackend",
							Name: "test-backend",
						},
						Permissions: ftpv1.UserPermissions{
							Read:  true,
							Write: true,
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &ftpv1.User{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance User")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

			By("Cleanup the MinioBackend resource")
			backendResource := &ftpv1.MinioBackend{}
			backendName := types.NamespacedName{
				Name:      "test-backend",
				Namespace: "default",
			}
			err = k8sClient.Get(ctx, backendName, backendResource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, backendResource)).To(Succeed())
			}
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &UserReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Should either requeue immediately (finalizer) or after delay (validation)
			Expect(result.RequeueAfter > 0).To(BeTrue())

			// Verify the user resource exists and is valid
			updatedUser := &ftpv1.User{}
			err = k8sClient.Get(ctx, typeNamespacedName, updatedUser)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedUser.Spec.Username).To(Equal("testuser"))
		})
	})
})

// Unit tests using testify
func TestUserReconciler_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	err := ftpv1.AddToScheme(scheme)
	assert.NoError(t, err)

	tests := []struct {
		name    string
		user    *ftpv1.User
		backend *ftpv1.MinioBackend
		wantErr bool
		wantReq bool
	}{
		{
			name: "valid user with minio backend",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-user",
					Namespace: "default",
				},
				Spec: ftpv1.UserSpec{
					Username: "testuser",
					Password: "testpass",
					Enabled:  true,
					Backend: ftpv1.BackendReference{
						Kind: "MinioBackend",
						Name: "test-backend",
					},
					HomeDirectory: "/home/testuser",
					Permissions: ftpv1.UserPermissions{
						Read:  true,
						Write: true,
					},
				},
			},
			backend: &ftpv1.MinioBackend{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-backend",
					Namespace: "default",
				},
				Spec: ftpv1.MinioBackendSpec{
					Endpoint: "http://minio:9000",
					Bucket:   "test-bucket",
					Credentials: ftpv1.MinioCredentials{
						AccessKeyID:     "testkey",
						SecretAccessKey: "testsecret",
					},
				},
			},
			wantErr: false,
			wantReq: false,
		},
		{
			name: "user with missing backend",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-user",
					Namespace: "default",
				},
				Spec: ftpv1.UserSpec{
					Username: "testuser",
					Password: "testpass",
					Enabled:  true,
					Backend: ftpv1.BackendReference{
						Kind: "MinioBackend",
						Name: "missing-backend",
					},
					HomeDirectory: "/home/testuser",
					Permissions: ftpv1.UserPermissions{
						Read:  true,
						Write: true,
					},
				},
			},
			backend: nil,
			wantErr: false, // Reconciler doesn't return error, just schedules requeue
			wantReq: true,  // But should request requeue
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objs []client.Object
			objs = append(objs, tt.user)
			if tt.backend != nil {
				objs = append(objs, tt.backend)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				Build()

			reconciler := &UserReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.user.Name,
					Namespace: tt.user.Namespace,
				},
			}

			result, err := reconciler.Reconcile(context.Background(), req)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.wantReq {
				assert.True(t, result.RequeueAfter > 0)
			}
		})
	}
}

func TestUserReconciler_validateUser(t *testing.T) {
	scheme := runtime.NewScheme()
	err := ftpv1.AddToScheme(scheme)
	assert.NoError(t, err)

	// Create a test backend for the valid user test
	testBackend := &ftpv1.MinioBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-backend",
			Namespace: "default",
		},
		Spec: ftpv1.MinioBackendSpec{
			Endpoint: "http://minio:9000",
			Bucket:   "test-bucket",
			Credentials: ftpv1.MinioCredentials{
				AccessKeyID:     "testkey",
				SecretAccessKey: "testsecret",
			},
		},
	}

	reconciler := &UserReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(testBackend).Build(),
		Scheme: scheme,
	}

	tests := []struct {
		name    string
		user    *ftpv1.User
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid user",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testuser",
					Namespace: "default",
				},
				Spec: ftpv1.UserSpec{
					Username:      "testuser",
					Password:      "testpass",
					HomeDirectory: "/home/testuser",
					Backend: ftpv1.BackendReference{
						Kind: "MinioBackend",
						Name: "test-backend",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "missing username",
			user: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Password:      "testpass",
					HomeDirectory: "/home/testuser",
					Backend: ftpv1.BackendReference{
						Kind: "MinioBackend",
						Name: "test-backend",
					},
				},
			},
			wantErr: true,
			errMsg:  "username is required",
		},
		{
			name: "valid user with secret",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testuser-secret",
					Namespace: "default",
				},
				Spec: ftpv1.UserSpec{
					Username: "testuser",
					PasswordSecret: &ftpv1.UserSecretRef{
						Name: "test-secret",
						Key:  "password",
					},
					HomeDirectory: "/home/testuser",
					Backend: ftpv1.BackendReference{
						Kind: "MinioBackend",
						Name: "test-backend",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "missing both password and secret",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testuser-nopass",
					Namespace: "default",
				},
				Spec: ftpv1.UserSpec{
					Username:      "testuser",
					HomeDirectory: "/home/testuser",
					Backend: ftpv1.BackendReference{
						Kind: "MinioBackend",
						Name: "test-backend",
					},
				},
			},
			wantErr: true,
			errMsg:  "either password or passwordSecret is required",
		},
		{
			name: "both password and secret specified",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testuser-both",
					Namespace: "default",
				},
				Spec: ftpv1.UserSpec{
					Username: "testuser",
					Password: "testpass",
					PasswordSecret: &ftpv1.UserSecretRef{
						Name: "test-secret",
						Key:  "password",
					},
					HomeDirectory: "/home/testuser",
					Backend: ftpv1.BackendReference{
						Kind: "MinioBackend",
						Name: "test-backend",
					},
				},
			},
			wantErr: true,
			errMsg:  "cannot specify both password and passwordSecret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := reconciler.validateUser(context.Background(), tt.user)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
