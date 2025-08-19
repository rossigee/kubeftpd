package backends

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

// FilesystemBackend interface for filesystem operations
type FilesystemBackend interface {
	ListFiles(dirPath string, recursive bool) ([]FileInfo, error)
	StatFile(filePath string) (*FileInfo, error)
	GetFile(filePath string, offset, length int64) (io.ReadCloser, error)
	PutFile(filePath string, reader io.Reader, size int64) error
	RemoveFile(filePath string) error
	RemoveDir(dirPath string, recursive bool) error
	MakeDir(dirPath string) error
	CopyFile(srcPath, dstPath string, deleteSource bool) error
	GetBasePath() string
	IsReadOnly() bool
}

// filesystemBackendImpl implements FilesystemBackend using local filesystem
type filesystemBackendImpl struct {
	basePath    string
	readOnly    bool
	fileMode    os.FileMode
	dirMode     os.FileMode
	maxFileSize int64
}

// NewFilesystemBackend creates a new filesystem backend
func NewFilesystemBackend(backend *ftpv1.FilesystemBackend, kubeClient client.Client) (FilesystemBackend, error) {
	// Parse file and directory modes
	fileMode, err := parseFileMode(backend.Spec.FileMode, 0644)
	if err != nil {
		return nil, fmt.Errorf("invalid file mode: %w", err)
	}

	dirMode, err := parseFileMode(backend.Spec.DirMode, 0755)
	if err != nil {
		return nil, fmt.Errorf("invalid directory mode: %w", err)
	}

	// Validate base path exists and is accessible
	basePath := backend.Spec.BasePath
	if _, err := os.Stat(basePath); err != nil {
		return nil, fmt.Errorf("base path %s is not accessible: %w", basePath, err)
	}

	return &filesystemBackendImpl{
		basePath:    basePath,
		readOnly:    backend.Spec.ReadOnly,
		fileMode:    fileMode,
		dirMode:     dirMode,
		maxFileSize: backend.Spec.MaxFileSize,
	}, nil
}

// parseFileMode parses a string file mode (e.g., "0644") to os.FileMode
func parseFileMode(modeStr string, defaultMode os.FileMode) (os.FileMode, error) {
	if modeStr == "" {
		return defaultMode, nil
	}

	mode, err := strconv.ParseUint(modeStr, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("failed to parse mode %s: %w", modeStr, err)
	}

	return os.FileMode(mode), nil
}

// getFullPath returns the full filesystem path for a given relative path
func (f *filesystemBackendImpl) getFullPath(relativePath string) string {
	// Clean the path to remove any ".." and other problematic components
	cleanPath := filepath.Clean(relativePath)

	// If the path starts with "/" treat it as relative to base (not absolute)
	cleanPath = strings.TrimPrefix(cleanPath, "/")

	// Join with base path
	fullPath := filepath.Join(f.basePath, cleanPath)

	// Security check: ensure the resolved path is within base path
	absBasePath, err := filepath.Abs(f.basePath)
	if err != nil {
		return f.basePath
	}

	absFullPath, err := filepath.Abs(fullPath)
	if err != nil {
		return f.basePath
	}

	// Check if the resolved path is within the base path
	relPath, err := filepath.Rel(absBasePath, absFullPath)
	if err != nil || relPath == ".." || strings.HasPrefix(relPath, ".."+string(os.PathSeparator)) {
		return f.basePath
	}

	return fullPath
}

// ListFiles lists files and directories
func (f *filesystemBackendImpl) ListFiles(dirPath string, recursive bool) ([]FileInfo, error) {
	fullPath := f.getFullPath(dirPath)
	var files []FileInfo

	if recursive {
		err := filepath.Walk(fullPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			// Skip the root directory itself
			if path == fullPath {
				return nil
			}

			// Get relative path from base
			relPath, err := filepath.Rel(fullPath, path)
			if err != nil {
				return err
			}

			files = append(files, FileInfo{
				Name:    filepath.Base(relPath),
				Size:    info.Size(),
				ModTime: info.ModTime(),
				IsDir:   info.IsDir(),
			})

			return nil
		})
		return files, err
	}

	// Non-recursive listing
	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %w", dirPath, err)
	}

	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}

		files = append(files, FileInfo{
			Name:    entry.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			IsDir:   entry.IsDir(),
		})
	}

	return files, nil
}

// StatFile gets file/directory information
func (f *filesystemBackendImpl) StatFile(filePath string) (*FileInfo, error) {
	fullPath := f.getFullPath(filePath)

	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file %s: %w", filePath, err)
	}

	return &FileInfo{
		Name:    filepath.Base(filePath),
		Size:    info.Size(),
		ModTime: info.ModTime(),
		IsDir:   info.IsDir(),
	}, nil
}

// GetFile retrieves a file with optional range
func (f *filesystemBackendImpl) GetFile(filePath string, offset, length int64) (io.ReadCloser, error) {
	fullPath := f.getFullPath(filePath)

	file, err := os.Open(fullPath) // nolint:gosec // File path is validated and controlled by backend
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", filePath, err)
	}

	if offset > 0 {
		_, err = file.Seek(offset, io.SeekStart)
		if err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("failed to seek to offset %d: %w", offset, err)
		}
	}

	if length > 0 {
		return &limitedReadCloser{
			reader: io.LimitReader(file, length),
			closer: file,
		}, nil
	}

	return file, nil
}

// PutFile uploads a file
func (f *filesystemBackendImpl) PutFile(filePath string, reader io.Reader, size int64) error {
	if f.readOnly {
		return fmt.Errorf("backend is read-only")
	}

	// Check file size limit
	if f.maxFileSize > 0 && size > f.maxFileSize {
		return fmt.Errorf("file size %d exceeds maximum allowed size %d", size, f.maxFileSize)
	}

	fullPath := f.getFullPath(filePath)

	// Ensure directory exists
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, f.dirMode); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Write to temporary file with verification
	tempPath := fullPath + ".tmp"
	bytesWritten, err := f.writeToTempFile(tempPath, reader)
	if err != nil {
		return err
	}

	// Verify temporary file
	if err = f.verifyTempFile(tempPath, size, bytesWritten); err != nil {
		return err
	}

	// Atomic rename
	if err = os.Rename(tempPath, fullPath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("failed to finalize file %s: %w", filePath, err)
	}

	// Final verification
	return f.verifyFinalFile(fullPath, size, bytesWritten)
}

// writeToTempFile handles the actual file writing with proper error handling
func (f *filesystemBackendImpl) writeToTempFile(tempPath string, reader io.Reader) (int64, error) {
	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.fileMode) // nolint:gosec // File path is validated and controlled by backend
	if err != nil {
		return 0, fmt.Errorf("failed to create temporary file %s: %w", tempPath, err)
	}

	// Copy data and track bytes written
	bytesWritten, copyErr := io.Copy(file, reader)

	// Force flush to disk before closing
	if syncErr := file.Sync(); syncErr != nil {
		_ = file.Close()
		_ = os.Remove(tempPath)
		return 0, fmt.Errorf("failed to flush file data to disk: %w", syncErr)
	}

	// Close file and check for any deferred errors
	if closeErr := file.Close(); closeErr != nil {
		_ = os.Remove(tempPath)
		return 0, fmt.Errorf("failed to close file: %w", closeErr)
	}

	// Check copy operation error after file is properly closed
	if copyErr != nil {
		_ = os.Remove(tempPath)
		return 0, fmt.Errorf("failed to write file data: %w", copyErr)
	}

	return bytesWritten, nil
}

// verifyTempFile validates the temporary file before final rename
func (f *filesystemBackendImpl) verifyTempFile(tempPath string, expectedSize, bytesWritten int64) error {
	tempStat, statErr := os.Stat(tempPath)
	if statErr != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("temporary file verification failed: %w", statErr)
	}

	if expectedSize > 0 && tempStat.Size() != expectedSize {
		_ = os.Remove(tempPath)
		return fmt.Errorf("file size mismatch: expected %d, got %d", expectedSize, tempStat.Size())
	}

	if expectedSize < 0 && tempStat.Size() != bytesWritten {
		// For streaming uploads (size = -1), verify bytes written matches file size
		_ = os.Remove(tempPath)
		return fmt.Errorf("streaming upload size mismatch: wrote %d bytes, file size %d", bytesWritten, tempStat.Size())
	}

	return nil
}

// verifyFinalFile confirms the final file is correctly stored
func (f *filesystemBackendImpl) verifyFinalFile(fullPath string, expectedSize, bytesWritten int64) error {
	finalStat, statErr := os.Stat(fullPath)
	if statErr != nil {
		return fmt.Errorf("final file verification failed: %w", statErr)
	}

	if expectedSize > 0 && finalStat.Size() != expectedSize {
		_ = os.Remove(fullPath)
		return fmt.Errorf("final file size verification failed: expected %d, got %d", expectedSize, finalStat.Size())
	}

	if expectedSize < 0 && finalStat.Size() != bytesWritten {
		_ = os.Remove(fullPath)
		return fmt.Errorf("final streaming file size verification failed: expected %d, got %d", bytesWritten, finalStat.Size())
	}

	return nil
}

// RemoveFile deletes a file
func (f *filesystemBackendImpl) RemoveFile(filePath string) error {
	if f.readOnly {
		return fmt.Errorf("backend is read-only")
	}

	fullPath := f.getFullPath(filePath)
	return os.Remove(fullPath)
}

// RemoveDir deletes a directory
func (f *filesystemBackendImpl) RemoveDir(dirPath string, recursive bool) error {
	if f.readOnly {
		return fmt.Errorf("backend is read-only")
	}

	fullPath := f.getFullPath(dirPath)

	if recursive {
		return os.RemoveAll(fullPath)
	}

	return os.Remove(fullPath)
}

// MakeDir creates a directory
func (f *filesystemBackendImpl) MakeDir(dirPath string) error {
	if f.readOnly {
		return fmt.Errorf("backend is read-only")
	}

	fullPath := f.getFullPath(dirPath)
	return os.MkdirAll(fullPath, f.dirMode)
}

// CopyFile copies a file, optionally deleting the source
func (f *filesystemBackendImpl) CopyFile(srcPath, dstPath string, deleteSource bool) error {
	if f.readOnly {
		return fmt.Errorf("backend is read-only")
	}

	srcFullPath := f.getFullPath(srcPath)
	dstFullPath := f.getFullPath(dstPath)

	// Ensure destination directory exists
	dstDir := filepath.Dir(dstFullPath)
	if err := os.MkdirAll(dstDir, f.dirMode); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Open source file
	srcFile, err := os.Open(srcFullPath) // nolint:gosec // File path is validated and controlled by backend
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer func() { _ = srcFile.Close() }()

	// Create destination file
	dstFile, err := os.OpenFile(dstFullPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.fileMode) // nolint:gosec // File path is validated and controlled by backend
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer func() { _ = dstFile.Close() }()

	// Copy data
	if _, err = io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("failed to copy file data: %w", err)
	}

	// Delete source if requested
	if deleteSource {
		if err = os.Remove(srcFullPath); err != nil {
			return fmt.Errorf("failed to remove source file: %w", err)
		}
	}

	return nil
}

// GetBasePath returns the base path
func (f *filesystemBackendImpl) GetBasePath() string {
	return f.basePath
}

// IsReadOnly returns whether the backend is read-only
func (f *filesystemBackendImpl) IsReadOnly() bool {
	return f.readOnly
}

// limitedReadCloser wraps a LimitReader with a Closer
type limitedReadCloser struct {
	reader io.Reader
	closer io.Closer
}

func (lrc *limitedReadCloser) Read(p []byte) (int, error) {
	return lrc.reader.Read(p)
}

func (lrc *limitedReadCloser) Close() error {
	return lrc.closer.Close()
}
