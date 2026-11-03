package backends

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

// fakeS3 is a minimal in-memory S3 endpoint, enough for the minio-go calls the
// MinIO backend makes: HEAD/GET (with ranges), PUT (including copy), DELETE,
// list-objects v2, multi-object delete and multipart upload.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte // key: "bucket/object"
	uploads map[string]*multipartUpload
	mtime   time.Time
}

type multipartUpload struct {
	target string
	parts  map[int][]byte
}

func newFakeS3(t *testing.T) (*httptest.Server, *fakeS3) {
	t.Helper()
	f := &fakeS3{
		objects: map[string][]byte{},
		uploads: map[string]*multipartUpload{},
		mtime:   time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return srv, f
}

// ServeHTTP routes a request to the bucket, multipart or object handler.
func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	bucket, object, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	q := r.URL.Query()

	switch {
	case object == "":
		f.serveBucket(w, r, bucket)
	case q.Has("uploads") || q.Get("uploadId") != "":
		f.serveMultipart(w, r, bucket, object)
	default:
		f.serveObject(w, r, bucket, object)
	}
}

func (f *fakeS3) serveBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodHead:
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodGet && q.Get("list-type") == "2":
		f.list(w, bucket, q.Get("prefix"))
	case r.Method == http.MethodPost && q.Has("delete"):
		f.multiDelete(w, r, bucket)
	default:
		w.WriteHeader(http.StatusNotImplemented)
	}
}

func (f *fakeS3) serveMultipart(w http.ResponseWriter, r *http.Request, bucket, object string) {
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodPost && q.Has("uploads"):
		f.initiateMultipart(w, bucket, object)
	case r.Method == http.MethodPut:
		f.uploadPart(w, r, q.Get("uploadId"), q.Get("partNumber"))
	case r.Method == http.MethodPost:
		f.completeMultipart(w, bucket, object, q.Get("uploadId"))
	case r.Method == http.MethodDelete:
		delete(f.uploads, q.Get("uploadId"))
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotImplemented)
	}
}

func (f *fakeS3) initiateMultipart(w http.ResponseWriter, bucket, object string) {
	id := fmt.Sprintf("upload-%d", len(f.uploads)+1)
	f.uploads[id] = &multipartUpload{target: bucket + "/" + object, parts: map[int][]byte{}}
	w.Header().Set("Content-Type", "application/xml")
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>`+
		`<InitiateMultipartUploadResult><Bucket>%s</Bucket><Key>%s</Key><UploadId>%s</UploadId></InitiateMultipartUploadResult>`,
		bucket, object, id)
}

func (f *fakeS3) uploadPart(w http.ResponseWriter, r *http.Request, uploadID, partNumber string) {
	up, ok := f.uploads[uploadID]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	partNo, _ := strconv.Atoi(partNumber)
	body, _ := io.ReadAll(r.Body)
	if r.Header.Get("X-Amz-Content-Sha256") == "STREAMING-AWS4-HMAC-SHA256-PAYLOAD" {
		body, _ = decodeAWSChunked(body)
	}
	up.parts[partNo] = body
	w.Header().Set("ETag", fmt.Sprintf(`"part-%d"`, partNo))
	w.WriteHeader(http.StatusOK)
}

func (f *fakeS3) completeMultipart(w http.ResponseWriter, bucket, object, uploadID string) {
	up, ok := f.uploads[uploadID]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	nums := make([]int, 0, len(up.parts))
	for n := range up.parts {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	var data []byte
	for _, n := range nums {
		data = append(data, up.parts[n]...)
	}
	f.objects[up.target] = data
	delete(f.uploads, uploadID)
	w.Header().Set("Content-Type", "application/xml")
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>`+
		`<CompleteMultipartUploadResult><Bucket>%s</Bucket><Key>%s</Key><ETag>"complete"</ETag></CompleteMultipartUploadResult>`,
		bucket, object)
}

func (f *fakeS3) serveObject(w http.ResponseWriter, r *http.Request, bucket, object string) {
	key := bucket + "/" + object
	switch {
	case r.Method == http.MethodHead:
		f.head(w, key, object)
	case r.Method == http.MethodGet:
		f.get(w, r, key, object)
	case r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "":
		f.copyObject(w, r, key)
	case r.Method == http.MethodPut:
		f.put(w, r, key, object)
	case r.Method == http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotImplemented)
	}
}

func (f *fakeS3) head(w http.ResponseWriter, key, object string) {
	data, ok := f.objects[key]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Last-Modified", f.mtime.UTC().Format(http.TimeFormat))
	w.Header().Set("ETag", `"etag-`+object+`"`)
	w.WriteHeader(http.StatusOK)
}

func (f *fakeS3) get(w http.ResponseWriter, r *http.Request, key, object string) {
	data, ok := f.objects[key]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	status := http.StatusOK
	if rng := r.Header.Get("Range"); rng != "" {
		start, end := parseRange(rng, len(data))
		data = data[start : end+1]
		status = http.StatusPartialContent
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(f.objects[key])))
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("ETag", `"etag-`+object+`"`)
	w.Header().Set("Last-Modified", f.mtime.UTC().Format(http.TimeFormat))
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func (f *fakeS3) copyObject(w http.ResponseWriter, r *http.Request, dstKey string) {
	src := strings.TrimPrefix(r.Header.Get("X-Amz-Copy-Source"), "/")
	data, ok := f.objects[src]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	f.objects[dstKey] = append([]byte(nil), data...)
	w.Header().Set("Content-Type", "application/xml")
	_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>`+
		`<CopyObjectResult><LastModified>2026-01-02T03:04:05.000Z</LastModified><ETag>"etag-copy"</ETag></CopyObjectResult>`)
}

func (f *fakeS3) put(w http.ResponseWriter, r *http.Request, key, object string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if r.Header.Get("X-Amz-Content-Sha256") == "STREAMING-AWS4-HMAC-SHA256-PAYLOAD" {
		body, err = decodeAWSChunked(body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}
	f.objects[key] = body
	w.Header().Set("ETag", `"etag-`+object+`"`)
	w.WriteHeader(http.StatusOK)
}

// decodeAWSChunked strips the aws-chunked framing minio-go uses for signed
// payloads over plain HTTP: "<hex-size>;chunk-signature=<sig>\r\n<data>\r\n"
// repeated, ending with a zero-size chunk.
func decodeAWSChunked(b []byte) ([]byte, error) {
	var out []byte
	for {
		nl := bytes.Index(b, []byte("\r\n"))
		if nl < 0 {
			return nil, fmt.Errorf("missing chunk header")
		}
		sizeHex, _, _ := strings.Cut(string(b[:nl]), ";")
		size, err := strconv.ParseInt(sizeHex, 16, 64)
		if err != nil {
			return nil, err
		}
		b = b[nl+2:]
		if size == 0 {
			return out, nil
		}
		if int64(len(b)) < size+2 {
			return nil, fmt.Errorf("short chunk")
		}
		out = append(out, b[:size]...)
		b = b[size+2:]
	}
}

func parseRange(header string, size int) (int, int) {
	spec := strings.TrimPrefix(header, "bytes=")
	startStr, endStr, _ := strings.Cut(spec, "-")
	start, _ := strconv.Atoi(startStr)
	end := size - 1
	if endStr != "" {
		if e, err := strconv.Atoi(endStr); err == nil && e < size {
			end = e
		}
	}
	return start, end
}

type s3ListResult struct {
	XMLName  xml.Name `xml:"ListBucketResult"`
	Name     string   `xml:"Name"`
	Prefix   string   `xml:"Prefix"`
	KeyCount int      `xml:"KeyCount"`
	Contents []s3Content
}

type s3Content struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

func (f *fakeS3) list(w http.ResponseWriter, bucket, prefix string) {
	res := s3ListResult{Name: bucket, Prefix: prefix}
	for k, v := range f.objects {
		b, obj, _ := strings.Cut(k, "/")
		if b != bucket || !strings.HasPrefix(obj, prefix) {
			continue
		}
		res.Contents = append(res.Contents, s3Content{
			Key:          obj,
			LastModified: f.mtime.UTC().Format("2006-01-02T15:04:05.000Z"),
			ETag:         `"etag-` + obj + `"`,
			Size:         int64(len(v)),
			StorageClass: "STANDARD",
		})
	}
	res.KeyCount = len(res.Contents)
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(res)
}

type s3DeleteRequest struct {
	Objects []struct {
		Key string `xml:"Key"`
	} `xml:"Object"`
}

func (f *fakeS3) multiDelete(w http.ResponseWriter, r *http.Request, bucket string) {
	var req s3DeleteRequest
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?><DeleteResult>`)
	for _, o := range req.Objects {
		delete(f.objects, bucket+"/"+o.Key)
		fmt.Fprintf(&buf, "<Deleted><Key>%s</Key></Deleted>", o.Key)
	}
	buf.WriteString("</DeleteResult>")
	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write(buf.Bytes())
}

// newFakeS3Backend returns a minioBackendImpl wired to a fake S3 endpoint.
func newFakeS3Backend(t *testing.T, bucket, pathPrefix string) (*minioBackendImpl, *fakeS3) {
	t.Helper()
	srv, store := newFakeS3(t)
	client, err := minio.New(strings.TrimPrefix(srv.URL, "http://"), &minio.Options{
		Creds:      credentials.NewStaticV4("key", "secret", ""),
		Secure:     false,
		Region:     "us-east-1",
		MaxRetries: 1, // the fake never recovers, so retries only add backoff time
	})
	require.NoError(t, err)
	return &minioBackendImpl{client: client, bucket: bucket, pathPrefix: pathPrefix}, store
}

func TestMinioBackendImpl_ObjectLifecycle(t *testing.T) {
	m, _ := newFakeS3Backend(t, "ftp", "")

	require.NoError(t, m.PutObject("docs/a.txt", strings.NewReader("hello"), 5))

	info, err := m.StatObject("docs/a.txt")
	require.NoError(t, err)
	assert.Equal(t, int64(5), info.Size)
	assert.Equal(t, "docs/a.txt", info.Key)

	rc, err := m.GetObject("docs/a.txt", 0, 0)
	require.NoError(t, err)
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	assert.Equal(t, "hello", string(data))

	rc, err = m.GetObject("docs/a.txt", 1, 3)
	require.NoError(t, err)
	data, _ = io.ReadAll(rc)
	_ = rc.Close()
	assert.Equal(t, "ell", string(data), "bounded range")

	rc, err = m.GetObject("docs/a.txt", 2, 0)
	require.NoError(t, err)
	data, _ = io.ReadAll(rc)
	_ = rc.Close()
	assert.Equal(t, "llo", string(data), "open-ended range from offset")

	require.NoError(t, m.RemoveObject("docs/a.txt"))
	_, err = m.StatObject("docs/a.txt")
	assert.ErrorContains(t, err, "failed to stat object docs/a.txt")
}

// minio-go fetches lazily: GetObject returns a reader and the NoSuchKey error
// only surfaces when the body is read.
func TestMinioBackendImpl_MissingObjectErrors(t *testing.T) {
	m, _ := newFakeS3Backend(t, "ftp", "")

	rc, err := m.GetObject("nope", 0, 0)
	if err == nil {
		_, err = io.ReadAll(rc)
		_ = rc.Close()
	}
	assert.Error(t, err)
}

func TestMinioBackendImpl_StreamingUploadWithUnknownSize(t *testing.T) {
	m, store := newFakeS3Backend(t, "ftp", "")

	require.NoError(t, m.PutObject("stream.bin", strings.NewReader("streamed"), -1))
	assert.Equal(t, "streamed", string(store.objects["ftp/stream.bin"]))
}

func TestMinioBackendImpl_RemoveObjectsRecursivePrefix(t *testing.T) {
	m, store := newFakeS3Backend(t, "ftp", "")
	store.objects["ftp/dir/a"] = []byte("a")
	store.objects["ftp/dir/sub/b"] = []byte("b")
	store.objects["ftp/other"] = []byte("o")

	require.NoError(t, m.RemoveObjects("dir", true))
	_, ok := store.objects["ftp/dir/a"]
	assert.False(t, ok)
	_, ok = store.objects["ftp/dir/sub/b"]
	assert.False(t, ok)
	_, ok = store.objects["ftp/other"]
	assert.True(t, ok, "objects outside the prefix are kept")
}

func TestMinioBackendImpl_CopyObject(t *testing.T) {
	m, store := newFakeS3Backend(t, "ftp", "")
	store.objects["ftp/src.txt"] = []byte("copy me")

	require.NoError(t, m.CopyObject("src.txt", "dst.txt", false))
	assert.Equal(t, "copy me", string(store.objects["ftp/dst.txt"]))
	_, ok := store.objects["ftp/src.txt"]
	assert.True(t, ok, "source kept when deleteSource is false")

	require.NoError(t, m.CopyObject("src.txt", "moved.txt", true))
	assert.Equal(t, "copy me", string(store.objects["ftp/moved.txt"]))
	_, ok = store.objects["ftp/src.txt"]
	assert.False(t, ok, "source removed when deleteSource is true")
}

func TestMinioBackendImpl_CopyMissingSource(t *testing.T) {
	m, _ := newFakeS3Backend(t, "ftp", "")
	err := m.CopyObject("absent", "dst", false)
	assert.ErrorContains(t, err, "failed to copy object absent to dst")
}

func TestMinioBackendImpl_ListObjectsStripsPathPrefix(t *testing.T) {
	m, store := newFakeS3Backend(t, "ftp", "tenant")
	store.objects["ftp/tenant/one.txt"] = []byte("1")
	store.objects["ftp/tenant/two.txt"] = []byte("22")
	store.objects["ftp/elsewhere.txt"] = []byte("x")

	objs, err := m.ListObjects("", true)
	require.NoError(t, err)
	keys := map[string]int64{}
	for _, o := range objs {
		keys[o.Key] = o.Size
	}
	assert.Equal(t, map[string]int64{"one.txt": 1, "two.txt": 2}, keys)
}

func TestMinioBackendImpl_GetFullPath(t *testing.T) {
	tests := []struct {
		name       string
		pathPrefix string
		object     string
		want       string
	}{
		{"no prefix", "", "a/b.txt", "a/b.txt"},
		{"prefix without slash", "base", "a.txt", "base/a.txt"},
		{"prefix with slash", "base/", "a.txt", "base/a.txt"},
		{"leading slash on object", "base", "/a.txt", "base/a.txt"},
		{"no prefix keeps leading slash", "", "/a.txt", "/a.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &minioBackendImpl{pathPrefix: tt.pathPrefix}
			assert.Equal(t, tt.want, m.getFullPath(tt.object))
		})
	}
}

// A declared size larger than the data makes the upload fail rather than
// recording a truncated object.
func TestMinioBackendImpl_PutObjectFailsOnShortData(t *testing.T) {
	m, store := newFakeS3Backend(t, "ftp", "")
	err := m.PutObject("short.txt", strings.NewReader("hello"), 10)
	assert.ErrorContains(t, err, "failed to put object short.txt")
	_, stored := store.objects["ftp/short.txt"]
	assert.False(t, stored, "a failed upload leaves no object behind")
}

func TestNewMinioBackendImpl_GuardsAndSecrets(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, ftpv1.AddToScheme(scheme))
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	srv, _ := newFakeS3(t)
	spec := ftpv1.MinioBackendSpec{
		Endpoint:    srv.URL,
		Bucket:      "ftp-bucket",
		Region:      "us-east-1",
		Credentials: ftpv1.MinioCredentials{AccessKeyID: "k", SecretAccessKey: "s"},
	}
	backend := &ftpv1.MinioBackend{ObjectMeta: metav1.ObjectMeta{Name: "s3", Namespace: "default"}, Spec: spec}

	t.Run("nil context is refused", func(t *testing.T) {
		_, err := newMinioBackendImpl(nil, backend, kubeClient) //nolint:staticcheck // deliberately nil
		assert.ErrorContains(t, err, "context cannot be nil")
	})

	t.Run("cancelled context is refused before connecting", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := newMinioBackendImpl(ctx, backend, kubeClient)
		assert.ErrorContains(t, err, "context cancelled before MinIO connection test")
	})

	t.Run("missing credentials secret is reported", func(t *testing.T) {
		withSecret := backend.DeepCopy()
		withSecret.Spec.Credentials = ftpv1.MinioCredentials{UseSecret: &ftpv1.MinioSecretRef{Name: "absent"}}
		_, err := newMinioBackendImpl(context.Background(), withSecret, kubeClient)
		assert.ErrorContains(t, err, "failed to get credentials from secret")
	})

	t.Run("valid backend connects", func(t *testing.T) {
		b, err := newMinioBackendImpl(context.Background(), backend, kubeClient)
		require.NoError(t, err)
		assert.NotNil(t, b)
	})
}
