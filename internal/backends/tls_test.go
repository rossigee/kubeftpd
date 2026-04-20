package backends

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

// selfSignedCA returns a PEM-encoded self-signed CA certificate for testing.
func selfSignedCA(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestBuildTLSConfig_NoCA(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	cfg, err := buildTLSConfig(false, "", nil, "default", kubeClient)
	require.NoError(t, err)
	assert.False(t, cfg.InsecureSkipVerify)
	assert.Nil(t, cfg.RootCAs)
}

func TestBuildTLSConfig_InsecureSkipVerify(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	cfg, err := buildTLSConfig(true, "", nil, "default", kubeClient)
	require.NoError(t, err)
	assert.True(t, cfg.InsecureSkipVerify)
	assert.Nil(t, cfg.RootCAs)
}

func TestBuildTLSConfig_InlinePEM(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	caPEM := selfSignedCA(t)
	cfg, err := buildTLSConfig(false, string(caPEM), nil, "default", kubeClient)
	require.NoError(t, err)
	assert.NotNil(t, cfg.RootCAs)
}

func TestBuildTLSConfig_InlinePEM_Invalid(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	_, err := buildTLSConfig(false, "not-a-pem", nil, "default", kubeClient)
	assert.ErrorContains(t, err, "no valid PEM certificates found")
}

func TestBuildTLSConfig_CASecretRef(t *testing.T) {
	caPEM := selfSignedCA(t)

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "my-ca", Namespace: "certs"},
		Data:       map[string][]byte{"ca.crt": caPEM},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()

	ns := "certs"
	ref := &ftpv1.TLSCASecretRef{Name: "my-ca", Namespace: &ns, Key: "ca.crt"}
	cfg, err := buildTLSConfig(false, "", ref, "default", kubeClient)
	require.NoError(t, err)
	assert.NotNil(t, cfg.RootCAs)
}

func TestBuildTLSConfig_CASecretRef_DefaultNamespace(t *testing.T) {
	caPEM := selfSignedCA(t)

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "my-ca", Namespace: "backend-ns"},
		Data:       map[string][]byte{"ca.crt": caPEM},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()

	// Namespace field omitted — should fall back to backendNamespace
	ref := &ftpv1.TLSCASecretRef{Name: "my-ca"}
	cfg, err := buildTLSConfig(false, "", ref, "backend-ns", kubeClient)
	require.NoError(t, err)
	assert.NotNil(t, cfg.RootCAs)
}

func TestBuildTLSConfig_CASecretRef_DefaultKey(t *testing.T) {
	caPEM := selfSignedCA(t)

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "my-ca", Namespace: "default"},
		Data:       map[string][]byte{"ca.crt": caPEM}, // default key
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()

	// Key field omitted — should default to "ca.crt"
	ref := &ftpv1.TLSCASecretRef{Name: "my-ca"}
	cfg, err := buildTLSConfig(false, "", ref, "default", kubeClient)
	require.NoError(t, err)
	assert.NotNil(t, cfg.RootCAs)
}

func TestBuildTLSConfig_CASecretRef_SecretNotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	ref := &ftpv1.TLSCASecretRef{Name: "missing-ca"}
	_, err := buildTLSConfig(false, "", ref, "default", kubeClient)
	assert.ErrorContains(t, err, "failed to get CA secret")
}

func TestBuildTLSConfig_CASecretRef_KeyNotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "my-ca", Namespace: "default"},
		Data:       map[string][]byte{"wrong-key": []byte("data")},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()

	ref := &ftpv1.TLSCASecretRef{Name: "my-ca", Key: "ca.crt"}
	_, err := buildTLSConfig(false, "", ref, "default", kubeClient)
	assert.ErrorContains(t, err, `key "ca.crt" not found`)
}

func TestBuildTLSConfig_CASecretRef_TakesPrecedenceOverInlinePEM(t *testing.T) {
	caPEM := selfSignedCA(t)

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "my-ca", Namespace: "default"},
		Data:       map[string][]byte{"ca.crt": caPEM},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()

	ref := &ftpv1.TLSCASecretRef{Name: "my-ca"}
	// Provide both — CASecretRef must win; if it were ignored, the missing-secret path
	// would never be exercised. We verify by pointing the ref at a non-existent secret
	// to prove the ref path (not the inline path) was taken.
	ref2 := &ftpv1.TLSCASecretRef{Name: "does-not-exist"}
	_, err := buildTLSConfig(false, string(caPEM), ref2, "default", kubeClient)
	assert.ErrorContains(t, err, "failed to get CA secret")

	// And when the ref exists, we get a valid pool (not an error from inline PEM parsing)
	cfg, err := buildTLSConfig(false, "not-a-pem", ref, "default", kubeClient)
	require.NoError(t, err)
	assert.NotNil(t, cfg.RootCAs)
}

func TestNewMinioBackend_WithInlineCACert(t *testing.T) {
	caPEM := selfSignedCA(t)

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, ftpv1.AddToScheme(scheme))
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	backend := &ftpv1.MinioBackend{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: ftpv1.MinioBackendSpec{
			Endpoint: "https://minio.example.com:9000",
			Bucket:   "test-bucket",
			Credentials: ftpv1.MinioCredentials{
				AccessKeyID:     "access",
				SecretAccessKey: "secret",
			},
			TLS: &ftpv1.MinioTLSConfig{
				CACert: string(caPEM),
			},
		},
	}

	_, err := NewMinioBackend(backend, kubeClient)
	// Expect a connection error (no real MinIO), not a TLS config error
	assert.ErrorContains(t, err, "failed to connect to MinIO bucket")
}

func TestNewMinioBackend_WithCASecretRef(t *testing.T) {
	caPEM := selfSignedCA(t)

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, ftpv1.AddToScheme(scheme))

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "minio-ca", Namespace: "default"},
		Data:       map[string][]byte{"ca.crt": caPEM},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()

	backend := &ftpv1.MinioBackend{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: ftpv1.MinioBackendSpec{
			Endpoint: "https://minio.example.com:9000",
			Bucket:   "test-bucket",
			Credentials: ftpv1.MinioCredentials{
				AccessKeyID:     "access",
				SecretAccessKey: "secret",
			},
			TLS: &ftpv1.MinioTLSConfig{
				CASecretRef: &ftpv1.TLSCASecretRef{Name: "minio-ca"},
			},
		},
	}

	_, err := NewMinioBackend(backend, kubeClient)
	assert.ErrorContains(t, err, "failed to connect to MinIO bucket")
}

func TestNewMinioBackend_WithCASecretRef_Missing(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, ftpv1.AddToScheme(scheme))
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	backend := &ftpv1.MinioBackend{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: ftpv1.MinioBackendSpec{
			Endpoint: "https://minio.example.com:9000",
			Bucket:   "test-bucket",
			Credentials: ftpv1.MinioCredentials{
				AccessKeyID:     "access",
				SecretAccessKey: "secret",
			},
			TLS: &ftpv1.MinioTLSConfig{
				CASecretRef: &ftpv1.TLSCASecretRef{Name: "does-not-exist"},
			},
		},
	}

	_, err := NewMinioBackend(backend, kubeClient)
	assert.ErrorContains(t, err, "failed to build TLS config")
	assert.ErrorContains(t, err, "failed to get CA secret")
}
