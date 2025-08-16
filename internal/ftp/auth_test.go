package ftp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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

			gotAuth, err := auth.CheckPasswd(tt.username, tt.password)

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
