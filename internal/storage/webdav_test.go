package storage

import (
	"io"
	"testing"

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
