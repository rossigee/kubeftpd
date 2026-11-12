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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
	"github.com/rossigee/kubeftpd/internal/backends"
)

// MockWebDavBackend for testing
type MockWebDavBackend struct {
	mock.Mock
}

func (m *MockWebDavBackend) Stat(filePath string) (*backends.FileInfo, error) {
	args := m.Called(filePath)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*backends.FileInfo), args.Error(1)
}

func (m *MockWebDavBackend) Exists(filePath string) (bool, error) {
	args := m.Called(filePath)
	return args.Bool(0), args.Error(1)
}

func (m *MockWebDavBackend) Open(filePath string) (io.ReadCloser, error) {
	args := m.Called(filePath)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(io.ReadCloser), args.Error(1)
}

func (m *MockWebDavBackend) WriteFile(filePath string, reader io.Reader) (int64, error) {
	args := m.Called(filePath, reader)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockWebDavBackend) Remove(filePath string) error {
	args := m.Called(filePath)
	return args.Error(0)
}

func (m *MockWebDavBackend) RemoveAll(dirPath string) error {
	args := m.Called(dirPath)
	return args.Error(0)
}

func (m *MockWebDavBackend) Rename(oldPath, newPath string) error {
	args := m.Called(oldPath, newPath)
	return args.Error(0)
}

func (m *MockWebDavBackend) Mkdir(dirPath string) error {
	args := m.Called(dirPath)
	return args.Error(0)
}

func (m *MockWebDavBackend) ReadDir(dirPath string) ([]*backends.FileInfo, error) {
	args := m.Called(dirPath)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*backends.FileInfo), args.Error(1)
}

// Close lets the mock stand for a backend that owns resources, which is
// what webdavStorage.Close asks for.
func (m *MockWebDavBackend) Close() error {
	args := m.Called()
	return args.Error(0)
}

func createTestWebDavStorage(user *ftpv1.User, backend backends.WebDavBackend) *webdavStorage {
	return &webdavStorage{
		user:       user,
		backend:    backend,
		basePath:   "/home/" + user.Name,
		currentDir: "/home/" + user.Name,
	}
}

func TestWebdavStorage_ChangeDir(t *testing.T) {
	mockBackend := &MockWebDavBackend{}
	user := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "testuser"},
		Spec: ftpv1.UserSpec{
			Permissions: ftpv1.UserPermissions{
				Read:  true,
				Write: true,
				List:  true,
			},
		},
	}
	storage := createTestWebDavStorage(user, mockBackend)

	mockBackend.On("Exists", "/home/testuser/subdir").Return(true, nil).Once()

	err := storage.ChangeDir("subdir")

	assert.NoError(t, err)
	assert.Equal(t, "/home/testuser/subdir", storage.currentDir)

	mockBackend.AssertExpectations(t)
}

func webdavTestUser(perms ftpv1.UserPermissions) *ftpv1.User {
	return &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "testuser"},
		Spec:       ftpv1.UserSpec{Permissions: perms},
	}
}

// Regression: a resumed download (REST then RETR) must start at the requested
// offset. The WebDAV storage used to ignore the offset and return the whole file.
func TestWebdavStorage_GetFile_HonoursOffset(t *testing.T) {
	mockBackend := &MockWebDavBackend{}
	storage := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{Read: true}), mockBackend)

	mockBackend.On("Stat", "/home/testuser/file.txt").
		Return(&backends.FileInfo{Size: 11}, nil).Once()
	mockBackend.On("Open", "/home/testuser/file.txt").
		Return(io.NopCloser(strings.NewReader("hello world")), nil).Once()

	size, reader, err := storage.GetFile("file.txt", 6)
	assert.NoError(t, err)
	assert.Equal(t, int64(11), size, "size reports the whole file, as the filesystem and MinIO storages do")

	data, err := io.ReadAll(reader)
	assert.NoError(t, err)
	assert.Equal(t, "world", string(data))
	assert.NoError(t, reader.Close())
	mockBackend.AssertExpectations(t)
}

func TestWebdavStorage_GetFile_OffsetBeyondEnd(t *testing.T) {
	mockBackend := &MockWebDavBackend{}
	storage := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{Read: true}), mockBackend)

	mockBackend.On("Stat", "/home/testuser/file.txt").
		Return(&backends.FileInfo{Size: 5}, nil).Once()

	_, _, err := storage.GetFile("file.txt", 10)
	assert.ErrorContains(t, err, "out of range")
	mockBackend.AssertExpectations(t)
}

func TestWebdavStorage_GetFile_FromStartAndErrors(t *testing.T) {
	t.Run("offset zero returns whole file", func(t *testing.T) {
		mockBackend := &MockWebDavBackend{}
		storage := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{Read: true}), mockBackend)
		mockBackend.On("Stat", "/home/testuser/a").Return(&backends.FileInfo{Size: 5}, nil).Once()
		mockBackend.On("Open", "/home/testuser/a").Return(io.NopCloser(strings.NewReader("hello")), nil).Once()

		size, reader, err := storage.GetFile("a", 0)
		assert.NoError(t, err)
		assert.Equal(t, int64(5), size)
		data, _ := io.ReadAll(reader)
		assert.Equal(t, "hello", string(data))
	})

	t.Run("read permission denied", func(t *testing.T) {
		storage := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{}), &MockWebDavBackend{})
		_, _, err := storage.GetFile("a", 0)
		assert.ErrorContains(t, err, "read permission denied")
	})

	t.Run("stat failure reports file not found", func(t *testing.T) {
		mockBackend := &MockWebDavBackend{}
		storage := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{Read: true}), mockBackend)
		mockBackend.On("Stat", "/home/testuser/a").Return(nil, errors.New("404")).Once()

		_, _, err := storage.GetFile("a", 0)
		assert.ErrorContains(t, err, "file not found: a")
	})

	t.Run("open failure is wrapped", func(t *testing.T) {
		mockBackend := &MockWebDavBackend{}
		storage := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{Read: true}), mockBackend)
		mockBackend.On("Stat", "/home/testuser/a").Return(&backends.FileInfo{Size: 5}, nil).Once()
		boom := errors.New("boom")
		mockBackend.On("Open", "/home/testuser/a").Return(nil, boom).Once()

		_, _, err := storage.GetFile("a", 0)
		assert.ErrorContains(t, err, "failed to open file")
		assert.ErrorIs(t, err, boom)
	})
}

func TestWebdavStorage_Stat(t *testing.T) {
	mockBackend := &MockWebDavBackend{}
	storage := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{Read: true}), mockBackend)

	mockBackend.On("Stat", "/home/testuser/dir/file.txt").
		Return(&backends.FileInfo{Size: 42, Mode: 0o644, IsDir: false}, nil).Once()
	info, err := storage.Stat("dir/file.txt")
	assert.NoError(t, err)
	assert.Equal(t, "file.txt", info.Name(), "name is the base of the requested path")
	assert.Equal(t, int64(42), info.Size())
	assert.False(t, info.IsDir())

	mockBackend.On("Stat", "/home/testuser/missing").Return(nil, errors.New("404")).Once()
	_, err = storage.Stat("missing")
	assert.ErrorContains(t, err, "file not found: missing")
	mockBackend.AssertExpectations(t)
}

func TestWebdavStorage_ListDir(t *testing.T) {
	entries := []*backends.FileInfo{
		{Name: "a.txt", Size: 1},
		{Name: "sub", IsDir: true},
	}

	t.Run("calls back for each entry", func(t *testing.T) {
		mockBackend := &MockWebDavBackend{}
		storage := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{List: true}), mockBackend)
		mockBackend.On("ReadDir", "/home/testuser/d").Return(entries, nil).Once()

		var names []string
		err := storage.ListDir("d", func(fi os.FileInfo) error {
			names = append(names, fi.Name())
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{"a.txt", "sub"}, names)
		mockBackend.AssertExpectations(t)
	})

	t.Run("callback error stops the listing", func(t *testing.T) {
		mockBackend := &MockWebDavBackend{}
		storage := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{List: true}), mockBackend)
		mockBackend.On("ReadDir", "/home/testuser").Return(entries, nil).Once()

		calls := 0
		stop := errors.New("stop")
		err := storage.ListDir("", func(os.FileInfo) error {
			calls++
			return stop
		})
		assert.ErrorIs(t, err, stop)
		assert.Equal(t, 1, calls)
	})

	t.Run("list permission denied", func(t *testing.T) {
		storage := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{}), &MockWebDavBackend{})
		err := storage.ListDir("d", func(os.FileInfo) error { return nil })
		assert.ErrorContains(t, err, "list permission denied")
	})

	t.Run("backend failure is wrapped", func(t *testing.T) {
		mockBackend := &MockWebDavBackend{}
		storage := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{List: true}), mockBackend)
		mockBackend.On("ReadDir", "/home/testuser/d").Return(nil, errors.New("down")).Once()

		err := storage.ListDir("d", func(os.FileInfo) error { return nil })
		assert.ErrorContains(t, err, "failed to list directory")
	})
}

func TestWebdavStorage_MutatingOperations(t *testing.T) {
	t.Run("delete file and directory use delete permission", func(t *testing.T) {
		mockBackend := &MockWebDavBackend{}
		storage := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{Delete: true}), mockBackend)
		mockBackend.On("Remove", "/home/testuser/f").Return(nil).Once()
		mockBackend.On("RemoveAll", "/home/testuser/d").Return(nil).Once()

		assert.NoError(t, storage.DeleteFile("f"))
		assert.NoError(t, storage.DeleteDir("d"))
		mockBackend.AssertExpectations(t)
	})

	t.Run("delete denied without permission", func(t *testing.T) {
		storage := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{Write: true}), &MockWebDavBackend{})
		assert.ErrorContains(t, storage.DeleteFile("f"), "delete permission denied")
		assert.ErrorContains(t, storage.DeleteDir("d"), "delete permission denied")
	})

	t.Run("rename and mkdir use write permission and resolve both paths", func(t *testing.T) {
		mockBackend := &MockWebDavBackend{}
		storage := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{Write: true}), mockBackend)
		mockBackend.On("Rename", "/home/testuser/a", "/home/testuser/b").Return(nil).Once()
		mockBackend.On("Mkdir", "/home/testuser/newdir").Return(nil).Once()

		assert.NoError(t, storage.Rename("a", "b"))
		assert.NoError(t, storage.MakeDir("newdir"))
		mockBackend.AssertExpectations(t)
	})

	t.Run("rename and mkdir denied without write permission", func(t *testing.T) {
		storage := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{Read: true}), &MockWebDavBackend{})
		assert.ErrorContains(t, storage.Rename("a", "b"), "write permission denied")
		assert.ErrorContains(t, storage.MakeDir("d"), "write permission denied")
	})
}

func TestWebdavStorage_PutFile(t *testing.T) {
	t.Run("writes and returns size", func(t *testing.T) {
		mockBackend := &MockWebDavBackend{}
		storage := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{Write: true}), mockBackend)
		mockBackend.On("WriteFile", "/home/testuser/up.txt", mock.Anything).Return(int64(4), nil).Once()

		n, err := storage.PutFile("up.txt", strings.NewReader("data"), 0)
		assert.NoError(t, err)
		assert.Equal(t, int64(4), n)
		mockBackend.AssertExpectations(t)
	})

	t.Run("resumed upload is rejected", func(t *testing.T) {
		storage := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{Write: true}), &MockWebDavBackend{})
		_, err := storage.PutFile("up.txt", strings.NewReader("data"), 4)
		assert.ErrorContains(t, err, "offset mode not supported")
	})

	t.Run("write permission denied", func(t *testing.T) {
		storage := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{}), &MockWebDavBackend{})
		_, err := storage.PutFile("up.txt", strings.NewReader("data"), 0)
		assert.ErrorContains(t, err, "write permission denied")
	})

	t.Run("backend failure is wrapped", func(t *testing.T) {
		mockBackend := &MockWebDavBackend{}
		storage := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{Write: true}), mockBackend)
		mockBackend.On("WriteFile", "/home/testuser/up.txt", mock.Anything).Return(int64(0), errors.New("disk full")).Once()

		_, err := storage.PutFile("up.txt", strings.NewReader("data"), 0)
		assert.ErrorContains(t, err, "failed to write file")
	})
}

func TestWebdavStorage_ResolvePath(t *testing.T) {
	storage := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{}), &MockWebDavBackend{})
	storage.currentDir = "/home/testuser/work"

	tests := map[string]string{
		"":          "/home/testuser/work",
		".":         "/home/testuser/work",
		"file.txt":  "/home/testuser/work/file.txt",
		"../x":      "/home/testuser/x",
		"/abs/path": "/home/testuser/abs/path",
	}
	for in, want := range tests {
		assert.Equal(t, want, storage.resolvePath(in), "resolvePath(%q)", in)
	}
}

func TestWebdavFileInfo_Accessors(t *testing.T) {
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	fi := &webdavFileInfo{name: "n", size: 3, isDir: true, mode: 0o750, modTime: when}
	assert.Equal(t, "n", fi.Name())
	assert.Equal(t, int64(3), fi.Size())
	assert.Equal(t, fs.FileMode(0o750), fi.Mode())
	assert.Equal(t, when, fi.ModTime())
	assert.True(t, fi.IsDir())
	assert.Empty(t, fi.Owner())
	assert.Empty(t, fi.Group())
	assert.Nil(t, fi.Sys())

	backend := &MockWebDavBackend{}
	backend.On("Close").Return(nil).Once()
	s := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{}), backend)
	assert.NoError(t, s.Close())
	backend.AssertExpectations(t)
}

// A failing existence check is an error, not a "directory does not exist".
func TestWebdavStorage_ChangeDirReportsCheckFailure(t *testing.T) {
	mockBackend := &MockWebDavBackend{}
	storage := createTestWebDavStorage(webdavTestUser(ftpv1.UserPermissions{}), mockBackend)
	mockBackend.On("Exists", "/home/testuser/x").Return(false, errors.New("connection reset")).Once()

	err := storage.ChangeDir("x")
	assert.ErrorContains(t, err, "failed to check directory")
	assert.Equal(t, "/home/testuser", storage.currentDir, "the working directory is unchanged")
	mockBackend.AssertExpectations(t)
}
