package ftp

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

// Test the isPathWithinHome utility function
func TestIsPathWithinHome(t *testing.T) {
	tests := []struct {
		name          string
		requestedPath string
		homeDir       string
		expected      bool
		description   string
	}{
		{
			name:          "path_within_home_absolute",
			requestedPath: "/home/user/documents",
			homeDir:       "/home/user",
			expected:      true,
			description:   "Absolute path within home directory should be allowed",
		},
		{
			name:          "path_within_home_relative",
			requestedPath: "documents/file.txt",
			homeDir:       "/home/user",
			expected:      true,
			description:   "Relative path within home directory should be allowed",
		},
		{
			name:          "path_equals_home",
			requestedPath: "/home/user",
			homeDir:       "/home/user",
			expected:      true,
			description:   "Path equal to home directory should be allowed",
		},
		{
			name:          "path_outside_home_absolute",
			requestedPath: "/etc/passwd",
			homeDir:       "/home/user",
			expected:      false,
			description:   "Absolute path outside home directory should be blocked",
		},
		{
			name:          "path_parent_traversal",
			requestedPath: "/home/user/../../../etc/passwd",
			homeDir:       "/home/user",
			expected:      false,
			description:   "Parent directory traversal should be blocked",
		},
		{
			name:          "path_relative_traversal",
			requestedPath: "../../../etc/passwd",
			homeDir:       "/home/user",
			expected:      false,
			description:   "Relative parent traversal should be blocked",
		},
		{
			name:          "path_sibling_directory",
			requestedPath: "/home/other_user/file.txt",
			homeDir:       "/home/user",
			expected:      false,
			description:   "Access to sibling directory should be blocked",
		},
		{
			name:          "path_with_trailing_slashes",
			requestedPath: "/home/user/documents/",
			homeDir:       "/home/user/",
			expected:      true,
			description:   "Paths with trailing slashes should work correctly",
		},
		{
			name:          "complex_relative_path",
			requestedPath: "./documents/../files/test.txt",
			homeDir:       "/home/user",
			expected:      true,
			description:   "Complex relative path staying within home should be allowed",
		},
		{
			name:          "symlink_style_traversal",
			requestedPath: "/home/user/link/../../../etc/shadow",
			homeDir:       "/home/user",
			expected:      false,
			description:   "Symlink-style traversal should be blocked",
		},
		{
			name:          "scanner_general_valid_path",
			requestedPath: "/general/documents/scan001.pdf",
			homeDir:       "/general",
			expected:      true,
			description:   "Scanner accessing files within their home directory should be allowed",
		},
		{
			name:          "scanner_general_root_access",
			requestedPath: "/",
			homeDir:       "/general",
			expected:      false,
			description:   "Scanner trying to access root should be blocked",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isPathWithinHome(tt.requestedPath, tt.homeDir)
			assert.Equal(t, tt.expected, result, tt.description)
		})
	}
}

// Test the validateChrootPath method
func TestKubeDriver_ValidateChrootPath(t *testing.T) {
	scheme := runtime.NewScheme()
	err := ftpv1.AddToScheme(scheme)
	assert.NoError(t, err)

	tests := []struct {
		name        string
		user        *ftpv1.User
		path        string
		expectedErr bool
		description string
	}{
		{
			name: "chroot_enabled_valid_path",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testuser",
					Namespace: "default",
				},
				Spec: ftpv1.UserSpec{
					Username:      "testuser",
					HomeDirectory: "/home/user",
					Chroot:        true, // Chroot enabled
				},
			},
			path:        "/home/user/documents/file.txt",
			expectedErr: false,
			description: "Valid path within home directory should be allowed with chroot enabled",
		},
		{
			name: "chroot_enabled_invalid_path",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testuser",
					Namespace: "default",
				},
				Spec: ftpv1.UserSpec{
					Username:      "testuser",
					HomeDirectory: "/home/user",
					Chroot:        true, // Chroot enabled
				},
			},
			path:        "/etc/passwd",
			expectedErr: true,
			description: "Invalid path outside home directory should be blocked with chroot enabled",
		},
		{
			name: "chroot_disabled_any_path",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testuser",
					Namespace: "default",
				},
				Spec: ftpv1.UserSpec{
					Username:      "testuser",
					HomeDirectory: "/home/user",
					Chroot:        false, // Chroot disabled
				},
			},
			path:        "/etc/passwd",
			expectedErr: false,
			description: "Any path should be allowed when chroot is disabled",
		},
		{
			name: "chroot_default_value_restriction",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testuser",
					Namespace: "default",
				},
				Spec: ftpv1.UserSpec{
					Username:      "testuser",
					HomeDirectory: "/general",
					Chroot:        true, // Explicitly set to true to match CRD default behavior
				},
			},
			path:        "/etc/passwd",
			expectedErr: true,
			description: "Path outside home should be blocked when chroot is true",
		},
		{
			name: "scanner_user_chroot_protection",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "scanner-general",
					Namespace: "kubeftpd",
				},
				Spec: ftpv1.UserSpec{
					Username:      "scanner-general",
					HomeDirectory: "/general",
					Chroot:        true,
				},
			},
			path:        "/general/scan001.pdf",
			expectedErr: false,
			description: "Scanner should access files within their home directory",
		},
		{
			name: "scanner_user_escape_attempt",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "scanner-general",
					Namespace: "kubeftpd",
				},
				Spec: ftpv1.UserSpec{
					Username:      "scanner-general",
					HomeDirectory: "/general",
					Chroot:        true,
				},
			},
			path:        "/general/../../../etc/passwd",
			expectedErr: true,
			description: "Scanner should be blocked from directory traversal attacks",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.user).
				Build()

			auth := NewKubeAuth(fakeClient)
			auth.userCache.Store(tt.user.Spec.Username, tt.user)

			driver := &KubeDriver{
				auth:              auth,
				client:            fakeClient,
				authenticatedUser: tt.user.Spec.Username,
				user:              tt.user, // Set user directly for test
			}

			err := driver.validateChrootPath(tt.path)

			if tt.expectedErr {
				assert.Error(t, err, tt.description)
				if err != nil {
					assert.Contains(t, err.Error(), "access denied", "Error should indicate access denied")
				}
			} else {
				assert.NoError(t, err, tt.description)
			}
		})
	}
}

// Test chroot validation in file operations
func TestKubeDriver_ChrootFileOperations(t *testing.T) {
	scheme := runtime.NewScheme()
	err := ftpv1.AddToScheme(scheme)
	assert.NoError(t, err)

	chrootUser := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "chrootuser",
			Namespace: "default",
		},
		Spec: ftpv1.UserSpec{
			Username:      "chrootuser",
			HomeDirectory: "/chroot/user",
			Chroot:        true,
			Permissions: ftpv1.UserPermissions{
				Read:   true,
				Write:  true,
				Delete: true,
				List:   true,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(chrootUser).
		Build()

	auth := NewKubeAuth(fakeClient)
	auth.userCache.Store("chrootuser", chrootUser)

	mockStorage := &MockStorage{}

	driver := &KubeDriver{
		auth:              auth,
		client:            fakeClient,
		authenticatedUser: "chrootuser",
		user:              chrootUser,
		storageImpl:       mockStorage,
	}

	t.Run("ChangeDir_blocked_outside_home", func(t *testing.T) {
		err := driver.ChangeDir(nil, "/etc")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "access denied")
	})

	t.Run("ChangeDir_allowed_within_home", func(t *testing.T) {
		mockStorage.On("ChangeDir", "/chroot/user/documents").Return(nil)

		err := driver.ChangeDir(nil, "/chroot/user/documents")
		assert.NoError(t, err)
	})

	t.Run("Stat_blocked_outside_home", func(t *testing.T) {
		_, err := driver.Stat(nil, "/etc/passwd")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "access denied")
	})

	t.Run("Stat_allowed_within_home", func(t *testing.T) {
		mockFileInfo := &MockFileInfo{name: "test.txt", size: 100}
		mockStorage.On("Stat", "/chroot/user/test.txt").Return(mockFileInfo, nil)

		_, err := driver.Stat(nil, "/chroot/user/test.txt")
		assert.NoError(t, err)
	})

	t.Run("ListDir_blocked_outside_home", func(t *testing.T) {
		err := driver.ListDir(nil, "/etc", func(info os.FileInfo) error { return nil })
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "access denied")
	})

	t.Run("Rename_both_paths_validated", func(t *testing.T) {
		// Test that both source and destination paths are validated
		err := driver.Rename(nil, "/chroot/user/file.txt", "/etc/passwd")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "access denied")

		// Test valid rename within home
		mockStorage.On("Rename", "/chroot/user/old.txt", "/chroot/user/new.txt").Return(nil)
		err = driver.Rename(nil, "/chroot/user/old.txt", "/chroot/user/new.txt")
		assert.NoError(t, err)
	})

	mockStorage.AssertExpectations(t)
}

// Test that user initialization is required for chroot validation
func TestKubeDriver_ChrootValidationRequiresUser(t *testing.T) {
	driver := &KubeDriver{
		user: nil, // No user initialized
	}

	err := driver.validateChrootPath("/any/path")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "user not initialized")
}

// Test chroot functionality with scanner-specific scenarios
func TestScannerChrootScenarios(t *testing.T) {
	scheme := runtime.NewScheme()
	err := ftpv1.AddToScheme(scheme)
	assert.NoError(t, err)

	scannerUser := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "scanner-general",
			Namespace: "kubeftpd",
		},
		Spec: ftpv1.UserSpec{
			Username:      "scanner-general",
			HomeDirectory: "/general",
			Chroot:        true,
			Backend: ftpv1.BackendReference{
				Kind: "MinioBackend",
				Name: "scanner-backend",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(scannerUser).
		Build()

	auth := NewKubeAuth(fakeClient)
	auth.userCache.Store("scanner-general", scannerUser)

	driver := &KubeDriver{
		auth:              auth,
		client:            fakeClient,
		authenticatedUser: "scanner-general",
		user:              scannerUser,
	}

	tests := []struct {
		name        string
		path        string
		shouldAllow bool
		description string
	}{
		{
			name:        "scanner_home_access",
			path:        "/general",
			shouldAllow: true,
			description: "Scanner should access their home directory",
		},
		{
			name:        "scanner_subdirectory",
			path:        "/general/documents",
			shouldAllow: true,
			description: "Scanner should access subdirectories",
		},
		{
			name:        "scanner_file_upload",
			path:        "/general/scan001.pdf",
			shouldAllow: true,
			description: "Scanner should upload files to home directory",
		},
		{
			name:        "scanner_root_escape",
			path:        "/",
			shouldAllow: false,
			description: "Scanner should not access root directory",
		},
		{
			name:        "scanner_etc_access",
			path:        "/etc/passwd",
			shouldAllow: false,
			description: "Scanner should not access system files",
		},
		{
			name:        "scanner_parent_traversal",
			path:        "/general/../etc/passwd",
			shouldAllow: false,
			description: "Scanner should not traverse parent directories",
		},
		{
			name:        "scanner_double_traversal",
			path:        "/general/../../etc/passwd",
			shouldAllow: false,
			description: "Scanner should not traverse multiple parent levels",
		},
		{
			name:        "scanner_sibling_directory",
			path:        "/other_scanner/file.txt",
			shouldAllow: false,
			description: "Scanner should not access other scanner directories",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := driver.validateChrootPath(tt.path)

			if tt.shouldAllow {
				assert.NoError(t, err, tt.description)
			} else {
				assert.Error(t, err, tt.description)
				if err != nil {
					assert.Contains(t, err.Error(), "access denied", "Should indicate access denied")
				}
			}
		})
	}
}
