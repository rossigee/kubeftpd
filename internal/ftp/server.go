package ftp

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"goftp.io/server/v2"
	ctrl "sigs.k8s.io/controller-runtime"
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

// isTracingEnabled returns true if OpenTelemetry is configured
func isTracingEnabled() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" ||
		os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != "" ||
		os.Getenv("OTEL_SERVICE_NAME") != ""
}

// Server represents the KubeFTPd server
type Server struct {
	Port           int
	PasvPorts      string
	PublicIP       string
	WelcomeMessage string
	client         client.Client
	server         *server.Server
}

// NewServer creates a new FTP server instance
func NewServer(port int, pasvPorts string, publicIP string, welcomeMessage string, kubeClient client.Client) *Server {
	return &Server{
		Port:           port,
		PasvPorts:      pasvPorts,
		PublicIP:       publicIP,
		WelcomeMessage: welcomeMessage,
		client:         kubeClient,
	}
}

// Start initializes and starts the FTP server
func (s *Server) Start(ctx context.Context) error {
	logger := getLogger()
	logger.Info("Starting KubeFTPd server", "port", s.Port, "pasv-ports", s.PasvPorts)

	// Create auth instance
	auth := NewKubeAuth(s.client)

	// Create FTP server configuration
	driver := &KubeDriver{
		client: s.client,
		auth:   auth,
	}

	opts := &server.Options{
		Driver:         driver,
		Port:           s.Port,
		Hostname:       "",
		PublicIP:       s.PublicIP,
		Auth:           auth,
		Logger:         &KubeLogger{},
		PassivePorts:   s.PasvPorts,
		WelcomeMessage: s.WelcomeMessage,
		Perm:           driver, // KubeDriver implements the Perm interface
	}

	ftpServer, err := server.NewServer(opts)
	if err != nil {
		return fmt.Errorf("failed to create FTP server: %w", err)
	}
	s.server = ftpServer

	// Start the server
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", s.Port))
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}

	go func() {
		<-ctx.Done()
		logger.Info("Shutting down FTP server")
		_ = listener.Close()
	}()

	logger.Info("FTP server listening", "port", s.Port)
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

// KubeDriver implements the FTP driver interface using Kubernetes backends
type KubeDriver struct {
	client            client.Client
	auth              *KubeAuth
	conn              *server.Context
	user              *ftpv1.User
	storageImpl       storage.Storage
	authenticatedUser string    // Track the authenticated username
	sessionStart      time.Time // Track session start time
	clientIP          string    // Track client IP
}

func (driver *KubeDriver) Init(conn *server.Context) {
	logger := getLogger()
	logger.Info("Initializing FTP driver for connection")

	// Store connection reference to get authenticated user later
	driver.conn = conn
	driver.sessionStart = time.Now()

	// Extract client IP - use placeholder for now since RemoteAddr is not directly accessible
	driver.clientIP = "unknown"

	// Record connection metrics
	username := driver.getAuthenticatedUsername()
	if username != "" {
		logger.Info("Recording connection metrics", "username", username, "client_ip", driver.clientIP)
		metrics.RecordConnection(username, driver.clientIP)
	}
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
func (driver *KubeDriver) validateChrootPath(path string) (string, error) {
	if driver.user == nil {
		return "", fmt.Errorf("user not initialized")
	}

	// If chroot is disabled, use path as-is
	if !driver.user.Spec.Chroot {
		return path, nil
	}

	homeDir := driver.user.Spec.HomeDirectory
	resolvedPath := resolveChrootPath(path, homeDir)

	if !isPathWithinHome(resolvedPath, homeDir) {
		logger := getLogger()
		username := driver.getAuthenticatedUsername()
		logger.Info("CHROOT VIOLATION: Attempted access outside home directory",
			"username", username, "requested_path", path, "resolved_path", resolvedPath, "home_directory", homeDir)
		return "", fmt.Errorf("access denied: path outside home directory")
	}

	logger := getLogger()
	username := driver.getAuthenticatedUsername()
	logger.Info("Chroot path resolved", "username", username, "requested_path", path, "resolved_path", resolvedPath)

	return resolvedPath, nil
}

func (driver *KubeDriver) ChangeDir(ctx *server.Context, path string) error {
	logger := getLogger()
	username := driver.getAuthenticatedUsername()
	logger.Info("FTP ChangeDir operation", "username", username, "path", path)

	if err := driver.ensureUserInitializedWithContext(ctx); err != nil {
		logger.Error(err, "ChangeDir failed during user initialization", "username", username, "path", path)
		return err
	}

	// Validate chroot restrictions and get resolved path
	resolvedPath, err := driver.validateChrootPath(path)
	if err != nil {
		logger.Info("ChangeDir failed due to chroot restriction", "username", username, "path", path, "error", err)
		return err
	}

	err = driver.storageImpl.ChangeDir(resolvedPath)
	if err != nil {
		logger.Error(err, "ChangeDir operation failed", "username", username, "path", path, "resolved_path", resolvedPath)
	} else {
		logger.Info("ChangeDir operation successful", "username", username, "path", path, "resolved_path", resolvedPath)
	}
	return err
}

func (driver *KubeDriver) Stat(ctx *server.Context, path string) (os.FileInfo, error) {
	logger := getLogger()
	username := driver.getAuthenticatedUsername()
	logger.Info("FTP Stat operation", "username", username, "path", path)

	if err := driver.ensureUserInitializedWithContext(ctx); err != nil {
		logger.Error(err, "Stat failed during user initialization", "username", username, "path", path)
		return nil, err
	}

	// Validate chroot restrictions and get resolved path
	resolvedPath, err := driver.validateChrootPath(path)
	if err != nil {
		logger.Info("Stat failed due to chroot restriction", "username", username, "path", path, "error", err)
		return nil, err
	}

	stat, err := driver.storageImpl.Stat(resolvedPath)
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
	username := driver.getAuthenticatedUsername()
	logger := getLogger()
	logger.Info("FTP LIST operation", "username", username, "path", path)

	if err := driver.ensureUserInitializedWithContext(ctx); err != nil {
		logger.Error(err, "LIST failed during user initialization", "username", username, "path", path)
		return err
	}

	// Validate chroot restrictions and get resolved path
	resolvedPath, err := driver.validateChrootPath(path)
	if err != nil {
		return err
	}

	err = driver.storageImpl.ListDir(resolvedPath, callback)
	if err != nil {
		logger.Error(err, "LIST operation failed", "username", username, "path", path)
	} else {
		logger.Info("LIST operation successful", "username", username, "path", path)
	}
	return err
}

func (driver *KubeDriver) DeleteDir(ctx *server.Context, path string) error {
	username := driver.getAuthenticatedUsername()
	logger := getLogger()
	logger.Info("FTP RMDIR operation", "username", username, "path", path)

	if err := driver.ensureUserInitializedWithContext(ctx); err != nil {
		logger.Error(err, "RMDIR failed during user initialization", "username", username, "path", path)
		return err
	}

	// Validate chroot restrictions and get resolved path
	resolvedPath, err := driver.validateChrootPath(path)
	if err != nil {
		return err
	}

	err = driver.storageImpl.DeleteDir(resolvedPath)
	if err != nil {
		logger.Error(err, "RMDIR operation failed", "username", username, "path", path)
	} else {
		logger.Info("RMDIR operation successful", "username", username, "path", path)
	}
	return err
}

func (driver *KubeDriver) DeleteFile(ctx *server.Context, path string) error {
	logger := getLogger()
	username := driver.getAuthenticatedUsername()
	logger.Info("FTP DELETE operation", "username", username, "path", path)

	if err := driver.ensureUserInitializedWithContext(ctx); err != nil {
		logger.Error(err, "DELETE failed during user initialization", "username", username, "path", path)
		return err
	}

	// Validate chroot restrictions and get resolved path
	resolvedPath, err := driver.validateChrootPath(path)
	if err != nil {
		logger.Info("DELETE failed due to chroot restriction", "username", username, "path", path, "error", err)
		return err
	}

	err = driver.storageImpl.DeleteFile(resolvedPath)
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
	username := driver.getAuthenticatedUsername()
	logger.Info("FTP RENAME operation", "username", username, "from_path", fromPath, "to_path", toPath)

	if err := driver.ensureUserInitializedWithContext(ctx); err != nil {
		logger.Error(err, "RENAME failed during user initialization", "username", username, "from_path", fromPath, "to_path", toPath)
		return err
	}

	// Validate chroot restrictions for both paths and get resolved paths
	resolvedFromPath, err := driver.validateChrootPath(fromPath)
	if err != nil {
		logger.Info("RENAME failed due to chroot restriction on source", "username", username, "from_path", fromPath, "error", err)
		return err
	}
	resolvedToPath, err := driver.validateChrootPath(toPath)
	if err != nil {
		logger.Info("RENAME failed due to chroot restriction on destination", "username", username, "to_path", toPath, "error", err)
		return err
	}

	err = driver.storageImpl.Rename(resolvedFromPath, resolvedToPath)
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
	username := driver.getAuthenticatedUsername()
	logger.Info("FTP MKDIR operation", "username", username, "path", path)
	if err := driver.ensureUserInitializedWithContext(ctx); err != nil {
		logger.Error(err, "MKDIR failed during user initialization", "username", username, "path", path)
		return err
	}

	// Validate chroot restrictions and get resolved path
	resolvedPath, err := driver.validateChrootPath(path)
	if err != nil {
		return err
	}

	err = driver.storageImpl.MakeDir(resolvedPath)
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

	if isTracingEnabled() {
		_, span = tracer.Start(traceCtx, "ftp.download",
			trace.WithAttributes(
				attribute.String("ftp.user", driver.getAuthenticatedUsername()),
				attribute.String("ftp.path", path),
				attribute.Int64("ftp.offset", offset),
				attribute.String("ftp.backend", driver.getBackendType()),
			))
		defer span.End()
	}

	logger := getLogger()
	username := driver.getAuthenticatedUsername()
	logger.Info("FTP DOWNLOAD operation", "username", username, "path", path, "offset", offset)
	start := time.Now()

	if err := driver.ensureUserInitializedWithContext(ctx); err != nil {
		logger.Error(err, "DOWNLOAD failed during user initialization", "username", username, "path", path)
		if span != nil {
			span.RecordError(err)
			span.SetAttributes(attribute.String("ftp.status", "error"))
		}
		metrics.RecordFileOperation(driver.authenticatedUser, "download", driver.getBackendType(), "error")
		return 0, nil, err
	}

	// Validate chroot restrictions and get resolved path
	resolvedPath, err := driver.validateChrootPath(path)
	if err != nil {
		logger.Error(err, "DOWNLOAD failed due to chroot restriction", "username", username, "path", path)
		if span != nil {
			span.RecordError(err)
			span.SetAttributes(attribute.String("ftp.status", "error"))
		}
		metrics.RecordFileOperation(driver.authenticatedUser, "download", driver.getBackendType(), "error")
		return 0, nil, err
	}

	size, reader, err := driver.storageImpl.GetFile(resolvedPath, offset)
	duration := time.Since(start)

	if err != nil {
		logger.Error(err, "DOWNLOAD operation failed", "username", username, "path", path)
		if span != nil {
			span.RecordError(err)
			span.SetAttributes(attribute.String("ftp.status", "error"))
		}
		metrics.RecordFileOperation(driver.authenticatedUser, "download", driver.getBackendType(), "error")
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
	metrics.RecordFileOperation(driver.authenticatedUser, "download", driver.getBackendType(), "success")
	metrics.RecordFileTransfer(driver.authenticatedUser, "download", driver.getBackendType(), size, duration)

	return size, reader, nil
}

func (driver *KubeDriver) PutFile(ctx *server.Context, path string, reader io.Reader, offset int64) (int64, error) {
	traceCtx := context.Background()
	var span trace.Span

	uploadType := "UPLOAD"
	operation := "ftp.upload"
	append := offset > 0
	if append {
		uploadType = "APPEND"
		operation = "ftp.append"
	}

	if isTracingEnabled() {
		_, span = tracer.Start(traceCtx, operation,
			trace.WithAttributes(
				attribute.String("ftp.user", driver.getAuthenticatedUsername()),
				attribute.String("ftp.path", path),
				attribute.Bool("ftp.append", append),
				attribute.Int64("ftp.offset", offset),
				attribute.String("ftp.backend", driver.getBackendType()),
			))
		defer span.End()
	}

	logger := getLogger()
	username := driver.getAuthenticatedUsername()
	logger.Info("FTP upload operation", "username", username, "operation", uploadType, "path", path, "offset", offset)

	// Storage backends don't support offset mode, so force offset to 0 for complete uploads
	// This ensures compatibility with FTP clients that may request resumable uploads
	if offset != 0 {
		logger.Info("Forcing offset to 0 - backends don't support resumable uploads", "username", username, "path", path, "requested_offset", offset)
		offset = 0
		uploadType = "UPLOAD" // Change from APPEND to UPLOAD
	}
	start := time.Now()

	if err := driver.ensureUserInitializedWithContext(ctx); err != nil {
		logger.Error(err, "Upload failed during user initialization", "username", username, "operation", uploadType, "path", path)
		if span != nil {
			span.RecordError(err)
			span.SetAttributes(attribute.String("ftp.status", "error"))
		}
		metrics.RecordFileOperation(driver.authenticatedUser, "upload", driver.getBackendType(), "error")
		return 0, err
	}

	// Validate chroot restrictions and get resolved path
	resolvedPath, err := driver.validateChrootPath(path)
	if err != nil {
		logger.Info("Upload failed due to chroot restriction", "username", username, "operation", uploadType, "path", path, "error", err)
		if span != nil {
			span.RecordError(err)
			span.SetAttributes(attribute.String("ftp.status", "error"))
		}
		metrics.RecordFileOperation(driver.authenticatedUser, "upload", driver.getBackendType(), "error")
		return 0, err
	}

	size, err := driver.storageImpl.PutFile(resolvedPath, reader, offset)
	duration := time.Since(start)

	if err != nil {
		logger.Error(err, "Upload operation failed", "username", username, "operation", uploadType, "path", path, "resolved_path", resolvedPath)
		if span != nil {
			span.RecordError(err)
			span.SetAttributes(attribute.String("ftp.status", "error"))
		}
		metrics.RecordFileOperation(driver.authenticatedUser, "upload", driver.getBackendType(), "error")
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
	metrics.RecordFileOperation(driver.authenticatedUser, "upload", driver.getBackendType(), "success")
	metrics.RecordFileTransfer(driver.authenticatedUser, "upload", driver.getBackendType(), size, duration)

	return size, nil
}

// ensureUserInitialized ensures the driver has an authenticated user and storage configured
func (driver *KubeDriver) ensureUserInitialized() error {
	return driver.ensureUserInitializedWithContext(nil)
}

func (driver *KubeDriver) ensureUserInitializedWithContext(ctx *server.Context) error {
	// If already initialized, return
	if driver.user != nil && driver.storageImpl != nil {
		return nil
	}

	// Get the authenticated username from the auth system
	// Try multiple approaches in order of preference
	var username string
	if ctx != nil && driver.auth != nil {
		// 1. Try context-based lookup (current context)
		username = driver.auth.GetContextUser(ctx)

		// 2. If context lookup fails, try session-based lookup
		if username == "" {
			sessionID := driver.auth.getSessionID(ctx)
			username = driver.auth.GetSessionUser(sessionID)
		}
	}
	if username == "" {
		// 3. Fall back to the stored connection approach (legacy)
		username = driver.getAuthenticatedUsername()
	}

	logger := getLogger()

	if username == "" {
		logger.Error(nil, "ensureUserInitialized failed: no authenticated username available",
			"ctx_provided", ctx != nil, "conn_nil", driver.conn == nil, "auth_nil", driver.auth == nil)
		return fmt.Errorf("user not authenticated")
	}

	logger.Info("ensureUserInitialized: setting up user", "username", username)

	// Get the user from the auth cache
	user := driver.auth.GetUser(username)
	if user == nil {
		logger.Error(nil, "ensureUserInitialized failed: user not found in auth cache", "username", username)
		return fmt.Errorf("user %s not found in auth cache", username)
	}

	// Initialize storage if not already done
	if driver.storageImpl == nil {
		logger.Info("ensureUserInitialized: initializing storage",
			"username", username, "backend_kind", user.Spec.Backend.Kind, "backend_name", user.Spec.Backend.Name)

		var err error
		driver.storageImpl, err = storage.NewStorage(user, driver.client)
		if err != nil {
			logger.Error(err, "ensureUserInitialized failed: storage initialization error", "username", username)
			return fmt.Errorf("failed to initialize storage for user %s: %w", user.Spec.Username, err)
		}

		driver.user = user
		driver.authenticatedUser = username
		logger.Info("User successfully configured with backend", "username", user.Spec.Username, "backend_kind", user.Spec.Backend.Kind)
	}

	return nil
}

// getAuthenticatedUsername returns the authenticated username for this driver instance
func (driver *KubeDriver) getAuthenticatedUsername() string {
	// Get the authenticated username from the context-specific mapping
	if driver.auth != nil && driver.conn != nil {
		if contextUser := driver.auth.GetContextUser(driver.conn); contextUser != "" {
			return contextUser
		}
	}
	// Fall back to the session-specific authenticatedUser (used in tests)
	return driver.authenticatedUser
}

// getBackendType returns the backend type for metrics
func (driver *KubeDriver) getBackendType() string {
	if driver.user != nil {
		return driver.user.Spec.Backend.Kind
	}
	return "unknown"
}

// Close handles connection cleanup and metrics recording
func (driver *KubeDriver) Close() error {
	// Clean up context mapping to prevent memory leaks
	if driver.auth != nil && driver.conn != nil {
		driver.auth.ClearContextUser(driver.conn)
	}

	if driver.authenticatedUser != "" && !driver.sessionStart.IsZero() {
		sessionDuration := time.Since(driver.sessionStart)
		metrics.RecordConnectionClosed(driver.authenticatedUser, sessionDuration)
		metrics.RecordUserSession(driver.authenticatedUser, sessionDuration)
	}
	return nil
}

// Perm interface implementation for goftp.io/server/v2
// These methods provide file ownership and permission information

func (driver *KubeDriver) GetOwner(path string) (string, error) {
	// Return the authenticated user as the owner of all files
	if err := driver.ensureUserInitialized(); err != nil {
		return "", err
	}
	return driver.authenticatedUser, nil
}

func (driver *KubeDriver) GetGroup(path string) (string, error) {
	// Return a default group - could be enhanced to use user groups
	if err := driver.ensureUserInitialized(); err != nil {
		return "", err
	}
	return "ftp", nil
}

func (driver *KubeDriver) GetMode(path string) (os.FileMode, error) {
	// Get file mode from the storage implementation
	if err := driver.ensureUserInitialized(); err != nil {
		return 0, err
	}
	stat, err := driver.storageImpl.Stat(path)
	if err != nil {
		return 0, err
	}
	return stat.Mode(), nil
}

func (driver *KubeDriver) ChOwner(path string, owner string) error {
	// Owner changes not supported - return success to avoid blocking operations
	logger := getLogger()
	username := driver.getAuthenticatedUsername()
	logger.Info("CHOWN operation not supported, ignoring", "username", username, "path", path, "owner", owner)
	return nil
}

func (driver *KubeDriver) ChGroup(path string, group string) error {
	// Group changes not supported - return success to avoid blocking operations
	logger := getLogger()
	username := driver.getAuthenticatedUsername()
	logger.Info("CHGRP operation not supported, ignoring", "username", username, "path", path, "group", group)
	return nil
}

func (driver *KubeDriver) ChMode(path string, mode os.FileMode) error {
	// Mode changes not supported for most backends - return success to avoid blocking operations
	logger := getLogger()
	username := driver.getAuthenticatedUsername()
	logger.Info("CHMOD operation not supported, ignoring", "username", username, "path", path, "mode", mode)
	return nil
}
