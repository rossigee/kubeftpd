package ftp

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"goftp.io/server/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
	"github.com/rossigee/kubeftpd/internal/metrics"
	"github.com/rossigee/kubeftpd/internal/storage"
)

var (
	tracer trace.Tracer
)

func init() {
	tracer = otel.Tracer("kubeftpd/ftp")
}

// getLogger returns a contextual logger for FTP operations
func getLogger() logr.Logger {
	return ctrl.Log.WithName("ftp")
}

// isFileNotFoundError checks if an error indicates a file was not found
// This helps distinguish between normal "file not found" cases (like RNFR checking)
// and actual storage errors that need attention
func isFileNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	// Check common "file not found" error patterns
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "not found") ||
		strings.Contains(errStr, "no such file") ||
		strings.Contains(errStr, "does not exist")
}

// wrapStorageError wraps storage/backend errors with a user-friendly message
// to avoid leaking implementation details to the FTP client. The original error
// is logged for debugging; the returned error provides a safe message for the client.
func wrapStorageError(err error) error {
	if err == nil {
		return nil
	}
	// Return a generic message that doesn't expose backend configuration or credentials
	return fmt.Errorf("storage system unavailable")
}

// isTracingEnabled returns true if OpenTelemetry is configured
func isTracingEnabled() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" ||
		os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != "" ||
		os.Getenv("OTEL_SERVICE_NAME") != ""
}

// Server represents the KubeFTPd server
type Server struct {
	BindAddress    string
	Port           int
	PasvPorts      string
	PublicIP       string
	WelcomeMessage string
	// TLSCertFile and TLSKeyFile are absolute paths to the server certificate and
	// private key. When both are non-empty the server enables explicit FTPS
	// (RFC 4217 / AUTH TLS) and uses a cert-watcher for hot reload.
	TLSCertFile string
	TLSKeyFile  string
	// ForceTLS requires clients to upgrade to TLS before issuing any command.
	ForceTLS bool
	// IdleTimeout closes a control connection that sends nothing for this long.
	// Zero disables it.
	IdleTimeout time.Duration
	// MaxConnections caps concurrent control connections; further clients get a
	// 421 reply. Zero means no cap.
	MaxConnections int
	client         client.Client
	server         *server.Server
}

// NewServer creates a new FTP server instance
func NewServer(bindAddress string, port int, pasvPorts string, publicIP string, welcomeMessage string, kubeClient client.Client) *Server {
	return &Server{
		BindAddress:    bindAddress,
		Port:           port,
		PasvPorts:      pasvPorts,
		PublicIP:       publicIP,
		WelcomeMessage: welcomeMessage,
		client:         kubeClient,
	}
}

// Start initializes and starts the FTP server using a custom listener.
// The server.Options.Port is intentionally set to 0 because we manage
// the TCP listener directly below. This allows us to:
// - Bind to a specific address (s.BindAddress)
// - Have full control over listener lifecycle
// - Support graceful shutdown via context
func (s *Server) Start(ctx context.Context) error {
	logger := getLogger()

	// Validate configuration before starting
	if s.Port < 0 || s.Port > 65535 {
		return fmt.Errorf("invalid port: %d (must be 0-65535)", s.Port)
	}
	if s.BindAddress == "" {
		s.BindAddress = "0.0.0.0"
	}

	logger.Info("Starting KubeFTPd server", "bind-address", s.BindAddress, "port", s.Port, "pasv-ports", s.PasvPorts)

	// Create auth instance
	auth := NewKubeAuth(s.client)

	// Start user cache refresh every 5 minutes in a tracked goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		auth.StartCacheRefresh(ctx, 5*time.Minute)
	}()

	// Create FTP server configuration
	driver := &KubeDriver{
		client: s.client,
		auth:   auth,
	}

	opts := &server.Options{
		Driver:         driver,
		Port:           0, // Don't set port when using custom listener
		Hostname:       "",
		PublicIP:       s.PublicIP,
		Auth:           auth,
		Logger:         &KubeLogger{},
		PassivePorts:   s.PasvPorts,
		WelcomeMessage: s.WelcomeMessage,
		Perm:           driver, // KubeDriver implements the Perm interface
	}

	if s.TLSCertFile != "" && s.TLSKeyFile != "" {
		cw, err := certwatcher.New(s.TLSCertFile, s.TLSKeyFile)
		if err != nil {
			return fmt.Errorf("failed to create FTP TLS cert watcher: %w", err)
		}
		// Start the cert-watcher in its own goroutine so it reloads on rotation.
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cw.Start(ctx); err != nil {
				logger.Error(err, "FTP TLS cert watcher stopped")
			}
		}()
		opts.TLS = true
		opts.ExplicitFTPS = true
		opts.ForceTLS = s.ForceTLS
		opts.TLSConfig = &tls.Config{
			GetCertificate: cw.GetCertificate,
			MinVersion:     tls.VersionTLS12,
		}
		logger.Info("FTPS enabled (explicit TLS / RFC 4217)", "cert", s.TLSCertFile, "force-tls", s.ForceTLS)
	}

	ftpServer, err := server.NewServer(opts)
	if err != nil {
		return fmt.Errorf("failed to create FTP server: %w", err)
	}
	s.server = ftpServer

	// Start the server with custom listener
	bindAddr := fmt.Sprintf("%s:%d", s.BindAddress, s.Port)
	listener, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return fmt.Errorf("failed to create listener on %s: %w", bindAddr, err)
	}
	if s.MaxConnections > 0 || s.IdleTimeout > 0 {
		listener = &limitedListener{
			Listener: listener,
			max:      s.MaxConnections,
			idle:     s.IdleTimeout,
			onRefuse: func(remote net.Addr) {
				getLogger().Info("Rejected FTP connection: connection limit reached",
					"remote", remote.String(), "max_connections", s.MaxConnections)
			},
		}
	}
	defer func() {
		_ = listener.Close()
	}()

	// Handle graceful shutdown
	go func() {
		<-ctx.Done()
		logger.Info("Shutting down FTP server", "address", bindAddr)
		if err := listener.Close(); err != nil {
			logger.Error(err, "Error closing FTP listener", "address", bindAddr)
		}
		wg.Wait()
	}()

	logger.Info("FTP server listening", "address", bindAddr, "passive_ports", s.PasvPorts)
	return ftpServer.Serve(listener)
}

// KubeLogger implements logging for the FTP server
type KubeLogger struct{}

func (kubeLogger *KubeLogger) Print(sessionId string, message interface{}) {
	logger := getLogger()
	logger.Info("FTP session message", "session_id", sessionId, "message", message)
}

func (kubeLogger *KubeLogger) Printf(sessionId string, format string, v ...interface{}) {
	logger := getLogger()
	message := fmt.Sprintf(format, v...)
	logger.Info("FTP session message", "session_id", sessionId, "message", message)
}

func (kubeLogger *KubeLogger) PrintCommand(sessionId string, command string, params string) {
	logger := getLogger()

	// Redact sensitive information in FTP commands
	logParams := params
	switch strings.ToUpper(command) {
	case "PASS":
		// Password commands should never log the actual password
		if params != "" {
			logParams = "[REDACTED]"
		}
	case "ACCT":
		// Account information may contain sensitive data
		if params != "" {
			logParams = "[REDACTED]"
		}
	}

	logger.Info("FTP command", "session_id", sessionId, "command", command, "params", logParams)
}

func (kubeLogger *KubeLogger) PrintResponse(sessionId string, code int, message string) {
	logger := getLogger()
	logger.Info("FTP response", "session_id", sessionId, "code", code, "message", message)
}

// KubeDriverFactory creates filesystem drivers for authenticated users
type KubeDriverFactory struct {
	client client.Client
	auth   *KubeAuth
}

func (factory *KubeDriverFactory) NewDriver() (server.Driver, error) {
	driver := &KubeDriver{
		client: factory.client,
		auth:   factory.auth,
	}
	// Note: authenticatedUser will be set via context mapping after authentication
	return driver, nil
}

// KubeDriver implements the FTP driver interface using Kubernetes backends.
//
// goftp.io/server/v2 gives every FTP session the same Driver value
// (server.Options.Driver) and offers no per-session driver factory, so a
// single KubeDriver serves all sessions of the process. The user and the
// storage implementation of a session therefore live in the sessions registry
// below, keyed by the session identity, and never in the driver struct: kept
// in the driver struct, the first session that completes a command would pin
// its backend for every later session, and one user's files would be written
// to another user's backend.
type KubeDriver struct {
	client client.Client
	auth   *KubeAuth

	// sessions holds the state of the live FTP sessions, keyed by session
	// identity (see sessionIdentity).
	sessionsMu sync.Mutex
	sessions   map[string]*driverSession

	// The fields below hold the state of the fallback session: the session
	// used by calls that carry no session context, which is the case for the
	// goftp Perm interface and for the driver unit tests.
	user              *ftpv1.User
	storageImpl       storage.Storage
	authenticatedUser string             // Track the authenticated username
	sessionStart      time.Time          // Track session start time
	clientIP          string             // Track client IP
	sessionID         string             // Track session ID for cleanup
	sessionCtx        context.Context    // Per-session context; cancelled in Close
	sessionCancel     context.CancelFunc // Cancels sessionCtx on connection close
}

// driverSession is the state of a single FTP session: the authenticated user
// and the storage implementation bound to the backend of that user.
type driverSession struct {
	user     *ftpv1.User
	storage  storage.Storage
	username string
	clientIP string
	start    time.Time
	lastUsed time.Time
}

// driverSessionTTL is how long an unused session is kept in the registry.
// The FTP idle timeout is 30 minutes, so a session that has been idle for
// longer than that is gone.
const driverSessionTTL = 30 * time.Minute

func (driver *KubeDriver) Init(conn *server.Context) {
	logger := getLogger()
	logger.Info("Initializing FTP driver for connection")

	driver.sessionCtx, driver.sessionCancel = context.WithCancel(context.Background())

	// Store session ID for authenticated user lookup
	driver.sessionID = driver.auth.getSessionID(conn)
	driver.sessionStart = time.Now()

	driver.clientIP = driver.auth.clientIPFromCtx(conn)

	// Record connection metrics
	username := driver.getAuthenticatedUsername(nil)
	if username != "" {
		logger.Info("Recording connection metrics", "username", username, "client_ip", driver.clientIP)
		metrics.RecordConnection(driver.clientIP)
	}
}

// sessionIdentity returns the identity of the FTP session that owns ctx and
// the username that session authenticated as.
//
// The identity is the pointer of the goftp session, which is unique per
// connection and is never reused, so two connections of the same client never
// share a session entry. The username comes from the session itself, which
// goftp sets when the PASS command succeeds; the auth session map is only a
// fallback for the case where the session does not know its user yet.
func (driver *KubeDriver) sessionIdentity(ctx *server.Context) (string, string) {
	if ctx == nil || ctx.Sess == nil {
		return "", ""
	}

	username := ctx.Sess.LoginUser()
	if username == "" && driver.auth != nil {
		username = driver.auth.GetSessionUser(driver.auth.getSessionID(ctx))
	}

	return fmt.Sprintf("sess-%p", ctx.Sess), username
}

// sessionFor returns the state of the session identified by key, creating it
// on first use. The storage implementation of a session is bound to the
// backend of the user that session authenticated as, so two sessions of
// different users never share a backend.
func (driver *KubeDriver) sessionFor(key, username, clientIP string) (*driverSession, error) {
	driver.sessionsMu.Lock()
	defer driver.sessionsMu.Unlock()

	if driver.sessions == nil {
		driver.sessions = make(map[string]*driverSession)
	}

	if sess, ok := driver.sessions[key]; ok {
		if sess.username == username {
			sess.lastUsed = time.Now()
			return sess, nil
		}
		// The connection authenticated as another user: drop the state of the
		// previous user instead of serving it from the same connection.
		logger := getLogger()
		logger.Info("FTP session changed user", "session", key, "previous_username", sess.username, "username", username)
		_ = sess.storage.Close()
		delete(driver.sessions, key)
	}

	if driver.auth == nil {
		return nil, fmt.Errorf("user %s not authenticated", username)
	}

	initCtx, initCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer initCancel()

	user := driver.auth.GetUser(initCtx, username)
	if user == nil {
		return nil, fmt.Errorf("user %s not found in auth cache", username)
	}

	storageImpl, err := storage.NewStorage(initCtx, user, driver.client)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage for user %s: %w", user.Spec.Username, err)
	}

	now := time.Now()
	sess := &driverSession{
		user:     user,
		storage:  storageImpl,
		username: username,
		clientIP: clientIP,
		start:    now,
		lastUsed: now,
	}
	driver.sessions[key] = sess
	driver.evictStaleSessions()

	return sess, nil
}

// evictStaleSessions releases the sessions that are no longer in use.
//
// goftp.io/server/v2 never calls Driver.Close: Session.Serve ends at
// sess.Close(), which only shuts the socket. This is therefore the only place
// where the storage of a finished session, and with it the transport of its
// backend, is released.
//
// A session goes when it has been idle for driverSessionTTL: the FTP server
// drops an idle connection after 30 minutes, so an entry older than that has no
// connection behind it. maxDriverSessions is then a hard bound, released by
// least recent use: a burst of new connections must not be able to hold on to
// an unbounded number of backends while the stale ones have not aged out yet.
func (driver *KubeDriver) evictStaleSessions() {
	now := time.Now()
	stale := 0
	for key, sess := range driver.sessions {
		if now.Sub(sess.lastUsed) > driverSessionTTL {
			_ = sess.storage.Close()
			delete(driver.sessions, key)
			stale++
		}
	}

	over := 0
	for len(driver.sessions) > maxDriverSessions {
		oldestKey, oldest := "", now
		for key, sess := range driver.sessions {
			if sess.lastUsed.Before(oldest) {
				oldestKey, oldest = key, sess.lastUsed
			}
		}
		if oldestKey == "" {
			break
		}
		_ = driver.sessions[oldestKey].storage.Close()
		delete(driver.sessions, oldestKey)
		over++
	}

	if stale > 0 || over > 0 {
		getLogger().Info("FTP session registry pruned",
			"stale", stale, "over_bound", over, "retained", len(driver.sessions))
	}
}

// sessionForContext returns the state of the FTP session that owns ctx.
func (driver *KubeDriver) sessionForContext(ctx *server.Context) (*driverSession, error) {
	key, username := driver.sessionIdentity(ctx)
	if key == "" {
		// No session context: fall back to the driver-level session.
		return driver.fallbackSession()
	}
	if username == "" {
		return nil, fmt.Errorf("user not authenticated")
	}

	clientIP := ""
	if driver.auth != nil {
		clientIP = driver.auth.clientIPFromCtx(ctx)
	}

	return driver.sessionFor(key, username, clientIP)
}

// fallbackSession returns the driver-level session, used by the calls that
// carry no session context.
func (driver *KubeDriver) fallbackSession() (*driverSession, error) {
	if driver.user == nil || driver.storageImpl == nil {
		if err := driver.initFallbackSession(); err != nil {
			return nil, err
		}
	}
	if driver.user == nil || driver.storageImpl == nil {
		return nil, fmt.Errorf("user not initialized")
	}

	return &driverSession{
		user:     driver.user,
		storage:  driver.storageImpl,
		username: driver.authenticatedUser,
		clientIP: driver.clientIP,
		start:    driver.sessionStart,
		lastUsed: time.Now(),
	}, nil
}

// initFallbackSession initializes the driver-level session from the
// authenticated user of the driver.
func (driver *KubeDriver) initFallbackSession() error {
	username := driver.authenticatedUser
	if username == "" && driver.auth != nil {
		username = driver.auth.GetSessionUser(driver.sessionID)
	}
	if username == "" {
		return fmt.Errorf("user not authenticated")
	}

	ctx := driver.sessionCtx
	if ctx == nil {
		ctx = context.Background()
	}

	initCtx, initCancel := context.WithTimeout(ctx, 30*time.Second)
	defer initCancel()

	user := driver.auth.GetUser(initCtx, username)
	if user == nil {
		return fmt.Errorf("user %s not found in auth cache", username)
	}

	storageImpl, err := storage.NewStorage(initCtx, user, driver.client)
	if err != nil {
		return fmt.Errorf("failed to initialize storage for user %s: %w", user.Spec.Username, err)
	}

	driver.user = user
	driver.storageImpl = storageImpl
	driver.authenticatedUser = username

	return nil
}

// resolveChrootPath converts a user's path request to the actual filesystem path within their home directory
func resolveChrootPath(requestedPath, homeDir string) string {
	// Clean the requested path
	cleanRequested := filepath.Clean(requestedPath)
	cleanHome := filepath.Clean(homeDir)

	// For chroot users, all absolute paths are relative to their home directory
	// Convert /file.txt to /home/user/file.txt
	if filepath.IsAbs(cleanRequested) {
		// Remove leading slash and join with home directory
		relativePath := strings.TrimPrefix(cleanRequested, "/")
		return filepath.Join(cleanHome, relativePath)
	}

	// Relative paths are resolved against home directory
	return filepath.Join(cleanHome, cleanRequested)
}

// chrootVirtualPath normalises a requested path into the virtual space of the
// user's home: an absolute, cleaned, POSIX-style path such as "/" or
// "/sub/file". Storage implementations join the path they receive with their
// base path, which is the user's homeDirectory, so the home must not be
// applied here as well. Applying it in both places resolved "/file" to
// "<home>/<home>/file": the user's own directory looked empty and every upload
// landed one level too deep.
func chrootVirtualPath(requestedPath string) string {
	return filepath.Join("/", filepath.Clean(requestedPath))
}

// isPathWithinHome validates that the resolved path stays within the user's home directory
func isPathWithinHome(resolvedPath, homeDir string) bool {
	// Clean and normalize both paths
	cleanResolved := filepath.Clean(resolvedPath)
	cleanHome := filepath.Clean(homeDir)

	// Ensure both paths end with separator for proper prefix matching
	if !strings.HasSuffix(cleanHome, "/") {
		cleanHome += "/"
	}
	if !strings.HasSuffix(cleanResolved, "/") {
		cleanResolved += "/"
	}

	// Check if resolved path is within or equal to home directory
	return strings.HasPrefix(cleanResolved, cleanHome) || cleanResolved == strings.TrimSuffix(cleanHome, "/")
}

// validateChrootPath checks if a path operation is allowed for a chroot user and returns the resolved path
func (sess *driverSession) validateChrootPath(path string) (string, error) {
	if sess.user == nil {
		return "", fmt.Errorf("user not initialized")
	}

	// If chroot is disabled, use path as-is
	if !sess.user.Spec.Chroot {
		return path, nil
	}

	homeDir := sess.user.Spec.HomeDirectory
	resolvedPath := resolveChrootPath(path, homeDir)

	if !isPathWithinHome(resolvedPath, homeDir) {
		logger := getLogger()
		logger.Info("CHROOT VIOLATION: Attempted access outside home directory",
			"username", sess.username, "requested_path", path, "resolved_path", resolvedPath, "home_directory", homeDir)
		return "", fmt.Errorf("access denied: path outside home directory")
	}

	logger := getLogger()
	logger.Info("Chroot path resolved", "username", sess.username, "requested_path", path, "resolved_path", resolvedPath, "storage_path", chrootVirtualPath(path))

	// The storage layer joins the user's homeDirectory with the path it is
	// given, so hand it the virtual path rather than the real one.
	return chrootVirtualPath(path), nil
}

func (driver *KubeDriver) ChangeDir(ctx *server.Context, path string) error {
	logger := getLogger()
	username := driver.getAuthenticatedUsername(ctx)
	logger.Info("FTP ChangeDir operation", "username", username, "path", path)

	sess, err := driver.sessionForContext(ctx)
	if err != nil {
		logger.Error(err, "ChangeDir failed during user initialization", "username", username, "path", path)
		return err
	}

	// Validate chroot restrictions and get resolved path
	resolvedPath, err := sess.validateChrootPath(path)
	if err != nil {
		logger.Info("ChangeDir failed due to chroot restriction", "username", username, "path", path, "error", err)
		return err
	}

	err = sess.storage.ChangeDir(resolvedPath)
	if err != nil {
		logger.Error(err, "ChangeDir operation failed", "username", username, "path", path, "resolved_path", resolvedPath)
	} else {
		logger.Info("ChangeDir operation successful", "username", username, "path", path, "resolved_path", resolvedPath)
	}
	return err
}

func (driver *KubeDriver) Stat(ctx *server.Context, path string) (os.FileInfo, error) {
	logger := getLogger()
	username := driver.getAuthenticatedUsername(ctx)
	logger.Info("FTP Stat operation", "username", username, "path", path)

	sess, err := driver.sessionForContext(ctx)
	if err != nil {
		logger.Error(err, "Stat failed during user initialization", "username", username, "path", path)
		return nil, wrapStorageError(err)
	}

	// Validate chroot restrictions and get resolved path
	resolvedPath, err := sess.validateChrootPath(path)
	if err != nil {
		logger.Info("Stat failed due to chroot restriction", "username", username, "path", path, "error", err)
		return nil, err
	}

	stat, err := sess.storage.Stat(resolvedPath)
	if err != nil {
		// File not found is a normal condition (e.g., for RNFR operations checking if file exists)
		// Only log as error if it's not a simple "file not found" case
		if isFileNotFoundError(err) {
			logger.Info("Stat operation: file not found", "username", username, "path", path, "resolved_path", resolvedPath)
		} else {
			logger.Error(err, "Stat operation failed", "username", username, "path", path, "resolved_path", resolvedPath)
		}
	} else {
		logger.Info("Stat operation successful", "username", username, "path", path, "resolved_path", resolvedPath, "size", stat.Size())
	}
	return stat, err
}

func (driver *KubeDriver) ListDir(ctx *server.Context, path string, callback func(os.FileInfo) error) error {
	username := driver.getAuthenticatedUsername(ctx)
	logger := getLogger()
	logger.Info("FTP LIST operation", "username", username, "path", path)

	sess, err := driver.sessionForContext(ctx)
	if err != nil {
		logger.Error(err, "LIST failed during user initialization", "username", username, "path", path)
		return wrapStorageError(err)
	}

	// Validate chroot restrictions and get resolved path
	resolvedPath, err := sess.validateChrootPath(path)
	if err != nil {
		return err
	}

	err = sess.storage.ListDir(resolvedPath, callback)
	if err != nil {
		logger.Error(err, "LIST operation failed", "username", username, "path", path)
	} else {
		logger.Info("LIST operation successful", "username", username, "path", path)
	}
	return err
}

func (driver *KubeDriver) DeleteDir(ctx *server.Context, path string) error {
	username := driver.getAuthenticatedUsername(ctx)
	logger := getLogger()
	logger.Info("FTP RMDIR operation", "username", username, "path", path)

	sess, err := driver.sessionForContext(ctx)
	if err != nil {
		logger.Error(err, "RMDIR failed during user initialization", "username", username, "path", path)
		return wrapStorageError(err)
	}

	// Validate chroot restrictions and get resolved path
	resolvedPath, err := sess.validateChrootPath(path)
	if err != nil {
		return err
	}

	err = sess.storage.DeleteDir(resolvedPath)
	if err != nil {
		logger.Error(err, "RMDIR operation failed", "username", username, "path", path)
	} else {
		logger.Info("RMDIR operation successful", "username", username, "path", path)
	}
	return err
}

func (driver *KubeDriver) DeleteFile(ctx *server.Context, path string) error {
	logger := getLogger()
	username := driver.getAuthenticatedUsername(ctx)
	logger.Info("FTP DELETE operation", "username", username, "path", path)

	sess, err := driver.sessionForContext(ctx)
	if err != nil {
		logger.Error(err, "DELETE failed during user initialization", "username", username, "path", path)
		return wrapStorageError(err)
	}

	// Validate chroot restrictions and get resolved path
	resolvedPath, err := sess.validateChrootPath(path)
	if err != nil {
		logger.Info("DELETE failed due to chroot restriction", "username", username, "path", path, "error", err)
		return err
	}

	err = sess.storage.DeleteFile(resolvedPath)
	if err != nil {
		// File not found is a normal condition for DELETE operations
		if isFileNotFoundError(err) {
			logger.Info("DELETE operation: file not found", "username", username, "path", path, "resolved_path", resolvedPath)
		} else {
			logger.Error(err, "DELETE operation failed", "username", username, "path", path, "resolved_path", resolvedPath)
		}
	} else {
		logger.Info("DELETE operation successful", "username", username, "path", path, "resolved_path", resolvedPath)
	}
	return err
}

func (driver *KubeDriver) Rename(ctx *server.Context, fromPath, toPath string) error {
	logger := getLogger()
	username := driver.getAuthenticatedUsername(ctx)
	logger.Info("FTP RENAME operation", "username", username, "from_path", fromPath, "to_path", toPath)

	sess, err := driver.sessionForContext(ctx)
	if err != nil {
		logger.Error(err, "RENAME failed during user initialization", "username", username, "from_path", fromPath, "to_path", toPath)
		return wrapStorageError(err)
	}

	// Validate chroot restrictions for both paths and get resolved paths
	resolvedFromPath, err := sess.validateChrootPath(fromPath)
	if err != nil {
		logger.Info("RENAME failed due to chroot restriction on source", "username", username, "from_path", fromPath, "error", err)
		return err
	}
	resolvedToPath, err := sess.validateChrootPath(toPath)
	if err != nil {
		logger.Info("RENAME failed due to chroot restriction on destination", "username", username, "to_path", toPath, "error", err)
		return err
	}

	err = sess.storage.Rename(resolvedFromPath, resolvedToPath)
	if err != nil {
		// File not found is expected for RENAME operations (RNFR checking if source exists)
		if isFileNotFoundError(err) {
			logger.Info("RENAME operation: source file not found", "username", username, "from_path", fromPath, "to_path", toPath, "resolved_from", resolvedFromPath)
		} else {
			logger.Error(err, "RENAME operation failed", "username", username, "from_path", fromPath, "to_path", toPath, "resolved_from", resolvedFromPath, "resolved_to", resolvedToPath)
		}
	} else {
		logger.Info("RENAME operation successful", "username", username, "from_path", fromPath, "to_path", toPath, "resolved_from", resolvedFromPath, "resolved_to", resolvedToPath)
	}
	return err
}

func (driver *KubeDriver) MakeDir(ctx *server.Context, path string) error {
	logger := getLogger()
	username := driver.getAuthenticatedUsername(ctx)
	logger.Info("FTP MKDIR operation", "username", username, "path", path)
	sess, err := driver.sessionForContext(ctx)
	if err != nil {
		logger.Error(err, "MKDIR failed during user initialization", "username", username, "path", path)
		return wrapStorageError(err)
	}

	// Validate chroot restrictions and get resolved path
	resolvedPath, err := sess.validateChrootPath(path)
	if err != nil {
		return err
	}

	err = sess.storage.MakeDir(resolvedPath)
	if err != nil {
		logger.Error(err, "MKDIR operation failed", "username", username, "path", path, "resolved_path", resolvedPath)
	} else {
		logger.Info("MKDIR operation successful", "username", username, "path", path, "resolved_path", resolvedPath)
	}
	return err
}

func (driver *KubeDriver) GetFile(ctx *server.Context, path string, offset int64) (int64, io.ReadCloser, error) {
	traceCtx := context.Background()
	var span trace.Span

	logger := getLogger()
	username := driver.getAuthenticatedUsername(ctx)
	logger.Info("FTP DOWNLOAD operation", "username", username, "path", path, "offset", offset)
	start := time.Now()

	sess, err := driver.sessionForContext(ctx)
	if err != nil {
		logger.Error(err, "DOWNLOAD failed during user initialization", "username", username, "path", path)
		metrics.RecordFileOperation("download", driver.getBackendType(nil), "error")
		return 0, nil, wrapStorageError(err)
	}

	if isTracingEnabled() {
		_, span = tracer.Start(traceCtx, "ftp.download",
			trace.WithAttributes(
				attribute.String("ftp.user", sess.username),
				attribute.String("ftp.path", path),
				attribute.Int64("ftp.offset", offset),
				attribute.String("ftp.backend", driver.getBackendType(sess)),
			))
		defer span.End()
	}

	// Validate chroot restrictions and get resolved path
	resolvedPath, err := sess.validateChrootPath(path)
	if err != nil {
		logger.Error(err, "DOWNLOAD failed due to chroot restriction", "username", username, "path", path)
		if span != nil {
			span.RecordError(err)
			span.SetAttributes(attribute.String("ftp.status", "error"))
		}
		metrics.RecordFileOperation("download", driver.getBackendType(sess), "error")
		return 0, nil, err
	}

	size, reader, err := sess.storage.GetFile(resolvedPath, offset)
	duration := time.Since(start)

	if err != nil {
		logger.Error(err, "DOWNLOAD operation failed", "username", username, "path", path)
		if span != nil {
			span.RecordError(err)
			span.SetAttributes(attribute.String("ftp.status", "error"))
		}
		metrics.RecordFileOperation("download", driver.getBackendType(sess), "error")
		return 0, nil, err
	}

	logger.Info("DOWNLOAD operation successful", "username", username, "path", path, "size_bytes", size, "duration_ms", duration.Milliseconds())
	if span != nil {
		span.SetAttributes(
			attribute.String("ftp.status", "success"),
			attribute.Int64("ftp.bytes", size),
			attribute.Int64("ftp.duration_ms", duration.Milliseconds()),
		)
	}
	metrics.RecordFileOperation("download", driver.getBackendType(sess), "success")
	metrics.RecordFileTransfer("download", driver.getBackendType(sess), size, duration)

	return size, reader, nil
}

func (driver *KubeDriver) PutFile(ctx *server.Context, path string, reader io.Reader, offset int64) (int64, error) {
	traceCtx := context.Background()
	var span trace.Span

	// goftp passes -1 for APPE and for STOR without REST, and the REST position
	// otherwise. APPE is identified by the command name; a REST position is a
	// resume point. Plain STOR overwrites.
	appendToExisting := offset == -1 && ctx != nil && ctx.Cmd == "APPE"
	resumeAt := max(offset, 0)
	append := appendToExisting || resumeAt > 0

	uploadType := "UPLOAD"
	operation := "ftp.upload"
	if append {
		uploadType = "APPEND"
		operation = "ftp.append"
	}

	logger := getLogger()
	username := driver.getAuthenticatedUsername(ctx)
	logger.Info("FTP upload operation", "username", username, "operation", uploadType, "path", path, "offset", offset)

	// Storage backends only write whole objects, so an append or resume is done
	// here by rewriting the object: the existing bytes that stay, then the new data.
	start := time.Now()

	sess, err := driver.sessionForContext(ctx)
	if err != nil {
		logger.Error(err, "Upload failed during user initialization", "username", username, "operation", uploadType, "path", path)
		metrics.RecordFileOperation("upload", driver.getBackendType(nil), "error")
		return 0, wrapStorageError(err)
	}

	if isTracingEnabled() {
		_, span = tracer.Start(traceCtx, operation,
			trace.WithAttributes(
				attribute.String("ftp.user", sess.username),
				attribute.String("ftp.path", path),
				attribute.Bool("ftp.append", append),
				attribute.Int64("ftp.offset", offset),
				attribute.String("ftp.backend", driver.getBackendType(sess)),
			))
		defer span.End()
	}

	// Validate chroot restrictions and get resolved path
	resolvedPath, err := sess.validateChrootPath(path)
	if err != nil {
		logger.Info("Upload failed due to chroot restriction", "username", username, "operation", uploadType, "path", path, "error", err)
		if span != nil {
			span.RecordError(err)
			span.SetAttributes(attribute.String("ftp.status", "error"))
		}
		metrics.RecordFileOperation("upload", driver.getBackendType(sess), "error")
		return 0, err
	}

	received := &byteCounter{r: reader}
	source, closeExisting, err := driver.uploadSource(sess.storage, resolvedPath, received, resumeAt, appendToExisting, append)
	if err == nil {
		defer closeExisting()
		// The backend receives whole-object data; size reports only what the client sent.
		_, err = sess.storage.PutFile(resolvedPath, source, 0)
	}
	size := received.n
	duration := time.Since(start)

	if err != nil {
		logger.Error(err, "Upload operation failed", "username", username, "operation", uploadType, "path", path, "resolved_path", resolvedPath)
		if span != nil {
			span.RecordError(err)
			span.SetAttributes(attribute.String("ftp.status", "error"))
		}
		metrics.RecordFileOperation("upload", driver.getBackendType(sess), "error")
		return 0, err
	}

	logger.Info("Upload operation successful", "username", username, "operation", uploadType, "path", path, "resolved_path", resolvedPath, "size_bytes", size, "duration_ms", duration.Milliseconds())
	if span != nil {
		span.SetAttributes(
			attribute.String("ftp.status", "success"),
			attribute.Int64("ftp.bytes", size),
			attribute.Int64("ftp.duration_ms", duration.Milliseconds()),
		)
	}
	metrics.RecordFileOperation("upload", driver.getBackendType(sess), "success")
	metrics.RecordFileTransfer("upload", driver.getBackendType(sess), size, duration)

	return size, nil
}

// byteCounter counts the bytes read through it.
type byteCounter struct {
	r io.Reader
	n int64
}

func (b *byteCounter) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.n += int64(n)
	return n, err
}

// uploadSource returns the data the backend should store for an upload.
//
// A plain upload stores the client's data. APPE stores the existing object
// followed by the new data; a resume at offset N stores the first N bytes of the
// existing object followed by the new data. APPE creates a missing object; a
// resume of a missing object is an error. The returned func releases the existing
// object.
func (driver *KubeDriver) uploadSource(storageImpl storage.Storage, resolvedPath string, data io.Reader, resumeAt int64, appendToExisting, appending bool) (io.Reader, func(), error) {
	noop := func() {}
	if !appending {
		return data, noop, nil
	}

	size, existing, err := storageImpl.GetFile(resolvedPath, 0)
	if err != nil {
		if !isFileNotFoundError(err) {
			return nil, noop, err
		}
		if resumeAt > 0 {
			return nil, noop, fmt.Errorf("cannot resume upload at offset %d: file does not exist", resumeAt)
		}
		return data, noop, nil
	}
	closeExisting := func() { _ = existing.Close() }

	if appendToExisting {
		return io.MultiReader(existing, data), closeExisting, nil
	}
	if size < resumeAt {
		closeExisting()
		return nil, noop, fmt.Errorf("cannot resume upload at offset %d: existing file is %d bytes", resumeAt, size)
	}
	return io.MultiReader(io.LimitReader(existing, resumeAt), data), closeExisting, nil
}

// ensureUserInitialized ensures the driver has an authenticated user and storage configured
func (driver *KubeDriver) ensureUserInitialized() error {
	return driver.ensureUserInitializedWithContext(nil)
}

// ensureUserInitializedWithContext resolves the session that owns ctx and
// makes sure that the storage implementation of that session is ready. The
// storage implementation is bound to the backend of the user the session
// authenticated as.
func (driver *KubeDriver) ensureUserInitializedWithContext(ctx *server.Context) error {
	_, err := driver.sessionForContext(ctx)
	return err
}

// getAuthenticatedUsername returns the username authenticated for the session
// that owns ctx. Without a session context it returns the user of the
// fallback session.
func (driver *KubeDriver) getAuthenticatedUsername(ctx *server.Context) string {
	if _, username := driver.sessionIdentity(ctx); username != "" {
		return username
	}
	// Fall back to the stored authenticatedUser (used in tests)
	return driver.authenticatedUser
}

// getBackendType returns the backend type for metrics
func (driver *KubeDriver) getBackendType(sess *driverSession) string {
	if sess != nil && sess.user != nil {
		return sess.user.Spec.Backend.Kind
	}
	return "unknown"
}

// Close handles connection cleanup and metrics recording
func (driver *KubeDriver) Close() error {
	if driver.sessionCancel != nil {
		driver.sessionCancel()
	}

	// Clean up session mapping to prevent memory leaks
	if driver.auth != nil && driver.sessionID != "" {
		driver.auth.ClearSessionUser(driver.sessionID)
	}

	// Close storage implementation to free resources
	if driver.storageImpl != nil {
		_ = driver.storageImpl.Close()
	}

	// Close the storage implementation of every session. goftp.io/server/v2
	// never calls Driver.Close, so this only runs when the driver is shut
	// down; the storage of a finished session is released by
	// evictStaleSessions.
	driver.sessionsMu.Lock()
	for key, sess := range driver.sessions {
		_ = sess.storage.Close()
		delete(driver.sessions, key)
	}
	driver.sessionsMu.Unlock()

	if driver.authenticatedUser != "" && !driver.sessionStart.IsZero() {
		sessionDuration := time.Since(driver.sessionStart)
		metrics.RecordConnectionClosed(sessionDuration)
		metrics.RecordUserSession(sessionDuration)
	}

	return nil
}

// Perm interface implementation for goftp.io/server/v2
// These methods provide file ownership and permission information

func (driver *KubeDriver) GetOwner(path string) (string, error) {
	// The goftp Perm interface carries no session context, so the owner
	// cannot be resolved per session. Report a fixed owner instead of the
	// user of another session.
	return "ftp", nil
}

func (driver *KubeDriver) GetGroup(path string) (string, error) {
	// Return a default group - could be enhanced to use user groups
	return "ftp", nil
}

func (driver *KubeDriver) GetMode(path string) (os.FileMode, error) {
	// The goftp Perm interface carries no session context, so the mode
	// cannot be read from the storage of the session that asked for it.
	// Report a fixed mode instead of reading the storage of another session.
	if strings.HasSuffix(path, "/") {
		return os.ModeDir | 0755, nil
	}
	return 0644, nil
}

func (driver *KubeDriver) ChOwner(path string, owner string) error {
	// Owner changes not supported - return success to avoid blocking operations
	logger := getLogger()
	username := driver.getAuthenticatedUsername(nil)
	logger.Info("CHOWN operation not supported, ignoring", "username", username, "path", path, "owner", owner)
	return nil
}

func (driver *KubeDriver) ChGroup(path string, group string) error {
	// Group changes not supported - return success to avoid blocking operations
	logger := getLogger()
	username := driver.getAuthenticatedUsername(nil)
	logger.Info("CHGRP operation not supported, ignoring", "username", username, "path", path, "group", group)
	return nil
}

func (driver *KubeDriver) ChMode(path string, mode os.FileMode) error {
	// Mode changes not supported for most backends - return success to avoid blocking operations
	logger := getLogger()
	username := driver.getAuthenticatedUsername(nil)
	logger.Info("CHMOD operation not supported, ignoring", "username", username, "path", path, "mode", mode)
	return nil
}
