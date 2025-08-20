package storage

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync/atomic"
	"time"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
	"github.com/rossigee/kubeftpd/internal/backends"
	"github.com/rossigee/kubeftpd/internal/metrics"
)

// minioStorage implements Storage interface using MinIO backend
type minioStorage struct {
	user        *ftpv1.User
	backend     backends.MinioBackend
	basePath    string
	currentDir  string
	backendName string
}

// ChangeDir changes the current working directory
func (s *minioStorage) ChangeDir(dir string) error {
	// Normalize the path
	newPath := s.resolvePath(dir)

	// Check if the directory exists by trying to list it
	_, err := s.backend.ListObjects(newPath, false)
	if err != nil {
		return fmt.Errorf("directory not found: %s", dir)
	}

	s.currentDir = newPath
	return nil
}

// Stat returns file information for the given path
func (s *minioStorage) Stat(filePath string) (os.FileInfo, error) {
	start := time.Now()
	fullPath := s.resolvePath(filePath)

	// Try to get object info
	objInfo, err := s.backend.StatObject(fullPath)
	if err != nil {
		// Maybe it's a directory, try listing it
		objects, err := s.backend.ListObjects(fullPath, false)
		duration := time.Since(start)

		if err != nil || len(objects) == 0 {
			metrics.RecordBackendOperation(s.backendName, "MinioBackend", "stat", "error", duration)
			return nil, fmt.Errorf("file not found: %s", filePath)
		}

		metrics.RecordBackendOperation(s.backendName, "MinioBackend", "stat", "success", duration)
		// Return directory info
		return &minioFileInfo{
			name:    path.Base(filePath),
			size:    0,
			mode:    fs.ModeDir | 0755,
			modTime: time.Now(),
			isDir:   true,
		}, nil
	}

	duration := time.Since(start)
	metrics.RecordBackendOperation(s.backendName, "MinioBackend", "stat", "success", duration)

	return &minioFileInfo{
		name:    path.Base(filePath),
		size:    objInfo.Size,
		mode:    0644,
		modTime: objInfo.LastModified,
		isDir:   false,
	}, nil
}

// ListDir lists directory contents
func (s *minioStorage) ListDir(dirPath string, callback func(os.FileInfo) error) error {
	fullPath := s.resolvePath(dirPath)

	objects, err := s.backend.ListObjects(fullPath, false)
	if err != nil {
		return fmt.Errorf("failed to list directory: %w", err)
	}

	// Track directories we've seen to avoid duplicates
	seenDirs := make(map[string]bool)

	for _, obj := range objects {
		// Get relative path from the listing prefix
		relativePath := strings.TrimPrefix(obj.Key, fullPath)
		if relativePath == "" {
			continue
		}

		// Remove leading slash
		relativePath = strings.TrimPrefix(relativePath, "/")

		// Check if this is a subdirectory
		parts := strings.Split(relativePath, "/")
		if len(parts) > 1 {
			// This is a file in a subdirectory, add the directory entry
			dirName := parts[0]
			if !seenDirs[dirName] {
				seenDirs[dirName] = true
				dirInfo := &minioFileInfo{
					name:    dirName,
					size:    0,
					mode:    fs.ModeDir | 0755,
					modTime: time.Now(),
					isDir:   true,
				}
				if err := callback(dirInfo); err != nil {
					return err
				}
			}
		} else {
			// This is a file in the current directory
			fileInfo := &minioFileInfo{
				name:    parts[0],
				size:    obj.Size,
				mode:    0644,
				modTime: obj.LastModified,
				isDir:   false,
			}
			if err := callback(fileInfo); err != nil {
				return err
			}
		}
	}

	return nil
}

// DeleteDir deletes a directory
func (s *minioStorage) DeleteDir(dirPath string) error {
	if !s.user.Spec.Permissions.Delete {
		return fmt.Errorf("delete permission denied")
	}

	fullPath := s.resolvePath(dirPath)
	return s.backend.RemoveObjects(fullPath, true) // recursive delete
}

// DeleteFile deletes a file
func (s *minioStorage) DeleteFile(filePath string) error {
	if !s.user.Spec.Permissions.Delete {
		return fmt.Errorf("delete permission denied")
	}

	fullPath := s.resolvePath(filePath)
	return s.backend.RemoveObject(fullPath)
}

// Rename renames/moves a file or directory
func (s *minioStorage) Rename(fromPath, toPath string) error {
	if !s.user.Spec.Permissions.Write {
		return fmt.Errorf("write permission denied")
	}

	fullFromPath := s.resolvePath(fromPath)
	fullToPath := s.resolvePath(toPath)

	// MinIO doesn't have native rename, so we copy and delete
	return s.backend.CopyObject(fullFromPath, fullToPath, true) // deleteSource = true
}

// MakeDir creates a directory
func (s *minioStorage) MakeDir(dirPath string) error {
	if !s.user.Spec.Permissions.Write {
		return fmt.Errorf("write permission denied")
	}

	fullPath := s.resolvePath(dirPath)
	// Create an empty object with trailing slash to represent directory
	return s.backend.PutObject(fullPath+"/", strings.NewReader(""), 0)
}

// GetFile downloads a file
func (s *minioStorage) GetFile(filePath string, offset int64) (int64, io.ReadCloser, error) {
	if !s.user.Spec.Permissions.Read {
		return 0, nil, fmt.Errorf("read permission denied")
	}

	fullPath := s.resolvePath(filePath)

	// Get object info for size
	objInfo, err := s.backend.StatObject(fullPath)
	if err != nil {
		return 0, nil, fmt.Errorf("file not found: %s", filePath)
	}

	// Get object data
	reader, err := s.backend.GetObject(fullPath, offset, objInfo.Size-offset)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to get file: %w", err)
	}

	return objInfo.Size, reader, nil
}

// PutFile uploads a file using streaming
func (s *minioStorage) PutFile(filePath string, reader io.Reader, offset int64) (int64, error) {
	if !s.user.Spec.Permissions.Write {
		return 0, fmt.Errorf("write permission denied")
	}

	fullPath := s.resolvePath(filePath)

	// For simplicity, we don't support offset mode for now
	if offset != 0 {
		return 0, fmt.Errorf("offset mode not supported")
	}

	// Create a counting reader to track bytes uploaded
	countingReader := &countingReader{reader: reader}

	// Upload directly to MinIO with unknown size (-1 for streaming)
	// MinIO will handle the upload efficiently without buffering entire file
	err := s.backend.PutObject(fullPath, countingReader, -1)
	if err != nil {
		return 0, fmt.Errorf("failed to put file: %w", err)
	}

	return atomic.LoadInt64(&countingReader.bytesRead), nil
}

// resolvePath resolves a relative path to an absolute path within the user's home directory
func (s *minioStorage) resolvePath(relativePath string) string {
	if relativePath == "" || relativePath == "." {
		return s.currentDir
	}

	if strings.HasPrefix(relativePath, "/") {
		// Absolute path relative to home directory
		return path.Join(s.basePath, relativePath)
	}

	// Relative path from current directory
	return path.Join(s.currentDir, relativePath)
}

// minioFileInfo implements server.FileInfo interface
type minioFileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
	isDir   bool
}

func (fi *minioFileInfo) Name() string       { return fi.name }
func (fi *minioFileInfo) Size() int64        { return fi.size }
func (fi *minioFileInfo) Mode() fs.FileMode  { return fi.mode }
func (fi *minioFileInfo) ModTime() time.Time { return fi.modTime }
func (fi *minioFileInfo) IsDir() bool        { return fi.isDir }
func (fi *minioFileInfo) Owner() string      { return "" }
func (fi *minioFileInfo) Group() string      { return "" }
func (fi *minioFileInfo) Sys() interface{}   { return nil }
