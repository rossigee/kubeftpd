package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

// The user rules are checked in order; each case isolates one rule.
func TestUserReconciler_validateUserRules(t *testing.T) {
	ns := "default"
	objs := []client.Object{
		&ftpv1.MinioBackend{ObjectMeta: metav1.ObjectMeta{Name: "m", Namespace: ns}},
		&ftpv1.WebDavBackend{ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: ns}},
		&ftpv1.FilesystemBackend{ObjectMeta: metav1.ObjectMeta{Name: "f", Namespace: ns}},
	}
	r := &UserReconciler{Client: fake.NewClientBuilder().WithScheme(backendTestScheme(t)).WithObjects(objs...).Build()}

	regular := func(kind, name string) ftpv1.UserSpec {
		return ftpv1.UserSpec{
			Username:      "alice",
			Password:      "pw",
			HomeDirectory: "/alice",
			Backend:       ftpv1.BackendReference{Kind: kind, Name: name},
		}
	}
	anonymous := func() ftpv1.UserSpec {
		return ftpv1.UserSpec{
			Type:          "anonymous",
			Username:      "anonymous",
			HomeDirectory: "/pub",
			Backend:       ftpv1.BackendReference{Kind: "FilesystemBackend", Name: "f"},
			Permissions:   ftpv1.UserPermissions{Read: true},
		}
	}
	admin := func() ftpv1.UserSpec {
		return ftpv1.UserSpec{
			Type:           "admin",
			Username:       "admin",
			HomeDirectory:  "/",
			PasswordSecret: &ftpv1.UserSecretRef{Name: "admin-secret"},
			Backend:        ftpv1.BackendReference{Kind: "FilesystemBackend", Name: "f"},
		}
	}

	tests := []struct {
		name    string
		mutate  func(s *ftpv1.UserSpec)
		base    func() ftpv1.UserSpec
		wantErr string
	}{
		{name: "valid regular user on MinIO", base: func() ftpv1.UserSpec { return regular("MinioBackend", "m") }},
		{name: "valid regular user on WebDAV", base: func() ftpv1.UserSpec { return regular("WebDavBackend", "w") }},
		{name: "valid regular user on filesystem", base: func() ftpv1.UserSpec { return regular("FilesystemBackend", "f") }},
		{name: "valid anonymous user", base: anonymous},
		{name: "valid admin user", base: admin},
		{
			name:    "username required",
			base:    func() ftpv1.UserSpec { return regular("MinioBackend", "m") },
			mutate:  func(s *ftpv1.UserSpec) { s.Username = "" },
			wantErr: "username is required",
		},
		{
			name:    "homeDirectory required",
			base:    func() ftpv1.UserSpec { return regular("MinioBackend", "m") },
			mutate:  func(s *ftpv1.UserSpec) { s.HomeDirectory = "" },
			wantErr: "homeDirectory is required",
		},
		{
			name:    "unknown user type",
			base:    func() ftpv1.UserSpec { return regular("MinioBackend", "m") },
			mutate:  func(s *ftpv1.UserSpec) { s.Type = "superuser" },
			wantErr: "invalid user type: superuser",
		},
		{
			name:    "regular user needs a password source",
			base:    func() ftpv1.UserSpec { return regular("MinioBackend", "m") },
			mutate:  func(s *ftpv1.UserSpec) { s.Password = "" },
			wantErr: "either password or passwordSecret is required",
		},
		{
			name: "regular user may not have both password sources",
			base: func() ftpv1.UserSpec { return regular("MinioBackend", "m") },
			mutate: func(s *ftpv1.UserSpec) {
				s.PasswordSecret = &ftpv1.UserSecretRef{Name: "x"}
			},
			wantErr: "cannot specify both password and passwordSecret",
		},
		{
			name:    "anonymous user must not have a password",
			base:    anonymous,
			mutate:  func(s *ftpv1.UserSpec) { s.Password = "pw" },
			wantErr: "anonymous users should not have password or passwordSecret specified",
		},
		{
			name:    "anonymous user must be called anonymous",
			base:    anonymous,
			mutate:  func(s *ftpv1.UserSpec) { s.Username = "guest" },
			wantErr: "anonymous type users must have username 'anonymous'",
		},
		{
			name:    "anonymous user is read-only",
			base:    anonymous,
			mutate:  func(s *ftpv1.UserSpec) { s.Permissions.Write = true },
			wantErr: "anonymous users must have read-only permissions",
		},
		{
			name:    "admin must not use a plaintext password",
			base:    admin,
			mutate:  func(s *ftpv1.UserSpec) { s.Password = "pw" },
			wantErr: "admin users must use passwordSecret",
		},
		{
			name:    "admin requires a passwordSecret",
			base:    admin,
			mutate:  func(s *ftpv1.UserSpec) { s.PasswordSecret = nil },
			wantErr: "admin users require passwordSecret",
		},
		{
			name:    "admin must be called admin",
			base:    admin,
			mutate:  func(s *ftpv1.UserSpec) { s.Username = "root" },
			wantErr: "admin type users must have username 'admin'",
		},
		{
			name:    "unsupported backend kind",
			base:    func() ftpv1.UserSpec { return regular("SftpBackend", "m") },
			wantErr: "unsupported backend kind: SftpBackend",
		},
		{
			name:    "MinIO backend missing",
			base:    func() ftpv1.UserSpec { return regular("MinioBackend", "gone") },
			wantErr: "failed to find MinioBackend default/gone",
		},
		{
			name:    "WebDAV backend missing",
			base:    func() ftpv1.UserSpec { return regular("WebDavBackend", "gone") },
			wantErr: "failed to find WebDavBackend default/gone",
		},
		{
			name:    "filesystem backend missing",
			base:    func() ftpv1.UserSpec { return regular("FilesystemBackend", "gone") },
			wantErr: "failed to find FilesystemBackend default/gone",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := tt.base()
			if tt.mutate != nil {
				tt.mutate(&spec)
			}
			user := &ftpv1.User{ObjectMeta: metav1.ObjectMeta{Name: "u", Namespace: ns}, Spec: spec}
			err := r.validateUser(context.Background(), user)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}
