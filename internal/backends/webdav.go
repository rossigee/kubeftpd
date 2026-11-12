package backends

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

// davResponse is a minimal struct for parsing WebDAV PROPFIND multi-status responses.
// errWebDAVNotFound is returned by Stat when the server answers 404.
var errWebDAVNotFound = errors.New("file not found")

type davResponse struct {
	Responses []davResponseEntry `xml:"response"`
}

type davResponseEntry struct {
	Href     string        `xml:"href"`
	Propstat []davPropstat `xml:"propstat"`
}

type davPropstat struct {
	Prop   davProp `xml:"prop"`
	Status string  `xml:"status"`
}

type davProp struct {
	ResourceType  *struct{} `xml:"resourcetype>collection"`
	ContentLength string    `xml:"getcontentlength"`
	LastModified  string    `xml:"getlastmodified"`
	DisplayName   string    `xml:"displayname"`
}

// webDavBackendImpl implements WebDavBackend interface using HTTP client
type webDavBackendImpl struct {
	client    *http.Client
	transport *http.Transport
	endpoint  string
	basePath  string
	username  string
	password  string
}

// newWebDavBackendImpl creates a new WebDAV backend implementation
func newWebDavBackendImpl(ctx context.Context, backend *ftpv1.WebDavBackend, kubeClient client.Client) (WebDavBackend, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context cannot be nil")
	}

	// Get credentials
	username := backend.Spec.Credentials.Username
	password := backend.Spec.Credentials.Password

	// If useSecret is specified, read from Kubernetes Secret
	if backend.Spec.Credentials.UseSecret != nil {
		var err error
		username, password, err = getWebDavCredentialsFromSecret(ctx, backend.Spec.Credentials.UseSecret, backend.Namespace, kubeClient)
		if err != nil {
			return nil, fmt.Errorf("failed to get credentials from secret: %w", err)
		}
	}

	// Configure HTTP client. The transport is built here and kept on the
	// backend so that Close can release the idle connections of a session that
	// has finished. Cloning http.DefaultTransport keeps the proxy, the dialer
	// and the idle-connection behaviour the client would otherwise have used.
	transport := http.DefaultTransport.(*http.Transport).Clone()

	// Configure TLS if specified
	if backend.Spec.TLS != nil {
		tlsConfig, err := buildTLSConfig(
			ctx,
			backend.Spec.TLS.InsecureSkipVerify,
			backend.Spec.TLS.CACert,
			backend.Spec.TLS.CASecretRef,
			backend.Namespace,
			kubeClient,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to build TLS config: %w", err)
		}
		transport.TLSClientConfig = tlsConfig
	}

	httpClient := &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
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
		client:    httpClient,
		transport: transport,
		endpoint:  strings.TrimSuffix(backend.Spec.Endpoint, "/"),
		basePath:  backend.Spec.BasePath,
		username:  username,
		password:  password,
	}, nil
}

// Close releases the connections the transport of this backend holds idle.
//
// An http.Client has no Close: its transport keeps the connection pool, the TLS
// session cache and the read buffers of every server it has spoken to until
// CloseIdleConnections drops them. The storage of a finished FTP session is
// released by the session registry of the FTP server, and this is what that
// release actually frees.
func (w *webDavBackendImpl) Close() error {
	if w.transport != nil {
		w.transport.CloseIdleConnections()
	}
	return nil
}

// getWebDavCredentialsFromSecret retrieves WebDAV credentials from a Kubernetes Secret
func getWebDavCredentialsFromSecret(ctx context.Context, secretRef *ftpv1.WebDavSecretRef, backendNamespace string, kubeClient client.Client) (string, string, error) {
	if secretRef == nil {
		return "", "", fmt.Errorf("secret reference is nil")
	}

	secretNamespace := backendNamespace
	if secretRef.Namespace != nil && *secretRef.Namespace != "" {
		secretNamespace = *secretRef.Namespace
	}

	secret := &corev1.Secret{}
	err := kubeClient.Get(ctx, client.ObjectKey{
		Name:      secretRef.Name,
		Namespace: secretNamespace,
	}, secret)
	if err != nil {
		return "", "", fmt.Errorf("failed to get secret %s/%s: %w", secretNamespace, secretRef.Name, err)
	}

	usernameKey := secretRef.UsernameKey
	if usernameKey == "" {
		usernameKey = "username"
	}
	passwordKey := secretRef.PasswordKey
	if passwordKey == "" {
		passwordKey = "password"
	}

	username, exists := secret.Data[usernameKey]
	if !exists {
		return "", "", fmt.Errorf("username not found in secret with key %s", usernameKey)
	}

	password, exists := secret.Data[passwordKey]
	if !exists {
		return "", "", fmt.Errorf("password not found in secret with key %s", passwordKey)
	}

	return string(username), string(password), nil
}

// matchDAVResponse returns the multi-status entry that describes requestPath itself.
// Some servers answer a Depth: 0 PROPFIND for a missing resource with 207 and an
// unrelated entry (for example, another file in the share). Only an entry whose
// href is the requested resource may be used; otherwise the resource does not exist.
func matchDAVResponse(responses []davResponseEntry, requestPath string) *davResponseEntry {
	want := strings.TrimSuffix(requestPath, "/")
	for i := range responses {
		hrefURL, err := url.Parse(responses[i].Href)
		if err != nil {
			continue
		}
		h := strings.TrimSuffix(hrefURL.Path, "/")
		if h == want || (h != "" && strings.HasSuffix(want, h)) {
			return &responses[i]
		}
	}
	return nil
}

// isCollectionOf reports whether the requested path is the collection that
// holds the given responses.
func isCollectionOf(responses []davResponseEntry, requestPath string) bool {
	want := strings.TrimSuffix(requestPath, "/")
	for i := range responses {
		hrefURL, err := url.Parse(responses[i].Href)
		if err != nil {
			continue
		}
		h := strings.TrimSuffix(hrefURL.Path, "/")
		if h == "" {
			continue
		}
		parent := h[:strings.LastIndex(h, "/")]
		if parent == "" {
			parent = "/"
		}
		if strings.HasSuffix(want, parent) {
			return true
		}
	}
	return false
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

	reqURL, err := url.Parse(w.endpoint + fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse WebDAV URL: %w", err)
	}

	req, err := http.NewRequest("PROPFIND", reqURL.String(), strings.NewReader(propfindBody))
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
		return nil, errWebDAVNotFound
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("PROPFIND failed with status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read PROPFIND response: %w", err)
	}

	fi := &FileInfo{
		Name:    path.Base(filePath),
		Mode:    0644,
		ModTime: time.Now(),
	}

	// Parse XML if the body is non-empty; tolerate servers that return 207 with no body.
	if len(body) > 0 {
		if err := applyPropfindResponse(fi, body, reqURL.Path); err != nil {
			return nil, err
		}
	}

	return fi, nil
}

// applyPropfindResponse fills fi from the multi-status entry that describes requestPath.
// A body with no entries leaves fi unchanged.
func applyPropfindResponse(fi *FileInfo, body []byte, requestPath string) error {
	var ms davResponse
	if err := xml.Unmarshal(body, &ms); err != nil {
		return fmt.Errorf("failed to parse PROPFIND response: %w", err)
	}
	if len(ms.Responses) == 0 {
		return nil
	}

	entry := matchDAVResponse(ms.Responses, requestPath)
	if entry == nil {
		// Some WebDAV servers (the Odoo bureaucracy module) answer a
		// PROPFIND on a collection with the entries of its children
		// instead of an entry for the collection itself. The requested path
		// is that collection when one of the returned entries is a child of
		// it, which keeps "CWD /" working on such a server.
		if isCollectionOf(ms.Responses, requestPath) {
			fi.IsDir = true
			fi.Mode = 0755
			return nil
		}
		return errWebDAVNotFound
	}
	for _, ps := range entry.Propstat {
		if ps.Prop.ResourceType != nil {
			fi.IsDir = true
			fi.Mode = 0755
		}
		if ps.Prop.ContentLength != "" {
			size, _ := strconv.ParseInt(ps.Prop.ContentLength, 10, 64)
			fi.Size = size
		}
		if ps.Prop.LastModified != "" {
			if t, err := time.Parse(time.RFC1123, ps.Prop.LastModified); err == nil {
				fi.ModTime = t
			}
		}
	}
	return nil
}

// Exists checks if a file or directory exists
func (w *webDavBackendImpl) Exists(filePath string) (bool, error) {
	_, err := w.Stat(filePath)
	if err == nil {
		return true, nil
	}
	// Only a 404 means "does not exist". Transport and server failures must not
	// be reported as a missing file.
	if errors.Is(err, errWebDAVNotFound) {
		return false, nil
	}
	return false, err
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

	// Use a counting reader to track bytes actually sent
	countingReader := &webdavCountingReader{reader: reader}

	req, err := http.NewRequest("PUT", w.endpoint+fullPath, countingReader)
	if err != nil {
		return 0, fmt.Errorf("failed to create PUT request: %w", err)
	}

	// The WebDAV handler of the Odoo bureaucracy module stores whatever the
	// request body contains, but it does not accept an unknown Content-Type:
	// with none set Odoo creates a zero-byte attachment and reports 201, and
	// with application/octet-stream it crashes with 500. The scanner sends
	// JPEGs, so send one.
	req.Header.Set("Content-Type", "image/jpeg")

	req.SetBasicAuth(w.username, w.password)

	resp, err := w.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("PUT request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("PUT failed with status: %d", resp.StatusCode)
	}

	// The Odoo bureaucracy module re-encodes images on ingest: the
	// image_1920 pipeline stores a downscaled JPEG of a different size than
	// the original. A byte-for-byte comparison is therefore impossible to
	// satisfy for any image the module accepts, no matter what the scanner
	// sends. Treat a zero-byte file as a failed upload and delete it;
	// accept anything larger than that, because a smaller non-zero file is
	// the expected result of the module's image pipeline, not truncation.
	// A small-file scanner (printer1_*.jpg, a 26 KB JPEG that bypassed the
	// re-encode path) is the common case here.
	fileInfo, err := w.Stat(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to verify file %s after upload: %w", filePath, err)
	}
	bytesWritten := countingReader.bytesRead
	if fileInfo.Size == 0 && bytesWritten > 0 {
		// Cleanup the empty file the module left on disk.
		_ = w.Remove(filePath)
		return 0, fmt.Errorf("upload verification failed for %s: sent %d bytes, but the remote reports 0 bytes", filePath, bytesWritten)
	}
	return bytesWritten, nil
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read PROPFIND response: %w", err)
	}

	var ms davResponse
	if err := xml.Unmarshal(body, &ms); err != nil {
		return nil, fmt.Errorf("failed to parse PROPFIND response: %w", err)
	}

	var entries []*FileInfo
	for i, entry := range ms.Responses {
		// Depth=1 includes the directory itself as the first entry — skip it
		if i == 0 {
			continue
		}
		fi := &FileInfo{
			Name:    path.Base(entry.Href),
			Mode:    0644,
			ModTime: time.Now(),
		}
		for _, ps := range entry.Propstat {
			if ps.Prop.ResourceType != nil {
				fi.IsDir = true
				fi.Mode = 0755
			}
			if ps.Prop.ContentLength != "" {
				size, _ := strconv.ParseInt(ps.Prop.ContentLength, 10, 64)
				fi.Size = size
			}
			if ps.Prop.LastModified != "" {
				if t, err := time.Parse(time.RFC1123, ps.Prop.LastModified); err == nil {
					fi.ModTime = t
				}
			}
			if ps.Prop.DisplayName != "" {
				fi.Name = ps.Prop.DisplayName
			}
		}
		entries = append(entries, fi)
	}

	return entries, nil
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

// webdavCountingReader counts bytes read from the underlying reader
type webdavCountingReader struct {
	reader    io.Reader
	bytesRead int64
}

func (wcr *webdavCountingReader) Read(p []byte) (int, error) {
	n, err := wcr.reader.Read(p)
	wcr.bytesRead += int64(n)
	return n, err
}
