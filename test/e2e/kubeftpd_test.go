package e2e

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

func TestKubeFTPdE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "KubeFTPd E2E Suite")
}

var _ = Describe("KubeFTPd E2E Tests", func() {
	var (
		ctx          context.Context
		namespace    string
		minioBackend *ftpv1.MinioBackend
		testUser     *ftpv1.User
	)

	BeforeEach(func() {
		ctx = context.Background()
		namespace = "default"

		// Create MinioBackend for testing
		minioBackend = &ftpv1.MinioBackend{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-minio-backend",
				Namespace: namespace,
			},
			Spec: ftpv1.MinioBackendSpec{
				Endpoint: "http://minio:9000",
				Bucket:   "test-bucket",
				Region:   "us-east-1",
				Credentials: ftpv1.MinioCredentials{
					AccessKeyID:     "minioadmin",
					SecretAccessKey: "minioadmin123",
				},
				TLS: &ftpv1.MinioTLSConfig{
					InsecureSkipVerify: true,
				},
			},
		}

		// Create test user
		testUser = &ftpv1.User{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-user-e2e",
				Namespace: namespace,
			},
			Spec: ftpv1.UserSpec{
				Username: "ftpuser",
				Password: "ftppass123",
				Enabled:  true,
				Backend: ftpv1.BackendReference{
					Kind: "MinioBackend",
					Name: "test-minio-backend",
				},
				HomeDirectory: "/home/ftpuser",
				Permissions: ftpv1.UserPermissions{
					Read:   true,
					Write:  true,
					Delete: true,
				},
			},
		}
	})

	AfterEach(func() {
		// Clean up resources
		if testUser != nil {
			k8sClient.Delete(ctx, testUser)
		}
		if minioBackend != nil {
			k8sClient.Delete(ctx, minioBackend)
		}
	})

	Context("When creating MinioBackend", func() {
		It("should create and become ready", func() {
			By("Creating MinioBackend")
			Expect(k8sClient.Create(ctx, minioBackend)).To(Succeed())

			By("Waiting for MinioBackend to be ready")
			Eventually(func() bool {
				backend := &ftpv1.MinioBackend{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      minioBackend.Name,
					Namespace: minioBackend.Namespace,
				}, backend)
				if err != nil {
					return false
				}
				return backend.Status.Ready
			}, time.Minute*2, time.Second*5).Should(BeTrue())
		})
	})

	Context("When creating User", func() {
		BeforeEach(func() {
			By("Creating MinioBackend first")
			Expect(k8sClient.Create(ctx, minioBackend)).To(Succeed())

			By("Waiting for MinioBackend to be ready")
			Eventually(func() bool {
				backend := &ftpv1.MinioBackend{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      minioBackend.Name,
					Namespace: minioBackend.Namespace,
				}, backend)
				if err != nil {
					return false
				}
				return backend.Status.Ready
			}, time.Minute*2, time.Second*5).Should(BeTrue())
		})

		It("should create and become ready", func() {
			By("Creating User")
			Expect(k8sClient.Create(ctx, testUser)).To(Succeed())

			By("Waiting for User to be ready")
			Eventually(func() bool {
				user := &ftpv1.User{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      testUser.Name,
					Namespace: testUser.Namespace,
				}, user)
				if err != nil {
					return false
				}
				return user.Status.Ready
			}, time.Minute*2, time.Second*5).Should(BeTrue())

			By("Verifying User status conditions")
			user := &ftpv1.User{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testUser.Name,
				Namespace: testUser.Namespace,
			}, user)).To(Succeed())

			Expect(user.Status.Conditions).NotTo(BeEmpty())
			readyCondition := findCondition(user.Status.Conditions, "Ready")
			Expect(readyCondition).NotTo(BeNil())
			Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue))
		})

		It("should handle invalid backend reference", func() {
			By("Creating User with invalid backend")
			invalidUser := testUser.DeepCopy()
			invalidUser.Name = "invalid-user"
			invalidUser.Spec.Backend.Name = "non-existent-backend"

			Expect(k8sClient.Create(ctx, invalidUser)).To(Succeed())
			defer k8sClient.Delete(ctx, invalidUser)

			By("Waiting for User to have error status")
			Eventually(func() bool {
				user := &ftpv1.User{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      invalidUser.Name,
					Namespace: invalidUser.Namespace,
				}, user)
				if err != nil {
					return false
				}
				return !user.Status.Ready && user.Status.Message != ""
			}, time.Minute*1, time.Second*5).Should(BeTrue())
		})
	})

	Context("When updating User", func() {
		BeforeEach(func() {
			By("Creating MinioBackend")
			Expect(k8sClient.Create(ctx, minioBackend)).To(Succeed())

			By("Creating User")
			Expect(k8sClient.Create(ctx, testUser)).To(Succeed())

			By("Waiting for User to be ready")
			Eventually(func() bool {
				user := &ftpv1.User{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      testUser.Name,
					Namespace: testUser.Namespace,
				}, user)
				if err != nil {
					return false
				}
				return user.Status.Ready
			}, time.Minute*2, time.Second*5).Should(BeTrue())
		})

		It("should handle user updates", func() {
			By("Updating User permissions")
			user := &ftpv1.User{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testUser.Name,
				Namespace: testUser.Namespace,
			}, user)).To(Succeed())

			user.Spec.Permissions.Delete = false
			Expect(k8sClient.Update(ctx, user)).To(Succeed())

			By("Verifying the update was processed")
			Eventually(func() bool {
				updatedUser := &ftpv1.User{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      testUser.Name,
					Namespace: testUser.Namespace,
				}, updatedUser)
				if err != nil {
					return false
				}
				return !updatedUser.Spec.Permissions.Delete
			}, time.Second*30, time.Second*2).Should(BeTrue())
		})

		It("should handle user disable/enable", func() {
			By("Disabling User")
			user := &ftpv1.User{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testUser.Name,
				Namespace: testUser.Namespace,
			}, user)).To(Succeed())

			user.Spec.Enabled = false
			Expect(k8sClient.Update(ctx, user)).To(Succeed())

			By("Verifying User is disabled")
			Eventually(func() bool {
				updatedUser := &ftpv1.User{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      testUser.Name,
					Namespace: testUser.Namespace,
				}, updatedUser)
				if err != nil {
					return false
				}
				return !updatedUser.Spec.Enabled
			}, time.Second*30, time.Second*2).Should(BeTrue())

			By("Re-enabling User")
			user.Spec.Enabled = true
			Expect(k8sClient.Update(ctx, user)).To(Succeed())

			By("Verifying User is enabled")
			Eventually(func() bool {
				updatedUser := &ftpv1.User{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      testUser.Name,
					Namespace: testUser.Namespace,
				}, updatedUser)
				if err != nil {
					return false
				}
				return updatedUser.Spec.Enabled
			}, time.Second*30, time.Second*2).Should(BeTrue())
		})
	})

	Context("When testing controller behavior", func() {
		It("should handle multiple backends", func() {
			By("Creating multiple MinioBackends")
			backend1 := minioBackend.DeepCopy()
			backend1.Name = "backend-1"
			backend1.Spec.Bucket = "bucket-1"

			backend2 := minioBackend.DeepCopy()
			backend2.Name = "backend-2"
			backend2.Spec.Bucket = "bucket-2"

			Expect(k8sClient.Create(ctx, backend1)).To(Succeed())
			Expect(k8sClient.Create(ctx, backend2)).To(Succeed())

			defer func() {
				k8sClient.Delete(ctx, backend1)
				k8sClient.Delete(ctx, backend2)
			}()

			By("Creating users with different backends")
			user1 := testUser.DeepCopy()
			user1.Name = "user-1"
			user1.Spec.Username = "user1"
			user1.Spec.Backend.Name = "backend-1"

			user2 := testUser.DeepCopy()
			user2.Name = "user-2"
			user2.Spec.Username = "user2"
			user2.Spec.Backend.Name = "backend-2"

			Expect(k8sClient.Create(ctx, user1)).To(Succeed())
			Expect(k8sClient.Create(ctx, user2)).To(Succeed())

			defer func() {
				k8sClient.Delete(ctx, user1)
				k8sClient.Delete(ctx, user2)
			}()

			By("Verifying both users become ready")
			Eventually(func() bool {
				u1 := &ftpv1.User{}
				u2 := &ftpv1.User{}

				err1 := k8sClient.Get(ctx, types.NamespacedName{Name: user1.Name, Namespace: user1.Namespace}, u1)
				err2 := k8sClient.Get(ctx, types.NamespacedName{Name: user2.Name, Namespace: user2.Namespace}, u2)

				return err1 == nil && err2 == nil && u1.Status.Ready && u2.Status.Ready
			}, time.Minute*2, time.Second*5).Should(BeTrue())
		})
	})
})

// Helper function to find a condition by type
func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
