package storage

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
	"github.com/rossigee/kubeftpd/internal/backends"
)

// MockFilesystemBackend for testing
type MockFilesystemBackend struct {
	mock.Mock
}

func (m *MockFilesystemBackend) PutFile(filePath string, reader io.Reader, size int64) error {
	// Consume the reader to simulate real behavior
	if reader != nil {
		_, _ = io.Copy(io.Discard, reader)
	}
	args := m.Called(filePath, reader, size)
	return args.Error(0)
}

func (m *MockFilesystemBackend) GetFile(filePath string, offset, length int64) (io.ReadCloser, error) {
	args := m.Called(filePath, offset, length)
	return args.Get(0).(io.ReadCloser), args.Error(1)
}

func (m *MockFilesystemBackend) StatFile(filePath string) (*backends.FileInfo, error) {
	args := m.Called(filePath)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*backends.FileInfo), args.Error(1)
}

func (m *MockFilesystemBackend) RemoveFile(filePath string) error {
	args := m.Called(filePath)
	return args.Error(0)
}

func (m *MockFilesystemBackend) ListFiles(dirPath string, recursive bool) ([]backends.FileInfo, error) {
	args := m.Called(dirPath, recursive)
	return args.Get(0).([]backends.FileInfo), args.Error(1)
}

func (m *MockFilesystemBackend) MakeDir(dirPath string) error {
	args := m.Called(dirPath)
	return args.Error(0)
}

func (m *MockFilesystemBackend) RemoveDir(dirPath string, recursive bool) error {
	args := m.Called(dirPath, recursive)
	return args.Error(0)
}

func (m *MockFilesystemBackend) CopyFile(srcPath, dstPath string, deleteSource bool) error {
	args := m.Called(srcPath, dstPath, deleteSource)
	return args.Error(0)
}

func (m *MockFilesystemBackend) GetBasePath() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockFilesystemBackend) IsReadOnly() bool {
	args := m.Called()
	return args.Bool(0)
}

func createTestUser() *ftpv1.User {
	return &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testuser",
		},
		Spec: ftpv1.UserSpec{
			Username:      "testuser",
			HomeDirectory: "/home/testuser",
			Permissions: ftpv1.UserPermissions{
				Read:   true,
				Write:  true,
				Delete: true,
				List:   true,
			},
		},
	}
}

func TestFilesystemStorage_Creation(t *testing.T) {
	user := createTestUser()
	mockBackend := &MockFilesystemBackend{}

	storage := &filesystemStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	assert.NotNil(t, storage)
	assert.Equal(t, user, storage.user)
	assert.Equal(t, mockBackend, storage.backend)
	assert.Equal(t, "/home/testuser", storage.basePath)
	assert.Equal(t, "/home/testuser", storage.currentDir)
}

func TestFilesystemStorage_ChangeDir(t *testing.T) {
	user := createTestUser()
	mockBackend := &MockFilesystemBackend{}

	storage := &filesystemStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	// Mock directory existence check
	dirInfo := &backends.FileInfo{
		Name:    "subdir",
		Size:    0,
		ModTime: metav1.Now().Time,
		IsDir:   true,
	}
	mockBackend.On("StatFile", "/home/testuser/subdir").Return(dirInfo, nil)

	err := storage.ChangeDir("subdir")
	assert.NoError(t, err)
	assert.Equal(t, "/home/testuser/subdir", storage.currentDir)

	mockBackend.AssertExpectations(t)
}

func TestFilesystemStorage_ChangeDir_InvalidPath(t *testing.T) {
	user := createTestUser()
	mockBackend := &MockFilesystemBackend{}

	storage := &filesystemStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	// Mock directory not found
	mockBackend.On("StatFile", "/home/testuser/nonexistent").Return((*backends.FileInfo)(nil), os.ErrNotExist)

	err := storage.ChangeDir("nonexistent")
	assert.Error(t, err)
	assert.Equal(t, "/home/testuser", storage.currentDir) // Should remain unchanged

	mockBackend.AssertExpectations(t)
}

func TestFilesystemStorage_Stat(t *testing.T) {
	user := createTestUser()
	mockBackend := &MockFilesystemBackend{}

	storage := &filesystemStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	fileInfo := &backends.FileInfo{
		Name:    "test.txt",
		Size:    1024,
		ModTime: metav1.Now().Time,
		IsDir:   false,
	}

	mockBackend.On("StatFile", "/home/testuser/test.txt").Return(fileInfo, nil)

	result, err := storage.Stat("test.txt")
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "test.txt", result.Name())
	assert.Equal(t, int64(1024), result.Size())
	assert.False(t, result.IsDir())

	mockBackend.AssertExpectations(t)
}

func TestFilesystemStorage_ListDir(t *testing.T) {
	user := createTestUser()
	mockBackend := &MockFilesystemBackend{}

	storage := &filesystemStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	fileInfos := []backends.FileInfo{
		{
			Name:    "file1.txt",
			Size:    1024,
			ModTime: metav1.Now().Time,
			IsDir:   false,
		},
		{
			Name:    "subdir",
			Size:    0,
			ModTime: metav1.Now().Time,
			IsDir:   true,
		},
	}

	mockBackend.On("ListFiles", "/home/testuser", false).Return(fileInfos, nil)

	var fileNames []string
	err := storage.ListDir("", func(info os.FileInfo) error {
		fileNames = append(fileNames, info.Name())
		return nil
	})

	assert.NoError(t, err)
	assert.Contains(t, fileNames, "file1.txt")
	assert.Contains(t, fileNames, "subdir")

	mockBackend.AssertExpectations(t)
}

func TestFilesystemStorage_ListDir_PermissionDenied(t *testing.T) {
	user := createTestUser()
	user.Spec.Permissions.List = false // Disable list permission

	mockBackend := &MockFilesystemBackend{}

	storage := &filesystemStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	err := storage.ListDir("", func(info os.FileInfo) error {
		return nil
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "list permission denied")

	// Backend should not be called
	mockBackend.AssertNotCalled(t, "ListFiles")
}

func TestFilesystemStorage_DeleteFile(t *testing.T) {
	user := createTestUser()
	mockBackend := &MockFilesystemBackend{}

	storage := &filesystemStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	mockBackend.On("IsReadOnly").Return(false)
	mockBackend.On("RemoveFile", "/home/testuser/test.txt").Return(nil)

	err := storage.DeleteFile("test.txt")
	assert.NoError(t, err)

	mockBackend.AssertExpectations(t)
}

func TestFilesystemStorage_DeleteFile_PermissionDenied(t *testing.T) {
	user := createTestUser()
	user.Spec.Permissions.Delete = false // Disable delete permission

	mockBackend := &MockFilesystemBackend{}

	storage := &filesystemStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	err := storage.DeleteFile("test.txt")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delete permission denied")

	// Backend should not be called
	mockBackend.AssertNotCalled(t, "RemoveFile")
}

func TestFilesystemStorage_Rename(t *testing.T) {
	user := createTestUser()
	mockBackend := &MockFilesystemBackend{}

	storage := &filesystemStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	// Mock the operations for rename using CopyFile
	mockBackend.On("IsReadOnly").Return(false)
	mockBackend.On("CopyFile", "/home/testuser/old.txt", "/home/testuser/new.txt", true).Return(nil)

	err := storage.Rename("old.txt", "new.txt")
	assert.NoError(t, err)

	mockBackend.AssertExpectations(t)
}

func TestFilesystemStorage_Rename_PermissionDenied(t *testing.T) {
	user := createTestUser()
	user.Spec.Permissions.Write = false // Disable write permission

	mockBackend := &MockFilesystemBackend{}

	storage := &filesystemStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	err := storage.Rename("old.txt", "new.txt")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "write permission denied")

	// Backend should not be called
	mockBackend.AssertNotCalled(t, "CopyFile")
}

func TestFilesystemStorage_GetFile(t *testing.T) {
	user := createTestUser()
	mockBackend := &MockFilesystemBackend{}

	storage := &filesystemStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	testContent := "test file content"
	reader := io.NopCloser(strings.NewReader(testContent))

	fileInfo := &backends.FileInfo{
		Name:    "test.txt",
		Size:    int64(len(testContent)),
		ModTime: metav1.Now().Time,
		IsDir:   false,
	}

	mockBackend.On("StatFile", "/home/testuser/test.txt").Return(fileInfo, nil)
	mockBackend.On("GetFile", "/home/testuser/test.txt", int64(0), int64(len(testContent))).Return(reader, nil)

	size, gotReader, err := storage.GetFile("test.txt", 0)
	assert.NoError(t, err)
	assert.Equal(t, int64(len(testContent)), size)
	assert.NotNil(t, gotReader)
	defer func() { _ = gotReader.Close() }()

	// Read content to verify
	content, err := io.ReadAll(gotReader)
	assert.NoError(t, err)
	assert.Equal(t, testContent, string(content))

	mockBackend.AssertExpectations(t)
}

func TestFilesystemStorage_GetFile_PermissionDenied(t *testing.T) {
	user := createTestUser()
	user.Spec.Permissions.Read = false // Disable read permission

	mockBackend := &MockFilesystemBackend{}

	storage := &filesystemStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	_, _, err := storage.GetFile("test.txt", 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read permission denied")

	// Backend should not be called
	mockBackend.AssertNotCalled(t, "StatFile")
	mockBackend.AssertNotCalled(t, "GetFile")
}

func TestFilesystemStorage_PutFile(t *testing.T) {
	user := createTestUser()
	mockBackend := &MockFilesystemBackend{}

	storage := &filesystemStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	testContent := "test file content"
	reader := strings.NewReader(testContent)

	// Expect streaming upload with unknown size (-1)
	mockBackend.On("IsReadOnly").Return(false)
	mockBackend.On("PutFile", "/home/testuser/test.txt", mock.Anything, int64(-1)).Return(nil)

	size, err := storage.PutFile("test.txt", reader, int64(0))
	assert.NoError(t, err)
	assert.Equal(t, int64(len(testContent)), size)

	mockBackend.AssertExpectations(t)
}

func TestFilesystemStorage_PutFile_PermissionDenied(t *testing.T) {
	user := createTestUser()
	user.Spec.Permissions.Write = false // Disable write permission

	mockBackend := &MockFilesystemBackend{}

	storage := &filesystemStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	testContent := "test file content"
	reader := strings.NewReader(testContent)

	_, err := storage.PutFile("test.txt", reader, int64(0))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "write permission denied")

	// Backend should not be called
	mockBackend.AssertNotCalled(t, "PutFile")
}

func TestFilesystemStorage_MakeDir(t *testing.T) {
	user := createTestUser()
	mockBackend := &MockFilesystemBackend{}

	storage := &filesystemStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	mockBackend.On("IsReadOnly").Return(false)
	mockBackend.On("MakeDir", "/home/testuser/newdir").Return(nil)

	err := storage.MakeDir("newdir")
	assert.NoError(t, err)

	mockBackend.AssertExpectations(t)
}

func TestFilesystemStorage_MakeDir_PermissionDenied(t *testing.T) {
	user := createTestUser()
	user.Spec.Permissions.Write = false // Disable write permission

	mockBackend := &MockFilesystemBackend{}

	storage := &filesystemStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	err := storage.MakeDir("newdir")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "write permission denied")

	// Backend should not be called
	mockBackend.AssertNotCalled(t, "MakeDir")
}

func TestFilesystemStorage_RemoveDir(t *testing.T) {
	user := createTestUser()
	mockBackend := &MockFilesystemBackend{}

	storage := &filesystemStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	mockBackend.On("IsReadOnly").Return(false)
	mockBackend.On("RemoveDir", "/home/testuser/olddir", true).Return(nil)

	err := storage.DeleteDir("olddir")
	assert.NoError(t, err)

	mockBackend.AssertExpectations(t)
}

func TestFilesystemStorage_RemoveDir_PermissionDenied(t *testing.T) {
	user := createTestUser()
	user.Spec.Permissions.Delete = false // Disable delete permission

	mockBackend := &MockFilesystemBackend{}

	storage := &filesystemStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	err := storage.DeleteDir("olddir")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delete permission denied")

	// Backend should not be called
	mockBackend.AssertNotCalled(t, "RemoveDir")
}

func TestFilesystemStorage_resolvePath(t *testing.T) {
	storage := &filesystemStorage{
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
			name:         "absolute path within home",
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
		{
			name:         "multiple parent traversals",
			relativePath: "../../file.txt",
			expected:     "/home/testuser/file.txt", // Should be constrained to basePath
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := storage.resolvePath(tt.relativePath)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFilesystemStorage_StreamingBehavior(t *testing.T) {
	user := createTestUser()
	mockBackend := &MockFilesystemBackend{}

	storage := &filesystemStorage{
		user:       user,
		backend:    mockBackend,
		basePath:   "/home/testuser",
		currentDir: "/home/testuser",
	}

	// Create a large content reader to test streaming
	largeContent := strings.Repeat("0123456789", 10000) // 100KB
	reader := strings.NewReader(largeContent)

	// Mock backend should receive the reader and unknown size
	mockBackend.On("IsReadOnly").Return(false)
	mockBackend.On("PutFile", "/home/testuser/large.txt", mock.Anything, int64(-1)).Return(nil)

	size, err := storage.PutFile("large.txt", reader, int64(0))
	assert.NoError(t, err)
	assert.Equal(t, int64(len(largeContent)), size)

	mockBackend.AssertExpectations(t)
}
