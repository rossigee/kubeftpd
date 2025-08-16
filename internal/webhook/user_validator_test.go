package webhook

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

func TestUserValidator_Handle(t *testing.T) {
	scheme := runtime.NewScheme()
	err := ftpv1.AddToScheme(scheme)
	assert.NoError(t, err)
	err = corev1.AddToScheme(scheme)
	assert.NoError(t, err)

	// Create test secret
	testSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-password-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"password": []byte("MyStrong97@"),
		},
	}

	// Create production namespace
	prodNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "production",
			Labels: map[string]string{
				"environment": "production",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(testSecret, prodNamespace).
		Build()

	validator := &UserValidator{
		Client: fakeClient,
	}

	tests := []struct {
		name     string
		user     *ftpv1.User
		wantDeny bool
		wantMsg  string
	}{
		{
			name: "valid user with strong plaintext password",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testuser",
					Namespace: "default",
				},
				Spec: ftpv1.UserSpec{
					Username: "testuser",
					Password: "MyStrong97@",
					Backend: ftpv1.BackendReference{
						Kind: "MinioBackend",
						Name: "test-backend",
					},
					HomeDirectory: "/home/testuser",
				},
			},
			wantDeny: false,
		},
		{
			name: "valid user with secret",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testuser",
					Namespace: "default",
				},
				Spec: ftpv1.UserSpec{
					Username: "testuser",
					PasswordSecret: &ftpv1.UserSecretRef{
						Name: "test-password-secret",
						Key:  "password",
					},
					Backend: ftpv1.BackendReference{
						Kind: "MinioBackend",
						Name: "test-backend",
					},
					HomeDirectory: "/home/testuser",
				},
			},
			wantDeny: false,
		},
		{
			name: "invalid - both password and secret",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testuser",
					Namespace: "default",
				},
				Spec: ftpv1.UserSpec{
					Username: "testuser",
					Password: "MyStrong97@",
					PasswordSecret: &ftpv1.UserSecretRef{
						Name: "test-password-secret",
						Key:  "password",
					},
					Backend: ftpv1.BackendReference{
						Kind: "MinioBackend",
						Name: "test-backend",
					},
					HomeDirectory: "/home/testuser",
				},
			},
			wantDeny: true,
			wantMsg:  "cannot specify both password and passwordSecret",
		},
		{
			name: "invalid - neither password nor secret",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testuser",
					Namespace: "default",
				},
				Spec: ftpv1.UserSpec{
					Username: "testuser",
					Backend: ftpv1.BackendReference{
						Kind: "MinioBackend",
						Name: "test-backend",
					},
					HomeDirectory: "/home/testuser",
				},
			},
			wantDeny: true,
			wantMsg:  "either password or passwordSecret must be specified",
		},
		{
			name: "invalid - weak password too short",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testuser",
					Namespace: "default",
				},
				Spec: ftpv1.UserSpec{
					Username: "testuser",
					Password: "weak",
					Backend: ftpv1.BackendReference{
						Kind: "MinioBackend",
						Name: "test-backend",
					},
					HomeDirectory: "/home/testuser",
				},
			},
			wantDeny: true,
			wantMsg:  "password must be at least 8 characters long",
		},
		{
			name: "invalid - weak password pattern",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testuser",
					Namespace: "default",
				},
				Spec: ftpv1.UserSpec{
					Username: "testuser",
					Password: "password123",
					Backend: ftpv1.BackendReference{
						Kind: "MinioBackend",
						Name: "test-backend",
					},
					HomeDirectory: "/home/testuser",
				},
			},
			wantDeny: true,
			wantMsg:  "password contains weak pattern",
		},
		{
			name: "invalid - missing complexity",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testuser",
					Namespace: "default",
				},
				Spec: ftpv1.UserSpec{
					Username: "testuser",
					Password: "lowercaseonly",
					Backend: ftpv1.BackendReference{
						Kind: "MinioBackend",
						Name: "test-backend",
					},
					HomeDirectory: "/home/testuser",
				},
			},
			wantDeny: true,
			wantMsg:  "password must contain at least one",
		},
		{
			name: "invalid - plaintext in production",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "produser",
					Namespace: "production",
				},
				Spec: ftpv1.UserSpec{
					Username: "produser",
					Password: "MyStrong97@",
					Backend: ftpv1.BackendReference{
						Kind: "MinioBackend",
						Name: "test-backend",
					},
					HomeDirectory: "/home/produser",
				},
			},
			wantDeny: true,
			wantMsg:  "plaintext passwords are not allowed in production environments",
		},
		{
			name: "invalid - secret not found",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testuser",
					Namespace: "default",
				},
				Spec: ftpv1.UserSpec{
					Username: "testuser",
					PasswordSecret: &ftpv1.UserSecretRef{
						Name: "nonexistent-secret",
						Key:  "password",
					},
					Backend: ftpv1.BackendReference{
						Kind: "MinioBackend",
						Name: "test-backend",
					},
					HomeDirectory: "/home/testuser",
				},
			},
			wantDeny: true,
			wantMsg:  "password secret default/nonexistent-secret not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Serialize the user object
			userJSON, err := json.Marshal(tt.user)
			assert.NoError(t, err)

			// Create admission request
			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: runtime.RawExtension{
						Raw: userJSON,
					},
					Namespace: tt.user.Namespace,
				},
			}

			// Mock decoder
			decoder := admission.NewDecoder(scheme)
			err = validator.InjectDecoder(&decoder)
			assert.NoError(t, err)

			// Handle the request
			resp := validator.Handle(context.Background(), req)

			if tt.wantDeny {
				assert.False(t, resp.Allowed, "Expected admission to be denied")
				if tt.wantMsg != "" {
					assert.Contains(t, resp.Result.Message, tt.wantMsg)
				}
			} else {
				assert.True(t, resp.Allowed, "Expected admission to be allowed")
			}
		})
	}
}

func TestUserValidator_validatePasswordStrength(t *testing.T) {
	validator := &UserValidator{}

	tests := []struct {
		name     string
		password string
		wantErr  bool
		errMsg   string
	}{
		{
			name:     "strong password",
			password: "MyStrong97@",
			wantErr:  false,
		},
		{
			name:     "too short",
			password: "Short1!",
			wantErr:  true,
			errMsg:   "password must be at least 8 characters long",
		},
		{
			name:     "weak pattern - password",
			password: "MyPassword123!",
			wantErr:  true,
			errMsg:   "password contains weak pattern: password",
		},
		{
			name:     "weak pattern - 123456",
			password: "Test123456!",
			wantErr:  true,
			errMsg:   "password contains weak pattern: 123456",
		},
		{
			name:     "missing uppercase",
			password: "lowercase123!",
			wantErr:  true,
			errMsg:   "password must contain at least one: uppercase letter",
		},
		{
			name:     "missing lowercase",
			password: "UPPERCASE123!",
			wantErr:  true,
			errMsg:   "password must contain at least one: lowercase letter",
		},
		{
			name:     "missing digit",
			password: "NoDigitsHere!",
			wantErr:  true,
			errMsg:   "password must contain at least one: digit",
		},
		{
			name:     "missing special character",
			password: "NoSpecialChars123",
			wantErr:  true,
			errMsg:   "password must contain at least one: special character",
		},
		{
			name:     "sequential characters",
			password: "MySecret123!",
			wantErr:  true,
			errMsg:   "password contains weak pattern",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.validatePasswordStrength(tt.password)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestUserValidator_validateProductionRestrictions(t *testing.T) {
	scheme := runtime.NewScheme()
	err := ftpv1.AddToScheme(scheme)
	assert.NoError(t, err)
	err = corev1.AddToScheme(scheme)
	assert.NoError(t, err)

	// Create production namespace
	prodNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "production",
			Labels: map[string]string{
				"environment": "production",
			},
		},
	}

	devNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "development",
			Labels: map[string]string{
				"environment": "development",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(prodNamespace, devNamespace).
		Build()

	validator := &UserValidator{
		Client: fakeClient,
	}

	tests := []struct {
		name    string
		user    *ftpv1.User
		wantErr bool
		errMsg  string
	}{
		{
			name: "production secret with correct naming",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "produser",
					Namespace: "production",
				},
				Spec: ftpv1.UserSpec{
					Username: "produser",
					PasswordSecret: &ftpv1.UserSecretRef{
						Name: "produser-ftp-password",
						Key:  "password",
					},
					Backend: ftpv1.BackendReference{
						Kind: "MinioBackend",
						Name: "prod-backend",
					},
					HomeDirectory: "/home/produser",
				},
			},
			wantErr: false,
		},
		{
			name: "production plaintext password",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "produser",
					Namespace: "production",
				},
				Spec: ftpv1.UserSpec{
					Username: "produser",
					Password: "MyStrong97@",
					Backend: ftpv1.BackendReference{
						Kind: "MinioBackend",
						Name: "prod-backend",
					},
					HomeDirectory: "/home/produser",
				},
			},
			wantErr: true,
			errMsg:  "plaintext passwords are not allowed in production environments",
		},
		{
			name: "production secret with wrong naming",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "produser",
					Namespace: "production",
				},
				Spec: ftpv1.UserSpec{
					Username: "produser",
					PasswordSecret: &ftpv1.UserSecretRef{
						Name: "wrong-name",
						Key:  "password",
					},
					Backend: ftpv1.BackendReference{
						Kind: "MinioBackend",
						Name: "prod-backend",
					},
					HomeDirectory: "/home/produser",
				},
			},
			wantErr: true,
			errMsg:  "production password secrets must follow naming convention",
		},
		{
			name: "development plaintext allowed",
			user: &ftpv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "devuser",
					Namespace: "development",
				},
				Spec: ftpv1.UserSpec{
					Username: "devuser",
					Password: "DevPassword123!",
					Backend: ftpv1.BackendReference{
						Kind: "MinioBackend",
						Name: "dev-backend",
					},
					HomeDirectory: "/home/devuser",
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.validateProductionRestrictions(context.Background(), tt.user)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
