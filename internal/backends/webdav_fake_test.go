package backends

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

// fakeDAV is an in-memory WebDAV server covering the verbs the WebDAV backend
// uses: GET, PUT, PROPFIND (depth 0 and 1), MKCOL, DELETE and MOVE.
type fakeDAV struct {
	mu    sync.Mutex
	files map[string][]byte
	dirs  map[string]bool
	mtime time.Time
	// fail, when set, makes every request return this status
	fail int
}

func newFakeDAV(t *testing.T) (*httptest.Server, *fakeDAV) {
	t.Helper()
	d := &fakeDAV{
		files: map[string][]byte{},
		dirs:  map[string]bool{"/": true},
		mtime: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	srv := httptest.NewServer(d)
	t.Cleanup(srv.Close)
	return srv, d
}

// ServeHTTP routes a request by method to the matching handler.
func (d *fakeDAV) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.fail != 0 {
		w.WriteHeader(d.fail)
		return
	}

	p := path.Clean(r.URL.Path)
	switch r.Method {
	case http.MethodGet:
		d.get(w, p)
	case http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		d.files[p] = body
		w.WriteHeader(http.StatusCreated)
	case "PROPFIND":
		d.propfind(w, p, r.Header.Get("Depth") == "1")
	case "MKCOL":
		d.mkcol(w, p)
	case http.MethodDelete:
		d.remove(w, p)
	case "MOVE":
		d.move(w, r, p)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (d *fakeDAV) get(w http.ResponseWriter, p string) {
	data, ok := d.files[p]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_, _ = w.Write(data)
}

func (d *fakeDAV) mkcol(w http.ResponseWriter, p string) {
	if d.dirs[p] || d.files[p] != nil {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	d.dirs[p] = true
	w.WriteHeader(http.StatusCreated)
}

// remove deletes a file, or a directory with everything beneath it.
func (d *fakeDAV) remove(w http.ResponseWriter, p string) {
	found := false
	if _, ok := d.files[p]; ok {
		delete(d.files, p)
		found = true
	}
	if d.dirs[p] {
		d.removeTree(p)
		found = true
	}
	if !found {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d *fakeDAV) removeTree(p string) {
	for k := range d.files {
		if strings.HasPrefix(k, p+"/") {
			delete(d.files, k)
		}
	}
	for k := range d.dirs {
		if k == p || strings.HasPrefix(k, p+"/") {
			delete(d.dirs, k)
		}
	}
}

func (d *fakeDAV) move(w http.ResponseWriter, r *http.Request, p string) {
	dest, err := url.Parse(r.Header.Get("Destination"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	data, ok := d.files[p]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	delete(d.files, p)
	d.files[path.Clean(dest.Path)] = data
	w.WriteHeader(http.StatusCreated)
}

// davEntry is one item in a multistatus response.
type davEntry struct {
	href string
	dir  bool
	size int
}

// propfind writes a multistatus body for p. Depth 1 also lists direct children.
func (d *fakeDAV) propfind(w http.ResponseWriter, p string, depth1 bool) {
	entries, found := d.entriesFor(p, depth1)
	if !found {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><D:multistatus xmlns:D="DAV:">`)
	for _, e := range entries {
		resType := ""
		if e.dir {
			resType = "<D:collection/>"
		}
		fmt.Fprintf(&b, `<D:response><D:href>%s</D:href><D:propstat><D:prop>`+
			`<D:resourcetype>%s</D:resourcetype>`+
			`<D:getcontentlength>%s</D:getcontentlength>`+
			`<D:getlastmodified>%s</D:getlastmodified>`+
			`</D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>`,
			e.href, resType, strconv.Itoa(e.size), d.mtime.UTC().Format(time.RFC1123))
	}
	b.WriteString(`</D:multistatus>`)
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusMultiStatus)
	_, _ = io.WriteString(w, b.String())
}

// entriesFor returns what a PROPFIND on p reports, and whether p exists.
func (d *fakeDAV) entriesFor(p string, depth1 bool) ([]davEntry, bool) {
	if data, ok := d.files[p]; ok {
		return []davEntry{{href: p, size: len(data)}}, true
	}
	if !d.dirs[p] {
		return nil, false
	}
	entries := []davEntry{{href: p, dir: true}}
	if depth1 {
		entries = append(entries, d.childrenOf(p)...)
	}
	return entries, true
}

// childrenOf lists the direct children of directory p, sorted by name.
func (d *fakeDAV) childrenOf(p string) []davEntry {
	prefix := strings.TrimSuffix(p, "/") + "/"
	if p == "/" {
		prefix = "/"
	}
	direct := func(name string) bool {
		return strings.HasPrefix(name, prefix) && name != p && !strings.Contains(strings.TrimPrefix(name, prefix), "/")
	}

	var kids []string
	for f := range d.files {
		if direct(f) {
			kids = append(kids, f)
		}
	}
	for dname := range d.dirs {
		if direct(dname) {
			kids = append(kids, dname)
		}
	}
	sort.Strings(kids)

	entries := make([]davEntry, 0, len(kids))
	for _, k := range kids {
		if d.dirs[k] {
			entries = append(entries, davEntry{href: k, dir: true})
		} else {
			entries = append(entries, davEntry{href: k, size: len(d.files[k])})
		}
	}
	return entries
}

// newFakeDAVBackend returns a webDavBackendImpl wired to a fake WebDAV server.
func newFakeDAVBackend(t *testing.T, basePath string) (*webDavBackendImpl, *fakeDAV) {
	t.Helper()
	srv, d := newFakeDAV(t)
	return &webDavBackendImpl{
		client:   srv.Client(),
		endpoint: srv.URL,
		basePath: basePath,
		username: "u",
		password: "p",
	}, d
}

func TestWebDavBackendImpl_FileOperations(t *testing.T) {
	w, d := newFakeDAVBackend(t, "")

	n, err := w.WriteFile("/docs/a.txt", strings.NewReader("hello"))
	require.NoError(t, err)
	assert.Equal(t, int64(5), n)
	assert.Equal(t, "hello", string(d.files["/docs/a.txt"]))

	info, err := w.Stat("/docs/a.txt")
	require.NoError(t, err)
	assert.Equal(t, int64(5), info.Size)
	assert.False(t, info.IsDir)
	assert.Equal(t, "a.txt", info.Name)
	assert.Equal(t, d.mtime.Unix(), info.ModTime.Unix())

	ok, err := w.Exists("/docs/a.txt")
	require.NoError(t, err)
	assert.True(t, ok)

	rc, err := w.Open("/docs/a.txt")
	require.NoError(t, err)
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	assert.Equal(t, "hello", string(data))

	require.NoError(t, w.Rename("/docs/a.txt", "/docs/b.txt"))
	_, exists := d.files["/docs/a.txt"]
	assert.False(t, exists)
	assert.Equal(t, "hello", string(d.files["/docs/b.txt"]))

	require.NoError(t, w.Remove("/docs/b.txt"))
	_, exists = d.files["/docs/b.txt"]
	assert.False(t, exists)
}

func TestWebDavBackendImpl_Stat_DirectoryAndMissing(t *testing.T) {
	w, d := newFakeDAVBackend(t, "")
	d.dirs["/reports"] = true

	info, err := w.Stat("/reports")
	require.NoError(t, err)
	assert.True(t, info.IsDir)
	assert.Equal(t, uint32(0o755), uint32(info.Mode))

	_, err = w.Stat("/absent")
	assert.ErrorContains(t, err, "file not found")

	ok, err := w.Exists("/absent")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestWebDavBackendImpl_DirectoryOperations(t *testing.T) {
	w, d := newFakeDAVBackend(t, "")

	require.NoError(t, w.Mkdir("/tree"))
	assert.True(t, d.dirs["/tree"])
	assert.Error(t, w.Mkdir("/tree"), "creating an existing directory fails")

	d.files["/tree/a.txt"] = []byte("a")
	d.dirs["/tree/sub"] = true
	d.files["/tree/sub/b.txt"] = []byte("bb")

	entries, err := w.ReadDir("/tree")
	require.NoError(t, err)
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = e.IsDir
	}
	assert.Equal(t, map[string]bool{"a.txt": false, "sub": true}, names, "the directory itself is not listed")

	require.NoError(t, w.RemoveAll("/tree"))
	_, ok := d.dirs["/tree"]
	assert.False(t, ok)
	_, ok = d.files["/tree/sub/b.txt"]
	assert.False(t, ok)
}

func TestWebDavBackendImpl_ErrorStatuses(t *testing.T) {
	w, d := newFakeDAVBackend(t, "")
	d.fail = http.StatusInternalServerError

	_, err := w.WriteFile("/x", strings.NewReader("x"))
	assert.ErrorContains(t, err, "PUT failed with status: 500")

	_, err = w.Stat("/x")
	assert.ErrorContains(t, err, "PROPFIND failed with status: 500")

	_, err = w.Open("/x")
	assert.Error(t, err)

	err = w.Remove("/x")
	assert.Error(t, err)

	_, err = w.ReadDir("/")
	assert.ErrorContains(t, err, "PROPFIND failed with status: 500")
}

func TestWebDavBackendImpl_GetFullPathWithBase(t *testing.T) {
	tests := []struct {
		base, file, want string
	}{
		{"", "/a.txt", "/a.txt"},
		{"ftp", "a.txt", "/ftp/a.txt"},
		{"/ftp/", "/a.txt", "/ftp/a.txt"},
	}
	for _, tt := range tests {
		w := &webDavBackendImpl{basePath: tt.base}
		assert.Equal(t, tt.want, w.getFullPath(tt.file), "base=%q file=%q", tt.base, tt.file)
	}
}

// With a base path, every operation must be confined under it on the server.
func TestWebDavBackendImpl_BasePathIsApplied(t *testing.T) {
	w, d := newFakeDAVBackend(t, "/ftp")
	d.dirs["/ftp"] = true

	_, err := w.WriteFile("a.txt", strings.NewReader("base"))
	require.NoError(t, err)
	assert.Equal(t, "base", string(d.files["/ftp/a.txt"]))
}

// Every operation must surface a server error status rather than succeed.
func TestWebDavBackendImpl_EveryOperationReportsServerErrors(t *testing.T) {
	w, d := newFakeDAVBackend(t, "")
	d.fail = http.StatusServiceUnavailable

	_, err := w.Open("/a")
	assert.Error(t, err)
	_, err = w.Stat("/a")
	assert.Error(t, err)
	_, err = w.ReadDir("/")
	assert.Error(t, err)
	assert.Error(t, w.Remove("/a"))
	assert.Error(t, w.RemoveAll("/a"))
	assert.Error(t, w.Rename("/a", "/b"))
	assert.Error(t, w.Mkdir("/a"))
}

// Construction contacts the server; a refused credential must fail construction.
func TestNewWebDavBackendImpl_RejectsServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	backend := &ftpv1.WebDavBackend{
		ObjectMeta: metav1.ObjectMeta{Name: "dav", Namespace: "default"},
		Spec: ftpv1.WebDavBackendSpec{
			Endpoint:    srv.URL,
			Credentials: ftpv1.WebDavCredentials{Username: "u", Password: "wrong"},
		},
	}
	_, err := newWebDavBackendImpl(context.Background(), backend, fake.NewClientBuilder().Build())
	assert.ErrorContains(t, err, "WebDAV server returned error: 401")
}

// When the server cannot be reached at all, every operation must report the
// transport failure rather than returning a half-result.
func TestWebDavBackendImpl_UnreachableServerFailsEveryOperation(t *testing.T) {
	w := &webDavBackendImpl{client: &http.Client{}, endpoint: unreachableURL(t), username: "u", password: "p"}

	_, err := w.Open("/a")
	assert.ErrorContains(t, err, "GET request failed")
	_, err = w.Stat("/a")
	assert.ErrorContains(t, err, "PROPFIND request failed")
	_, err = w.ReadDir("/")
	assert.ErrorContains(t, err, "PROPFIND request failed")
	_, err = w.WriteFile("/a", strings.NewReader("x"))
	assert.ErrorContains(t, err, "PUT request failed")
	assert.Error(t, w.Remove("/a"))
	assert.Error(t, w.RemoveAll("/a"))
	assert.Error(t, w.Rename("/a", "/b"))
	assert.Error(t, w.Mkdir("/a"))

	ok, err := w.Exists("/a")
	assert.False(t, ok)
	assert.Error(t, err)
}

// A backend whose inline CA bundle is not PEM must fail construction, rather than
// silently falling back to the system roots.
func TestNewWebDavBackendImpl_RejectsBadCABundle(t *testing.T) {
	backend := &ftpv1.WebDavBackend{
		ObjectMeta: metav1.ObjectMeta{Name: "dav", Namespace: "default"},
		Spec: ftpv1.WebDavBackendSpec{
			Endpoint:    "https://127.0.0.1:1",
			Credentials: ftpv1.WebDavCredentials{Username: "u", Password: "p"},
			TLS:         &ftpv1.WebDavTLSConfig{CACert: "not a certificate"},
		},
	}
	_, err := newWebDavBackendImpl(context.Background(), backend, fake.NewClientBuilder().Build())
	assert.ErrorContains(t, err, "failed to build TLS config")
}

// unreachableURL returns the address of a server that is already closed.
func unreachableURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	return url
}
