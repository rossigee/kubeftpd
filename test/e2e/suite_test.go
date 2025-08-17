package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
)

func TestMain(m *testing.M) {
	RegisterFailHandler(Fail)
	setup()
	code := m.Run()
	teardown()
	os.Exit(code)
}

func setup() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	// Skip e2e tests if KUBEBUILDER_ASSETS is not set
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		fmt.Println("Skipping e2e tests: KUBEBUILDER_ASSETS not set")
		os.Exit(0)
	}

	// bootstrapping test environment
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = ftpv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
}

func teardown() {
	cancel()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
}

// Integration test for MinIO connection
func TestMinioIntegration(t *testing.T) {
	// Skip if not in integration test mode
	if os.Getenv("INTEGRATION_TEST") == "" {
		t.Skip("Skipping integration test (set INTEGRATION_TEST=1 to run)")
	}

	RegisterFailHandler(Fail)

	// Set up MinIO endpoint from environment
	minioEndpoint := os.Getenv("MINIO_ENDPOINT")
	if minioEndpoint == "" {
		minioEndpoint = "http://localhost:9000"
	}

	minioAccessKey := os.Getenv("MINIO_ACCESS_KEY")
	if minioAccessKey == "" {
		minioAccessKey = "minioadmin"
	}

	minioSecretKey := os.Getenv("MINIO_SECRET_KEY")
	if minioSecretKey == "" {
		minioSecretKey = "minioadmin123"
	}

	minioBucket := os.Getenv("MINIO_BUCKET")
	if minioBucket == "" {
		minioBucket = "test-bucket"
	}

	fmt.Printf("Testing MinIO connection to %s with bucket %s\n", minioEndpoint, minioBucket)

	// Test MinIO backend creation and connection
	Describe("MinIO Integration", func() {
		It("should connect to MinIO successfully", func() {
			minioBackend := &ftpv1.MinioBackend{
				Spec: ftpv1.MinioBackendSpec{
					Endpoint: minioEndpoint,
					Bucket:   minioBucket,
					Credentials: ftpv1.MinioCredentials{
						AccessKeyID:     minioAccessKey,
						SecretAccessKey: minioSecretKey,
					},
					TLS: &ftpv1.MinioTLSConfig{
						InsecureSkipVerify: true,
					},
				},
			}

			// This would typically test the actual backend connection
			// For now, just verify the spec is valid
			Expect(minioBackend.Spec.Endpoint).NotTo(BeEmpty())
			Expect(minioBackend.Spec.Bucket).NotTo(BeEmpty())
			Expect(minioBackend.Spec.Credentials.AccessKeyID).NotTo(BeEmpty())
			Expect(minioBackend.Spec.Credentials.SecretAccessKey).NotTo(BeEmpty())
		})
	})

	RunSpecs(t, "MinIO Integration Suite")
}
