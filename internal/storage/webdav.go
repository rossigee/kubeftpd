package storage

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"time"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
	"github.com/rossigee/kubeftpd/internal/backends"
)

// webdavStorage implements Storage interface using WebDAV backend
type webdavStorage struct {
	user       *ftpv1.User
	backend    backends.WebDavBackend
	basePath   string
	currentDir string
}

// ChangeDir changes the current working directory
func (s *webdavStorage) ChangeDir(dir string) error {
	// Normalize the path
	newPath := s.resolvePath(dir)

	// Check if the directory exists
	exists, err := s.backend.Exists(newPath)
	if err != nil {
		return fmt.Errorf("failed to check directory: %w", err)
	}
	if !exists {
		return fmt.Errorf("directory not found: %s", dir)
	}

	s.currentDir = newPath
	return nil
}

// Stat returns file information for the given path
func (s *webdavStorage) Stat(filePath string) (os.FileInfo, error) {
	fullPath := s.resolvePath(filePath)

	// Get file/directory info from WebDAV
	info, err := s.backend.Stat(fullPath)
	if err != nil {
		return nil, fmt.Errorf("file not found: %s", filePath)
	}

	return &webdavFileInfo{
		name:    path.Base(filePath),
		size:    info.Size,
		mode:    info.Mode,
		modTime: info.ModTime,
		isDir:   info.IsDir,
	}, nil
}

// ListDir lists directory contents
func (s *webdavStorage) ListDir(dirPath string, callback func(os.FileInfo) error) error {
	fullPath := s.resolvePath(dirPath)

	entries, err := s.backend.ReadDir(fullPath)
	if err != nil {
		return fmt.Errorf("failed to list directory: %w", err)
	}

	for _, entry := range entries {
		fileInfo := &webdavFileInfo{
			name:    entry.Name,
			size:    entry.Size,
			mode:    entry.Mode,
			modTime: entry.ModTime,
			isDir:   entry.IsDir,
		}
		if err := callback(fileInfo); err != nil {
			return err
		}
	}

	return nil
}

// DeleteDir deletes a directory
func (s *webdavStorage) DeleteDir(dirPath string) error {
	if !s.user.Spec.Permissions.Delete {
		return fmt.Errorf("delete permission denied")
	}

	fullPath := s.resolvePath(dirPath)
	return s.backend.RemoveAll(fullPath)
}

// DeleteFile deletes a file
func (s *webdavStorage) DeleteFile(filePath string) error {
	if !s.user.Spec.Permissions.Delete {
		return fmt.Errorf("delete permission denied")
	}

	fullPath := s.resolvePath(filePath)
	return s.backend.Remove(fullPath)
}

// Rename renames/moves a file or directory
func (s *webdavStorage) Rename(fromPath, toPath string) error {
	if !s.user.Spec.Permissions.Write {
		return fmt.Errorf("write permission denied")
	}

	fullFromPath := s.resolvePath(fromPath)
	fullToPath := s.resolvePath(toPath)

	return s.backend.Rename(fullFromPath, fullToPath)
}

// MakeDir creates a directory
func (s *webdavStorage) MakeDir(dirPath string) error {
	if !s.user.Spec.Permissions.Write {
		return fmt.Errorf("write permission denied")
	}

	fullPath := s.resolvePath(dirPath)
	return s.backend.Mkdir(fullPath)
}

// GetFile downloads a file
func (s *webdavStorage) GetFile(filePath string, offset int64) (int64, io.ReadCloser, error) {
	if !s.user.Spec.Permissions.Read {
		return 0, nil, fmt.Errorf("read permission denied")
	}

	fullPath := s.resolvePath(filePath)

	// Get file info for size
	info, err := s.backend.Stat(fullPath)
	if err != nil {
		return 0, nil, fmt.Errorf("file not found: %s", filePath)
	}

	// Open file for reading
	reader, err := s.backend.Open(fullPath)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to open file: %w", err)
	}

	return info.Size, reader, nil
}

// PutFile uploads a file
func (s *webdavStorage) PutFile(filePath string, reader io.Reader, offset int64) (int64, error) {
	if !s.user.Spec.Permissions.Write {
		return 0, fmt.Errorf("write permission denied")
	}

	fullPath := s.resolvePath(filePath)

	// For simplicity, we don't support offset mode for now
	if offset != 0 {
		return 0, fmt.Errorf("offset mode not supported")
	}

	// Create/write file
	size, err := s.backend.WriteFile(fullPath, reader)
	if err != nil {
		return 0, fmt.Errorf("failed to write file: %w", err)
	}

	return size, nil
}

// resolvePath resolves a relative path to an absolute path within the user's home directory
func (s *webdavStorage) resolvePath(relativePath string) string {
	if relativePath == "" || relativePath == "." {
		return s.currentDir
	}

	if path.IsAbs(relativePath) {
		// Absolute path relative to home directory
		return path.Join(s.basePath, relativePath)
	}

	// Relative path from current directory
	return path.Join(s.currentDir, relativePath)
}

// webdavFileInfo implements server.FileInfo interface
type webdavFileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
	isDir   bool
}

func (fi *webdavFileInfo) Name() string       { return fi.name }
func (fi *webdavFileInfo) Size() int64        { return fi.size }
func (fi *webdavFileInfo) Mode() fs.FileMode  { return fi.mode }
func (fi *webdavFileInfo) ModTime() time.Time { return fi.modTime }
func (fi *webdavFileInfo) IsDir() bool        { return fi.isDir }
func (fi *webdavFileInfo) Owner() string      { return "" }
func (fi *webdavFileInfo) Group() string      { return "" }
func (fi *webdavFileInfo) Sys() interface{}   { return nil }
