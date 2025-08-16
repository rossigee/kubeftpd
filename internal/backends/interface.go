package backends

import (
	"io"
	"io/fs"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

// ObjectInfo represents metadata about a storage object
type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
	ETag         string
	ContentType  string
}

// FileInfo represents file/directory information
type FileInfo struct {
	Name    string
	Size    int64
	Mode    fs.FileMode
	ModTime time.Time
	IsDir   bool
}

// MinioBackend interface for MinIO operations
type MinioBackend interface {
	// Object operations
	StatObject(objectName string) (*ObjectInfo, error)
	GetObject(objectName string, offset, length int64) (io.ReadCloser, error)
	PutObject(objectName string, reader io.Reader, size int64) error
	RemoveObject(objectName string) error
	RemoveObjects(prefix string, recursive bool) error
	CopyObject(srcObject, dstObject string, deleteSource bool) error

	// Directory operations
	ListObjects(prefix string, recursive bool) ([]*ObjectInfo, error)
}

// WebDavBackend interface for WebDAV operations
type WebDavBackend interface {
	// File operations
	Stat(path string) (*FileInfo, error)
	Exists(path string) (bool, error)
	Open(path string) (io.ReadCloser, error)
	WriteFile(path string, reader io.Reader) (int64, error)
	Remove(path string) error
	RemoveAll(path string) error
	Rename(oldPath, newPath string) error

	// Directory operations
	Mkdir(path string) error
	ReadDir(path string) ([]*FileInfo, error)
}

// NewMinioBackend creates a new MinIO backend from a MinioBackend CRD
func NewMinioBackend(backend *ftpv1.MinioBackend, kubeClient client.Client) (MinioBackend, error) {
	return newMinioBackendImpl(backend, kubeClient)
}

// NewWebDavBackend creates a new WebDAV backend from a WebDavBackend CRD
func NewWebDavBackend(backend *ftpv1.WebDavBackend, kubeClient client.Client) (WebDavBackend, error) {
	return newWebDavBackendImpl(backend, kubeClient)
}
