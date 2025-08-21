package storage

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

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
	err := storage.ListDir("", func(info os.FileInfo) error {
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

	size, err := storage.PutFile("testfile.txt", reader, int64(0))
	assert.NoError(t, err)
	assert.Equal(t, int64(len(testContent)), size)

	mockBackend.AssertExpectations(t)
}

// NOTE: MinIO storage layer write verification is tested through the backend layer.
// The enhanced verification includes:
// 1. StatObject verification after PutObject to confirm upload completion
// 2. Size validation to ensure object size matches expected
// 3. Cleanup on verification failure
// The countingReader tracks bytes uploaded and verifies consistency
// Full integration testing requires a real MinIO instance

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

// Regression test for empty directory handling
func TestMinioStorage_Stat_EmptyDirectory(t *testing.T) {
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
		user:        user,
		backend:     mockBackend,
		basePath:    "/home/testuser",
		currentDir:  "/home/testuser",
		backendName: "test-backend",
	}

	// Mock StatObject to fail (file doesn't exist)
	mockBackend.On("StatObject", "/home/testuser").Return((*backends.ObjectInfo)(nil), errors.New("object not found"))

	// Mock ListObjects to succeed with empty result (empty directory)
	mockBackend.On("ListObjects", "/home/testuser", false).Return([]*backends.ObjectInfo{}, nil)

	fileInfo, err := storage.Stat("")

	// Should return directory info, not error
	assert.NoError(t, err)
	assert.NotNil(t, fileInfo)
	assert.True(t, fileInfo.IsDir())
	assert.Equal(t, ".", fileInfo.Name()) // Empty path resolves to current directory "."
	assert.Equal(t, int64(0), fileInfo.Size())

	mockBackend.AssertExpectations(t)
}

// Regression test for the MinIO empty directory fix (commit 4da8db3)
func TestMinioStorage_Stat_EmptyDirectoryRegression(t *testing.T) {
	// This test specifically validates the fix where empty directories
	// (just key prefixes in object storage) should be treated as valid directories
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
		user:        user,
		backend:     mockBackend,
		basePath:    "/home/testuser",
		currentDir:  "/home/testuser",
		backendName: "test-backend",
	}

	// Test empty directory with trailing slash - should be treated as directory
	mockBackend.On("StatObject", "/home/testuser/documents").Return((*backends.ObjectInfo)(nil), errors.New("object not found"))
	mockBackend.On("ListObjects", "/home/testuser/documents", false).Return([]*backends.ObjectInfo{}, nil)

	fileInfo, err := storage.Stat("documents/")
	assert.NoError(t, err, "Empty directory with trailing slash should not fail")
	assert.NotNil(t, fileInfo)
	assert.True(t, fileInfo.IsDir())
	assert.Equal(t, "documents", fileInfo.Name())

	// Test empty directory without extension - should be treated as directory
	mockBackend.On("StatObject", "/home/testuser/projects").Return((*backends.ObjectInfo)(nil), errors.New("object not found"))
	mockBackend.On("ListObjects", "/home/testuser/projects", false).Return([]*backends.ObjectInfo{}, nil)

	fileInfo2, err2 := storage.Stat("projects")
	assert.NoError(t, err2, "Empty directory without extension should not fail")
	assert.NotNil(t, fileInfo2)
	assert.True(t, fileInfo2.IsDir())
	assert.Equal(t, "projects", fileInfo2.Name())

	mockBackend.AssertExpectations(t)
}

func TestMinioStorage_Stat_FileWithExtensionNotFound(t *testing.T) {
	// Test that files with extensions immediately fail if not found
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
		user:        user,
		backend:     mockBackend,
		basePath:    "/home/testuser",
		currentDir:  "/home/testuser",
		backendName: "test-backend",
	}

	// File with extension not found - should return error immediately
	mockBackend.On("StatObject", "/home/testuser/nonexistent.txt").Return((*backends.ObjectInfo)(nil), errors.New("object not found"))

	fileInfo, err := storage.Stat("nonexistent.txt")
	assert.Error(t, err)
	assert.Nil(t, fileInfo)
	assert.Contains(t, err.Error(), "file not found")

	// Should not call ListObjects for files with extensions
	mockBackend.AssertNotCalled(t, "ListObjects")
	mockBackend.AssertExpectations(t)
}

func TestMinioStorage_ChangeDir_NonexistentDirectory(t *testing.T) {
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

	// Mock ListObjects to fail for nonexistent directory
	mockBackend.On("ListObjects", "/home/testuser/nonexistent", false).Return(([]*backends.ObjectInfo)(nil), errors.New("directory not found"))

	err := storage.ChangeDir("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "directory not found")

	// Current directory should remain unchanged
	assert.Equal(t, "/home/testuser", storage.currentDir)

	mockBackend.AssertExpectations(t)
}

func TestMinioStorage_DeleteDir(t *testing.T) {
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

	storage := &minioStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	// Mock successful directory deletion (recursive)
	mockBackend.On("RemoveObjects", "/home/testuser/testdir", true).Return(nil)

	err := storage.DeleteDir("testdir")
	assert.NoError(t, err)

	mockBackend.AssertExpectations(t)
}

func TestMinioStorage_DeleteDir_PermissionDenied(t *testing.T) {
	user := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testuser",
		},
		Spec: ftpv1.UserSpec{
			Username:      "testuser",
			HomeDirectory: "/home/testuser",
			Permissions: ftpv1.UserPermissions{
				Delete: false, // Delete permission disabled
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

	err := storage.DeleteDir("testdir")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delete permission denied")

	// Should not attempt to remove objects
	mockBackend.AssertNotCalled(t, "RemoveObjects")
}

func TestMinioStorage_MakeDir(t *testing.T) {
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
	storage := &minioStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	// MakeDir should create an empty object with trailing slash to represent directory
	mockBackend.On("PutObject", "/home/testuser/newdir/", mock.Anything, int64(0)).Return(nil)

	err := storage.MakeDir("newdir")
	assert.NoError(t, err, "MakeDir should always succeed in object storage")
}

func TestMinioStorage_GetFile_PermissionDenied(t *testing.T) {
	user := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testuser",
		},
		Spec: ftpv1.UserSpec{
			Username:      "testuser",
			HomeDirectory: "/home/testuser",
			Permissions: ftpv1.UserPermissions{
				Read: false, // Read permission disabled
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

	size, reader, err := storage.GetFile("testfile.txt", 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read permission denied")
	assert.Equal(t, int64(0), size)
	assert.Nil(t, reader)

	// Should not call backend methods
	mockBackend.AssertNotCalled(t, "StatObject")
	mockBackend.AssertNotCalled(t, "GetObject")
}

func TestMinioStorage_PutFile_PermissionDenied(t *testing.T) {
	user := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testuser",
		},
		Spec: ftpv1.UserSpec{
			Username:      "testuser",
			HomeDirectory: "/home/testuser",
			Permissions: ftpv1.UserPermissions{
				Write: false, // Write permission disabled
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

	testContent := "test content"
	reader := strings.NewReader(testContent)

	size, err := storage.PutFile("testfile.txt", reader, int64(len(testContent)))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "write permission denied")
	assert.Equal(t, int64(0), size)

	// Should not call backend methods
	mockBackend.AssertNotCalled(t, "PutObject")
}

func TestMinioStorage_Rename_PermissionDenied(t *testing.T) {
	user := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testuser",
		},
		Spec: ftpv1.UserSpec{
			Username:      "testuser",
			HomeDirectory: "/home/testuser",
			Permissions: ftpv1.UserPermissions{
				Write: false, // Write permission disabled
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

	err := storage.Rename("oldfile.txt", "newfile.txt")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "write permission denied")

	// Should not call backend methods
	mockBackend.AssertNotCalled(t, "CopyObject")
}

// Test MinioFileInfo methods for full coverage
func TestMinioFileInfo_Methods(t *testing.T) {
	now := time.Now()
	fileInfo := &minioFileInfo{
		name:    "testfile.txt",
		size:    1024,
		mode:    0644,
		modTime: now,
		isDir:   false,
	}

	assert.Equal(t, "testfile.txt", fileInfo.Name())
	assert.Equal(t, int64(1024), fileInfo.Size())
	assert.Equal(t, os.FileMode(0644), fileInfo.Mode())
	assert.Equal(t, now, fileInfo.ModTime())
	assert.False(t, fileInfo.IsDir())
	assert.Equal(t, "", fileInfo.Owner()) // Always returns empty string
	assert.Equal(t, "", fileInfo.Group()) // Always returns empty string
	assert.Nil(t, fileInfo.Sys())         // Always returns nil

	// Test directory file info
	dirInfo := &minioFileInfo{
		name:    "testdir",
		size:    0,
		mode:    os.ModeDir | 0755,
		modTime: now,
		isDir:   true,
	}

	assert.True(t, dirInfo.IsDir())
	assert.Equal(t, "testdir", dirInfo.Name())
	assert.Equal(t, int64(0), dirInfo.Size())
}
