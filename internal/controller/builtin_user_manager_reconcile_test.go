package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

func builtInTestConfig(anonymous, admin bool) BuiltInUserConfig {
	return BuiltInUserConfig{
		EnableAnonymous:      anonymous,
		AnonymousHomeDir:     "/pub",
		AnonymousBackendKind: "FilesystemBackend",
		AnonymousBackendName: "anon-backend",
		EnableAdmin:          admin,
		AdminPasswordSecret:  "admin-secret",
		AdminHomeDir:         "/",
		AdminBackendKind:     "FilesystemBackend",
		AdminBackendName:     "admin-backend",
		Namespace:            "default",
	}
}

func newBuiltInManager(t *testing.T, cfg BuiltInUserConfig, objs ...client.Object) (*BuiltInUserManager, client.Client) {
	t.Helper()
	c := fake.NewClientBuilder().WithScheme(backendTestScheme(t)).WithObjects(objs...).Build()
	return &BuiltInUserManager{Client: c, Scheme: backendTestScheme(t), Config: cfg}, c
}

func getUser(t *testing.T, c client.Client, name string) (*ftpv1.User, error) {
	t.Helper()
	u := &ftpv1.User{}
	err := c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, u)
	return u, err
}

func TestBuiltInUserManager_EnableCreatesBuiltInUsers(t *testing.T) {
	m, c := newBuiltInManager(t, builtInTestConfig(true, true))

	res, err := m.Reconcile(context.Background(), ctrl.Request{NamespacedName: reconcileKey("anything")})
	require.NoError(t, err)
	assert.Zero(t, res)

	anon, err := getUser(t, c, "builtin-anonymous")
	require.NoError(t, err)
	assert.Equal(t, "anonymous", anon.Spec.Username)
	assert.Equal(t, "/pub", anon.Spec.HomeDirectory)
	assert.Equal(t, "true", anon.Labels["kubeftpd.golder.org/builtin"])
	assert.True(t, anon.Spec.Permissions.Read)
	assert.False(t, anon.Spec.Permissions.Write)

	adm, err := getUser(t, c, "builtin-admin")
	require.NoError(t, err)
	assert.Equal(t, "admin", adm.Spec.Username)
	assert.True(t, adm.Spec.Permissions.Delete)
}

// Reconciling an ordinary (non built-in) User must not touch the built-in set.
func TestBuiltInUserManager_IgnoresOrdinaryUsers(t *testing.T) {
	ordinary := &ftpv1.User{ObjectMeta: metav1.ObjectMeta{Name: "alice", Namespace: "default"}}
	m, c := newBuiltInManager(t, builtInTestConfig(true, false), ordinary)

	res, err := m.Reconcile(context.Background(), ctrl.Request{NamespacedName: reconcileKey("alice")})
	require.NoError(t, err)
	assert.Zero(t, res)

	_, err = getUser(t, c, "builtin-anonymous")
	assert.Error(t, err, "no built-in user is created for an ordinary User event")
}

// Reconciling a built-in User triggers the full built-in reconcile.
func TestBuiltInUserManager_BuiltInUserEventReconciles(t *testing.T) {
	builtin := &ftpv1.User{ObjectMeta: metav1.ObjectMeta{
		Name: "builtin-anonymous", Namespace: "default",
		Labels: map[string]string{"kubeftpd.golder.org/builtin": "true"},
	}}
	m, c := newBuiltInManager(t, builtInTestConfig(false, false), builtin)

	// Anonymous is disabled, so the existing built-in User is removed.
	_, err := m.Reconcile(context.Background(), ctrl.Request{NamespacedName: reconcileKey("builtin-anonymous")})
	require.NoError(t, err)
	_, err = getUser(t, c, "builtin-anonymous")
	assert.Error(t, err)
}

func TestBuiltInUserManager_DeletedUserRecreatesBuiltIns(t *testing.T) {
	m, c := newBuiltInManager(t, builtInTestConfig(true, false))

	// Event for a User that no longer exists
	_, err := m.Reconcile(context.Background(), ctrl.Request{NamespacedName: reconcileKey("gone")})
	require.NoError(t, err)

	_, err = getUser(t, c, "builtin-anonymous")
	assert.NoError(t, err)
}

func TestBuiltInUserManager_DisableDeletesExisting(t *testing.T) {
	existing := &ftpv1.User{ObjectMeta: metav1.ObjectMeta{
		Name: "builtin-admin", Namespace: "default",
		Labels: map[string]string{"kubeftpd.golder.org/builtin": "true"},
	}}
	m, c := newBuiltInManager(t, builtInTestConfig(false, false), existing)

	require.NoError(t, m.UpdateConfig(context.Background(), builtInTestConfig(false, false)))
	_, err := getUser(t, c, "builtin-admin")
	assert.Error(t, err, "disabled admin is removed")
}

func TestBuiltInUserManager_UpdateConfigChangesExistingUser(t *testing.T) {
	m, c := newBuiltInManager(t, builtInTestConfig(true, false))
	require.NoError(t, m.UpdateConfig(context.Background(), builtInTestConfig(true, false)))

	cfg := builtInTestConfig(true, false)
	cfg.AnonymousHomeDir = "/shared"
	require.NoError(t, m.UpdateConfig(context.Background(), cfg))

	anon, err := getUser(t, c, "builtin-anonymous")
	require.NoError(t, err)
	assert.Equal(t, "/shared", anon.Spec.HomeDirectory, "drifted spec is brought back to config")
}

func TestBuiltInUserManager_AdminWithoutSecretIsRejected(t *testing.T) {
	cfg := builtInTestConfig(false, true)
	cfg.AdminPasswordSecret = ""
	m, _ := newBuiltInManager(t, cfg)

	err := m.UpdateConfig(context.Background(), cfg)
	assert.ErrorContains(t, err, "no password secret specified")
}

// faultyClient fails the chosen operation so error paths can be exercised.
type faultyClient struct {
	client.Client
	failGet, failCreate, failUpdate, failDelete bool
	err                                         error
}

func (f faultyClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if f.failGet {
		return f.err
	}
	return f.Client.Get(ctx, key, obj, opts...)
}

func (f faultyClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if f.failCreate {
		return f.err
	}
	return f.Client.Create(ctx, obj, opts...)
}

func (f faultyClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if f.failUpdate {
		return f.err
	}
	return f.Client.Update(ctx, obj, opts...)
}

func (f faultyClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if f.failDelete {
		return f.err
	}
	return f.Client.Delete(ctx, obj, opts...)
}

func TestBuiltInUserManager_APIFailuresAreReported(t *testing.T) {
	boom := errors.New("apiserver unavailable")
	existing := &ftpv1.User{ObjectMeta: metav1.ObjectMeta{
		Name: "builtin-anonymous", Namespace: "default",
		Labels: map[string]string{"kubeftpd.golder.org/builtin": "true"},
	}}

	base := func(t *testing.T, objs ...client.Object) client.Client {
		return fake.NewClientBuilder().WithScheme(backendTestScheme(t)).WithObjects(objs...).Build()
	}

	tests := []struct {
		name    string
		cfg     BuiltInUserConfig
		client  func(c client.Client) client.Client
		wantErr string
	}{
		{
			name: "get fails for anonymous",
			cfg:  builtInTestConfig(true, false),
			client: func(c client.Client) client.Client {
				return faultyClient{Client: c, failGet: true, err: boom}
			},
			wantErr: "failed to get anonymous user",
		},
		{
			name: "create fails for anonymous",
			cfg:  builtInTestConfig(true, false),
			client: func(c client.Client) client.Client {
				return faultyClient{Client: c, failCreate: true, err: boom}
			},
			wantErr: "failed to create anonymous user",
		},
		{
			name: "get fails for admin",
			cfg:  builtInTestConfig(false, true),
			client: func(c client.Client) client.Client {
				return faultyClient{Client: c, failGet: true, err: boom}
			},
			wantErr: "failed to get admin user",
		},
		{
			name: "create fails for admin",
			cfg:  builtInTestConfig(false, true),
			client: func(c client.Client) client.Client {
				return faultyClient{Client: c, failCreate: true, err: boom}
			},
			wantErr: "failed to create admin user",
		},
		{
			name: "update fails when anonymous drifted",
			cfg:  builtInTestConfig(true, false),
			client: func(c client.Client) client.Client {
				return faultyClient{Client: c, failUpdate: true, err: boom}
			},
			wantErr: "failed to update anonymous user",
		},
		{
			name: "delete fails when anonymous is disabled",
			cfg:  builtInTestConfig(false, false),
			client: func(c client.Client) client.Client {
				return faultyClient{Client: c, failDelete: true, err: boom}
			},
			wantErr: "failed to delete anonymous user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := []client.Object{}
			if tt.name == "update fails when anonymous drifted" || tt.name == "delete fails when anonymous is disabled" {
				objs = append(objs, existing.DeepCopy())
			}
			m := &BuiltInUserManager{Client: tt.client(base(t, objs...)), Scheme: backendTestScheme(t), Config: tt.cfg}
			if tt.name == "update fails when anonymous drifted" {
				// make the existing object differ from what the config wants
				drift := tt.cfg
				drift.AnonymousHomeDir = "/elsewhere"
				m.Config = drift
			}
			err := m.UpdateConfig(context.Background(), m.Config)
			assert.ErrorContains(t, err, tt.wantErr)
			assert.ErrorIs(t, err, boom)
		})
	}
}
