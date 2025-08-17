package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/secsy/goftp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

var _ = Describe("FilesystemBackend E2E Tests", func() {
	var (
		namespace    string
		backendName  string
		userName     string
		pvcName      string
		ctx          context.Context
		testDataPath string
	)

	BeforeEach(func() {
		ctx = context.Background()
		namespace = "kubeftpd-system"
		backendName = fmt.Sprintf("test-filesystem-backend-%d", time.Now().Unix())
		userName = fmt.Sprintf("test-filesystem-user-%d", time.Now().Unix())
		pvcName = fmt.Sprintf("test-filesystem-pvc-%d", time.Now().Unix())

		// Create a unique test data path for this test
		testDataPath = fmt.Sprintf("/tmp/kubeftpd-e2e-test-%d", time.Now().Unix())
		err := os.MkdirAll(testDataPath, 0755)
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(func() {
			_ = os.RemoveAll(testDataPath)
		})
	})

	AfterEach(func() {
		// Clean up resources
		cleanupFilesystemResources(ctx, namespace, backendName, userName, pvcName)
	})

	Context("FilesystemBackend with local directory", func() {
		It("should create a working filesystem backend and allow FTP operations", func() {
			By("Creating a FilesystemBackend")
			backend := &ftpv1.FilesystemBackend{
				ObjectMeta: metav1.ObjectMeta{
					Name:      backendName,
					Namespace: namespace,
				},
				Spec: ftpv1.FilesystemBackendSpec{
					BasePath:    testDataPath,
					ReadOnly:    false,
					FileMode:    "0644",
					DirMode:     "0755",
					MaxFileSize: 10 * 1024 * 1024, // 10MB
				},
			}

			err := k8sClient.Create(ctx, backend)
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for FilesystemBackend to be ready")
			Eventually(func() bool {
				var createdBackend ftpv1.FilesystemBackend
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      backendName,
					Namespace: namespace,
				}, &createdBackend)
				if err != nil {
					return false
				}
				return createdBackend.Status.Ready
			}, time.Minute*2, time.Second*5).Should(BeTrue())

			By("Creating a User with the FilesystemBackend")
			user := &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      userName,
					Namespace: namespace,
				},
				Spec: ftpv1.UserSpec{
					Username:      "testuser",
					Password:      "testpassword",
					HomeDirectory: "/home/testuser",
					Backend: ftpv1.BackendReference{
						Kind: "FilesystemBackend",
						Name: backendName,
					},
					Permissions: ftpv1.UserPermissions{
						Read:   true,
						Write:  true,
						Delete: true,
						List:   true,
					},
				},
			}

			err = k8sClient.Create(ctx, user)
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for User to be ready")
			Eventually(func() bool {
				var createdUser ftpv1.User
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      userName,
					Namespace: namespace,
				}, &createdUser)
				if err != nil {
					return false
				}
				return createdUser.Status.Ready
			}, time.Minute*2, time.Second*5).Should(BeTrue())

			By("Testing FTP operations")
			testFilesystemFTPOperations(testDataPath)
		})
	})

	Context("FilesystemBackend with PVC", func() {
		It("should create a working filesystem backend with PVC", func() {
			By("Creating a PersistentVolumeClaim")
			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: namespace,
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{
						corev1.ReadWriteOnce,
					},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						},
					},
				},
			}

			err := k8sClient.Create(ctx, pvc)
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for PVC to be bound")
			Eventually(func() bool {
				var createdPVC corev1.PersistentVolumeClaim
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      pvcName,
					Namespace: namespace,
				}, &createdPVC)
				if err != nil {
					return false
				}
				return createdPVC.Status.Phase == corev1.ClaimBound
			}, time.Minute*5, time.Second*10).Should(BeTrue())

			By("Creating a FilesystemBackend with PVC reference")
			backend := &ftpv1.FilesystemBackend{
				ObjectMeta: metav1.ObjectMeta{
					Name:      backendName,
					Namespace: namespace,
				},
				Spec: ftpv1.FilesystemBackendSpec{
					BasePath:    testDataPath,
					ReadOnly:    false,
					FileMode:    "0644",
					DirMode:     "0755",
					MaxFileSize: 10 * 1024 * 1024, // 10MB
					VolumeClaimRef: &ftpv1.VolumeClaimReference{
						Name: pvcName,
					},
				},
			}

			err = k8sClient.Create(ctx, backend)
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for FilesystemBackend to be ready")
			Eventually(func() bool {
				var createdBackend ftpv1.FilesystemBackend
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      backendName,
					Namespace: namespace,
				}, &createdBackend)
				if err != nil {
					return false
				}
				return createdBackend.Status.Ready
			}, time.Minute*2, time.Second*5).Should(BeTrue())

			By("Verifying storage statistics are populated")
			var backend2 ftpv1.FilesystemBackend
			err = k8sClient.Get(ctx, types.NamespacedName{
				Name:      backendName,
				Namespace: namespace,
			}, &backend2)
			Expect(err).NotTo(HaveOccurred())
			Expect(backend2.Status.AvailableSpace).NotTo(BeNil())
			Expect(backend2.Status.TotalSpace).NotTo(BeNil())
			Expect(*backend2.Status.AvailableSpace).To(BeNumerically(">", 0))
			Expect(*backend2.Status.TotalSpace).To(BeNumerically(">", 0))
		})
	})

	Context("FilesystemBackend read-only mode", func() {
		It("should prevent write operations in read-only mode", func() {
			By("Creating test files in the directory")
			testFile := filepath.Join(testDataPath, "readonly-test.txt")
			err := os.WriteFile(testFile, []byte("read-only test content"), 0644)
			Expect(err).NotTo(HaveOccurred())

			By("Creating a read-only FilesystemBackend")
			backend := &ftpv1.FilesystemBackend{
				ObjectMeta: metav1.ObjectMeta{
					Name:      backendName,
					Namespace: namespace,
				},
				Spec: ftpv1.FilesystemBackendSpec{
					BasePath:    testDataPath,
					ReadOnly:    true,
					FileMode:    "0644",
					DirMode:     "0755",
					MaxFileSize: 10 * 1024 * 1024,
				},
			}

			err = k8sClient.Create(ctx, backend)
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for FilesystemBackend to be ready")
			Eventually(func() bool {
				var createdBackend ftpv1.FilesystemBackend
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      backendName,
					Namespace: namespace,
				}, &createdBackend)
				if err != nil {
					return false
				}
				return createdBackend.Status.Ready
			}, time.Minute*2, time.Second*5).Should(BeTrue())

			By("Creating a User with read-only permissions")
			user := &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      userName,
					Namespace: namespace,
				},
				Spec: ftpv1.UserSpec{
					Username:      "testuser",
					Password:      "testpassword",
					HomeDirectory: "/home/testuser",
					Backend: ftpv1.BackendReference{
						Kind: "FilesystemBackend",
						Name: backendName,
					},
					Permissions: ftpv1.UserPermissions{
						Read:   true,
						Write:  false, // No write permission
						Delete: false, // No delete permission
						List:   true,
					},
				},
			}

			err = k8sClient.Create(ctx, user)
			Expect(err).NotTo(HaveOccurred())

			By("Testing read operations work but write operations fail")
			testReadOnlyFTPOperations()
		})
	})

	Context("FilesystemBackend with invalid configuration", func() {
		It("should handle invalid base path gracefully", func() {
			By("Creating a FilesystemBackend with invalid path")
			backend := &ftpv1.FilesystemBackend{
				ObjectMeta: metav1.ObjectMeta{
					Name:      backendName,
					Namespace: namespace,
				},
				Spec: ftpv1.FilesystemBackendSpec{
					BasePath:    "/nonexistent/invalid/path",
					ReadOnly:    false,
					FileMode:    "0644",
					DirMode:     "0755",
					MaxFileSize: 10 * 1024 * 1024,
				},
			}

			err := k8sClient.Create(ctx, backend)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying FilesystemBackend shows as not ready")
			Eventually(func() bool {
				var createdBackend ftpv1.FilesystemBackend
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      backendName,
					Namespace: namespace,
				}, &createdBackend)
				if err != nil {
					return false
				}
				return !createdBackend.Status.Ready &&
					createdBackend.Status.Message == "Base path does not exist"
			}, time.Minute*2, time.Second*5).Should(BeTrue())
		})
	})
})

func testFilesystemFTPOperations(basePath string) {
	By("Connecting to FTP server")
	ftpConfig := goftp.Config{
		User:     "testuser",
		Password: "testpassword",
		Timeout:  time.Second * 30,
	}

	client, err := goftp.DialConfig(ftpConfig, "localhost:21")
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = client.Close() }()

	By("Testing directory listing")
	entries, err := client.ReadDir("/")
	Expect(err).NotTo(HaveOccurred())
	Expect(len(entries)).To(BeNumerically(">=", 0))

	By("Creating a directory")
	_, err = client.Mkdir("testdir")
	Expect(err).NotTo(HaveOccurred())

	// Verify directory was created on filesystem
	dirPath := filepath.Join(basePath, "testdir")
	_, err = os.Stat(dirPath)
	Expect(err).NotTo(HaveOccurred())

	By("Uploading a file")
	testContent := "Hello, filesystem backend!"
	reader := strings.NewReader(testContent)
	err = client.Store("testfile.txt", reader)
	Expect(err).NotTo(HaveOccurred())

	// Verify file was created on filesystem
	filePath := filepath.Join(basePath, "testfile.txt")
	fileContent, err := os.ReadFile(filePath)
	Expect(err).NotTo(HaveOccurred())
	Expect(string(fileContent)).To(Equal(testContent))

	By("Downloading the file")
	var downloadedContent bytes.Buffer
	err = client.Retrieve("testfile.txt", &downloadedContent)
	Expect(err).NotTo(HaveOccurred())
	Expect(downloadedContent.String()).To(Equal(testContent))

	By("Uploading a file to the subdirectory")
	nestedContent := "Nested file content"
	nestedReader := strings.NewReader(nestedContent)
	err = client.Store("testdir/nested.txt", nestedReader)
	Expect(err).NotTo(HaveOccurred())

	// Verify nested file was created
	nestedPath := filepath.Join(basePath, "testdir", "nested.txt")
	nestedFileContent, err := os.ReadFile(nestedPath)
	Expect(err).NotTo(HaveOccurred())
	Expect(string(nestedFileContent)).To(Equal(nestedContent))

	By("Renaming a file")
	err = client.Rename("testfile.txt", "renamed.txt")
	Expect(err).NotTo(HaveOccurred())

	// Verify file was renamed on filesystem
	oldPath := filepath.Join(basePath, "testfile.txt")
	newPath := filepath.Join(basePath, "renamed.txt")
	_, err = os.Stat(oldPath)
	Expect(os.IsNotExist(err)).To(BeTrue())
	_, err = os.Stat(newPath)
	Expect(err).NotTo(HaveOccurred())

	By("Deleting a file")
	err = client.Delete("renamed.txt")
	Expect(err).NotTo(HaveOccurred())

	// Verify file was deleted from filesystem
	_, err = os.Stat(newPath)
	Expect(os.IsNotExist(err)).To(BeTrue())

	By("Testing file size statistics")
	largeContent := strings.Repeat("Large file content. ", 1000) // ~20KB
	largeReader := strings.NewReader(largeContent)
	err = client.Store("large.txt", largeReader)
	Expect(err).NotTo(HaveOccurred())

	// Verify large file
	largePath := filepath.Join(basePath, "large.txt")
	largeFileContent, err := os.ReadFile(largePath)
	Expect(err).NotTo(HaveOccurred())
	Expect(string(largeFileContent)).To(Equal(largeContent))
}

func testReadOnlyFTPOperations() {
	By("Connecting to FTP server")
	ftpConfig := goftp.Config{
		User:     "testuser",
		Password: "testpassword",
		Timeout:  time.Second * 30,
	}

	client, err := goftp.DialConfig(ftpConfig, "localhost:21")
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = client.Close() }()

	By("Testing directory listing (should work)")
	entries, err := client.ReadDir("/")
	Expect(err).NotTo(HaveOccurred())
	Expect(len(entries)).To(BeNumerically(">=", 0))

	By("Testing file download (should work)")
	var readContent bytes.Buffer
	err = client.Retrieve("readonly-test.txt", &readContent)
	if err == nil {
		Expect(readContent.String()).To(Equal("read-only test content"))
	}

	By("Testing file upload (should fail)")
	testContent := "This should fail"
	reader := strings.NewReader(testContent)
	err = client.Store("forbidden.txt", reader)
	Expect(err).To(HaveOccurred())

	By("Testing directory creation (should fail)")
	_, err = client.Mkdir("forbidden-dir")
	Expect(err).To(HaveOccurred())

	By("Testing file deletion (should fail)")
	err = client.Delete("readonly-test.txt")
	Expect(err).To(HaveOccurred())
}

func cleanupFilesystemResources(ctx context.Context, namespace, backendName, userName, pvcName string) {
	// Clean up User
	user := &ftpv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:      userName,
			Namespace: namespace,
		},
	}
	_ = k8sClient.Delete(ctx, user, &client.DeleteOptions{})

	// Clean up FilesystemBackend
	backend := &ftpv1.FilesystemBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      backendName,
			Namespace: namespace,
		},
	}
	_ = k8sClient.Delete(ctx, backend, &client.DeleteOptions{})

	// Clean up PVC
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: namespace,
		},
	}
	_ = k8sClient.Delete(ctx, pvc, &client.DeleteOptions{})
}
