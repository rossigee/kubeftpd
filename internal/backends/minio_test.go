package backends

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

func TestGetMinioCredentialsFromSecret_UseBackendNamespace(t *testing.T) {
	// Test that when no namespace is specified in secretRef, it uses the backend's namespace
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, ftpv1.AddToScheme(scheme))

	// Create a secret in the 'kubeftpd' namespace
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-credentials",
			Namespace: "kubeftpd",
		},
		Data: map[string][]byte{
			"accessKeyID":     []byte("test-access-key"),
			"secretAccessKey": []byte("test-secret-key"),
		},
	}

	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(secret).
		Build()

	secretRef := &ftpv1.MinioSecretRef{
		Name: "test-credentials",
		// Namespace is nil - should default to backend namespace
	}

	accessKey, secretKey, err := getMinioCredentialsFromSecret(secretRef, "kubeftpd", kubeClient)

	assert.NoError(t, err)
	assert.Equal(t, "test-access-key", accessKey)
	assert.Equal(t, "test-secret-key", secretKey)
}

func TestGetMinioCredentialsFromSecret_ExplicitNamespace(t *testing.T) {
	// Test that explicit namespace in secretRef is respected
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, ftpv1.AddToScheme(scheme))

	// Create a secret in the 'custom' namespace
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-credentials",
			Namespace: "custom",
		},
		Data: map[string][]byte{
			"accessKeyID":     []byte("test-access-key"),
			"secretAccessKey": []byte("test-secret-key"),
		},
	}

	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(secret).
		Build()

	explicitNamespace := "custom"
	secretRef := &ftpv1.MinioSecretRef{
		Name:      "test-credentials",
		Namespace: &explicitNamespace,
	}

	// Backend is in 'kubeftpd' namespace, but secret should be found in 'custom' namespace
	accessKey, secretKey, err := getMinioCredentialsFromSecret(secretRef, "kubeftpd", kubeClient)

	assert.NoError(t, err)
	assert.Equal(t, "test-access-key", accessKey)
	assert.Equal(t, "test-secret-key", secretKey)
}

func TestGetMinioCredentialsFromSecret_RegressionTest_NoDefaultNamespace(t *testing.T) {
	// Regression test: ensure we never look in 'default' namespace unless explicitly specified
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, ftpv1.AddToScheme(scheme))

	// Create secrets in multiple namespaces
	secretInDefault := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-credentials",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"accessKeyID":     []byte("wrong-access-key"),
			"secretAccessKey": []byte("wrong-secret-key"),
		},
	}

	secretInKubeftpd := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-credentials",
			Namespace: "kubeftpd",
		},
		Data: map[string][]byte{
			"accessKeyID":     []byte("correct-access-key"),
			"secretAccessKey": []byte("correct-secret-key"),
		},
	}

	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(secretInDefault, secretInKubeftpd).
		Build()

	secretRef := &ftpv1.MinioSecretRef{
		Name: "test-credentials",
		// Namespace is nil - should use backend namespace (kubeftpd), NOT default
	}

	accessKey, secretKey, err := getMinioCredentialsFromSecret(secretRef, "kubeftpd", kubeClient)

	assert.NoError(t, err)
	// Should get the secret from kubeftpd namespace, not default
	assert.Equal(t, "correct-access-key", accessKey)
	assert.Equal(t, "correct-secret-key", secretKey)
}

func TestGetMinioCredentialsFromSecret_CustomKeys(t *testing.T) {
	// Test custom key names for access key and secret key
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, ftpv1.AddToScheme(scheme))

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-credentials",
			Namespace: "kubeftpd",
		},
		Data: map[string][]byte{
			"custom-access-key": []byte("test-access-key"),
			"custom-secret-key": []byte("test-secret-key"),
		},
	}

	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(secret).
		Build()

	secretRef := &ftpv1.MinioSecretRef{
		Name:               "test-credentials",
		AccessKeyIDKey:     "custom-access-key",
		SecretAccessKeyKey: "custom-secret-key",
	}

	accessKey, secretKey, err := getMinioCredentialsFromSecret(secretRef, "kubeftpd", kubeClient)

	assert.NoError(t, err)
	assert.Equal(t, "test-access-key", accessKey)
	assert.Equal(t, "test-secret-key", secretKey)
}

func TestGetMinioCredentialsFromSecret_SecretNotFound(t *testing.T) {
	// Test error handling when secret doesn't exist
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, ftpv1.AddToScheme(scheme))

	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	secretRef := &ftpv1.MinioSecretRef{
		Name: "nonexistent-secret",
	}

	accessKey, secretKey, err := getMinioCredentialsFromSecret(secretRef, "kubeftpd", kubeClient)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get secret kubeftpd/nonexistent-secret")
	assert.Empty(t, accessKey)
	assert.Empty(t, secretKey)
}

func TestGetMinioCredentialsFromSecret_MissingKeys(t *testing.T) {
	// Test error handling when required keys are missing from secret
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, ftpv1.AddToScheme(scheme))

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "incomplete-credentials",
			Namespace: "kubeftpd",
		},
		Data: map[string][]byte{
			"accessKeyID": []byte("test-access-key"),
			// Missing secretAccessKey
		},
	}

	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(secret).
		Build()

	secretRef := &ftpv1.MinioSecretRef{
		Name: "incomplete-credentials",
	}

	accessKey, secretKey, err := getMinioCredentialsFromSecret(secretRef, "kubeftpd", kubeClient)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "secret access key not found in secret")
	assert.Empty(t, accessKey)
	assert.Empty(t, secretKey)
}

func TestGetMinioCredentialsFromSecret_NilSecretRef(t *testing.T) {
	// Test error handling when secretRef is nil
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, ftpv1.AddToScheme(scheme))

	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	accessKey, secretKey, err := getMinioCredentialsFromSecret(nil, "kubeftpd", kubeClient)

	assert.Error(t, err)
	assert.Equal(t, "secret reference is nil", err.Error())
	assert.Empty(t, accessKey)
	assert.Empty(t, secretKey)
}

// Integration-style test for the fix
func TestMinioBackendNamespaceFix_IntegrationStyle(t *testing.T) {
	// This test simulates the real-world scenario that was failing
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, ftpv1.AddToScheme(scheme))

	// Create secret in kubeftpd namespace (where MinioBackend lives)
	scannerCredentials := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "scanner-minio-credentials",
			Namespace: "kubeftpd",
		},
		Data: map[string][]byte{
			"accessKeyID":     []byte("scanner-access"),
			"secretAccessKey": []byte("scanner-secret"),
		},
	}

	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(scannerCredentials).
		Build()

	// Create MinioBackend with secret reference (no explicit namespace)
	backend := &ftpv1.MinioBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "scanner-backend",
			Namespace: "kubeftpd",
		},
		Spec: ftpv1.MinioBackendSpec{
			Endpoint: "http://minio.storage.svc.cluster.local:9000",
			Bucket:   "scanner-receipts",
			Region:   "us-east-1",
			Credentials: ftpv1.MinioCredentials{
				UseSecret: &ftpv1.MinioSecretRef{
					Name: "scanner-minio-credentials",
					// No namespace specified - should use backend's namespace
				},
			},
		},
	}

	// Test the secret lookup directly
	accessKey, secretKey, err := getMinioCredentialsFromSecret(
		backend.Spec.Credentials.UseSecret,
		backend.Namespace,
		kubeClient,
	)

	assert.NoError(t, err, "Should successfully find secret in backend's namespace")
	assert.Equal(t, "scanner-access", accessKey)
	assert.Equal(t, "scanner-secret", secretKey)
}
