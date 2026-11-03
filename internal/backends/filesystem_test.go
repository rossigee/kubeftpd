package backends

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

func createTestDir(t *testing.T) string {
	tmpDir, err := os.MkdirTemp("", "kubeftpd-filesystem-test-*")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.RemoveAll(tmpDir)
	})
	return tmpDir
}

func createTestBackend(t *testing.T, basePath string, readOnly bool) FilesystemBackend {
	kubeClient := fake.NewClientBuilder().Build()
	backendCR := &ftpv1.FilesystemBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-backend",
			Namespace: "default",
		},
		Spec: ftpv1.FilesystemBackendSpec{
			BasePath:    basePath,
			ReadOnly:    readOnly,
			FileMode:    "0644",
			DirMode:     "0755",
			MaxFileSize: 1024 * 1024,
		},
	}

	backend, err := NewFilesystemBackend(backendCR, kubeClient)
	require.NoError(t, err)
	return backend
}

func TestNewFilesystemBackend(t *testing.T) {
	testDir := createTestDir(t)
	kubeClient := fake.NewClientBuilder().Build()

	tests := []struct {
		name          string
		backend       *ftpv1.FilesystemBackend
		shouldSucceed bool
	}{
		{
			name: "valid configuration",
			backend: &ftpv1.FilesystemBackend{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-backend",
					Namespace: "default",
				},
				Spec: ftpv1.FilesystemBackendSpec{
					BasePath:    testDir,
					ReadOnly:    false,
					FileMode:    "0644",
					DirMode:     "0755",
					MaxFileSize: 1024 * 1024,
				},
			},
			shouldSucceed: true,
		},
		{
			name: "read-only configuration",
			backend: &ftpv1.FilesystemBackend{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-backend",
					Namespace: "default",
				},
				Spec: ftpv1.FilesystemBackendSpec{
					BasePath:    testDir,
					ReadOnly:    true,
					FileMode:    "0644",
					DirMode:     "0755",
					MaxFileSize: 0,
				},
			},
			shouldSucceed: true,
		},
		{
			name: "invalid base path",
			backend: &ftpv1.FilesystemBackend{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-backend",
					Namespace: "default",
				},
				Spec: ftpv1.FilesystemBackendSpec{
					BasePath:    "/nonexistent/path",
					ReadOnly:    false,
					FileMode:    "0644",
					DirMode:     "0755",
					MaxFileSize: 1024,
				},
			},
			shouldSucceed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, err := NewFilesystemBackend(tt.backend, kubeClient)

			if tt.shouldSucceed {
				assert.NoError(t, err)
				assert.NotNil(t, backend)
				assert.Equal(t, tt.backend.Spec.BasePath, backend.GetBasePath())
				assert.Equal(t, tt.backend.Spec.ReadOnly, backend.IsReadOnly())
			} else {
				assert.Error(t, err)
				assert.Nil(t, backend)
			}
		})
	}
}

func TestFilesystemBackend_PutFile(t *testing.T) {
	testDir := createTestDir(t)
	backend := createTestBackend(t, testDir, false)

	tests := []struct {
		name          string
		filePath      string
		content       string
		shouldSucceed bool
	}{
		{
			name:          "simple file",
			filePath:      "test.txt",
			content:       "hello world",
			shouldSucceed: true,
		},
		{
			name:          "nested file",
			filePath:      "subdir/nested.txt",
			content:       "nested content",
			shouldSucceed: true,
		},
		{
			name:          "file with special characters",
			filePath:      "special-file_123.log",
			content:       "log content\nwith newlines",
			shouldSucceed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := strings.NewReader(tt.content)
			err := backend.PutFile(tt.filePath, reader, int64(len(tt.content)))

			if tt.shouldSucceed {
				assert.NoError(t, err)

				// Verify file was created
				fullPath := filepath.Join(testDir, tt.filePath)
				assert.FileExists(t, fullPath)

				// Verify content matches exactly
				content, err := os.ReadFile(fullPath)
				assert.NoError(t, err)
				assert.Equal(t, tt.content, string(content))

				// Verify file size matches expected
				stat, err := os.Stat(fullPath)
				assert.NoError(t, err)
				assert.Equal(t, int64(len(tt.content)), stat.Size())
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestFilesystemBackend_PutFile_WriteVerification(t *testing.T) {
	testDir := createTestDir(t)
	backend := createTestBackend(t, testDir, false)

	t.Run("write verification with streaming upload", func(t *testing.T) {
		content := "streaming upload test content"
		reader := strings.NewReader(content)

		// Test streaming upload (size = -1)
		err := backend.PutFile("stream.txt", reader, -1)
		assert.NoError(t, err)

		// Verify file exists and has correct content
		fullPath := filepath.Join(testDir, "stream.txt")
		assert.FileExists(t, fullPath)

		savedContent, err := os.ReadFile(fullPath)
		assert.NoError(t, err)
		assert.Equal(t, content, string(savedContent))

		// Verify file size
		stat, err := os.Stat(fullPath)
		assert.NoError(t, err)
		assert.Equal(t, int64(len(content)), stat.Size())
	})

	t.Run("atomic write operation", func(t *testing.T) {
		content := "atomic write test"
		reader := strings.NewReader(content)

		err := backend.PutFile("atomic.txt", reader, int64(len(content)))
		assert.NoError(t, err)

		// Verify no temporary files left behind
		entries, err := os.ReadDir(testDir)
		assert.NoError(t, err)

		for _, entry := range entries {
			assert.False(t, strings.HasSuffix(entry.Name(), ".tmp"),
				"Temporary file left behind: %s", entry.Name())
		}

		// Verify final file exists and is complete
		fullPath := filepath.Join(testDir, "atomic.txt")
		assert.FileExists(t, fullPath)

		savedContent, err := os.ReadFile(fullPath)
		assert.NoError(t, err)
		assert.Equal(t, content, string(savedContent))
	})
}

func TestFilesystemBackend_PutFile_ReadOnly(t *testing.T) {
	testDir := createTestDir(t)
	backend := createTestBackend(t, testDir, true)

	reader := strings.NewReader("test content")
	err := backend.PutFile("test.txt", reader, 12)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read-only")
}

func TestFilesystemBackend_GetFile(t *testing.T) {
	testDir := createTestDir(t)
	backend := createTestBackend(t, testDir, false)

	// Create test file
	testContent := "test file content for reading"
	testFile := filepath.Join(testDir, "test.txt")
	err := os.WriteFile(testFile, []byte(testContent), 0644)
	require.NoError(t, err)

	// Test reading the file
	reader, err := backend.GetFile("test.txt", 0, -1)
	assert.NoError(t, err)
	assert.NotNil(t, reader)
	defer func() { _ = reader.Close() }()

	content, err := io.ReadAll(reader)
	assert.NoError(t, err)
	assert.Equal(t, testContent, string(content))
}

func TestFilesystemBackend_StatFile(t *testing.T) {
	testDir := createTestDir(t)
	backend := createTestBackend(t, testDir, false)

	// Create test file
	testContent := "test file content"
	testFile := filepath.Join(testDir, "test.txt")
	err := os.WriteFile(testFile, []byte(testContent), 0644)
	require.NoError(t, err)

	// Stat the file
	fileInfo, err := backend.StatFile("test.txt")
	assert.NoError(t, err)
	assert.NotNil(t, fileInfo)
	assert.Equal(t, "test.txt", fileInfo.Name)
	assert.Equal(t, int64(len(testContent)), fileInfo.Size)
	assert.False(t, fileInfo.IsDir)
}

func TestFilesystemBackend_RemoveFile(t *testing.T) {
	testDir := createTestDir(t)
	backend := createTestBackend(t, testDir, false)

	// Create test file
	testFile := filepath.Join(testDir, "test.txt")
	err := os.WriteFile(testFile, []byte("test content"), 0644)
	require.NoError(t, err)
	assert.FileExists(t, testFile)

	// Remove the file
	err = backend.RemoveFile("test.txt")
	assert.NoError(t, err)
	assert.NoFileExists(t, testFile)
}

func TestFilesystemBackend_RemoveFile_ReadOnly(t *testing.T) {
	testDir := createTestDir(t)
	backend := createTestBackend(t, testDir, true)

	err := backend.RemoveFile("test.txt")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read-only")
}

func TestFilesystemBackend_ListFiles(t *testing.T) {
	testDir := createTestDir(t)
	backend := createTestBackend(t, testDir, false)

	// Create test files and directories
	files := []string{"file1.txt", "file2.log", "subdir/nested.txt"}
	for _, file := range files {
		fullPath := filepath.Join(testDir, file)
		err := os.MkdirAll(filepath.Dir(fullPath), 0755)
		require.NoError(t, err)
		err = os.WriteFile(fullPath, []byte("content"), 0644)
		require.NoError(t, err)
	}

	// List files in root directory
	fileInfos, err := backend.ListFiles("", false)
	assert.NoError(t, err)
	assert.True(t, len(fileInfos) >= 3) // file1.txt, file2.log, subdir

	// Check that we have the expected files
	names := make([]string, len(fileInfos))
	for i, info := range fileInfos {
		names[i] = info.Name
	}
	assert.Contains(t, names, "file1.txt")
	assert.Contains(t, names, "file2.log")
	assert.Contains(t, names, "subdir")
}

func TestFilesystemBackend_MakeDir(t *testing.T) {
	testDir := createTestDir(t)
	backend := createTestBackend(t, testDir, false)

	// Create directory
	err := backend.MakeDir("newdir")
	assert.NoError(t, err)

	// Verify directory exists
	dirPath := filepath.Join(testDir, "newdir")
	info, err := os.Stat(dirPath)
	assert.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestFilesystemBackend_MakeDir_ReadOnly(t *testing.T) {
	testDir := createTestDir(t)
	backend := createTestBackend(t, testDir, true)

	err := backend.MakeDir("newdir")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read-only")
}

func TestFilesystemBackend_RemoveDir(t *testing.T) {
	testDir := createTestDir(t)
	backend := createTestBackend(t, testDir, false)

	// Create directory with file
	dirPath := filepath.Join(testDir, "testdir")
	err := os.MkdirAll(dirPath, 0755)
	require.NoError(t, err)

	filePath := filepath.Join(dirPath, "test.txt")
	err = os.WriteFile(filePath, []byte("content"), 0644)
	require.NoError(t, err)

	// Remove directory recursively
	err = backend.RemoveDir("testdir", true)
	assert.NoError(t, err)

	// Verify directory is gone
	_, err = os.Stat(dirPath)
	assert.True(t, os.IsNotExist(err))
}

func TestFilesystemBackend_CopyFile(t *testing.T) {
	testDir := createTestDir(t)
	backend := createTestBackend(t, testDir, false)

	// Create source file
	testContent := "test content for copying"
	srcFile := filepath.Join(testDir, "source.txt")
	err := os.WriteFile(srcFile, []byte(testContent), 0644)
	require.NoError(t, err)

	// Copy file
	err = backend.CopyFile("source.txt", "dest.txt", false)
	assert.NoError(t, err)

	// Verify both files exist
	assert.FileExists(t, srcFile)
	destFile := filepath.Join(testDir, "dest.txt")
	assert.FileExists(t, destFile)

	// Verify content
	destContent, err := os.ReadFile(destFile)
	assert.NoError(t, err)
	assert.Equal(t, testContent, string(destContent))
}

func TestFilesystemBackend_CopyFile_WithDelete(t *testing.T) {
	testDir := createTestDir(t)
	backend := createTestBackend(t, testDir, false)

	// Create source file
	testContent := "test content for moving"
	srcFile := filepath.Join(testDir, "source.txt")
	err := os.WriteFile(srcFile, []byte(testContent), 0644)
	require.NoError(t, err)

	// Move file (copy with delete)
	err = backend.CopyFile("source.txt", "dest.txt", true)
	assert.NoError(t, err)

	// Verify source is gone, dest exists
	assert.NoFileExists(t, srcFile)
	destFile := filepath.Join(testDir, "dest.txt")
	assert.FileExists(t, destFile)

	// Verify content
	destContent, err := os.ReadFile(destFile)
	assert.NoError(t, err)
	assert.Equal(t, testContent, string(destContent))
}

func TestFilesystemBackend_PathSecurity(t *testing.T) {
	testDir := createTestDir(t)
	backend := createTestBackend(t, testDir, false)

	// Test path traversal attempts
	maliciousPaths := []string{
		"../../../etc/passwd",
		"/etc/passwd",
		"subdir/../../../etc/passwd",
	}

	for _, maliciousPath := range maliciousPaths {
		t.Run("path_traversal_"+maliciousPath, func(t *testing.T) {
			reader := strings.NewReader("malicious content")
			err := backend.PutFile(maliciousPath, reader, 16)

			// The file should either fail to be created or be contained within testDir
			if err == nil {
				// If it succeeded, verify the file is within our test directory
				// by checking that no files exist outside the base directory

				// Walk the filesystem to check all created files
				var foundFiles []string
				err := filepath.Walk(testDir, func(path string, info os.FileInfo, err error) error {
					if err != nil {
						return err
					}
					if !info.IsDir() {
						foundFiles = append(foundFiles, path)
					}
					return nil
				})
				assert.NoError(t, err)

				// Verify all files are within the test directory
				for _, filePath := range foundFiles {
					rel, err := filepath.Rel(testDir, filePath)
					assert.NoError(t, err)
					assert.False(t, strings.HasPrefix(rel, ".."), "File escaped test directory: %s -> %s", filePath, rel)
				}

				// Additionally check that no files were created outside testDir
				// by checking if any files contain our test content outside the base dir
				testContent := "malicious content"
				if strings.HasPrefix(maliciousPath, "/") {
					// Absolute path attempt - check if our content was written there
					if data, err := os.ReadFile(maliciousPath); err == nil && string(data) == testContent {
						t.Errorf("Malicious content was written to absolute path %s", maliciousPath)
					}
				} else {
					// Relative path traversal attempt
					attemptPath := filepath.Clean(filepath.Join(testDir, maliciousPath))
					if !strings.HasPrefix(attemptPath, testDir) {
						if data, err := os.ReadFile(attemptPath); err == nil && string(data) == testContent {
							t.Errorf("Malicious content was written to traversal path %s", attemptPath)
						}
					}
				}
			}
		})
	}
}

func TestFilesystemBackendImpl_WriteToTempFile(t *testing.T) {
	backend := createTestBackend(t, createTestDir(t), false).(*filesystemBackendImpl)

	tempFile, err := os.CreateTemp("", "test-temp-*")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tempFile.Name()) }()
	require.NoError(t, tempFile.Close())

	testContent := "Hello, temporary file!"
	reader := strings.NewReader(testContent)

	bytesWritten, err := backend.writeToTempFile(tempFile.Name(), reader)

	assert.NoError(t, err)
	assert.Equal(t, int64(len(testContent)), bytesWritten)

	// Verify content was written
	content, err := os.ReadFile(tempFile.Name())
	assert.NoError(t, err)
	assert.Equal(t, testContent, string(content))
}

func TestFilesystemBackend_ListFilesModes(t *testing.T) {
	base := createTestDir(t)
	require.NoError(t, os.MkdirAll(filepath.Join(base, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(base, "a.txt"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(base, "sub", "b.txt"), []byte("bb"), 0o644))
	b := createTestBackend(t, base, false)

	t.Run("non-recursive lists only the top level", func(t *testing.T) {
		files, err := b.ListFiles("", false)
		require.NoError(t, err)
		names := map[string]bool{}
		for _, f := range files {
			names[f.Name] = f.IsDir
		}
		assert.Equal(t, map[string]bool{"a.txt": false, "sub": true}, names)
	})

	t.Run("recursive includes nested entries", func(t *testing.T) {
		files, err := b.ListFiles("", true)
		require.NoError(t, err)
		names := map[string]int64{}
		for _, f := range files {
			names[f.Name] = f.Size
		}
		assert.Equal(t, int64(2), names["b.txt"])
		assert.Contains(t, names, "a.txt")
		assert.Contains(t, names, "sub")
	})

	t.Run("missing directory is an error", func(t *testing.T) {
		_, err := b.ListFiles("nope", false)
		assert.ErrorContains(t, err, "failed to read directory nope")
	})
}

func TestFilesystemBackend_GetFileRanges(t *testing.T) {
	base := createTestDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(base, "f.txt"), []byte("hello world"), 0o644))
	b := createTestBackend(t, base, false)

	read := func(t *testing.T, rc io.ReadCloser) string {
		t.Helper()
		data, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		return string(data)
	}

	rc, err := b.GetFile("f.txt", 6, 0)
	require.NoError(t, err)
	assert.Equal(t, "world", read(t, rc), "offset without length reads to end")

	rc, err = b.GetFile("f.txt", 0, 5)
	require.NoError(t, err)
	assert.Equal(t, "hello", read(t, rc), "length limits the read")

	rc, err = b.GetFile("f.txt", 6, 2)
	require.NoError(t, err)
	assert.Equal(t, "wo", read(t, rc), "offset and length together")

	_, err = b.GetFile("missing.txt", 0, 0)
	assert.ErrorContains(t, err, "failed to open file missing.txt")
}

func TestFilesystemBackend_PutFileRefusals(t *testing.T) {
	base := createTestDir(t)

	t.Run("read-only backend refuses writes", func(t *testing.T) {
		b := createTestBackend(t, base, true)
		err := b.PutFile("x.txt", strings.NewReader("data"), 4)
		assert.ErrorContains(t, err, "read-only")
		assert.ErrorContains(t, b.MakeDir("d"), "read-only")
		assert.ErrorContains(t, b.RemoveDir("d", false), "read-only")
	})

	t.Run("file over the size limit is refused before writing", func(t *testing.T) {
		b := createTestBackend(t, base, false) // MaxFileSize is 1 MiB in the helper
		err := b.PutFile("big.bin", strings.NewReader("x"), 2*1024*1024)
		assert.ErrorContains(t, err, "exceeds maximum allowed size")
		_, statErr := os.Stat(filepath.Join(base, "big.bin"))
		assert.True(t, os.IsNotExist(statErr))
	})

	t.Run("declared size that differs from the data is rejected", func(t *testing.T) {
		b := createTestBackend(t, base, false)
		err := b.PutFile("short.txt", strings.NewReader("abc"), 10)
		assert.ErrorContains(t, err, "size mismatch")
		_, statErr := os.Stat(filepath.Join(base, "short.txt"))
		assert.True(t, os.IsNotExist(statErr), "no partial file is left behind")
	})
}

func TestFilesystemBackend_PutFileRoundTrip(t *testing.T) {
	base := createTestDir(t)
	b := createTestBackend(t, base, false)

	require.NoError(t, b.PutFile("dir/new.txt", strings.NewReader("payload"), 7))
	info, err := b.StatFile("dir/new.txt")
	require.NoError(t, err)
	assert.Equal(t, int64(7), info.Size)

	rc, err := b.GetFile("dir/new.txt", 0, 0)
	require.NoError(t, err)
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	assert.Equal(t, "payload", string(data))
}

func TestFilesystemBackend_RemoveDirAndMakeDir(t *testing.T) {
	base := createTestDir(t)
	b := createTestBackend(t, base, false)

	require.NoError(t, b.MakeDir("tree/inner"))
	require.NoError(t, os.WriteFile(filepath.Join(base, "tree", "inner", "f"), []byte("x"), 0o644))

	assert.Error(t, b.RemoveDir("tree", false), "non-recursive removal of a non-empty directory fails")
	require.NoError(t, b.RemoveDir("tree", true))
	_, err := os.Stat(filepath.Join(base, "tree"))
	assert.True(t, os.IsNotExist(err))
}

// Regression (path traversal): no requested path may resolve outside the base
// directory. Escapes fall back to the base itself rather than a host path.
func TestFilesystemBackend_GetFullPathStaysWithinBase(t *testing.T) {
	base := createTestDir(t)
	b := createTestBackend(t, base, false).(*filesystemBackendImpl)

	escapes := []string{"../../etc/passwd", "/../../../etc/passwd", "a/../../../../etc", "..", "../"}
	for _, p := range escapes {
		got := b.getFullPath(p)
		rel, err := filepath.Rel(base, got)
		require.NoError(t, err)
		assert.False(t, strings.HasPrefix(rel, ".."), "%q resolved to %q, outside the base", p, got)
	}

	assert.Equal(t, filepath.Join(base, "docs", "a.txt"), b.getFullPath("/docs/a.txt"), "absolute paths are base-relative")
	assert.Equal(t, filepath.Join(base, "x", "x"), b.getFullPath("x/./y/../x"), "clean paths are normalised")
}

func TestNewFilesystemBackend_ValidatesSpec(t *testing.T) {
	base := createTestDir(t)
	kubeClient := fake.NewClientBuilder().Build()
	spec := func(fileMode, dirMode, basePath string) *ftpv1.FilesystemBackend {
		return &ftpv1.FilesystemBackend{
			ObjectMeta: metav1.ObjectMeta{Name: "fs", Namespace: "default"},
			Spec:       ftpv1.FilesystemBackendSpec{BasePath: basePath, FileMode: fileMode, DirMode: dirMode},
		}
	}

	t.Run("empty modes take the defaults", func(t *testing.T) {
		b, err := NewFilesystemBackend(spec("", "", base), kubeClient)
		require.NoError(t, err)
		impl := b.(*filesystemBackendImpl)
		assert.Equal(t, os.FileMode(0o644), impl.fileMode)
		assert.Equal(t, os.FileMode(0o755), impl.dirMode)
	})

	t.Run("octal modes are parsed", func(t *testing.T) {
		b, err := NewFilesystemBackend(spec("0600", "0700", base), kubeClient)
		require.NoError(t, err)
		impl := b.(*filesystemBackendImpl)
		assert.Equal(t, os.FileMode(0o600), impl.fileMode)
		assert.Equal(t, os.FileMode(0o700), impl.dirMode)
	})

	t.Run("invalid file mode is rejected", func(t *testing.T) {
		_, err := NewFilesystemBackend(spec("rwx", "", base), kubeClient)
		assert.ErrorContains(t, err, "invalid file mode")
	})

	t.Run("invalid directory mode is rejected", func(t *testing.T) {
		_, err := NewFilesystemBackend(spec("", "9", base), kubeClient)
		assert.ErrorContains(t, err, "invalid directory mode")
	})

	t.Run("missing base path is rejected", func(t *testing.T) {
		_, err := NewFilesystemBackend(spec("", "", filepath.Join(base, "absent")), kubeClient)
		assert.ErrorContains(t, err, "is not accessible")
	})
}

func TestFilesystemBackend_CopyFileAndFailures(t *testing.T) {
	base := createTestDir(t)
	b := createTestBackend(t, base, false)
	require.NoError(t, os.WriteFile(filepath.Join(base, "src.txt"), []byte("copy me"), 0o644))

	t.Run("copy keeps the source", func(t *testing.T) {
		require.NoError(t, b.CopyFile("src.txt", "dir/dst.txt", false))
		data, err := os.ReadFile(filepath.Join(base, "dir", "dst.txt"))
		require.NoError(t, err)
		assert.Equal(t, "copy me", string(data))
		_, err = os.Stat(filepath.Join(base, "src.txt"))
		assert.NoError(t, err)
	})

	t.Run("move removes the source", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(base, "moving.txt"), []byte("m"), 0o644))
		require.NoError(t, b.CopyFile("moving.txt", "moved.txt", true))
		_, err := os.Stat(filepath.Join(base, "moving.txt"))
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("missing source", func(t *testing.T) {
		assert.ErrorContains(t, b.CopyFile("absent.txt", "x.txt", false), "failed to open source file")
	})

	t.Run("destination that is a directory", func(t *testing.T) {
		require.NoError(t, os.MkdirAll(filepath.Join(base, "adir"), 0o755))
		assert.ErrorContains(t, b.CopyFile("src.txt", "adir", false), "failed to create destination file")
	})

	t.Run("read-only backend refuses copies", func(t *testing.T) {
		ro := createTestBackend(t, base, true)
		assert.ErrorContains(t, ro.CopyFile("src.txt", "other.txt", false), "read-only")
	})

	t.Run("upload below a regular file cannot create its directory", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(base, "blocker"), []byte("file"), 0o644))
		err := b.PutFile("blocker/child.txt", strings.NewReader("x"), 1)
		assert.ErrorContains(t, err, "failed to create directory")
	})

	t.Run("recursive listing of a missing directory fails", func(t *testing.T) {
		_, err := b.ListFiles("no-such-dir", true)
		assert.Error(t, err)
	})

	t.Run("stat of a missing file fails", func(t *testing.T) {
		_, err := b.StatFile("no-such-file")
		assert.Error(t, err)
	})

	t.Run("streaming upload of unknown size is verified by the bytes written", func(t *testing.T) {
		require.NoError(t, b.PutFile("streamed.bin", strings.NewReader("streamed-data"), -1))
		info, err := b.StatFile("streamed.bin")
		require.NoError(t, err)
		assert.Equal(t, int64(len("streamed-data")), info.Size)
	})

	t.Run("empty upload with a zero declared size", func(t *testing.T) {
		require.NoError(t, b.PutFile("empty.txt", strings.NewReader(""), 0))
		info, err := b.StatFile("empty.txt")
		require.NoError(t, err)
		assert.Equal(t, int64(0), info.Size)
	})

	t.Run("stat reports directories", func(t *testing.T) {
		require.NoError(t, b.MakeDir("statdir"))
		info, err := b.StatFile("statdir")
		require.NoError(t, err)
		assert.True(t, info.IsDir)
	})
}
