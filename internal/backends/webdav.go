package backends

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

// webDavBackendImpl implements WebDavBackend interface using HTTP client
type webDavBackendImpl struct {
	client   *http.Client
	endpoint string
	basePath string
	username string
	password string
}

// newWebDavBackendImpl creates a new WebDAV backend implementation
func newWebDavBackendImpl(backend *ftpv1.WebDavBackend, kubeClient client.Client) (WebDavBackend, error) {
	// Get credentials
	username := backend.Spec.Credentials.Username
	password := backend.Spec.Credentials.Password

	// If useSecret is specified, read from Kubernetes Secret
	if backend.Spec.Credentials.UseSecret != nil {
		var err error
		username, password, err = getWebDavCredentialsFromSecret(backend.Spec.Credentials.UseSecret, kubeClient)
		if err != nil {
			return nil, fmt.Errorf("failed to get credentials from secret: %w", err)
		}
	}

	// Configure HTTP client
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Configure TLS if specified
	if backend.Spec.TLS != nil {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: backend.Spec.TLS.InsecureSkipVerify,
		}

		// TODO: Add CA certificate support if backend.Spec.TLS.CACert is provided

		httpClient.Transport = &http.Transport{
			TLSClientConfig: tlsConfig,
		}
	}

	// Test connection with a PROPFIND request
	req, err := http.NewRequest("PROPFIND", backend.Spec.Endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create test request: %w", err)
	}

	req.SetBasicAuth(username, password)
	req.Header.Set("Depth", "0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to WebDAV server: %w", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("WebDAV server returned error: %d %s", resp.StatusCode, resp.Status)
	}

	return &webDavBackendImpl{
		client:   httpClient,
		endpoint: strings.TrimSuffix(backend.Spec.Endpoint, "/"),
		basePath: backend.Spec.BasePath,
		username: username,
		password: password,
	}, nil
}

// getWebDavCredentialsFromSecret retrieves WebDAV credentials from a Kubernetes Secret
func getWebDavCredentialsFromSecret(secretRef *ftpv1.WebDavSecretRef, kubeClient client.Client) (string, string, error) {
	// TODO: Implement reading from Kubernetes Secret
	// For now, return empty strings - this would need to be implemented
	// based on your specific Secret structure
	return "", "", fmt.Errorf("reading credentials from secrets not implemented yet")
}

// Stat returns file/directory information
func (w *webDavBackendImpl) Stat(filePath string) (*FileInfo, error) {
	fullPath := w.getFullPath(filePath)

	// Create PROPFIND request
	propfindBody := `<?xml version="1.0" encoding="utf-8" ?>
<propfind xmlns="DAV:">
    <prop>
        <resourcetype/>
        <getcontentlength/>
        <getlastmodified/>
    </prop>
</propfind>`

	req, err := http.NewRequest("PROPFIND", w.endpoint+fullPath, strings.NewReader(propfindBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create PROPFIND request: %w", err)
	}

	req.SetBasicAuth(w.username, w.password)
	req.Header.Set("Content-Type", "text/xml")
	req.Header.Set("Depth", "0")

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("PROPFIND request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("file not found")
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("PROPFIND failed with status: %d", resp.StatusCode)
	}

	// TODO: Parse XML response to extract file information
	// For now, return basic file info
	return &FileInfo{
		Name:    path.Base(filePath),
		Size:    0,
		Mode:    0644,
		ModTime: time.Now(),
		IsDir:   false,
	}, nil
}

// Exists checks if a file or directory exists
func (w *webDavBackendImpl) Exists(filePath string) (bool, error) {
	_, err := w.Stat(filePath)
	if err != nil {
		return false, nil
	}
	return true, nil
}

// Open opens a file for reading
func (w *webDavBackendImpl) Open(filePath string) (io.ReadCloser, error) {
	fullPath := w.getFullPath(filePath)

	req, err := http.NewRequest("GET", w.endpoint+fullPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create GET request: %w", err)
	}

	req.SetBasicAuth(w.username, w.password)

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET request failed: %w", err)
	}

	if resp.StatusCode >= 400 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("GET failed with status: %d", resp.StatusCode)
	}

	// Wrap the response body to implement ReadSeekCloser
	return &webdavReadSeekCloser{
		ReadCloser: resp.Body,
		size:       resp.ContentLength,
	}, nil
}

// WriteFile writes data to a file
func (w *webDavBackendImpl) WriteFile(filePath string, reader io.Reader) (int64, error) {
	fullPath := w.getFullPath(filePath)

	req, err := http.NewRequest("PUT", w.endpoint+fullPath, reader)
	if err != nil {
		return 0, fmt.Errorf("failed to create PUT request: %w", err)
	}

	req.SetBasicAuth(w.username, w.password)

	resp, err := w.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("PUT request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("PUT failed with status: %d", resp.StatusCode)
	}

	// TODO: Return actual bytes written
	return 0, nil
}

// Remove deletes a file
func (w *webDavBackendImpl) Remove(filePath string) error {
	fullPath := w.getFullPath(filePath)

	req, err := http.NewRequest("DELETE", w.endpoint+fullPath, nil)
	if err != nil {
		return fmt.Errorf("failed to create DELETE request: %w", err)
	}

	req.SetBasicAuth(w.username, w.password)

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("DELETE request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("DELETE failed with status: %d", resp.StatusCode)
	}

	return nil
}

// RemoveAll deletes a directory and all its contents
func (w *webDavBackendImpl) RemoveAll(dirPath string) error {
	fullPath := w.getFullPath(dirPath)

	req, err := http.NewRequest("DELETE", w.endpoint+fullPath, nil)
	if err != nil {
		return fmt.Errorf("failed to create DELETE request: %w", err)
	}

	req.SetBasicAuth(w.username, w.password)
	req.Header.Set("Depth", "infinity")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("DELETE request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("DELETE failed with status: %d", resp.StatusCode)
	}

	return nil
}

// Rename moves/renames a file or directory
func (w *webDavBackendImpl) Rename(oldPath, newPath string) error {
	fullOldPath := w.getFullPath(oldPath)
	fullNewPath := w.getFullPath(newPath)

	req, err := http.NewRequest("MOVE", w.endpoint+fullOldPath, nil)
	if err != nil {
		return fmt.Errorf("failed to create MOVE request: %w", err)
	}

	req.SetBasicAuth(w.username, w.password)
	req.Header.Set("Destination", w.endpoint+fullNewPath)

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("MOVE request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("MOVE failed with status: %d", resp.StatusCode)
	}

	return nil
}

// Mkdir creates a directory
func (w *webDavBackendImpl) Mkdir(dirPath string) error {
	fullPath := w.getFullPath(dirPath)

	req, err := http.NewRequest("MKCOL", w.endpoint+fullPath, nil)
	if err != nil {
		return fmt.Errorf("failed to create MKCOL request: %w", err)
	}

	req.SetBasicAuth(w.username, w.password)

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("MKCOL request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("MKCOL failed with status: %d", resp.StatusCode)
	}

	return nil
}

// ReadDir lists directory contents
func (w *webDavBackendImpl) ReadDir(dirPath string) ([]*FileInfo, error) {
	fullPath := w.getFullPath(dirPath)

	// Create PROPFIND request for directory listing
	propfindBody := `<?xml version="1.0" encoding="utf-8" ?>
<propfind xmlns="DAV:">
    <prop>
        <resourcetype/>
        <getcontentlength/>
        <getlastmodified/>
        <displayname/>
    </prop>
</propfind>`

	req, err := http.NewRequest("PROPFIND", w.endpoint+fullPath, strings.NewReader(propfindBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create PROPFIND request: %w", err)
	}

	req.SetBasicAuth(w.username, w.password)
	req.Header.Set("Content-Type", "text/xml")
	req.Header.Set("Depth", "1")

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("PROPFIND request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("PROPFIND failed with status: %d", resp.StatusCode)
	}

	// TODO: Parse XML response to extract directory entries
	// For now, return empty list
	return []*FileInfo{}, nil
}

// getFullPath combines the base path with the file path
func (w *webDavBackendImpl) getFullPath(filePath string) string {
	if w.basePath == "" {
		return filePath
	}

	// Ensure base path starts with /
	basePath := w.basePath
	if !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}

	// Ensure base path ends with /
	basePath = strings.TrimSuffix(basePath, "/") + "/"

	// Remove leading / from file path if present
	filePath = strings.TrimPrefix(filePath, "/")

	return basePath + filePath
}

// webdavReadSeekCloser implements server.ReadSeekCloser for WebDAV responses
type webdavReadSeekCloser struct {
	io.ReadCloser
	size int64
}

func (w *webdavReadSeekCloser) Seek(offset int64, whence int) (int64, error) {
	// WebDAV doesn't support seeking in HTTP responses
	// This would need a more sophisticated implementation
	switch whence {
	case io.SeekStart:
		if offset == 0 {
			return 0, nil
		}
		return 0, fmt.Errorf("WebDAV seek not supported")
	case io.SeekCurrent:
		if offset == 0 {
			return 0, nil
		}
		return 0, fmt.Errorf("WebDAV seek not supported")
	case io.SeekEnd:
		return w.size, nil
	default:
		return 0, fmt.Errorf("invalid whence")
	}
}
