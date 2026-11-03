package storage

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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

// Regression-style coverage for the storage wrapper: permission checks must
// refuse before the backend is touched, and Stat must report the right mode.
func TestFilesystemStorage_PermissionsAndFileInfo(t *testing.T) {
	noPerms := &ftpv1.User{Spec: ftpv1.UserSpec{Username: "nobody", HomeDirectory: "/home/nobody"}}
	newStorage := func(user *ftpv1.User, b *MockFilesystemBackend) *filesystemStorage {
		return &filesystemStorage{user: user, backend: b, basePath: user.Spec.HomeDirectory, currentDir: user.Spec.HomeDirectory}
	}

	t.Run("denied operations never reach the backend", func(t *testing.T) {
		b := &MockFilesystemBackend{} // no expectations: any call fails the test
		s := newStorage(noPerms, b)

		assert.ErrorContains(t, s.ListDir("d", func(os.FileInfo) error { return nil }), "list permission denied")
		_, _, err := s.GetFile("f", 0)
		assert.ErrorContains(t, err, "read permission denied")
		_, err = s.PutFile("f", strings.NewReader("x"), 0)
		assert.ErrorContains(t, err, "write permission denied")
		assert.ErrorContains(t, s.MakeDir("d"), "write permission denied")
		assert.ErrorContains(t, s.Rename("a", "b"), "write permission denied")
		assert.ErrorContains(t, s.DeleteFile("f"), "delete permission denied")
		assert.ErrorContains(t, s.DeleteDir("d"), "delete permission denied")
		b.AssertNotCalled(t, "ListFiles", mock.Anything, mock.Anything)
		b.AssertNotCalled(t, "GetFile", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("stat reports directory and file modes", func(t *testing.T) {
		b := &MockFilesystemBackend{}
		s := newStorage(createTestUser(), b)

		b.On("StatFile", "/home/testuser/dir").Return(&backends.FileInfo{Name: "dir", IsDir: true}, nil).Once()
		info, err := s.Stat("dir")
		require.NoError(t, err)
		assert.True(t, info.IsDir())
		assert.Equal(t, fs.ModeDir|0o755, info.Mode())
		assert.Equal(t, "dir", info.Name())

		b.On("StatFile", "/home/testuser/f.txt").Return(&backends.FileInfo{Name: "f.txt", Size: 9, ModTime: time.Unix(1, 0)}, nil).Once()
		info, err = s.Stat("f.txt")
		require.NoError(t, err)
		assert.False(t, info.IsDir())
		assert.Equal(t, fs.FileMode(0o644), info.Mode())
		assert.Equal(t, int64(9), info.Size())
		assert.Equal(t, time.Unix(1, 0), info.ModTime())
		b.AssertExpectations(t)
	})

	t.Run("stat failure is reported as not found", func(t *testing.T) {
		b := &MockFilesystemBackend{}
		s := newStorage(createTestUser(), b)
		b.On("StatFile", "/home/testuser/missing").Return(nil, errors.New("gone")).Once()
		_, err := s.Stat("missing")
		assert.ErrorContains(t, err, "file not found: missing")
	})

	t.Run("listing passes entries to the callback and stops on its error", func(t *testing.T) {
		b := &MockFilesystemBackend{}
		s := newStorage(createTestUser(), b)
		b.On("ListFiles", "/home/testuser/d", false).
			Return([]backends.FileInfo{{Name: "one"}, {Name: "two"}}, nil).Once()

		stop := errors.New("stop")
		calls := 0
		err := s.ListDir("d", func(os.FileInfo) error {
			calls++
			return stop
		})
		assert.ErrorIs(t, err, stop)
		assert.Equal(t, 1, calls)
	})

	t.Run("file info accessors and close", func(t *testing.T) {
		now := time.Unix(42, 0)
		fi := &filesystemFileInfo{name: "n", size: 7, mode: 0o600, modTime: now, isDir: true}
		assert.Equal(t, "n", fi.Name())
		assert.Equal(t, int64(7), fi.Size())
		assert.Equal(t, fs.FileMode(0o600), fi.Mode())
		assert.Equal(t, now, fi.ModTime())
		assert.True(t, fi.IsDir())
		assert.Empty(t, fi.Owner())
		assert.Empty(t, fi.Group())
		assert.Nil(t, fi.Sys())
		assert.NoError(t, newStorage(createTestUser(), &MockFilesystemBackend{}).Close())
	})
}

// Error paths of the filesystem storage: each backend failure is reported with
// the storage-level context, and directory-only rules are enforced.
func TestFilesystemStorage_BackendFailures(t *testing.T) {
	boom := errors.New("disk error")
	user := createTestUser()
	newStorage := func(b *MockFilesystemBackend) *filesystemStorage {
		return &filesystemStorage{user: user, backend: b, basePath: "/home/testuser", currentDir: "/home/testuser"}
	}

	t.Run("cd to a missing directory", func(t *testing.T) {
		b := &MockFilesystemBackend{}
		b.On("StatFile", "/home/testuser/nope").Return(nil, boom).Once()
		assert.ErrorContains(t, newStorage(b).ChangeDir("nope"), "directory not found: nope")
	})

	t.Run("cd to a regular file", func(t *testing.T) {
		b := &MockFilesystemBackend{}
		b.On("StatFile", "/home/testuser/f.txt").Return(&backends.FileInfo{Name: "f.txt"}, nil).Once()
		assert.ErrorContains(t, newStorage(b).ChangeDir("f.txt"), "not a directory: f.txt")
	})

	t.Run("download of a directory is refused", func(t *testing.T) {
		b := &MockFilesystemBackend{}
		b.On("StatFile", "/home/testuser/d").Return(&backends.FileInfo{IsDir: true}, nil).Once()
		_, _, err := newStorage(b).GetFile("d", 0)
		assert.ErrorContains(t, err, "cannot download directory: d")
	})

	t.Run("download backend failure is wrapped", func(t *testing.T) {
		b := &MockFilesystemBackend{}
		b.On("StatFile", "/home/testuser/f").Return(&backends.FileInfo{Size: 4}, nil).Once()
		b.On("GetFile", "/home/testuser/f", int64(0), int64(4)).
			Return(io.NopCloser(strings.NewReader("")), boom).Once()
		_, _, err := newStorage(b).GetFile("f", 0)
		assert.ErrorContains(t, err, "failed to get file")
	})

	t.Run("download from an offset past the end reads nothing", func(t *testing.T) {
		b := &MockFilesystemBackend{}
		b.On("StatFile", "/home/testuser/f").Return(&backends.FileInfo{Size: 4}, nil).Once()
		b.On("GetFile", "/home/testuser/f", int64(9), int64(0)).
			Return(io.NopCloser(strings.NewReader("")), nil).Once()
		size, rc, err := newStorage(b).GetFile("f", 9)
		require.NoError(t, err)
		assert.Equal(t, int64(4), size, "size still reports the whole file")
		_ = rc.Close()
	})

	t.Run("listing backend failure is wrapped", func(t *testing.T) {
		b := &MockFilesystemBackend{}
		b.On("ListFiles", "/home/testuser/d", false).Return([]backends.FileInfo(nil), boom).Once()
		err := newStorage(b).ListDir("d", func(os.FileInfo) error { return nil })
		assert.ErrorContains(t, err, "failed to list directory")
	})

	t.Run("mutation backend failures are returned", func(t *testing.T) {
		b := &MockFilesystemBackend{}
		b.On("IsReadOnly").Return(false)
		b.On("RemoveFile", "/home/testuser/f").Return(boom).Once()
		b.On("RemoveDir", "/home/testuser/d", true).Return(boom).Once()
		b.On("CopyFile", "/home/testuser/a", "/home/testuser/b", true).Return(boom).Once()
		b.On("MakeDir", "/home/testuser/m").Return(boom).Once()
		s := newStorage(b)
		assert.ErrorIs(t, s.DeleteFile("f"), boom)
		assert.ErrorIs(t, s.DeleteDir("d"), boom)
		assert.ErrorIs(t, s.Rename("a", "b"), boom)
		assert.ErrorIs(t, s.MakeDir("m"), boom)
		b.AssertExpectations(t)
	})
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
