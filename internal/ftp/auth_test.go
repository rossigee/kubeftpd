package ftp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

// MockClient for testing
type MockClient struct {
	mock.Mock
	client.Client
}

func (m *MockClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	args := m.Called(ctx, key, obj, opts)
	return args.Error(0)
}

func (m *MockClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	args := m.Called(ctx, list, opts)
	return args.Error(0)
}

func TestKubeAuth_CheckPasswd(t *testing.T) {
	scheme := runtime.NewScheme()
	err := ftpv1.AddToScheme(scheme)
	assert.NoError(t, err)

	tests := []struct {
		name     string
		username string
		password string
		users    []ftpv1.User
		wantAuth bool
		wantErr  bool
	}{
		{
			name:     "valid user and password",
			username: "testuser",
			password: "testpass",
			users: []ftpv1.User{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "testuser",
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
						HomeDirectory: "/test",
						Permissions: ftpv1.UserPermissions{
							Read:  true,
							Write: true,
						},
					},
				},
			},
			wantAuth: true,
			wantErr:  false,
		},
		{
			name:     "invalid password",
			username: "testuser",
			password: "wrongpass",
			users: []ftpv1.User{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "testuser",
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
						HomeDirectory: "/test",
						Permissions: ftpv1.UserPermissions{
							Read:  true,
							Write: true,
						},
					},
				},
			},
			wantAuth: false,
			wantErr:  false,
		},
		{
			name:     "disabled user",
			username: "testuser",
			password: "testpass",
			users: []ftpv1.User{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "testuser",
						Namespace: "default",
					},
					Spec: ftpv1.UserSpec{
						Username: "testuser",
						Password: "testpass",
						Enabled:  false,
						Backend: ftpv1.BackendReference{
							Kind: "MinioBackend",
							Name: "test-backend",
						},
						HomeDirectory: "/test",
						Permissions: ftpv1.UserPermissions{
							Read:  true,
							Write: true,
						},
					},
				},
			},
			wantAuth: false,
			wantErr:  false,
		},
		{
			name:     "user not found",
			username: "nonexistent",
			password: "testpass",
			users:    []ftpv1.User{},
			wantAuth: false,
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client with test data
			objs := make([]client.Object, len(tt.users))
			for i, user := range tt.users {
				objs[i] = &user
			}
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				Build()

			auth := NewKubeAuth(fakeClient)

			// Add users to cache
			for _, user := range tt.users {
				userCopy := user
				auth.userCache.Store(user.Spec.Username, &userCopy)
			}

			gotAuth, err := auth.CheckPasswd(nil, tt.username, tt.password)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.wantAuth, gotAuth)
		})
	}
}

func TestKubeAuth_GetUser(t *testing.T) {
	scheme := runtime.NewScheme()
	err := ftpv1.AddToScheme(scheme)
	assert.NoError(t, err)

	testUser := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testuser",
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
			HomeDirectory: "/test",
			Permissions: ftpv1.UserPermissions{
				Read:  true,
				Write: true,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(testUser).
		Build()

	auth := NewKubeAuth(fakeClient)

	// Test cache miss - should load from Kubernetes
	user := auth.GetUser("testuser")
	assert.NotNil(t, user)
	assert.Equal(t, "testuser", user.Spec.Username)

	// Test cache hit
	user2 := auth.GetUser("testuser")
	assert.NotNil(t, user2)
	assert.Equal(t, "testuser", user2.Spec.Username)

	// Test user not found
	user3 := auth.GetUser("nonexistent")
	assert.Nil(t, user3)
}

func TestKubeAuth_RefreshUserCache(t *testing.T) {
	scheme := runtime.NewScheme()
	err := ftpv1.AddToScheme(scheme)
	assert.NoError(t, err)

	testUser := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testuser",
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
			HomeDirectory: "/test",
			Permissions: ftpv1.UserPermissions{
				Read:  true,
				Write: true,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(testUser).
		Build()

	auth := NewKubeAuth(fakeClient)

	// Test initial cache refresh
	err = auth.RefreshUserCache(context.Background())
	assert.NoError(t, err)

	// Verify user is in cache
	user := auth.GetUser("testuser")
	assert.NotNil(t, user)
	assert.Equal(t, "testuser", user.Spec.Username)
}

func TestKubeAuth_StartCacheRefresh(t *testing.T) {
	scheme := runtime.NewScheme()
	err := ftpv1.AddToScheme(scheme)
	assert.NoError(t, err)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	auth := NewKubeAuth(fakeClient)

	// Start cache refresh with very short interval for testing
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// This should not block
	go auth.StartCacheRefresh(ctx, 10*time.Millisecond)

	// Wait for context to be done
	<-ctx.Done()

	// Test should complete without hanging
	assert.True(t, true)
}

func TestKubeAuth_UpdateUser(t *testing.T) {
	scheme := runtime.NewScheme()
	err := ftpv1.AddToScheme(scheme)
	assert.NoError(t, err)

	testUser := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testuser",
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
			HomeDirectory: "/test",
			Permissions: ftpv1.UserPermissions{
				Read:  true,
				Write: true,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(testUser).
		Build()

	auth := NewKubeAuth(fakeClient)

	// Update user in cache
	updatedUser := testUser.DeepCopy()
	updatedUser.Spec.Enabled = false
	auth.UpdateUser(updatedUser)

	// Verify user is updated in cache
	cachedUser := auth.GetUser("testuser")
	assert.NotNil(t, cachedUser)
	assert.False(t, cachedUser.Spec.Enabled)
}

func TestKubeAuth_DeleteUser(t *testing.T) {
	scheme := runtime.NewScheme()
	err := ftpv1.AddToScheme(scheme)
	assert.NoError(t, err)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	auth := NewKubeAuth(fakeClient)

	// Add user to cache
	testUser := &ftpv1.User{
		Spec: ftpv1.UserSpec{
			Username: "testuser",
		},
	}
	auth.userCache.Store("testuser", testUser)

	// Verify user exists
	user := auth.GetUser("testuser")
	assert.NotNil(t, user)

	// Delete user
	auth.DeleteUser("testuser")

	// Verify user is removed from cache
	user = auth.GetUser("testuser")
	assert.Nil(t, user)
}

func TestKubeAuth_SecretBasedPassword(t *testing.T) {
	scheme := runtime.NewScheme()
	err := ftpv1.AddToScheme(scheme)
	assert.NoError(t, err)
	err = corev1.AddToScheme(scheme)
	assert.NoError(t, err)

	// Create test secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-password-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"password": []byte("secret123"),
		},
	}

	// Create test user with secret reference
	user := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testuser",
			Namespace: "default",
		},
		Spec: ftpv1.UserSpec{
			Username: "testuser",
			PasswordSecret: &ftpv1.UserSecretRef{
				Name: "test-password-secret",
				Key:  "password",
			},
			Enabled: true,
			Backend: ftpv1.BackendReference{
				Kind: "MinioBackend",
				Name: "test-backend",
			},
			HomeDirectory: "/test",
			Permissions: ftpv1.UserPermissions{
				Read:  true,
				Write: true,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(secret, user).
		Build()

	auth := NewKubeAuth(fakeClient)

	// Test authentication with correct password
	authenticated, err := auth.CheckPasswd(nil, "testuser", "secret123")
	assert.NoError(t, err)
	assert.True(t, authenticated)

	// Test authentication with wrong password
	authenticated, err = auth.CheckPasswd(nil, "testuser", "wrongpass")
	assert.NoError(t, err)
	assert.False(t, authenticated)
}

func TestKubeAuth_SecretBasedPasswordCustomKey(t *testing.T) {
	scheme := runtime.NewScheme()
	err := ftpv1.AddToScheme(scheme)
	assert.NoError(t, err)
	err = corev1.AddToScheme(scheme)
	assert.NoError(t, err)

	// Create test secret with custom key
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-custom-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"custom-password-key": []byte("customsecret456"),
		},
	}

	// Create test user with secret reference using custom key
	user := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testuser",
			Namespace: "default",
		},
		Spec: ftpv1.UserSpec{
			Username: "testuser",
			PasswordSecret: &ftpv1.UserSecretRef{
				Name: "test-custom-secret",
				Key:  "custom-password-key",
			},
			Enabled: true,
			Backend: ftpv1.BackendReference{
				Kind: "MinioBackend",
				Name: "test-backend",
			},
			HomeDirectory: "/test",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(secret, user).
		Build()

	auth := NewKubeAuth(fakeClient)

	// Test authentication with correct password
	authenticated, err := auth.CheckPasswd(nil, "testuser", "customsecret456")
	assert.NoError(t, err)
	assert.True(t, authenticated)
}

// TestKubeAuth_CheckPasswdUserTypes tests authentication for different user types
func TestKubeAuth_CheckPasswdUserTypes(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = ftpv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	// Create test users for each type
	regularUser := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "regular-user",
			Namespace: "default",
		},
		Spec: ftpv1.UserSpec{
			Type:          "regular",
			Username:      "regular",
			Password:      "regularpass",
			HomeDirectory: "/home/regular",
			Enabled:       true,
			Backend: ftpv1.BackendReference{
				Kind: "FilesystemBackend",
				Name: "test-backend",
			},
		},
	}

	anonymousUser := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "anonymous-user",
			Namespace: "default",
		},
		Spec: ftpv1.UserSpec{
			Type:          "anonymous",
			Username:      "anonymous",
			HomeDirectory: "/pub",
			Enabled:       true,
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
	}

	// Create admin password secret
	adminSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "admin-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"password": []byte("adminpass"),
		},
	}

	adminUser := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "admin-user",
			Namespace: "default",
		},
		Spec: ftpv1.UserSpec{
			Type:     "admin",
			Username: "admin",
			PasswordSecret: &ftpv1.UserSecretRef{
				Name: "admin-secret",
				Key:  "password",
			},
			HomeDirectory: "/",
			Enabled:       true,
			Backend: ftpv1.BackendReference{
				Kind: "FilesystemBackend",
				Name: "admin-backend",
			},
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
		WithObjects(regularUser, anonymousUser, adminUser, adminSecret).
		Build()

	auth := NewKubeAuth(fakeClient)

	tests := []struct {
		name        string
		username    string
		password    string
		expectAuth  bool
		description string
	}{
		{
			name:        "Regular user with correct password",
			username:    "regular",
			password:    "regularpass",
			expectAuth:  true,
			description: "Regular user authentication should work with correct password",
		},
		{
			name:        "Regular user with wrong password",
			username:    "regular",
			password:    "wrongpass",
			expectAuth:  false,
			description: "Regular user authentication should fail with wrong password",
		},
		{
			name:        "Anonymous user with any password",
			username:    "anonymous",
			password:    "any@email.com",
			expectAuth:  true,
			description: "Anonymous user should authenticate with any password (RFC 1635)",
		},
		{
			name:        "Anonymous user with empty password",
			username:    "anonymous",
			password:    "",
			expectAuth:  true,
			description: "Anonymous user should authenticate with empty password",
		},
		{
			name:        "Admin user with correct password",
			username:    "admin",
			password:    "adminpass",
			expectAuth:  true,
			description: "Admin user should authenticate with correct secret password",
		},
		{
			name:        "Admin user with wrong password",
			username:    "admin",
			password:    "wrongpass",
			expectAuth:  false,
			description: "Admin user should fail with wrong password",
		},
		{
			name:        "Nonexistent user",
			username:    "nonexistent",
			password:    "anypass",
			expectAuth:  false,
			description: "Nonexistent user should fail authentication",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authenticated, err := auth.CheckPasswd(nil, tt.username, tt.password)
			assert.NoError(t, err, "CheckPasswd should not return error")
			assert.Equal(t, tt.expectAuth, authenticated, tt.description)
		})
	}
}

// TestKubeAuth_UserTypeDefaults tests that default user type is handled correctly
func TestKubeAuth_UserTypeDefaults(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = ftpv1.AddToScheme(scheme)

	// Create user without explicit type (should default to "regular")
	userWithoutType := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-without-type",
			Namespace: "default",
		},
		Spec: ftpv1.UserSpec{
			// Type field not set - should default to "regular"
			Username:      "testuser",
			Password:      "testpass",
			HomeDirectory: "/home/testuser",
			Enabled:       true,
			Backend: ftpv1.BackendReference{
				Kind: "FilesystemBackend",
				Name: "test-backend",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(userWithoutType).
		Build()

	auth := NewKubeAuth(fakeClient)

	// Test that user without type behaves as regular user
	authenticated, err := auth.CheckPasswd(nil, "testuser", "testpass")
	assert.NoError(t, err)
	assert.True(t, authenticated, "User without type should authenticate as regular user")

	authenticated, err = auth.CheckPasswd(nil, "testuser", "wrongpass")
	assert.NoError(t, err)
	assert.False(t, authenticated, "User without type should fail with wrong password")
}
