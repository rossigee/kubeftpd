package ftp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"goftp.io/server/v2"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
	"github.com/rossigee/kubeftpd/internal/metrics"
)

var (
	// Prometheus metrics for password security monitoring
	authAttempts = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kubeftpd_auth_attempts_total",
			Help: "Total number of FTP authentication attempts",
		},
		[]string{"username", "method", "result"},
	)

	authFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kubeftpd_auth_failures_total",
			Help: "Total number of FTP authentication failures",
		},
		[]string{"username", "reason"},
	)

	secretAccessErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kubeftpd_secret_access_errors_total",
			Help: "Total number of secret access errors",
		},
		[]string{"namespace", "secret_name", "error_type"},
	)

	userSecretMissing = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kubeftpd_user_secret_missing",
			Help: "Users with missing password secrets",
		},
		[]string{"namespace", "username", "secret_name"},
	)
)

// KubeAuth implements FTP authentication against Kubernetes User CRDs
type KubeAuth struct {
	client         client.Client
	userCache      sync.Map // Thread-safe cache for User objects: string -> *ftpv1.User
	contextUserMap sync.Map // Thread-safe map for session-specific authentication: *server.Context -> string
}

// NewKubeAuth creates a new KubeAuth instance
func NewKubeAuth(kubeClient client.Client) *KubeAuth {
	return &KubeAuth{
		client: kubeClient,
	}
}

// CheckPasswd validates user credentials against User CRDs
func (auth *KubeAuth) CheckPasswd(ctx *server.Context, username, password string) (bool, error) {
	logger := ctrl.Log.WithName("auth")
	logger.Info("Authenticating user", "username", username)

	// Get user from cache or Kubernetes
	user := auth.GetUser(username)
	if user == nil {
		logger.Info("User not found", "username", username)
		metrics.RecordUserLogin(username, "user_not_found")
		return false, nil
	}

	// Check if user is enabled
	if !user.Spec.Enabled {
		logger.Info("User is disabled", "username", username)
		authFailures.WithLabelValues(username, "user_disabled").Inc()
		metrics.RecordUserLogin(username, "failure")
		return false, nil
	}

	// Handle authentication based on user type
	userType := user.Spec.Type
	if userType == "" {
		userType = "regular" // default
	}

	var authenticated bool
	var err error

	switch userType {
	case "anonymous":
		// RFC 1635: anonymous FTP allows any password (typically email)
		authenticated = true
		authAttempts.WithLabelValues(username, "anonymous", "success").Inc()
	case "admin":
		// Admin users must authenticate against secret
		authenticated, err = auth.checkAdminPassword(user, password)
		if err != nil {
			logger.Error(err, "Failed to check admin password", "username", username)
			authFailures.WithLabelValues(username, "secret_error").Inc()
			authAttempts.WithLabelValues(username, "admin", "failure").Inc()
			return false, nil
		}
		if authenticated {
			authAttempts.WithLabelValues(username, "admin", "success").Inc()
		} else {
			logger.Info("Invalid password for admin user", "username", username)
			authFailures.WithLabelValues(username, "invalid_password").Inc()
			authAttempts.WithLabelValues(username, "admin", "failure").Inc()
		}
	default: // "regular"
		// Regular users use existing password validation logic
		authenticated, err = auth.checkRegularUserPassword(user, password)
		if err != nil {
			logger.Error(err, "Failed to check password for user", "username", username)
			authFailures.WithLabelValues(username, "secret_error").Inc()
			authAttempts.WithLabelValues(username, "regular", "failure").Inc()
			return false, nil
		}
		if authenticated {
			method := "plaintext"
			if user.Spec.PasswordSecret != nil {
				method = "secret"
			}
			authAttempts.WithLabelValues(username, method, "success").Inc()
		} else {
			logger.Info("Invalid password for user", "username", username)
			authFailures.WithLabelValues(username, "invalid_password").Inc()
			authAttempts.WithLabelValues(username, "regular", "failure").Inc()
		}
	}

	if authenticated {
		logger.Info("User authenticated successfully", "username", username, "user_type", userType)
		auth.setContextUser(ctx, username)
		metrics.RecordUserLogin(username, "success")
		return true, nil
	}

	logger.Info("User authentication failed", "username", username)
	metrics.RecordUserLogin(username, "failure")
	return false, nil
}

// checkRegularUserPassword validates regular user passwords (existing logic)
func (auth *KubeAuth) checkRegularUserPassword(user *ftpv1.User, password string) (bool, error) {
	userPassword, err := auth.getUserPassword(user)
	if err != nil {
		return false, err
	}
	return userPassword == password, nil
}

// checkAdminPassword validates admin user passwords against Kubernetes Secret
func (auth *KubeAuth) checkAdminPassword(user *ftpv1.User, password string) (bool, error) {
	if user.Spec.PasswordSecret == nil {
		return false, fmt.Errorf("admin user has no passwordSecret configured")
	}

	userPassword, err := auth.getPasswordFromSecret(user.Spec.PasswordSecret, user.Namespace)
	if err != nil {
		return false, err
	}

	return userPassword == password, nil
}

// GetUser returns a user from cache or loads from Kubernetes
func (auth *KubeAuth) GetUser(username string) *ftpv1.User {
	// Try cache first
	if cachedUser, ok := auth.userCache.Load(username); ok {
		return cachedUser.(*ftpv1.User)
	}

	// Load from Kubernetes
	userList := &ftpv1.UserList{}
	if err := auth.client.List(context.TODO(), userList); err != nil {
		logger := getLogger()
		logger.Error(err, "Failed to list users", "username", username)
		return nil
	}

	for _, user := range userList.Items {
		if user.Spec.Username == username {
			userCopy := user.DeepCopy()
			auth.userCache.Store(username, userCopy)
			return userCopy
		}
	}

	return nil
}

// RefreshUserCache refreshes the user cache from Kubernetes
func (auth *KubeAuth) RefreshUserCache(ctx context.Context) error {
	logger := getLogger()
	logger.Info("Refreshing user cache")

	userList := &ftpv1.UserList{}
	if err := auth.client.List(ctx, userList); err != nil {
		logger.Error(err, "Failed to refresh user cache")
		return err
	}

	// Clear existing cache and populate with fresh data
	auth.userCache.Range(func(key, value interface{}) bool {
		auth.userCache.Delete(key)
		return true
	})

	for _, user := range userList.Items {
		userCopy := user.DeepCopy()
		auth.userCache.Store(user.Spec.Username, userCopy)
	}

	logger.Info("User cache refreshed", "user_count", len(userList.Items))
	return nil
}

// StartCacheRefresh starts a background goroutine to periodically refresh the user cache
func (auth *KubeAuth) StartCacheRefresh(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger := getLogger()
			logger.Info("Stopping user cache refresh")
			return
		case <-ticker.C:
			if err := auth.RefreshUserCache(ctx); err != nil {
				logger := getLogger()
				logger.Error(err, "Failed to refresh user cache")
			}
		}
	}
}

// UpdateUser updates a user in the cache
func (auth *KubeAuth) UpdateUser(user *ftpv1.User) {
	if user != nil && user.Spec.Username != "" {
		userCopy := user.DeepCopy()
		auth.userCache.Store(user.Spec.Username, userCopy)
		logger := getLogger()
		logger.Info("Updated user in cache", "username", user.Spec.Username)
	}
}

// DeleteUser removes a user from the cache
func (auth *KubeAuth) DeleteUser(username string) {
	auth.userCache.Delete(username)
	logger := getLogger()
	logger.Info("Deleted user from cache", "username", username)
}

// setContextUser safely sets the authenticated user for a specific context
func (auth *KubeAuth) setContextUser(ctx *server.Context, username string) {
	auth.contextUserMap.Store(ctx, username)
}

// GetContextUser safely gets the authenticated user for a specific context
func (auth *KubeAuth) GetContextUser(ctx *server.Context) string {
	if username, ok := auth.contextUserMap.Load(ctx); ok {
		return username.(string)
	}
	return ""
}

// ClearContextUser removes the authenticated user mapping for a specific context
func (auth *KubeAuth) ClearContextUser(ctx *server.Context) {
	auth.contextUserMap.Delete(ctx)
}

// getUserPassword retrieves the user's password from either direct field or secret
func (auth *KubeAuth) getUserPassword(user *ftpv1.User) (string, error) {
	// If plaintext password is provided, use it
	if user.Spec.Password != "" {
		return user.Spec.Password, nil
	}

	// If secret reference is provided, retrieve from secret
	if user.Spec.PasswordSecret != nil {
		return auth.getPasswordFromSecret(user.Spec.PasswordSecret, user.Namespace)
	}

	return "", fmt.Errorf("no password or passwordSecret specified for user %s", user.Spec.Username)
}

// getPasswordFromSecret retrieves password from a Kubernetes Secret
func (auth *KubeAuth) getPasswordFromSecret(secretRef *ftpv1.UserSecretRef, userNamespace string) (string, error) {
	if secretRef == nil {
		return "", fmt.Errorf("secret reference is nil")
	}

	ctx := context.TODO()
	secretNamespace := userNamespace
	if secretRef.Namespace != nil && *secretRef.Namespace != "" {
		secretNamespace = *secretRef.Namespace
	}

	secret := &corev1.Secret{}
	err := auth.client.Get(ctx, client.ObjectKey{
		Name:      secretRef.Name,
		Namespace: secretNamespace,
	}, secret)
	if err != nil {
		// Record secret access error
		secretAccessErrors.WithLabelValues(secretNamespace, secretRef.Name, "not_found").Inc()
		userSecretMissing.WithLabelValues(secretNamespace, "unknown", secretRef.Name).Set(1)
		return "", fmt.Errorf("failed to get secret %s/%s: %w", secretNamespace, secretRef.Name, err)
	}

	passwordKey := secretRef.Key
	if passwordKey == "" {
		passwordKey = "password"
	}

	passwordBytes, exists := secret.Data[passwordKey]
	if !exists {
		// Record secret key error
		secretAccessErrors.WithLabelValues(secretNamespace, secretRef.Name, "key_not_found").Inc()
		return "", fmt.Errorf("password not found in secret with key %s", passwordKey)
	}

	// Clear the missing secret metric since we found it
	userSecretMissing.WithLabelValues(secretNamespace, "unknown", secretRef.Name).Set(0)

	return string(passwordBytes), nil
}
