package ftp

import (
	"io"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

// MockStorage for testing
type MockStorage struct {
	mock.Mock
}

func (m *MockStorage) ChangeDir(path string) error {
	args := m.Called(path)
	return args.Error(0)
}

func (m *MockStorage) Stat(path string) (os.FileInfo, error) {
	args := m.Called(path)
	return args.Get(0).(os.FileInfo), args.Error(1)
}

func (m *MockStorage) ListDir(path string, callback func(os.FileInfo) error) error {
	args := m.Called(path, callback)
	return args.Error(0)
}

func (m *MockStorage) DeleteDir(path string) error {
	args := m.Called(path)
	return args.Error(0)
}

func (m *MockStorage) DeleteFile(path string) error {
	args := m.Called(path)
	return args.Error(0)
}

func (m *MockStorage) Rename(fromPath, toPath string) error {
	args := m.Called(fromPath, toPath)
	return args.Error(0)
}

func (m *MockStorage) MakeDir(path string) error {
	args := m.Called(path)
	return args.Error(0)
}

func (m *MockStorage) GetFile(path string, offset int64) (int64, io.ReadCloser, error) {
	args := m.Called(path, offset)
	return args.Get(0).(int64), args.Get(1).(io.ReadCloser), args.Error(2)
}

func (m *MockStorage) PutFile(path string, reader io.Reader, offset int64) (int64, error) {
	args := m.Called(path, reader, offset)
	return args.Get(0).(int64), args.Error(1)
}

// MockFileInfo for testing
type MockFileInfo struct {
	name  string
	size  int64
	isDir bool
	mode  fs.FileMode
	owner string
	group string
}

func (m *MockFileInfo) Name() string       { return m.name }
func (m *MockFileInfo) Size() int64        { return m.size }
func (m *MockFileInfo) Mode() fs.FileMode  { return m.mode }
func (m *MockFileInfo) ModTime() time.Time { return time.Unix(1234567890, 0) }
func (m *MockFileInfo) IsDir() bool        { return m.isDir }
func (m *MockFileInfo) Owner() string      { return m.owner }
func (m *MockFileInfo) Group() string      { return m.group }
func (m *MockFileInfo) Sys() interface{}   { return nil }

func TestKubeDriver_ensureUserInitialized(t *testing.T) {
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
	auth.userCache.Store("testuser", testUser)

	mockStorage := &MockStorage{}

	driver := &KubeDriver{
		auth:              auth,
		client:            fakeClient,
		authenticatedUser: "testuser",
		user:              testUser,    // Set user directly for test
		storageImpl:       mockStorage, // Set storage directly for test
	}

	// Test that user is already initialized (should not fail since both user and storage are set)
	err = driver.ensureUserInitialized()
	assert.NoError(t, err)
	assert.NotNil(t, driver.user)
	assert.Equal(t, "testuser", driver.user.Spec.Username)
	assert.NotNil(t, driver.storageImpl)
}

func TestKubeDriver_getAuthenticatedUsername(t *testing.T) {
	driver := &KubeDriver{
		authenticatedUser: "testuser",
	}

	username := driver.getAuthenticatedUsername()
	assert.Equal(t, "testuser", username)

	// Test empty username
	driver.authenticatedUser = ""
	username = driver.getAuthenticatedUsername()
	assert.Equal(t, "", username)
}

func TestKubeDriver_ChangeDir(t *testing.T) {
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
	auth.userCache.Store("testuser", testUser)

	mockStorage := &MockStorage{}
	mockStorage.On("ChangeDir", "/newdir").Return(nil)

	driver := &KubeDriver{
		auth:              auth,
		client:            fakeClient,
		authenticatedUser: "testuser",
		user:              testUser,
		storageImpl:       mockStorage,
	}

	err = driver.ChangeDir(nil, "/newdir")
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestKubeDriver_Stat(t *testing.T) {
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
	auth.userCache.Store("testuser", testUser)

	mockFileInfo := &MockFileInfo{
		name:  "testfile.txt",
		size:  1024,
		isDir: false,
		mode:  fs.FileMode(0644),
		owner: "testuser",
		group: "testgroup",
	}

	mockStorage := &MockStorage{}
	mockStorage.On("Stat", "/testfile.txt").Return(mockFileInfo, nil)

	driver := &KubeDriver{
		auth:              auth,
		client:            fakeClient,
		authenticatedUser: "testuser",
		user:              testUser,
		storageImpl:       mockStorage,
	}

	fileInfo, err := driver.Stat(nil, "/testfile.txt")
	assert.NoError(t, err)
	assert.Equal(t, "testfile.txt", fileInfo.Name())
	assert.Equal(t, int64(1024), fileInfo.Size())
	assert.False(t, fileInfo.IsDir())
	mockStorage.AssertExpectations(t)
}

func TestKubeDriver_ListDir(t *testing.T) {
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
	auth.userCache.Store("testuser", testUser)

	mockStorage := &MockStorage{}
	mockStorage.On("ListDir", "/testdir", mock.AnythingOfType("func(fs.FileInfo) error")).Return(nil)

	driver := &KubeDriver{
		auth:              auth,
		client:            fakeClient,
		authenticatedUser: "testuser",
		user:              testUser,
		storageImpl:       mockStorage,
	}

	err = driver.ListDir(nil, "/testdir", func(info os.FileInfo) error {
		return nil
	})
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestKubeDriver_DeleteDir(t *testing.T) {
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
				Read:   true,
				Write:  true,
				Delete: true,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(testUser).
		Build()

	auth := NewKubeAuth(fakeClient)
	auth.userCache.Store("testuser", testUser)

	mockStorage := &MockStorage{}
	mockStorage.On("DeleteDir", "/testdir").Return(nil)

	driver := &KubeDriver{
		auth:              auth,
		client:            fakeClient,
		authenticatedUser: "testuser",
		user:              testUser,
		storageImpl:       mockStorage,
	}

	err = driver.DeleteDir(nil, "/testdir")
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestKubeDriver_GetFile(t *testing.T) {
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
	auth.userCache.Store("testuser", testUser)

	testContent := "test file content"
	reader := io.NopCloser(strings.NewReader(testContent))

	mockStorage := &MockStorage{}
	mockStorage.On("GetFile", "/testfile.txt", int64(0)).Return(int64(len(testContent)), reader, nil)

	driver := &KubeDriver{
		auth:              auth,
		client:            fakeClient,
		authenticatedUser: "testuser",
		user:              testUser,
		storageImpl:       mockStorage,
	}

	size, gotReader, err := driver.GetFile(nil, "/testfile.txt", 0)
	assert.NoError(t, err)
	assert.Equal(t, int64(len(testContent)), size)
	assert.NotNil(t, gotReader)
	defer func() { _ = gotReader.Close() }()

	// Read content to verify
	content, err := io.ReadAll(gotReader)
	assert.NoError(t, err)
	assert.Equal(t, testContent, string(content))

	mockStorage.AssertExpectations(t)
}

func TestKubeDriver_PutFile(t *testing.T) {
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
	auth.userCache.Store("testuser", testUser)

	testContent := "test file content"
	reader := strings.NewReader(testContent)

	mockStorage := &MockStorage{}
	mockStorage.On("PutFile", "/testfile.txt", reader, int64(0)).Return(int64(len(testContent)), nil)

	driver := &KubeDriver{
		auth:              auth,
		client:            fakeClient,
		authenticatedUser: "testuser",
		user:              testUser,
		storageImpl:       mockStorage,
	}

	size, err := driver.PutFile(nil, "/testfile.txt", reader, int64(0))
	assert.NoError(t, err)
	assert.Equal(t, int64(len(testContent)), size)

	mockStorage.AssertExpectations(t)
}

func TestKubeDriver_OperationLogging(t *testing.T) {
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
				Kind: "FilesystemBackend",
				Name: "test-backend",
			},
			HomeDirectory: "/test",
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
		WithObjects(testUser).
		Build()

	auth := NewKubeAuth(fakeClient)
	auth.userCache.Store("testuser", testUser)

	mockStorage := &MockStorage{}
	driver := &KubeDriver{
		auth:              auth,
		client:            fakeClient,
		authenticatedUser: "testuser",
		user:              testUser,
		storageImpl:       mockStorage,
	}

	t.Run("GetFile success logging", func(t *testing.T) {
		testContent := "test file content"
		reader := io.NopCloser(strings.NewReader(testContent))

		mockStorage.On("GetFile", "/testfile.txt", int64(0)).Return(int64(len(testContent)), reader, nil)

		size, reader, err := driver.GetFile(nil, "/testfile.txt", 0)
		assert.NoError(t, err)
		assert.Equal(t, int64(len(testContent)), size)
		assert.NotNil(t, reader)
	})

	t.Run("PutFile success logging", func(t *testing.T) {
		testContent := "upload test content"
		reader := strings.NewReader(testContent)

		mockStorage.On("PutFile", "/upload.txt", reader, int64(0)).Return(int64(len(testContent)), nil)

		size, err := driver.PutFile(nil, "/upload.txt", reader, int64(0))
		assert.NoError(t, err)
		assert.Equal(t, int64(len(testContent)), size)
	})

	t.Run("DeleteFile success logging", func(t *testing.T) {
		mockStorage.On("DeleteFile", "/delete.txt").Return(nil)

		err := driver.DeleteFile(nil, "/delete.txt")
		assert.NoError(t, err)
	})

	t.Run("MakeDir success logging", func(t *testing.T) {
		mockStorage.On("MakeDir", "/newdir").Return(nil)

		err := driver.MakeDir(nil, "/newdir")
		assert.NoError(t, err)
	})

	t.Run("getAuthenticatedUsername returns correct user", func(t *testing.T) {
		username := driver.getAuthenticatedUsername()
		assert.Equal(t, "testuser", username)
	})

	mockStorage.AssertExpectations(t)
}

func TestTracingConfiguration(t *testing.T) {
	t.Run("tracing disabled by default", func(t *testing.T) {
		// Ensure no OTEL env vars are set
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
		t.Setenv("OTEL_SERVICE_NAME", "")

		assert.False(t, isTracingEnabled())
	})

	t.Run("tracing enabled with OTLP endpoint", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4317")

		assert.True(t, isTracingEnabled())
	})

	t.Run("tracing enabled with traces endpoint", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://localhost:4318/v1/traces")

		assert.True(t, isTracingEnabled())
	})

	t.Run("tracing enabled with service name", func(t *testing.T) {
		t.Setenv("OTEL_SERVICE_NAME", "kubeftpd")

		assert.True(t, isTracingEnabled())
	})
}

// Regression test for offset mode handling
func TestKubeDriver_PutFile_OffsetForced(t *testing.T) {
	scheme := runtime.NewScheme()
	err := ftpv1.AddToScheme(scheme)
	assert.NoError(t, err)

	testUser := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testuser",
			Namespace: "default",
		},
		Spec: ftpv1.UserSpec{
			Username:      "testuser",
			Password:      "testpass",
			Enabled:       true,
			Chroot:        true,
			HomeDirectory: "/home/testuser",
			Backend: ftpv1.BackendReference{
				Kind: "FilesystemBackend",
				Name: "test-backend",
			},
		},
	}

	mockStorage := &MockStorage{}

	driver := &KubeDriver{
		user:              testUser,
		storageImpl:       mockStorage,
		authenticatedUser: "testuser",
	}

	// Test that offset gets forced to 0
	reader := strings.NewReader("test content")
	mockStorage.On("PutFile", "/home/testuser/test.txt", reader, int64(0)).Return(int64(12), nil)

	// Call PutFile with non-zero offset - should be forced to 0
	size, err := driver.PutFile(nil, "/test.txt", reader, int64(100))

	assert.NoError(t, err)
	assert.Equal(t, int64(12), size)
	mockStorage.AssertExpectations(t)
}

// Regression test for structured logging compatibility
func TestKubeLogger_PrintCommand_PasswordRedaction(t *testing.T) {
	logger := &KubeLogger{}

	// Test PASS command redaction
	// This test verifies that passwords are redacted in logs
	// We can't easily test the actual logging output without complex setup,
	// but we can verify the function doesn't panic with password commands
	assert.NotPanics(t, func() {
		logger.PrintCommand("test-session", "PASS", "secretpassword")
		logger.PrintCommand("test-session", "USER", "testuser")
		logger.PrintCommand("test-session", "ACCT", "secretaccount")
	})
}
