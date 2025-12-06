package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync/atomic"

	"sigs.k8s.io/controller-runtime/pkg/client"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
	"github.com/rossigee/kubeftpd/internal/backends"
)

// Storage interface defines the operations supported by storage backends
type Storage interface {
	ChangeDir(path string) error
	Stat(path string) (os.FileInfo, error)
	ListDir(path string, callback func(os.FileInfo) error) error
	DeleteDir(path string) error
	DeleteFile(path string) error
	Rename(fromPath, toPath string) error
	MakeDir(path string) error
	GetFile(path string, offset int64) (int64, io.ReadCloser, error)
	PutFile(path string, reader io.Reader, offset int64) (int64, error)
	Close() error
}

// countingReader counts bytes read from the underlying reader
type countingReader struct {
	reader    io.Reader
	bytesRead int64
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.reader.Read(p)
	atomic.AddInt64(&cr.bytesRead, int64(n))
	return n, err
}

// NewStorage creates a new storage implementation based on the user's backend configuration
func NewStorage(user *ftpv1.User, kubeClient client.Client) (Storage, error) {
	switch user.Spec.Backend.Kind {
	case "MinioBackend":
		return newMinioStorage(user, kubeClient)
	case "WebDavBackend":
		return newWebDavStorage(user, kubeClient)
	case "FilesystemBackend":
		return newFilesystemStorage(user, kubeClient)
	default:
		return nil, fmt.Errorf("unsupported backend kind: %s", user.Spec.Backend.Kind)
	}
}

// newMinioStorage creates a MinIO-backed storage implementation
func newMinioStorage(user *ftpv1.User, kubeClient client.Client) (Storage, error) {
	// Get the MinioBackend CRD
	backend := &ftpv1.MinioBackend{}
	backendName := user.Spec.Backend.Name
	backendNamespace := user.Namespace
	if user.Spec.Backend.Namespace != nil {
		backendNamespace = *user.Spec.Backend.Namespace
	}

	err := kubeClient.Get(context.TODO(), client.ObjectKey{
		Name:      backendName,
		Namespace: backendNamespace,
	}, backend)
	if err != nil {
		return nil, fmt.Errorf("failed to get MinioBackend %s/%s: %w", backendNamespace, backendName, err)
	}

	// Create MinIO backend adapter
	minioBackend, err := backends.NewMinioBackend(backend, kubeClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create MinIO backend: %w", err)
	}

	return &minioStorage{
		user:        user,
		backend:     minioBackend,
		basePath:    user.Spec.HomeDirectory,
		currentDir:  user.Spec.HomeDirectory,
		backendName: backendName,
	}, nil
}

// newWebDavStorage creates a WebDAV-backed storage implementation
func newWebDavStorage(user *ftpv1.User, kubeClient client.Client) (Storage, error) {
	// Get the WebDavBackend CRD
	backend := &ftpv1.WebDavBackend{}
	backendName := user.Spec.Backend.Name
	backendNamespace := user.Namespace
	if user.Spec.Backend.Namespace != nil {
		backendNamespace = *user.Spec.Backend.Namespace
	}

	err := kubeClient.Get(context.TODO(), client.ObjectKey{
		Name:      backendName,
		Namespace: backendNamespace,
	}, backend)
	if err != nil {
		return nil, fmt.Errorf("failed to get WebDavBackend %s/%s: %w", backendNamespace, backendName, err)
	}

	// Create WebDAV backend adapter
	webdavBackend, err := backends.NewWebDavBackend(backend, kubeClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create WebDAV backend: %w", err)
	}

	return &webdavStorage{
		user:       user,
		backend:    webdavBackend,
		basePath:   user.Spec.HomeDirectory,
		currentDir: user.Spec.HomeDirectory,
	}, nil
}

// newFilesystemStorage creates a filesystem-backed storage implementation
func newFilesystemStorage(user *ftpv1.User, kubeClient client.Client) (Storage, error) {
	// Get the FilesystemBackend CRD
	backend := &ftpv1.FilesystemBackend{}
	backendName := user.Spec.Backend.Name
	backendNamespace := user.Namespace
	if user.Spec.Backend.Namespace != nil {
		backendNamespace = *user.Spec.Backend.Namespace
	}

	err := kubeClient.Get(context.TODO(), client.ObjectKey{
		Name:      backendName,
		Namespace: backendNamespace,
	}, backend)
	if err != nil {
		return nil, fmt.Errorf("failed to get FilesystemBackend %s/%s: %w", backendNamespace, backendName, err)
	}

	// Create filesystem backend adapter
	filesystemBackend, err := backends.NewFilesystemBackend(backend, kubeClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create filesystem backend: %w", err)
	}

	return &filesystemStorage{
		user:       user,
		backend:    filesystemBackend,
		basePath:   user.Spec.HomeDirectory,
		currentDir: user.Spec.HomeDirectory,
	}, nil
}
