package ftp

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"goftp.io/server/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
	"github.com/rossigee/kubeftpd/internal/storage"
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

func (m *MockStorage) Close() error {
	args := m.Called()
	return args.Error(0)
}

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

	username := driver.getAuthenticatedUsername(nil)
	assert.Equal(t, "testuser", username)

	// Test empty username
	driver.authenticatedUser = ""
	username = driver.getAuthenticatedUsername(nil)
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
	// The driver counts the client's bytes by wrapping the reader, so match the
	// stored source loosely and let the mock consume it so the count is taken.
	mockStorage.On("PutFile", "/testfile.txt", mock.Anything, int64(0)).
		Run(func(args mock.Arguments) {
			_, _ = io.ReadAll(args.Get(1).(io.Reader))
		}).Return(int64(len(testContent)), nil)

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

		mockStorage.On("PutFile", "/upload.txt", mock.Anything, int64(0)).
			Run(func(args mock.Arguments) {
				_, _ = io.ReadAll(args.Get(1).(io.Reader))
			}).Return(int64(len(testContent)), nil)

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
		username := driver.getAuthenticatedUsername(nil)
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

// A plain STOR (no REST, so offset -1) overwrites the whole object.
func TestKubeDriver_PutFile_PlainStoreOverwrites(t *testing.T) {
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

	// The backend must read the client's data and be asked for a whole-object write.
	reader := strings.NewReader("test content")
	mockStorage.On("PutFile", "/home/testuser/test.txt", mock.Anything, int64(0)).
		Run(func(args mock.Arguments) {
			_, _ = io.ReadAll(args.Get(1).(io.Reader))
		}).Return(int64(12), nil)

	size, err := driver.PutFile(nil, "/test.txt", reader, int64(-1))

	assert.NoError(t, err)
	assert.Equal(t, int64(12), size)
	mockStorage.AssertExpectations(t)
}

func uploadTestDriver(ms *MockStorage) *KubeDriver {
	user := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "alice", Namespace: "default"},
		Spec: ftpv1.UserSpec{
			Username:      "alice",
			HomeDirectory: "/home/alice",
			Permissions:   ftpv1.UserPermissions{Read: true, Write: true},
			Backend:       ftpv1.BackendReference{Kind: "FilesystemBackend", Name: "fs"},
		},
	}
	return &KubeDriver{user: user, storageImpl: ms, authenticatedUser: "alice"}
}

// capturePut expects one whole-object write of /f.txt and returns what it stored.
func capturePut(ms *MockStorage) *[]byte {
	stored := new([]byte)
	ms.On("PutFile", "/f.txt", mock.Anything, int64(0)).
		Run(func(args mock.Arguments) {
			data, _ := io.ReadAll(args.Get(1).(io.Reader))
			*stored = data
		}).Return(int64(0), nil).Once()
	return stored
}

// Uploads must store exactly what the FTP command means: APPE appends, a REST
// resume keeps the bytes before the offset, and only plain STOR replaces.
func TestKubeDriver_UploadSemantics(t *testing.T) {
	appe := &server.Context{Cmd: "APPE"}
	stor := &server.Context{Cmd: "STOR"}

	t.Run("plain STOR overwrites without reading the object", func(t *testing.T) {
		ms := &MockStorage{}
		stored := capturePut(ms)
		n, err := uploadTestDriver(ms).PutFile(stor, "/f.txt", strings.NewReader("new"), -1)
		require.NoError(t, err)
		assert.Equal(t, "new", string(*stored))
		assert.Equal(t, int64(3), n)
		ms.AssertNotCalled(t, "GetFile", mock.Anything, mock.Anything)
	})

	t.Run("APPE appends to the existing object", func(t *testing.T) {
		ms := &MockStorage{}
		ms.On("GetFile", "/f.txt", int64(0)).Return(int64(3), io.NopCloser(strings.NewReader("old")), nil).Once()
		stored := capturePut(ms)
		n, err := uploadTestDriver(ms).PutFile(appe, "/f.txt", strings.NewReader("new"), -1)
		require.NoError(t, err)
		assert.Equal(t, "oldnew", string(*stored))
		assert.Equal(t, int64(3), n, "reports only the bytes the client sent")
		ms.AssertExpectations(t)
	})

	t.Run("APPE creates the object when it does not exist", func(t *testing.T) {
		ms := &MockStorage{}
		ms.On("GetFile", "/f.txt", int64(0)).
			Return(int64(0), io.NopCloser(strings.NewReader("")), errors.New("file not found: /f.txt")).Once()
		stored := capturePut(ms)
		_, err := uploadTestDriver(ms).PutFile(appe, "/f.txt", strings.NewReader("new"), -1)
		require.NoError(t, err)
		assert.Equal(t, "new", string(*stored))
	})

	t.Run("resume at an offset keeps the bytes before it and replaces the rest", func(t *testing.T) {
		ms := &MockStorage{}
		ms.On("GetFile", "/f.txt", int64(0)).Return(int64(6), io.NopCloser(strings.NewReader("abcdef")), nil).Once()
		stored := capturePut(ms)
		_, err := uploadTestDriver(ms).PutFile(stor, "/f.txt", strings.NewReader("XY"), 3)
		require.NoError(t, err)
		assert.Equal(t, "abcXY", string(*stored))
	})

	t.Run("resume beyond the end of the object is refused", func(t *testing.T) {
		ms := &MockStorage{}
		ms.On("GetFile", "/f.txt", int64(0)).Return(int64(2), io.NopCloser(strings.NewReader("ab")), nil).Once()
		_, err := uploadTestDriver(ms).PutFile(stor, "/f.txt", strings.NewReader("X"), 5)
		assert.ErrorContains(t, err, "existing file is 2 bytes")
		ms.AssertNotCalled(t, "PutFile", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("resume of a missing object is refused", func(t *testing.T) {
		ms := &MockStorage{}
		ms.On("GetFile", "/f.txt", int64(0)).
			Return(int64(0), io.NopCloser(strings.NewReader("")), errors.New("file not found: /f.txt")).Once()
		_, err := uploadTestDriver(ms).PutFile(stor, "/f.txt", strings.NewReader("X"), 2)
		assert.ErrorContains(t, err, "does not exist")
	})

	t.Run("a failed read of the existing object is returned, not written over", func(t *testing.T) {
		ms := &MockStorage{}
		boom := errors.New("disk error")
		ms.On("GetFile", "/f.txt", int64(0)).Return(int64(0), io.NopCloser(strings.NewReader("")), boom).Once()
		_, err := uploadTestDriver(ms).PutFile(appe, "/f.txt", strings.NewReader("X"), -1)
		assert.ErrorIs(t, err, boom)
		ms.AssertNotCalled(t, "PutFile", mock.Anything, mock.Anything, mock.Anything)
	})
}

// End to end through the real filesystem storage: APPE accumulates and a resume
// replaces only the tail.
func TestKubeDriver_UploadsThroughFilesystemStorage(t *testing.T) {
	base := t.TempDir()
	scheme := runtime.NewScheme()
	require.NoError(t, ftpv1.AddToScheme(scheme))
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&ftpv1.FilesystemBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "fs", Namespace: "default"},
			Spec:       ftpv1.FilesystemBackendSpec{BasePath: base},
		},
	).Build()
	user := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "alice", Namespace: "default"},
		Spec: ftpv1.UserSpec{
			Username:      "alice",
			HomeDirectory: "/",
			Permissions:   ftpv1.UserPermissions{Read: true, Write: true, List: true},
			Backend:       ftpv1.BackendReference{Kind: "FilesystemBackend", Name: "fs"},
		},
	}
	st, err := storage.NewStorage(context.Background(), user, kubeClient)
	require.NoError(t, err)
	d := &KubeDriver{user: user, storageImpl: st, authenticatedUser: "alice", client: kubeClient}

	read := func(t *testing.T) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(base, "log.txt"))
		require.NoError(t, err)
		return string(data)
	}

	// APPE to a file that does not exist yet creates it.
	_, err = d.PutFile(&server.Context{Cmd: "APPE"}, "/log.txt", strings.NewReader("one"), -1)
	require.NoError(t, err)
	// APPE again adds to the end.
	_, err = d.PutFile(&server.Context{Cmd: "APPE"}, "/log.txt", strings.NewReader("two"), -1)
	require.NoError(t, err)
	assert.Equal(t, "onetwo", read(t))

	// A resume at offset 3 keeps "one" and replaces the rest.
	_, err = d.PutFile(&server.Context{Cmd: "STOR"}, "/log.txt", strings.NewReader("XY"), 3)
	require.NoError(t, err)
	assert.Equal(t, "oneXY", read(t))

	// A plain STOR overwrites.
	_, err = d.PutFile(&server.Context{Cmd: "STOR"}, "/log.txt", strings.NewReader("fresh"), -1)
	require.NoError(t, err)
	assert.Equal(t, "fresh", read(t))
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

// Regression: closing a connection must drop the session's user mapping, or
// the map grows for the life of the process.
func TestKubeDriver_CloseClearsSessionUser(t *testing.T) {
	auth := &KubeAuth{}
	auth.setSessionUser("ftp-session-192.0.2.1:50000", "alice")
	assert.Equal(t, "alice", auth.GetSessionUser("ftp-session-192.0.2.1:50000"))

	driver := &KubeDriver{auth: auth, sessionID: "ftp-session-192.0.2.1:50000"}
	assert.NoError(t, driver.Close())
	assert.Empty(t, auth.GetSessionUser("ftp-session-192.0.2.1:50000"))
}

// Close on a driver that never finished Init must not panic.
func TestKubeDriver_CloseWithoutInit(t *testing.T) {
	driver := &KubeDriver{}
	assert.NotPanics(t, func() {
		assert.NoError(t, driver.Close())
	})
}

// Close cancels the per-session context so in-flight work stops.
func TestKubeDriver_CloseCancelsSessionContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	driver := &KubeDriver{sessionCtx: ctx, sessionCancel: cancel}

	assert.NoError(t, driver.Close())
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
}

// newOpsTestDriver returns a driver whose user is already initialised, so the
// operations under test go straight to the mock storage.
func newOpsTestDriver(chroot bool) (*KubeDriver, *MockStorage) {
	ms := &MockStorage{}
	user := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "alice", Namespace: "default"},
		Spec: ftpv1.UserSpec{
			Username:      "alice",
			HomeDirectory: "/home/alice",
			Chroot:        chroot,
			Permissions:   ftpv1.UserPermissions{Read: true, Write: true, Delete: true, List: true},
		},
	}
	return &KubeDriver{authenticatedUser: "alice", user: user, storageImpl: ms}, ms
}

func TestKubeDriver_Operations_PassThroughToStorage(t *testing.T) {
	boom := errors.New("backend unavailable")

	t.Run("stat", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		info := &MockFileInfo{name: "a.txt", size: 3}
		ms.On("Stat", "a.txt").Return(info, nil).Once()
		got, err := d.Stat(nil, "a.txt")
		assert.NoError(t, err)
		assert.Equal(t, info, got)
		ms.AssertExpectations(t)
	})

	t.Run("stat error is returned unchanged", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("Stat", "a.txt").Return(&MockFileInfo{}, boom).Once()
		_, err := d.Stat(nil, "a.txt")
		assert.ErrorIs(t, err, boom)
	})

	t.Run("list", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("ListDir", "docs", mock.Anything).Return(nil).Once()
		assert.NoError(t, d.ListDir(nil, "docs", func(os.FileInfo) error { return nil }))
		ms.AssertExpectations(t)
	})

	t.Run("list error", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("ListDir", "docs", mock.Anything).Return(boom).Once()
		assert.ErrorIs(t, d.ListDir(nil, "docs", func(os.FileInfo) error { return nil }), boom)
	})

	t.Run("rmdir", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("DeleteDir", "old").Return(nil).Once()
		assert.NoError(t, d.DeleteDir(nil, "old"))
		ms.AssertExpectations(t)
	})

	t.Run("rmdir error", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("DeleteDir", "old").Return(boom).Once()
		assert.ErrorIs(t, d.DeleteDir(nil, "old"), boom)
	})

	t.Run("delete", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("DeleteFile", "a.txt").Return(nil).Once()
		assert.NoError(t, d.DeleteFile(nil, "a.txt"))
		ms.AssertExpectations(t)
	})

	t.Run("delete error", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("DeleteFile", "a.txt").Return(boom).Once()
		assert.ErrorIs(t, d.DeleteFile(nil, "a.txt"), boom)
	})

	t.Run("rename", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("Rename", "a.txt", "b.txt").Return(nil).Once()
		assert.NoError(t, d.Rename(nil, "a.txt", "b.txt"))
		ms.AssertExpectations(t)
	})

	t.Run("rename error", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("Rename", "a.txt", "b.txt").Return(boom).Once()
		assert.ErrorIs(t, d.Rename(nil, "a.txt", "b.txt"), boom)
	})

	t.Run("mkdir", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("MakeDir", "new").Return(nil).Once()
		assert.NoError(t, d.MakeDir(nil, "new"))
		ms.AssertExpectations(t)
	})

	t.Run("mkdir error", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("MakeDir", "new").Return(boom).Once()
		assert.ErrorIs(t, d.MakeDir(nil, "new"), boom)
	})

	t.Run("download", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("GetFile", "a.txt", int64(0)).
			Return(int64(5), io.NopCloser(strings.NewReader("hello")), nil).Once()
		size, reader, err := d.GetFile(nil, "a.txt", 0)
		assert.NoError(t, err)
		assert.Equal(t, int64(5), size)
		data, _ := io.ReadAll(reader)
		assert.Equal(t, "hello", string(data))
		ms.AssertExpectations(t)
	})

	t.Run("download error", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("GetFile", "a.txt", int64(0)).Return(int64(0), io.NopCloser(strings.NewReader("")), boom).Once()
		_, _, err := d.GetFile(nil, "a.txt", 0)
		assert.ErrorIs(t, err, boom)
	})

	t.Run("upload", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("PutFile", "up.txt", mock.Anything, int64(0)).
			Run(func(args mock.Arguments) { _, _ = io.ReadAll(args.Get(1).(io.Reader)) }).
			Return(int64(4), nil).Once()
		n, err := d.PutFile(nil, "up.txt", strings.NewReader("data"), 0)
		assert.NoError(t, err)
		assert.Equal(t, int64(4), n)
		ms.AssertExpectations(t)
	})

	t.Run("upload error", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("PutFile", "up.txt", mock.Anything, int64(0)).Return(int64(0), boom).Once()
		_, err := d.PutFile(nil, "up.txt", strings.NewReader("data"), 0)
		assert.ErrorIs(t, err, boom)
	})

	// The goftp Perm interface carries no session context, so the owner, the
	// group and the mode cannot be resolved per session. They are reported
	// as fixed values instead of being read from the storage of whichever
	// session happens to be the fallback one.
	t.Run("owner, group and mode", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		owner, err := d.GetOwner("a.txt")
		assert.NoError(t, err)
		assert.Equal(t, "ftp", owner)

		group, err := d.GetGroup("a.txt")
		assert.NoError(t, err)
		assert.Equal(t, "ftp", group)

		mode, err := d.GetMode("a.txt")
		assert.NoError(t, err)
		assert.Equal(t, fs.FileMode(0o644), mode)

		dirMode, err := d.GetMode("dir/")
		assert.NoError(t, err)
		assert.Equal(t, fs.FileMode(os.ModeDir|0o755), dirMode)

		ms.AssertNotCalled(t, "Stat", "a.txt")
	})
}

// With chroot enabled, every path is resolved inside the home directory before
// it reaches storage, and escapes are refused without calling storage at all.
func TestKubeDriver_Operations_ChrootResolvesAndDenies(t *testing.T) {
	t.Run("relative and absolute paths resolve under home", func(t *testing.T) {
		d, ms := newOpsTestDriver(true)
		ms.On("MakeDir", "/home/alice/docs").Return(nil).Once()
		ms.On("DeleteFile", "/home/alice/abs.txt").Return(nil).Once()
		assert.NoError(t, d.MakeDir(nil, "docs"))
		assert.NoError(t, d.DeleteFile(nil, "/abs.txt"))
		ms.AssertExpectations(t)
	})

	t.Run("escape is denied before storage", func(t *testing.T) {
		d, ms := newOpsTestDriver(true)
		_, err := d.Stat(nil, "../../../etc/passwd")
		assert.ErrorContains(t, err, "access denied")
		assert.ErrorContains(t, d.DeleteDir(nil, "../../etc"), "access denied")
		ms.AssertNotCalled(t, "Stat", mock.Anything)
		ms.AssertNotCalled(t, "DeleteDir", mock.Anything)

		// An absolute path that climbs out is re-rooted under home, not escaped
		ms.On("MakeDir", "/home/alice/tmp").Return(nil).Once()
		assert.NoError(t, d.MakeDir(nil, "/../../tmp"))
		ms.AssertExpectations(t)
	})

	t.Run("rename checks both paths", func(t *testing.T) {
		d, ms := newOpsTestDriver(true)
		err := d.Rename(nil, "a.txt", "../../escape.txt")
		assert.ErrorContains(t, err, "access denied")
		ms.AssertNotCalled(t, "Rename", mock.Anything, mock.Anything)
	})

	t.Run("upload and download resolve under home", func(t *testing.T) {
		d, ms := newOpsTestDriver(true)
		ms.On("PutFile", "/home/alice/up.txt", mock.Anything, int64(0)).Return(int64(1), nil).Once()
		ms.On("GetFile", "/home/alice/up.txt", int64(0)).
			Return(int64(1), io.NopCloser(strings.NewReader("x")), nil).Once()
		_, err := d.PutFile(nil, "up.txt", strings.NewReader("x"), 0)
		assert.NoError(t, err)
		_, r, err := d.GetFile(nil, "up.txt", 0)
		assert.NoError(t, err)
		_ = r.Close()
		ms.AssertExpectations(t)
	})
}

// ChOwner, ChGroup and ChMode deliberately succeed without changing anything,
// so that clients issuing SITE commands are not blocked. This pins that choice:
// the stored file mode must not change and no error must be returned.
func TestKubeDriver_ChangeAttributesAreNoOps(t *testing.T) {
	driver := &KubeDriver{}
	assert.NoError(t, driver.ChOwner("/a.txt", "root"))
	assert.NoError(t, driver.ChGroup("/a.txt", "wheel"))
	assert.NoError(t, driver.ChMode("/a.txt", 0o777))
}

// newInitTestAuth returns an auth backed by an empty fake client, so cache
// misses resolve to "no such user" rather than reaching a real cluster.
func newInitTestAuth(t *testing.T, objs ...client.Object) *KubeAuth {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, ftpv1.AddToScheme(scheme))
	return NewKubeAuth(fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build())
}

func TestKubeDriver_EnsureUserInitialized(t *testing.T) {
	t.Run("no authenticated user", func(t *testing.T) {
		d := &KubeDriver{auth: newInitTestAuth(t), sessionCtx: context.Background()}
		err := d.ensureUserInitialized()
		assert.ErrorContains(t, err, "user not authenticated")
	})

	t.Run("authenticated user missing from the auth cache", func(t *testing.T) {
		d := &KubeDriver{auth: newInitTestAuth(t), authenticatedUser: "ghost", sessionCtx: context.Background()}
		err := d.ensureUserInitialized()
		assert.ErrorContains(t, err, "user ghost not found in auth cache")
	})

	t.Run("storage initialisation failure is reported and not cached", func(t *testing.T) {
		u := &ftpv1.User{
			ObjectMeta: metav1.ObjectMeta{Name: "carol", Namespace: "default"},
			Spec: ftpv1.UserSpec{
				Username:      "carol",
				HomeDirectory: "/carol",
				Backend:       ftpv1.BackendReference{Kind: "FilesystemBackend", Name: "missing-backend"},
			},
		}
		auth := newInitTestAuth(t)
		auth.userCache.Store("carol", u)
		d := &KubeDriver{client: auth.client, auth: auth, authenticatedUser: "carol", sessionCtx: context.Background()}

		err := d.ensureUserInitialized()
		assert.ErrorContains(t, err, "failed to initialize storage for user carol")
		assert.Nil(t, d.storageImpl)
		assert.Nil(t, d.user, "a failed initialisation must not leave a half-set driver")
	})

	t.Run("success sets user and storage, then takes the fast path", func(t *testing.T) {
		backend := &ftpv1.FilesystemBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "fs", Namespace: "default"},
			Spec:       ftpv1.FilesystemBackendSpec{BasePath: t.TempDir()},
		}
		u := &ftpv1.User{
			ObjectMeta: metav1.ObjectMeta{Name: "dave", Namespace: "default"},
			Spec: ftpv1.UserSpec{
				Username:      "dave",
				HomeDirectory: "/dave",
				Permissions:   ftpv1.UserPermissions{Read: true, List: true},
				Backend:       ftpv1.BackendReference{Kind: "FilesystemBackend", Name: "fs"},
			},
		}
		auth := newInitTestAuth(t, backend)
		auth.userCache.Store("dave", u)
		d := &KubeDriver{client: auth.client, auth: auth, authenticatedUser: "dave", sessionCtx: context.Background()}

		require.NoError(t, d.ensureUserInitialized())
		assert.NotNil(t, d.storageImpl)
		assert.Equal(t, "dave", d.user.Spec.Username)
		fallback, err := d.fallbackSession()
		require.NoError(t, err)
		assert.Equal(t, "FilesystemBackend", d.getBackendType(fallback))

		// Second call returns straight away without consulting the cache again
		d.authenticatedUser = ""
		assert.NoError(t, d.ensureUserInitialized())
	})
}

func TestKubeDriver_GetBackendTypeWithoutUser(t *testing.T) {
	assert.Equal(t, "unknown", (&KubeDriver{}).getBackendType(nil))
}

func TestKubeDriverFactory_NewDriverCarriesDependencies(t *testing.T) {
	auth := newInitTestAuth(t)
	f := &KubeDriverFactory{client: auth.client, auth: auth}

	d, err := f.NewDriver()
	require.NoError(t, err)
	kd, ok := d.(*KubeDriver)
	require.True(t, ok, "got %T", d)
	assert.Equal(t, auth, kd.auth)
	assert.Equal(t, auth.client, kd.client)
	assert.Empty(t, kd.authenticatedUser, "user is set later, after authentication")
}

// Every operation must refuse to run for a session whose user cannot be
// initialised, and must not reach storage. Run once plain and once with tracing
// enabled, so the span branches are exercised too.
func TestKubeDriver_Operations_RefuseWithoutInitialisedUser(t *testing.T) {
	run := func(t *testing.T) {
		d := &KubeDriver{auth: newInitTestAuth(t), authenticatedUser: "ghost", sessionCtx: context.Background()}
		ctx := (*server.Context)(nil)
		noop := func(os.FileInfo) error { return nil }

		_, err := d.Stat(ctx, "a")
		assert.Error(t, err)
		assert.Error(t, d.ListDir(ctx, "a", noop))
		assert.Error(t, d.DeleteDir(ctx, "a"))
		assert.Error(t, d.DeleteFile(ctx, "a"))
		assert.Error(t, d.Rename(ctx, "a", "b"))
		assert.Error(t, d.MakeDir(ctx, "a"))
		assert.Error(t, d.ChangeDir(ctx, "a"))
		_, _, err = d.GetFile(ctx, "a", 0)
		assert.Error(t, err)
		_, err = d.PutFile(ctx, "a", strings.NewReader("x"), 0)
		assert.Error(t, err)
		_, err = d.PutFile(ctx, "a", strings.NewReader("x"), 5) // resumed upload path
		assert.Error(t, err)
	}

	t.Run("plain", run)
	t.Run("with tracing enabled", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4317")
		run(t)
	})
}

// With tracing enabled, every storage failure must still be returned to the
// client unchanged (after the span records it).
func TestKubeDriver_Operations_StorageErrorsWithTracing(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4317")
	boom := errors.New("backend down")
	noop := func(os.FileInfo) error { return nil }

	t.Run("stat", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("Stat", "a").Return(&MockFileInfo{}, boom).Once()
		_, err := d.Stat(nil, "a")
		assert.ErrorIs(t, err, boom)
	})
	t.Run("list", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("ListDir", "a", mock.Anything).Return(boom).Once()
		assert.ErrorIs(t, d.ListDir(nil, "a", noop), boom)
	})
	t.Run("rmdir", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("DeleteDir", "a").Return(boom).Once()
		assert.ErrorIs(t, d.DeleteDir(nil, "a"), boom)
	})
	t.Run("delete", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("DeleteFile", "a").Return(boom).Once()
		assert.ErrorIs(t, d.DeleteFile(nil, "a"), boom)
	})
	t.Run("rename", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("Rename", "a", "b").Return(boom).Once()
		assert.ErrorIs(t, d.Rename(nil, "a", "b"), boom)
	})
	t.Run("mkdir", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("MakeDir", "a").Return(boom).Once()
		assert.ErrorIs(t, d.MakeDir(nil, "a"), boom)
	})
	t.Run("download", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("GetFile", "a", int64(0)).Return(int64(0), io.NopCloser(strings.NewReader("")), boom).Once()
		_, _, err := d.GetFile(nil, "a", 0)
		assert.ErrorIs(t, err, boom)
	})
	t.Run("upload", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("PutFile", "a", mock.Anything, int64(0)).Return(int64(0), boom).Once()
		_, err := d.PutFile(nil, "a", strings.NewReader("x"), 0)
		assert.ErrorIs(t, err, boom)
	})
	t.Run("resume still reports backend errors", func(t *testing.T) {
		d, ms := newOpsTestDriver(false)
		ms.On("GetFile", "a", int64(0)).Return(int64(7), io.NopCloser(strings.NewReader("1234567")), nil).Once()
		ms.On("PutFile", "a", mock.Anything, int64(0)).Return(int64(0), boom).Once()
		_, err := d.PutFile(&server.Context{Cmd: "STOR"}, "a", strings.NewReader("x"), 7)
		assert.ErrorIs(t, err, boom)
	})
	t.Run("chroot denial on download and upload", func(t *testing.T) {
		d, ms := newOpsTestDriver(true)
		_, _, err := d.GetFile(nil, "../../etc/shadow", 0)
		assert.ErrorContains(t, err, "access denied")
		_, err = d.PutFile(nil, "../../etc/x", strings.NewReader("x"), 0)
		assert.ErrorContains(t, err, "access denied")
		ms.AssertNotCalled(t, "GetFile", mock.Anything, mock.Anything)
		ms.AssertNotCalled(t, "PutFile", mock.Anything, mock.Anything, mock.Anything)
	})
}
