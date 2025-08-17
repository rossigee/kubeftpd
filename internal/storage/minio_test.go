package storage

import (
	"io"
	"strings"
	"testing"

	"github.com/goftp/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
	"github.com/rossigee/kubeftpd/internal/backends"
)

// MockMinioBackend for testing
type MockMinioBackend struct {
	mock.Mock
}

func (m *MockMinioBackend) StatObject(objectName string) (*backends.ObjectInfo, error) {
	args := m.Called(objectName)
	return args.Get(0).(*backends.ObjectInfo), args.Error(1)
}

func (m *MockMinioBackend) GetObject(objectName string, offset, length int64) (io.ReadCloser, error) {
	args := m.Called(objectName, offset, length)
	return args.Get(0).(io.ReadCloser), args.Error(1)
}

func (m *MockMinioBackend) PutObject(objectName string, reader io.Reader, size int64) error {
	// Consume the reader to simulate real MinIO behavior
	if reader != nil {
		_, _ = io.Copy(io.Discard, reader)
	}
	args := m.Called(objectName, reader, size)
	return args.Error(0)
}

func (m *MockMinioBackend) RemoveObject(objectName string) error {
	args := m.Called(objectName)
	return args.Error(0)
}

func (m *MockMinioBackend) RemoveObjects(prefix string, recursive bool) error {
	args := m.Called(prefix, recursive)
	return args.Error(0)
}

func (m *MockMinioBackend) CopyObject(srcObject, dstObject string, deleteSource bool) error {
	args := m.Called(srcObject, dstObject, deleteSource)
	return args.Error(0)
}

func (m *MockMinioBackend) ListObjects(prefix string, recursive bool) ([]*backends.ObjectInfo, error) {
	args := m.Called(prefix, recursive)
	return args.Get(0).([]*backends.ObjectInfo), args.Error(1)
}

func TestMinioStorage_ChangeDir(t *testing.T) {
	user := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testuser",
		},
		Spec: ftpv1.UserSpec{
			Username:      "testuser",
			HomeDirectory: "/home/testuser",
		},
	}

	mockBackend := &MockMinioBackend{}

	storage := &minioStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	// Set up mock expectations for ChangeDir calls in order
	mockBackend.On("ListObjects", "/home/testuser/subdir", false).Return([]*backends.ObjectInfo{}, nil)
	mockBackend.On("ListObjects", "/home/testuser/subdir/another", false).Return([]*backends.ObjectInfo{}, nil)

	// Test changing to a subdirectory
	err := storage.ChangeDir("subdir")
	assert.NoError(t, err)

	// Test changing to another directory (relative path)
	err = storage.ChangeDir("another")
	assert.NoError(t, err)

	mockBackend.AssertExpectations(t)
}

func TestMinioStorage_Stat(t *testing.T) {
	user := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testuser",
		},
		Spec: ftpv1.UserSpec{
			Username:      "testuser",
			HomeDirectory: "/home/testuser",
		},
	}

	mockBackend := &MockMinioBackend{}

	objectInfo := &backends.ObjectInfo{
		Key:  "testfile.txt",
		Size: 1024,
	}

	mockBackend.On("StatObject", "/home/testuser/testfile.txt").Return(objectInfo, nil)

	storage := &minioStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	fileInfo, err := storage.Stat("testfile.txt")
	assert.NoError(t, err)
	assert.NotNil(t, fileInfo)
	assert.Equal(t, "testfile.txt", fileInfo.Name())
	assert.Equal(t, int64(1024), fileInfo.Size())

	mockBackend.AssertExpectations(t)
}

func TestMinioStorage_ListDir(t *testing.T) {
	user := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testuser",
		},
		Spec: ftpv1.UserSpec{
			Username:      "testuser",
			HomeDirectory: "/home/testuser",
		},
	}

	mockBackend := &MockMinioBackend{}

	objects := []*backends.ObjectInfo{
		{
			Key:  "file1.txt",
			Size: 1024,
		},
		{
			Key:  "file2.txt",
			Size: 2048,
		},
	}

	mockBackend.On("ListObjects", "/home/testuser", false).Return(objects, nil)

	storage := &minioStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	var fileNames []string
	err := storage.ListDir("", func(info server.FileInfo) error {
		fileNames = append(fileNames, info.Name())
		return nil
	})

	assert.NoError(t, err)
	assert.Contains(t, fileNames, "file1.txt")
	assert.Contains(t, fileNames, "file2.txt")

	mockBackend.AssertExpectations(t)
}

func TestMinioStorage_DeleteFile(t *testing.T) {
	user := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testuser",
		},
		Spec: ftpv1.UserSpec{
			Username:      "testuser",
			HomeDirectory: "/home/testuser",
			Permissions: ftpv1.UserPermissions{
				Delete: true,
			},
		},
	}

	mockBackend := &MockMinioBackend{}
	mockBackend.On("RemoveObject", "/home/testuser/testfile.txt").Return(nil)

	storage := &minioStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	err := storage.DeleteFile("testfile.txt")
	assert.NoError(t, err)

	mockBackend.AssertExpectations(t)
}

func TestMinioStorage_DeleteFile_PermissionDenied(t *testing.T) {
	user := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testuser",
		},
		Spec: ftpv1.UserSpec{
			Username:      "testuser",
			HomeDirectory: "/home/testuser",
			Permissions: ftpv1.UserPermissions{
				Delete: false,
			},
		},
	}

	mockBackend := &MockMinioBackend{}

	storage := &minioStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	err := storage.DeleteFile("testfile.txt")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delete permission denied")

	mockBackend.AssertNotCalled(t, "RemoveObject")
}

func TestMinioStorage_Rename(t *testing.T) {
	user := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testuser",
		},
		Spec: ftpv1.UserSpec{
			Username:      "testuser",
			HomeDirectory: "/home/testuser",
			Permissions: ftpv1.UserPermissions{
				Write: true,
			},
		},
	}

	mockBackend := &MockMinioBackend{}
	mockBackend.On("CopyObject", "/home/testuser/oldfile.txt", "/home/testuser/newfile.txt", true).Return(nil)

	storage := &minioStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	err := storage.Rename("oldfile.txt", "newfile.txt")
	assert.NoError(t, err)

	mockBackend.AssertExpectations(t)
}

func TestMinioStorage_GetFile(t *testing.T) {
	user := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testuser",
		},
		Spec: ftpv1.UserSpec{
			Username:      "testuser",
			HomeDirectory: "/home/testuser",
			Permissions: ftpv1.UserPermissions{
				Read: true,
			},
		},
	}

	mockBackend := &MockMinioBackend{}

	objectInfo := &backends.ObjectInfo{
		Key:  "testfile.txt",
		Size: 1024,
	}

	testContent := "test file content"
	reader := io.NopCloser(strings.NewReader(testContent))

	mockBackend.On("StatObject", "/home/testuser/testfile.txt").Return(objectInfo, nil)
	mockBackend.On("GetObject", "/home/testuser/testfile.txt", int64(0), int64(1024)).Return(reader, nil)

	storage := &minioStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	size, gotReader, err := storage.GetFile("testfile.txt", 0)
	assert.NoError(t, err)
	assert.Equal(t, int64(1024), size)
	assert.NotNil(t, gotReader)
	defer func() { _ = gotReader.Close() }()

	// Read content to verify
	content, err := io.ReadAll(gotReader)
	assert.NoError(t, err)
	assert.Equal(t, testContent, string(content))

	mockBackend.AssertExpectations(t)
}

func TestMinioStorage_PutFile(t *testing.T) {
	user := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testuser",
		},
		Spec: ftpv1.UserSpec{
			Username:      "testuser",
			HomeDirectory: "/home/testuser",
			Permissions: ftpv1.UserPermissions{
				Write: true,
			},
		},
	}

	mockBackend := &MockMinioBackend{}

	testContent := "test file content"
	reader := strings.NewReader(testContent)

	// Expect streaming upload with unknown size (-1)
	mockBackend.On("PutObject", "/home/testuser/testfile.txt", mock.Anything, int64(-1)).Return(nil)

	storage := &minioStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	size, err := storage.PutFile("testfile.txt", reader, false)
	assert.NoError(t, err)
	assert.Equal(t, int64(len(testContent)), size)

	mockBackend.AssertExpectations(t)
}

func TestMinioStorage_resolvePath(t *testing.T) {
	storage := &minioStorage{
		basePath:   "/home/testuser",
		currentDir: "/home/testuser/subdir",
	}

	tests := []struct {
		name         string
		relativePath string
		expected     string
	}{
		{
			name:         "empty path",
			relativePath: "",
			expected:     "/home/testuser/subdir",
		},
		{
			name:         "current directory",
			relativePath: ".",
			expected:     "/home/testuser/subdir",
		},
		{
			name:         "relative path",
			relativePath: "file.txt",
			expected:     "/home/testuser/subdir/file.txt",
		},
		{
			name:         "absolute path",
			relativePath: "/documents/file.txt",
			expected:     "/home/testuser/documents/file.txt",
		},
		{
			name:         "parent directory",
			relativePath: "../",
			expected:     "/home/testuser",
		},
		{
			name:         "parent with file",
			relativePath: "../file.txt",
			expected:     "/home/testuser/file.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := storage.resolvePath(tt.relativePath)
			assert.Equal(t, tt.expected, result)
		})
	}
}
