package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

func TestBuiltInUserManager_needsUpdate(t *testing.T) {
	manager := &BuiltInUserManager{}

	tests := []struct {
		name     string
		existing *ftpv1.User
		desired  *ftpv1.User
		expected bool
	}{
		{
			name: "no changes needed",
			existing: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Username:      "testuser",
					Type:          "regular",
					HomeDirectory: "/home/testuser",
					Enabled:       true,
					Permissions: ftpv1.UserPermissions{
						Read:  true,
						Write: true,
						List:  true,
					},
					Backend: ftpv1.BackendReference{
						Kind: "FilesystemBackend",
						Name: "test-backend",
					},
				},
			},
			desired: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Username:      "testuser",
					Type:          "regular",
					HomeDirectory: "/home/testuser",
					Enabled:       true,
					Permissions: ftpv1.UserPermissions{
						Read:  true,
						Write: true,
						List:  true,
					},
					Backend: ftpv1.BackendReference{
						Kind: "FilesystemBackend",
						Name: "test-backend",
					},
				},
			},
			expected: false,
		},
		{
			name: "username changed",
			existing: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Username: "testuser",
				},
			},
			desired: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Username: "newuser",
				},
			},
			expected: true,
		},
		{
			name: "permissions changed",
			existing: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Permissions: ftpv1.UserPermissions{
						Read:  true,
						Write: true,
						List:  true,
					},
				},
			},
			desired: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Permissions: ftpv1.UserPermissions{
						Read:  true,
						Write: false, // Changed
						List:  true,
					},
				},
			},
			expected: true,
		},
		{
			name: "password secret added",
			existing: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					PasswordSecret: nil,
				},
			},
			desired: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					PasswordSecret: &ftpv1.UserSecretRef{
						Name: "secret",
						Key:  "password",
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.needsUpdate(tt.existing, tt.desired)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuiltInUserManager_basicFieldsNeedUpdate(t *testing.T) {
	manager := &BuiltInUserManager{}

	tests := []struct {
		name     string
		existing *ftpv1.User
		desired  *ftpv1.User
		expected bool
	}{
		{
			name: "no basic field changes",
			existing: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Username:      "testuser",
					Type:          "regular",
					HomeDirectory: "/home/testuser",
					Enabled:       true,
					Backend: ftpv1.BackendReference{
						Kind: "FilesystemBackend",
						Name: "test-backend",
					},
				},
			},
			desired: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Username:      "testuser",
					Type:          "regular",
					HomeDirectory: "/home/testuser",
					Enabled:       true,
					Backend: ftpv1.BackendReference{
						Kind: "FilesystemBackend",
						Name: "test-backend",
					},
				},
			},
			expected: false,
		},
		{
			name: "username changed",
			existing: &ftpv1.User{
				Spec: ftpv1.UserSpec{Username: "olduser"},
			},
			desired: &ftpv1.User{
				Spec: ftpv1.UserSpec{Username: "newuser"},
			},
			expected: true,
		},
		{
			name: "type changed",
			existing: &ftpv1.User{
				Spec: ftpv1.UserSpec{Type: "regular"},
			},
			desired: &ftpv1.User{
				Spec: ftpv1.UserSpec{Type: "admin"},
			},
			expected: true,
		},
		{
			name: "home directory changed",
			existing: &ftpv1.User{
				Spec: ftpv1.UserSpec{HomeDirectory: "/home/old"},
			},
			desired: &ftpv1.User{
				Spec: ftpv1.UserSpec{HomeDirectory: "/home/new"},
			},
			expected: true,
		},
		{
			name: "enabled changed",
			existing: &ftpv1.User{
				Spec: ftpv1.UserSpec{Enabled: true},
			},
			desired: &ftpv1.User{
				Spec: ftpv1.UserSpec{Enabled: false},
			},
			expected: true,
		},
		{
			name: "backend kind changed",
			existing: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Backend: ftpv1.BackendReference{Kind: "FilesystemBackend"},
				},
			},
			desired: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Backend: ftpv1.BackendReference{Kind: "MinioBackend"},
				},
			},
			expected: true,
		},
		{
			name: "backend name changed",
			existing: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Backend: ftpv1.BackendReference{Name: "old-backend"},
				},
			},
			desired: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Backend: ftpv1.BackendReference{Name: "new-backend"},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.basicFieldsNeedUpdate(tt.existing, tt.desired)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuiltInUserManager_permissionsNeedUpdate(t *testing.T) {
	manager := &BuiltInUserManager{}

	tests := []struct {
		name     string
		existing *ftpv1.User
		desired  *ftpv1.User
		expected bool
	}{
		{
			name: "no permission changes",
			existing: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Permissions: ftpv1.UserPermissions{
						Read:   true,
						Write:  true,
						Delete: false,
						List:   true,
					},
				},
			},
			desired: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Permissions: ftpv1.UserPermissions{
						Read:   true,
						Write:  true,
						Delete: false,
						List:   true,
					},
				},
			},
			expected: false,
		},
		{
			name: "read permission changed",
			existing: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Permissions: ftpv1.UserPermissions{Read: true},
				},
			},
			desired: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Permissions: ftpv1.UserPermissions{Read: false},
				},
			},
			expected: true,
		},
		{
			name: "write permission changed",
			existing: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Permissions: ftpv1.UserPermissions{Write: true},
				},
			},
			desired: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Permissions: ftpv1.UserPermissions{Write: false},
				},
			},
			expected: true,
		},
		{
			name: "delete permission changed",
			existing: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Permissions: ftpv1.UserPermissions{Delete: false},
				},
			},
			desired: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Permissions: ftpv1.UserPermissions{Delete: true},
				},
			},
			expected: true,
		},
		{
			name: "list permission changed",
			existing: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Permissions: ftpv1.UserPermissions{List: true},
				},
			},
			desired: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					Permissions: ftpv1.UserPermissions{List: false},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.permissionsNeedUpdate(tt.existing, tt.desired)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuiltInUserManager_passwordSecretNeedsUpdate(t *testing.T) {
	manager := &BuiltInUserManager{}

	tests := []struct {
		name     string
		existing *ftpv1.User
		desired  *ftpv1.User
		expected bool
	}{
		{
			name: "no password secret changes",
			existing: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					PasswordSecret: &ftpv1.UserSecretRef{
						Name: "secret",
						Key:  "password",
					},
				},
			},
			desired: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					PasswordSecret: &ftpv1.UserSecretRef{
						Name: "secret",
						Key:  "password",
					},
				},
			},
			expected: false,
		},
		{
			name: "password secret added",
			existing: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					PasswordSecret: nil,
				},
			},
			desired: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					PasswordSecret: &ftpv1.UserSecretRef{
						Name: "secret",
						Key:  "password",
					},
				},
			},
			expected: true,
		},
		{
			name: "password secret removed",
			existing: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					PasswordSecret: &ftpv1.UserSecretRef{
						Name: "secret",
						Key:  "password",
					},
				},
			},
			desired: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					PasswordSecret: nil,
				},
			},
			expected: true,
		},
		{
			name: "password secret name changed",
			existing: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					PasswordSecret: &ftpv1.UserSecretRef{
						Name: "old-secret",
						Key:  "password",
					},
				},
			},
			desired: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					PasswordSecret: &ftpv1.UserSecretRef{
						Name: "new-secret",
						Key:  "password",
					},
				},
			},
			expected: true,
		},
		{
			name: "password secret key changed",
			existing: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					PasswordSecret: &ftpv1.UserSecretRef{
						Name: "secret",
						Key:  "old-key",
					},
				},
			},
			desired: &ftpv1.User{
				Spec: ftpv1.UserSpec{
					PasswordSecret: &ftpv1.UserSecretRef{
						Name: "secret",
						Key:  "new-key",
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.passwordSecretNeedsUpdate(tt.existing, tt.desired)
			assert.Equal(t, tt.expected, result)
		})
	}
}
