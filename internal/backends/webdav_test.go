package backends

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

func TestGetWebDavCredentialsFromSecret(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, ftpv1.AddToScheme(scheme))

	tests := []struct {
		name         string
		secretRef    *ftpv1.WebDavSecretRef
		backendNs    string
		secret       *corev1.Secret
		expectedUser string
		expectedPass string
		expectError  bool
	}{
		{
			name: "success with default keys",
			secretRef: &ftpv1.WebDavSecretRef{
				Name: "test-secret",
			},
			backendNs: "test-ns",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "test-ns",
				},
				Data: map[string][]byte{
					"username": []byte("testuser"),
					"password": []byte("testpass"),
				},
			},
			expectedUser: "testuser",
			expectedPass: "testpass",
			expectError:  false,
		},
		{
			name: "success with custom keys",
			secretRef: &ftpv1.WebDavSecretRef{
				Name:        "test-secret",
				UsernameKey: "user",
				PasswordKey: "pass",
			},
			backendNs: "test-ns",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "test-ns",
				},
				Data: map[string][]byte{
					"user": []byte("customuser"),
					"pass": []byte("custompass"),
				},
			},
			expectedUser: "customuser",
			expectedPass: "custompass",
			expectError:  false,
		},
		{
			name: "success with cross-namespace secret",
			secretRef: &ftpv1.WebDavSecretRef{
				Name:      "test-secret",
				Namespace: stringPtr("other-ns"),
			},
			backendNs: "test-ns",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "other-ns",
				},
				Data: map[string][]byte{
					"username": []byte("crossuser"),
					"password": []byte("crosspass"),
				},
			},
			expectedUser: "crossuser",
			expectedPass: "crosspass",
			expectError:  false,
		},
		{
			name: "error - secret not found",
			secretRef: &ftpv1.WebDavSecretRef{
				Name: "missing-secret",
			},
			backendNs:   "test-ns",
			secret:      nil,
			expectError: true,
		},
		{
			name: "error - username key missing",
			secretRef: &ftpv1.WebDavSecretRef{
				Name: "test-secret",
			},
			backendNs: "test-ns",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "test-ns",
				},
				Data: map[string][]byte{
					"password": []byte("testpass"),
				},
			},
			expectError: true,
		},
		{
			name: "error - password key missing",
			secretRef: &ftpv1.WebDavSecretRef{
				Name: "test-secret",
			},
			backendNs: "test-ns",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "test-ns",
				},
				Data: map[string][]byte{
					"username": []byte("testuser"),
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kubeClientBuilder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.secret != nil {
				kubeClientBuilder = kubeClientBuilder.WithObjects(tt.secret)
			}
			kubeClient := kubeClientBuilder.Build()

			username, password, err := getWebDavCredentialsFromSecret(context.TODO(), tt.secretRef, tt.backendNs, kubeClient)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedUser, username)
				assert.Equal(t, tt.expectedPass, password)
			}
		})
	}
}

func stringPtr(s string) *string {
	return &s
}

func TestNewWebDavBackendImpl(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, ftpv1.AddToScheme(scheme))

	tests := []struct {
		name        string
		backend     *ftpv1.WebDavBackend
		secret      *corev1.Secret
		mockServer  func() *httptest.Server
		expectError bool
	}{
		{
			name: "success with inline credentials",
			backend: &ftpv1.WebDavBackend{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-backend",
					Namespace: "test-ns",
				},
				Spec: ftpv1.WebDavBackendSpec{
					Endpoint: "http://example.com/webdav",
					BasePath: "/files",
					Credentials: ftpv1.WebDavCredentials{
						Username: "testuser",
						Password: "testpass",
					},
				},
			},
			mockServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// Check basic auth
					user, pass, ok := r.BasicAuth()
					if !ok || user != "testuser" || pass != "testpass" {
						w.WriteHeader(401)
						return
					}
					if r.Method == "PROPFIND" {
						w.WriteHeader(207)
					} else {
						w.WriteHeader(200)
					}
				}))
			},
			expectError: false,
		},
		{
			name: "success with secret credentials",
			backend: &ftpv1.WebDavBackend{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-backend",
					Namespace: "test-ns",
				},
				Spec: ftpv1.WebDavBackendSpec{
					Endpoint: "http://example.com/webdav",
					BasePath: "/files",
					Credentials: ftpv1.WebDavCredentials{
						UseSecret: &ftpv1.WebDavSecretRef{
							Name: "test-secret",
						},
					},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "test-ns",
				},
				Data: map[string][]byte{
					"username": []byte("secretuser"),
					"password": []byte("secretpass"),
				},
			},
			mockServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					user, pass, ok := r.BasicAuth()
					if !ok || user != "secretuser" || pass != "secretpass" {
						w.WriteHeader(401)
						return
					}
					if r.Method == "PROPFIND" {
						w.WriteHeader(207)
					} else {
						w.WriteHeader(200)
					}
				}))
			},
			expectError: false,
		},
		{
			name: "error - connection test fails",
			backend: &ftpv1.WebDavBackend{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-backend",
					Namespace: "test-ns",
				},
				Spec: ftpv1.WebDavBackendSpec{
					Endpoint: "http://invalid-endpoint/webdav",
					Credentials: ftpv1.WebDavCredentials{
						Username: "testuser",
						Password: "testpass",
					},
				},
			},
			expectError: true,
		},
		{
			name: "error - auth fails",
			backend: &ftpv1.WebDavBackend{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-backend",
					Namespace: "test-ns",
				},
				Spec: ftpv1.WebDavBackendSpec{
					Endpoint: "http://example.com/webdav",
					Credentials: ftpv1.WebDavCredentials{
						Username: "testuser",
						Password: "wrongpass",
					},
				},
			},
			mockServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(401) // Unauthorized
				}))
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kubeClientBuilder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.secret != nil {
				kubeClientBuilder = kubeClientBuilder.WithObjects(tt.secret)
			}
			kubeClient := kubeClientBuilder.Build()

			if tt.mockServer != nil {
				server := tt.mockServer()
				defer server.Close()
				tt.backend.Spec.Endpoint = server.URL
			}

			backend, err := newWebDavBackendImpl(context.TODO(), tt.backend, kubeClient)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, backend)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, backend)
			}
		})
	}
}

func TestWebDavBackendImpl_Stat(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PROPFIND" && r.URL.Path == "/base/test.txt" {
			user, pass, ok := r.BasicAuth()
			if ok && user == "testuser" && pass == "testpass" {
				w.Header().Set("Content-Type", "text/xml")
				w.WriteHeader(207)
				// Simplified XML response
				xmlResponse := `<?xml version="1.0" encoding="utf-8"?>
<multistatus xmlns="DAV:">
  <response>
    <href>/base/test.txt</href>
    <propstat>
      <prop>
        <resourcetype/>
        <getcontentlength>1024</getcontentlength>
        <getlastmodified>Wed, 21 Oct 2015 07:28:00 GMT</getlastmodified>
      </prop>
      <status>HTTP/1.1 200 OK</status>
    </propstat>
  </response>
</multistatus>`
				_, _ = w.Write([]byte(xmlResponse))
				return
			}
		}
		w.WriteHeader(404)
	}))
	defer testServer.Close()

	kubeClient := fake.NewClientBuilder().Build()
	backendCR := &ftpv1.WebDavBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-backend",
			Namespace: "default",
		},
		Spec: ftpv1.WebDavBackendSpec{
			Endpoint: testServer.URL,
			BasePath: "/base",
			Credentials: ftpv1.WebDavCredentials{
				Username: "testuser",
				Password: "testpass",
			},
		},
	}

	backend, err := newWebDavBackendImpl(context.TODO(), backendCR, kubeClient)
	require.NoError(t, err)

	fileInfo, err := backend.Stat("test.txt")
	assert.NoError(t, err)
	assert.NotNil(t, fileInfo)
	assert.Equal(t, "test.txt", fileInfo.Name)
	// Note: Size and other fields are not parsed from XML yet
}

func TestWebDavBackendImpl_Exists(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PROPFIND" && r.URL.Path == "/base/exists.txt" {
			user, pass, ok := r.BasicAuth()
			if ok && user == "testuser" && pass == "testpass" {
				w.WriteHeader(207)
				return
			}
		}
		w.WriteHeader(404)
	}))
	defer testServer.Close()

	kubeClient := fake.NewClientBuilder().Build()
	backendCR := &ftpv1.WebDavBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-backend",
			Namespace: "default",
		},
		Spec: ftpv1.WebDavBackendSpec{
			Endpoint: testServer.URL,
			BasePath: "/base",
			Credentials: ftpv1.WebDavCredentials{
				Username: "testuser",
				Password: "testpass",
			},
		},
	}

	backend, err := newWebDavBackendImpl(context.TODO(), backendCR, kubeClient)
	require.NoError(t, err)

	exists, err := backend.Exists("exists.txt")
	assert.NoError(t, err)
	assert.True(t, exists)

	exists, err = backend.Exists("nonexistent.txt")
	assert.NoError(t, err)
	assert.False(t, exists)
}

func TestWebDavBackendImpl_Open(t *testing.T) {
	testContent := "Hello, WebDAV!"
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/base/test.txt" {
			user, pass, ok := r.BasicAuth()
			if ok && user == "testuser" && pass == "testpass" {
				w.Header().Set("Content-Length", "13")
				w.WriteHeader(200)
				_, _ = w.Write([]byte(testContent))
				return
			}
		}
		w.WriteHeader(404)
	}))
	defer testServer.Close()

	kubeClient := fake.NewClientBuilder().Build()
	backendCR := &ftpv1.WebDavBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-backend",
			Namespace: "default",
		},
		Spec: ftpv1.WebDavBackendSpec{
			Endpoint: testServer.URL,
			BasePath: "/base",
			Credentials: ftpv1.WebDavCredentials{
				Username: "testuser",
				Password: "testpass",
			},
		},
	}

	backend, err := newWebDavBackendImpl(context.TODO(), backendCR, kubeClient)
	require.NoError(t, err)

	reader, err := backend.Open("test.txt")
	assert.NoError(t, err)
	assert.NotNil(t, reader)

	content, err := io.ReadAll(reader)
	assert.NoError(t, err)
	assert.Equal(t, testContent, string(content))

	_ = reader.Close()
}

func TestWebDavBackendImpl_WriteFile(t *testing.T) {
	testContent := "Hello, WebDAV write!"
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" && r.URL.Path == "/base/write.txt" {
			user, pass, ok := r.BasicAuth()
			if ok && user == "testuser" && pass == "testpass" {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					w.WriteHeader(500)
					return
				}
				if string(body) == testContent {
					w.WriteHeader(201)
					return
				}
			}
		}
		if r.Method == "PROPFIND" && r.URL.Path == "/base/write.txt" {
			user, pass, ok := r.BasicAuth()
			if ok && user == "testuser" && pass == "testpass" {
				xmlResponse := `<?xml version="1.0" encoding="utf-8"?>
<multistatus xmlns="DAV:">
  <response>
    <href>/base/write.txt</href>
    <propstat>
      <prop>
        <resourcetype/>
        <getcontentlength>21</getcontentlength>
      </prop>
      <status>HTTP/1.1 200 OK</status>
    </propstat>
  </response>
</multistatus>`
				w.Header().Set("Content-Type", "text/xml")
				w.WriteHeader(207)
				_, _ = w.Write([]byte(xmlResponse))
				return
			}
		}
		w.WriteHeader(404)
	}))
	defer testServer.Close()

	kubeClient := fake.NewClientBuilder().Build()
	backendCR := &ftpv1.WebDavBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-backend",
			Namespace: "default",
		},
		Spec: ftpv1.WebDavBackendSpec{
			Endpoint: testServer.URL,
			BasePath: "/base",
			Credentials: ftpv1.WebDavCredentials{
				Username: "testuser",
				Password: "testpass",
			},
		},
	}

	backend, err := newWebDavBackendImpl(context.TODO(), backendCR, kubeClient)
	require.NoError(t, err)

	n, err := backend.WriteFile("write.txt", strings.NewReader(testContent))
	assert.NoError(t, err)
	assert.Equal(t, int64(len(testContent)), n)
}

func TestWebDavBackendImpl_Remove(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" && r.URL.Path == "/base/delete.txt" {
			user, pass, ok := r.BasicAuth()
			if ok && user == "testuser" && pass == "testpass" {
				w.WriteHeader(204)
				return
			}
		}
		w.WriteHeader(404)
	}))
	defer testServer.Close()

	kubeClient := fake.NewClientBuilder().Build()
	backendCR := &ftpv1.WebDavBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-backend",
			Namespace: "default",
		},
		Spec: ftpv1.WebDavBackendSpec{
			Endpoint: testServer.URL,
			BasePath: "/base",
			Credentials: ftpv1.WebDavCredentials{
				Username: "testuser",
				Password: "testpass",
			},
		},
	}

	backend, err := newWebDavBackendImpl(context.TODO(), backendCR, kubeClient)
	require.NoError(t, err)

	err = backend.Remove("delete.txt")
	assert.NoError(t, err)
}

func TestWebDavBackendImpl_RemoveAll(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" && r.URL.Path == "/base/deletedir" {
			user, pass, ok := r.BasicAuth()
			if ok && user == "testuser" && pass == "testpass" && r.Header.Get("Depth") == "infinity" {
				w.WriteHeader(204)
				return
			}
		}
		w.WriteHeader(404)
	}))
	defer testServer.Close()

	kubeClient := fake.NewClientBuilder().Build()
	backendCR := &ftpv1.WebDavBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-backend",
			Namespace: "default",
		},
		Spec: ftpv1.WebDavBackendSpec{
			Endpoint: testServer.URL,
			BasePath: "/base",
			Credentials: ftpv1.WebDavCredentials{
				Username: "testuser",
				Password: "testpass",
			},
		},
	}

	backend, err := newWebDavBackendImpl(context.TODO(), backendCR, kubeClient)
	require.NoError(t, err)

	err = backend.RemoveAll("deletedir")
	assert.NoError(t, err)
}

func TestWebDavBackendImpl_Rename(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "MOVE" && r.URL.Path == "/base/old.txt" {
			user, pass, ok := r.BasicAuth()
			if ok && user == "testuser" && pass == "testpass" {
				w.WriteHeader(201)
				return
			}
		}
		w.WriteHeader(404)
	}))
	defer testServer.Close()

	kubeClient := fake.NewClientBuilder().Build()
	backendCR := &ftpv1.WebDavBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-backend",
			Namespace: "default",
		},
		Spec: ftpv1.WebDavBackendSpec{
			Endpoint: testServer.URL,
			BasePath: "/base",
			Credentials: ftpv1.WebDavCredentials{
				Username: "testuser",
				Password: "testpass",
			},
		},
	}

	backend, err := newWebDavBackendImpl(context.TODO(), backendCR, kubeClient)
	require.NoError(t, err)

	err = backend.Rename("old.txt", "new.txt")
	assert.NoError(t, err)
}

func TestWebDavBackendImpl_Mkdir(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "MKCOL" && r.URL.Path == "/base/newdir" {
			user, pass, ok := r.BasicAuth()
			if ok && user == "testuser" && pass == "testpass" {
				w.WriteHeader(201)
				return
			}
		}
		w.WriteHeader(404)
	}))
	defer testServer.Close()

	kubeClient := fake.NewClientBuilder().Build()
	backendCR := &ftpv1.WebDavBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-backend",
			Namespace: "default",
		},
		Spec: ftpv1.WebDavBackendSpec{
			Endpoint: testServer.URL,
			BasePath: "/base",
			Credentials: ftpv1.WebDavCredentials{
				Username: "testuser",
				Password: "testpass",
			},
		},
	}

	backend, err := newWebDavBackendImpl(context.TODO(), backendCR, kubeClient)
	require.NoError(t, err)

	err = backend.Mkdir("newdir")
	assert.NoError(t, err)
}

func TestWebDavBackendImpl_ReadDir(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PROPFIND" && r.URL.Path == "/base/testdir" && r.Header.Get("Depth") == "1" {
			user, pass, ok := r.BasicAuth()
			if ok && user == "testuser" && pass == "testpass" {
				w.Header().Set("Content-Type", "text/xml")
				w.WriteHeader(207)
				// Simplified XML response - in real implementation this would be parsed
				xmlResponse := `<?xml version="1.0" encoding="utf-8"?>
<multistatus xmlns="DAV:">
  <response>
    <href>/base/testdir/</href>
    <propstat>
      <prop>
        <resourcetype><collection/></resourcetype>
      </prop>
      <status>HTTP/1.1 200 OK</status>
    </propstat>
  </response>
  <response>
    <href>/base/testdir/file1.txt</href>
    <propstat>
      <prop>
        <resourcetype/>
        <getcontentlength>100</getcontentlength>
      </prop>
      <status>HTTP/1.1 200 OK</status>
    </propstat>
  </response>
</multistatus>`
				_, _ = w.Write([]byte(xmlResponse))
				return
			}
		}
		w.WriteHeader(404)
	}))
	defer testServer.Close()

	kubeClient := fake.NewClientBuilder().Build()
	backendCR := &ftpv1.WebDavBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-backend",
			Namespace: "default",
		},
		Spec: ftpv1.WebDavBackendSpec{
			Endpoint: testServer.URL,
			BasePath: "/base",
			Credentials: ftpv1.WebDavCredentials{
				Username: "testuser",
				Password: "testpass",
			},
		},
	}

	backend, err := newWebDavBackendImpl(context.TODO(), backendCR, kubeClient)
	require.NoError(t, err)

	entries, err := backend.ReadDir("testdir")
	assert.NoError(t, err)
	// Note: Currently returns empty list as XML parsing is not implemented
	assert.Equal(t, 0, len(entries))
}

func TestWebDavBackendImpl_GetFullPath(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PROPFIND" {
			user, pass, ok := r.BasicAuth()
			if ok && user == "testuser" && pass == "testpass" {
				w.WriteHeader(207)
				return
			}
		}
		w.WriteHeader(401)
	}))
	defer testServer.Close()

	kubeClient := fake.NewClientBuilder().Build()
	backendCR := &ftpv1.WebDavBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-backend",
			Namespace: "default",
		},
		Spec: ftpv1.WebDavBackendSpec{
			Endpoint: testServer.URL,
			BasePath: "/base",
			Credentials: ftpv1.WebDavCredentials{
				Username: "testuser",
				Password: "testpass",
			},
		},
	}

	backend, err := newWebDavBackendImpl(context.TODO(), backendCR, kubeClient)
	require.NoError(t, err)

	impl := backend.(*webDavBackendImpl)

	tests := []struct {
		input    string
		expected string
	}{
		{"file.txt", "/base/file.txt"},
		{"/file.txt", "/base/file.txt"},
		{"dir/file.txt", "/base/dir/file.txt"},
		{"", "/base/"},
	}

	for _, tt := range tests {
		result := impl.getFullPath(tt.input)
		assert.Equal(t, tt.expected, result)
	}
}

// Test for webdavReadSeekCloser Seek method
func TestWebdavReadSeekCloser_Seek(t *testing.T) {
	reader := &webdavReadSeekCloser{
		ReadCloser: io.NopCloser(bytes.NewReader([]byte("test"))),
		size:       4,
	}

	// Test SeekStart with offset 0
	pos, err := reader.Seek(0, io.SeekStart)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), pos)

	// Test SeekCurrent with offset 0
	pos, err = reader.Seek(0, io.SeekCurrent)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), pos)

	// Test SeekEnd
	pos, err = reader.Seek(0, io.SeekEnd)
	assert.NoError(t, err)
	assert.Equal(t, int64(4), pos)

	// Test unsupported seek operations
	_, err = reader.Seek(1, io.SeekStart)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "WebDAV seek not supported")

	_, err = reader.Seek(1, io.SeekCurrent)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "WebDAV seek not supported")

	_, err = reader.Seek(0, 999) // Invalid whence
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid whence")
}
