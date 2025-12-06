package storage

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
	"github.com/rossigee/kubeftpd/internal/backends"
)

// filesystemStorage implements Storage interface using filesystem backend
type filesystemStorage struct {
	user       *ftpv1.User
	backend    backends.FilesystemBackend
	basePath   string
	currentDir string
}

// ChangeDir changes the current working directory
func (s *filesystemStorage) ChangeDir(dir string) error {
	// Resolve the new path
	newPath := s.resolvePath(dir)

	// Check if the directory exists by trying to stat it
	fileInfo, err := s.backend.StatFile(newPath)
	if err != nil {
		return fmt.Errorf("directory not found: %s", dir)
	}

	if !fileInfo.IsDir {
		return fmt.Errorf("not a directory: %s", dir)
	}

	s.currentDir = newPath
	return nil
}

// Stat returns file information for the given path
func (s *filesystemStorage) Stat(filePath string) (os.FileInfo, error) {
	fullPath := s.resolvePath(filePath)

	fileInfo, err := s.backend.StatFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("file not found: %s", filePath)
	}

	return &filesystemFileInfo{
		name:    path.Base(filePath),
		size:    fileInfo.Size,
		mode:    s.getModeFromInfo(fileInfo),
		modTime: fileInfo.ModTime,
		isDir:   fileInfo.IsDir,
	}, nil
}

// ListDir lists directory contents
func (s *filesystemStorage) ListDir(dirPath string, callback func(os.FileInfo) error) error {
	if !s.user.Spec.Permissions.List {
		return fmt.Errorf("list permission denied")
	}

	fullPath := s.resolvePath(dirPath)

	files, err := s.backend.ListFiles(fullPath, false)
	if err != nil {
		return fmt.Errorf("failed to list directory: %w", err)
	}

	for _, file := range files {
		fileInfo := &filesystemFileInfo{
			name:    file.Name,
			size:    file.Size,
			mode:    s.getModeFromInfo(&file),
			modTime: file.ModTime,
			isDir:   file.IsDir,
		}

		if err := callback(fileInfo); err != nil {
			return err
		}
	}

	return nil
}

// DeleteDir deletes a directory
func (s *filesystemStorage) DeleteDir(dirPath string) error {
	if !s.user.Spec.Permissions.Delete {
		return fmt.Errorf("delete permission denied")
	}

	if s.backend.IsReadOnly() {
		return fmt.Errorf("backend is read-only")
	}

	fullPath := s.resolvePath(dirPath)
	return s.backend.RemoveDir(fullPath, true) // recursive delete
}

// DeleteFile deletes a file
func (s *filesystemStorage) DeleteFile(filePath string) error {
	if !s.user.Spec.Permissions.Delete {
		return fmt.Errorf("delete permission denied")
	}

	if s.backend.IsReadOnly() {
		return fmt.Errorf("backend is read-only")
	}

	fullPath := s.resolvePath(filePath)
	return s.backend.RemoveFile(fullPath)
}

// Rename renames/moves a file or directory
func (s *filesystemStorage) Rename(fromPath, toPath string) error {
	if !s.user.Spec.Permissions.Write {
		return fmt.Errorf("write permission denied")
	}

	if s.backend.IsReadOnly() {
		return fmt.Errorf("backend is read-only")
	}

	fullFromPath := s.resolvePath(fromPath)
	fullToPath := s.resolvePath(toPath)

	// Use copy and delete for rename/move operation
	return s.backend.CopyFile(fullFromPath, fullToPath, true) // deleteSource = true
}

// MakeDir creates a directory
func (s *filesystemStorage) MakeDir(dirPath string) error {
	if !s.user.Spec.Permissions.Write {
		return fmt.Errorf("write permission denied")
	}

	if s.backend.IsReadOnly() {
		return fmt.Errorf("backend is read-only")
	}

	fullPath := s.resolvePath(dirPath)
	return s.backend.MakeDir(fullPath)
}

// GetFile downloads a file
func (s *filesystemStorage) GetFile(filePath string, offset int64) (int64, io.ReadCloser, error) {
	if !s.user.Spec.Permissions.Read {
		return 0, nil, fmt.Errorf("read permission denied")
	}

	fullPath := s.resolvePath(filePath)

	// Get file info for size
	fileInfo, err := s.backend.StatFile(fullPath)
	if err != nil {
		return 0, nil, fmt.Errorf("file not found: %s", filePath)
	}

	if fileInfo.IsDir {
		return 0, nil, fmt.Errorf("cannot download directory: %s", filePath)
	}

	// Calculate length for range request
	length := fileInfo.Size - offset
	if length < 0 {
		length = 0
	}

	// Get file data
	reader, err := s.backend.GetFile(fullPath, offset, length)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to get file: %w", err)
	}

	return fileInfo.Size, reader, nil
}

// PutFile uploads a file using streaming
func (s *filesystemStorage) PutFile(filePath string, reader io.Reader, offset int64) (int64, error) {
	if !s.user.Spec.Permissions.Write {
		return 0, fmt.Errorf("write permission denied")
	}

	if s.backend.IsReadOnly() {
		return 0, fmt.Errorf("backend is read-only")
	}

	fullPath := s.resolvePath(filePath)

	// For simplicity, we don't support offset mode for now
	if offset != 0 {
		return 0, fmt.Errorf("offset mode not supported")
	}

	// Create a counting reader to track bytes uploaded
	countingReader := &countingReader{reader: reader}

	// Upload to filesystem with unknown size (-1 for streaming)
	err := s.backend.PutFile(fullPath, countingReader, -1)
	if err != nil {
		return 0, fmt.Errorf("failed to put file: %w", err)
	}

	return atomic.LoadInt64(&countingReader.bytesRead), nil
}

// resolvePath resolves a relative path to an absolute path within the user's home directory
func (s *filesystemStorage) resolvePath(relativePath string) string {
	if relativePath == "" || relativePath == "." {
		return s.currentDir
	}

	var fullPath string
	if strings.HasPrefix(relativePath, "/") {
		// Absolute path relative to home directory
		fullPath = filepath.Join(s.basePath, relativePath)
	} else {
		// Relative path from current directory
		fullPath = filepath.Join(s.currentDir, relativePath)
	}

	// Security check: ensure the resolved path is within base path
	absBasePath, err := filepath.Abs(s.basePath)
	if err != nil {
		return s.basePath
	}

	absFullPath, err := filepath.Abs(fullPath)
	if err != nil {
		return s.basePath
	}

	// Check if the resolved path is within the base path
	relPath, err := filepath.Rel(absBasePath, absFullPath)
	if err != nil || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		// Path tries to escape base path, clamp it to base path
		// Extract the file name and put it at base path root
		filename := filepath.Base(relativePath)
		return filepath.Join(s.basePath, filename)
	}

	return fullPath
}

// getModeFromInfo returns appropriate file mode based on file info
func (s *filesystemStorage) getModeFromInfo(fileInfo *backends.FileInfo) fs.FileMode {
	if fileInfo.IsDir {
		return fs.ModeDir | 0755
	}
	return 0644
}

// filesystemFileInfo implements server.FileInfo interface
type filesystemFileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
	isDir   bool
}

func (fi *filesystemFileInfo) Name() string       { return fi.name }
func (fi *filesystemFileInfo) Size() int64        { return fi.size }
func (fi *filesystemFileInfo) Mode() fs.FileMode  { return fi.mode }
func (fi *filesystemFileInfo) ModTime() time.Time { return fi.modTime }
func (fi *filesystemFileInfo) IsDir() bool        { return fi.isDir }
func (fi *filesystemFileInfo) Owner() string      { return "" }
func (fi *filesystemFileInfo) Group() string      { return "" }
func (fi *filesystemFileInfo) Sys() interface{}   { return nil }

// Close cleans up resources
func (s *filesystemStorage) Close() error {
	// Filesystem backend does not require explicit closing
	return nil
}
