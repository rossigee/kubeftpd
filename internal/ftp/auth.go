package ftp

import (
	"context"
	"crypto/subtle"
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
		[]string{"method", "result"},
	)

	authFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kubeftpd_auth_failures_total",
			Help: "Total number of FTP authentication failures",
		},
		[]string{"reason"},
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

func recordAuthAttempt(method, result string) {
	authAttempts.WithLabelValues(method, result).Inc()
}

func recordAuthFailure(reason string) {
	authFailures.WithLabelValues(reason).Inc()
}

// KubeAuth implements FTP authentication against Kubernetes User CRDs
type KubeAuth struct {
	client         client.Client
	userCache      sync.Map // Thread-safe cache for User objects: string -> *ftpv1.User
	sessionUserMap sync.Map // Thread-safe map for session-based authentication: sessionID -> string
	bruteForce     *BruteForceProtector
}

// NewKubeAuth creates a new KubeAuth instance
func NewKubeAuth(kubeClient client.Client) *KubeAuth {
	return &KubeAuth{
		client:     kubeClient,
		bruteForce: newBruteForceProtector(),
	}
}

// CheckPasswd validates user credentials against User CRDs
func (auth *KubeAuth) CheckPasswd(ctx *server.Context, username, password string) (bool, error) {
	logger := ctrl.Log.WithName("auth")
	logger.Info("Authenticating user", "username", username)

	clientIP := auth.clientIPFromCtx(ctx)

	// Reject immediately if the username or source IP is currently locked out.
	if auth.bruteForce.IsLockedOut(username, clientIP) {
		recordAuthFailure("locked_out")
		metrics.RecordUserLogin(username, "locked_out")
		return false, nil
	}

	authCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Get user from cache or Kubernetes
	user := auth.GetUser(authCtx, username)
	if user == nil {
		logger.Info("User not found", "username", username)
		auth.bruteForce.RecordFailure(username, clientIP)
		metrics.RecordUserLogin(username, "user_not_found")
		return false, nil
	}

	// Check if user is enabled
	if !user.Spec.Enabled {
		logger.Info("User is disabled", "username", username)
		auth.bruteForce.RecordFailure(username, clientIP)
		recordAuthFailure("user_disabled")
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
		recordAuthAttempt("anonymous", "success")
	case "admin":
		// Admin users must authenticate against secret
		authenticated, err = auth.checkAdminPassword(authCtx, user, password)
		if err != nil {
			logger.Error(err, "Failed to check admin password", "username", username)
			recordAuthFailure("secret_error")
			recordAuthAttempt("admin", "failure")
			return false, nil
		}
		if authenticated {
			recordAuthAttempt("admin", "success")
		} else {
			logger.Info("Invalid password for admin user", "username", username)
			recordAuthFailure("invalid_password")
			recordAuthAttempt("admin", "failure")
		}
	default: // "regular"
		// Regular users use existing password validation logic
		authenticated, err = auth.checkRegularUserPassword(authCtx, user, password)
		if err != nil {
			logger.Error(err, "Failed to check password for user", "username", username)
			recordAuthFailure("secret_error")
			recordAuthAttempt("regular", "failure")
			return false, nil
		}
		if authenticated {
			method := "plaintext"
			if user.Spec.PasswordSecret != nil {
				method = "secret"
			}
			recordAuthAttempt(method, "success")
		} else {
			logger.Info("Invalid password for user", "username", username)
			recordAuthFailure("invalid_password")
			recordAuthAttempt("regular", "failure")
		}
	}

	if authenticated {
		logger.Info("User authenticated successfully", "username", username, "user_type", userType)
		auth.bruteForce.RecordSuccess(username, clientIP)
		// Store in session-based map using connection identifier
		sessionID := auth.getSessionID(ctx)
		auth.setSessionUser(sessionID, username)
		metrics.RecordUserLogin(username, "success")
		return true, nil
	}

	logger.Info("User authentication failed", "username", username)
	auth.bruteForce.RecordFailure(username, clientIP)
	metrics.RecordUserLogin(username, "failure")
	return false, nil
}

// clientIPFromCtx extracts the client IP address from an FTP server context.
func (auth *KubeAuth) clientIPFromCtx(ctx *server.Context) string {
	if ctx == nil || ctx.Sess == nil {
		return "unknown"
	}
	addr := ctx.Sess.RemoteAddr()
	if addr == nil {
		return "unknown"
	}
	return addr.String()
}

// checkRegularUserPassword validates regular user passwords (existing logic)
func (auth *KubeAuth) checkRegularUserPassword(ctx context.Context, user *ftpv1.User, password string) (bool, error) {
	userPassword, err := auth.getUserPassword(ctx, user)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare([]byte(userPassword), []byte(password)) == 1, nil
}

// checkAdminPassword validates admin user passwords against Kubernetes Secret
func (auth *KubeAuth) checkAdminPassword(ctx context.Context, user *ftpv1.User, password string) (bool, error) {
	if user.Spec.PasswordSecret == nil {
		return false, fmt.Errorf("admin user has no passwordSecret configured")
	}

	userPassword, err := auth.getPasswordFromSecret(ctx, user.Spec.PasswordSecret, user.Namespace)
	if err != nil {
		return false, err
	}

	return subtle.ConstantTimeCompare([]byte(userPassword), []byte(password)) == 1, nil
}

// GetUser returns a user from cache or loads from Kubernetes
func (auth *KubeAuth) GetUser(ctx context.Context, username string) *ftpv1.User {
	// Try cache first
	if cachedUser, ok := auth.userCache.Load(username); ok {
		return cachedUser.(*ftpv1.User)
	}

	// Load from Kubernetes
	userList := &ftpv1.UserList{}
	if err := auth.client.List(ctx, userList); err != nil {
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

// Session-based authentication methods

// getSessionID generates a session identifier from connection context
// This provides a stable identifier across different contexts within the same FTP session
func (auth *KubeAuth) getSessionID(ctx *server.Context) string {
	// Use the FTP session's remote address:port which remains stable throughout the connection
	if ctx == nil || ctx.Sess == nil {
		return ""
	}

	// Use the remote address:port combination as a stable session identifier
	// This remains consistent across all contexts within the same FTP connection
	// and ensures uniqueness even when multiple connections come from the same IP
	remoteAddr := ctx.Sess.RemoteAddr()
	if remoteAddr == nil {
		// Fallback to context pointer if remote address is not available
		return fmt.Sprintf("session-%p", ctx)
	}

	// remoteAddr.String() already includes both IP and port (e.g., "192.168.1.100:54321")
	// This ensures each connection gets a unique session ID even from the same client IP
	return fmt.Sprintf("ftp-session-%s", remoteAddr.String())
}

// setSessionUser safely sets the authenticated user for a session
func (auth *KubeAuth) setSessionUser(sessionID, username string) {
	if sessionID != "" {
		auth.sessionUserMap.Store(sessionID, username)
	}
}

// GetSessionUser safely gets the authenticated user for a session
func (auth *KubeAuth) GetSessionUser(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	if username, ok := auth.sessionUserMap.Load(sessionID); ok {
		return username.(string)
	}
	return ""
}

// ClearSessionUser removes the authenticated user mapping for a session
func (auth *KubeAuth) ClearSessionUser(sessionID string) {
	if sessionID != "" {
		auth.sessionUserMap.Delete(sessionID)
	}
}

// getUserPassword retrieves the user's password from either direct field or secret
func (auth *KubeAuth) getUserPassword(ctx context.Context, user *ftpv1.User) (string, error) {
	// If plaintext password is provided, use it
	if user.Spec.Password != "" {
		return user.Spec.Password, nil
	}

	// If secret reference is provided, retrieve from secret
	if user.Spec.PasswordSecret != nil {
		return auth.getPasswordFromSecret(ctx, user.Spec.PasswordSecret, user.Namespace)
	}

	return "", fmt.Errorf("no password or passwordSecret specified for user %s", user.Spec.Username)
}

// getPasswordFromSecret retrieves password from a Kubernetes Secret
func (auth *KubeAuth) getPasswordFromSecret(ctx context.Context, secretRef *ftpv1.UserSecretRef, userNamespace string) (string, error) {
	if secretRef == nil {
		return "", fmt.Errorf("secret reference is nil")
	}

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
