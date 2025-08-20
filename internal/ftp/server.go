package ftp

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"goftp.io/server/v2"
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
	WelcomeMessage string
	client         client.Client
	server         *server.Server
}

// NewServer creates a new FTP server instance
func NewServer(port int, pasvPorts string, welcomeMessage string, kubeClient client.Client) *Server {
	return &Server{
		Port:           port,
		PasvPorts:      pasvPorts,
		WelcomeMessage: welcomeMessage,
		client:         kubeClient,
	}
}

// Start initializes and starts the FTP server
func (s *Server) Start(ctx context.Context) error {
	log.Printf("Starting KubeFTPd server on port %d with PASV ports %s", s.Port, s.PasvPorts)

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
		log.Println("Shutting down FTP server...")
		_ = listener.Close()
	}()

	log.Printf("FTP server listening on port %d", s.Port)
	return ftpServer.Serve(listener)
}

// KubeLogger implements logging for the FTP server
type KubeLogger struct{}

func (logger *KubeLogger) Print(sessionId string, message interface{}) {
	log.Printf("[%s] %v", sessionId, message)
}

func (logger *KubeLogger) Printf(sessionId string, format string, v ...interface{}) {
	log.Printf("[%s] "+format, append([]interface{}{sessionId}, v...)...)
}

func (logger *KubeLogger) PrintCommand(sessionId string, command string, params string) {
	log.Printf("[%s] COMMAND: %s %s", sessionId, command, params)
}

func (logger *KubeLogger) PrintResponse(sessionId string, code int, message string) {
	log.Printf("[%s] RESPONSE: %d %s", sessionId, code, message)
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
	// Set the last authenticated user on the driver
	driver.authenticatedUser = factory.auth.GetLastAuthUser()
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
	log.Printf("Initializing driver for connection")
	// Store connection reference to get authenticated user later
	driver.conn = conn
	driver.sessionStart = time.Now()

	// Extract client IP - use placeholder for now since RemoteAddr is not directly accessible
	driver.clientIP = "unknown"

	// Record connection metrics
	username := driver.getAuthenticatedUsername()
	if username != "" {
		metrics.RecordConnection(username, driver.clientIP)
	}
}

func (driver *KubeDriver) ChangeDir(ctx *server.Context, path string) error {
	log.Printf("ChangeDir: %s", path)
	if err := driver.ensureUserInitialized(); err != nil {
		return err
	}
	return driver.storageImpl.ChangeDir(path)
}

func (driver *KubeDriver) Stat(ctx *server.Context, path string) (os.FileInfo, error) {
	log.Printf("Stat: %s", path)
	if err := driver.ensureUserInitialized(); err != nil {
		return nil, err
	}
	return driver.storageImpl.Stat(path)
}

func (driver *KubeDriver) ListDir(ctx *server.Context, path string, callback func(os.FileInfo) error) error {
	log.Printf("[%s] LIST: %s", driver.getAuthenticatedUsername(), path)
	if err := driver.ensureUserInitialized(); err != nil {
		log.Printf("[%s] LIST FAILED: %s - %v", driver.getAuthenticatedUsername(), path, err)
		return err
	}

	err := driver.storageImpl.ListDir(path, callback)
	if err != nil {
		log.Printf("[%s] LIST FAILED: %s - %v", driver.getAuthenticatedUsername(), path, err)
	} else {
		log.Printf("[%s] LIST SUCCESS: %s", driver.getAuthenticatedUsername(), path)
	}
	return err
}

func (driver *KubeDriver) DeleteDir(ctx *server.Context, path string) error {
	log.Printf("[%s] RMDIR: %s", driver.getAuthenticatedUsername(), path)
	if err := driver.ensureUserInitialized(); err != nil {
		log.Printf("[%s] RMDIR FAILED: %s - %v", driver.getAuthenticatedUsername(), path, err)
		return err
	}

	err := driver.storageImpl.DeleteDir(path)
	if err != nil {
		log.Printf("[%s] RMDIR FAILED: %s - %v", driver.getAuthenticatedUsername(), path, err)
	} else {
		log.Printf("[%s] RMDIR SUCCESS: %s", driver.getAuthenticatedUsername(), path)
	}
	return err
}

func (driver *KubeDriver) DeleteFile(ctx *server.Context, path string) error {
	log.Printf("[%s] DELETE: %s", driver.getAuthenticatedUsername(), path)
	if err := driver.ensureUserInitialized(); err != nil {
		log.Printf("[%s] DELETE FAILED: %s - %v", driver.getAuthenticatedUsername(), path, err)
		return err
	}

	err := driver.storageImpl.DeleteFile(path)
	if err != nil {
		log.Printf("[%s] DELETE FAILED: %s - %v", driver.getAuthenticatedUsername(), path, err)
	} else {
		log.Printf("[%s] DELETE SUCCESS: %s", driver.getAuthenticatedUsername(), path)
	}
	return err
}

func (driver *KubeDriver) Rename(ctx *server.Context, fromPath, toPath string) error {
	log.Printf("[%s] RENAME: %s -> %s", driver.getAuthenticatedUsername(), fromPath, toPath)
	if err := driver.ensureUserInitialized(); err != nil {
		log.Printf("[%s] RENAME FAILED: %s -> %s - %v", driver.getAuthenticatedUsername(), fromPath, toPath, err)
		return err
	}

	err := driver.storageImpl.Rename(fromPath, toPath)
	if err != nil {
		log.Printf("[%s] RENAME FAILED: %s -> %s - %v", driver.getAuthenticatedUsername(), fromPath, toPath, err)
	} else {
		log.Printf("[%s] RENAME SUCCESS: %s -> %s", driver.getAuthenticatedUsername(), fromPath, toPath)
	}
	return err
}

func (driver *KubeDriver) MakeDir(ctx *server.Context, path string) error {
	log.Printf("[%s] MKDIR: %s", driver.getAuthenticatedUsername(), path)
	if err := driver.ensureUserInitialized(); err != nil {
		log.Printf("[%s] MKDIR FAILED: %s - %v", driver.getAuthenticatedUsername(), path, err)
		return err
	}

	err := driver.storageImpl.MakeDir(path)
	if err != nil {
		log.Printf("[%s] MKDIR FAILED: %s - %v", driver.getAuthenticatedUsername(), path, err)
	} else {
		log.Printf("[%s] MKDIR SUCCESS: %s", driver.getAuthenticatedUsername(), path)
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

	log.Printf("[%s] DOWNLOAD: %s (offset: %d)", driver.getAuthenticatedUsername(), path, offset)
	start := time.Now()

	if err := driver.ensureUserInitialized(); err != nil {
		log.Printf("[%s] DOWNLOAD FAILED: %s - %v", driver.getAuthenticatedUsername(), path, err)
		if span != nil {
			span.RecordError(err)
			span.SetAttributes(attribute.String("ftp.status", "error"))
		}
		metrics.RecordFileOperation(driver.authenticatedUser, "download", driver.getBackendType(), "error")
		return 0, nil, err
	}

	size, reader, err := driver.storageImpl.GetFile(path, offset)
	duration := time.Since(start)

	if err != nil {
		log.Printf("[%s] DOWNLOAD FAILED: %s - %v", driver.getAuthenticatedUsername(), path, err)
		if span != nil {
			span.RecordError(err)
			span.SetAttributes(attribute.String("ftp.status", "error"))
		}
		metrics.RecordFileOperation(driver.authenticatedUser, "download", driver.getBackendType(), "error")
		return 0, nil, err
	}

	log.Printf("[%s] DOWNLOAD SUCCESS: %s (%d bytes, %v)", driver.getAuthenticatedUsername(), path, size, duration)
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

	log.Printf("[%s] %s: %s", driver.getAuthenticatedUsername(), uploadType, path)
	start := time.Now()

	if err := driver.ensureUserInitialized(); err != nil {
		log.Printf("[%s] %s FAILED: %s - %v", driver.getAuthenticatedUsername(), uploadType, path, err)
		if span != nil {
			span.RecordError(err)
			span.SetAttributes(attribute.String("ftp.status", "error"))
		}
		metrics.RecordFileOperation(driver.authenticatedUser, "upload", driver.getBackendType(), "error")
		return 0, err
	}

	size, err := driver.storageImpl.PutFile(path, reader, offset)
	duration := time.Since(start)

	if err != nil {
		log.Printf("[%s] %s FAILED: %s - %v", driver.getAuthenticatedUsername(), uploadType, path, err)
		if span != nil {
			span.RecordError(err)
			span.SetAttributes(attribute.String("ftp.status", "error"))
		}
		metrics.RecordFileOperation(driver.authenticatedUser, "upload", driver.getBackendType(), "error")
		return 0, err
	}

	log.Printf("[%s] %s SUCCESS: %s (%d bytes, %v)", driver.getAuthenticatedUsername(), uploadType, path, size, duration)
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
	// If already initialized, return
	if driver.user != nil && driver.storageImpl != nil {
		return nil
	}

	// Get the authenticated username from the connection
	if driver.conn == nil {
		return fmt.Errorf("no connection available")
	}

	// The goftp/server doesn't expose the authenticated username directly
	// We need to try to get it from the auth cache using the connection
	// For now, we'll implement a workaround by storing the username in the auth
	username := driver.getAuthenticatedUsername()
	if username == "" {
		return fmt.Errorf("user not authenticated")
	}

	// Get the user from the auth cache
	user := driver.auth.GetUser(username)
	if user == nil {
		return fmt.Errorf("user %s not found in auth cache", username)
	}

	// Initialize storage if not already done
	if driver.storageImpl == nil {
		var err error
		driver.storageImpl, err = storage.NewStorage(user, driver.client)
		if err != nil {
			return fmt.Errorf("failed to initialize storage for user %s: %w", user.Spec.Username, err)
		}

		driver.user = user
		log.Printf("User %s configured with %s backend", user.Spec.Username, user.Spec.Backend.Kind)
	}

	return nil
}

// getAuthenticatedUsername returns the authenticated username for this driver instance
func (driver *KubeDriver) getAuthenticatedUsername() string {
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
	log.Printf("[%s] CHOWN: %s to %s (not supported, ignoring)", driver.getAuthenticatedUsername(), path, owner)
	return nil
}

func (driver *KubeDriver) ChGroup(path string, group string) error {
	// Group changes not supported - return success to avoid blocking operations
	log.Printf("[%s] CHGRP: %s to %s (not supported, ignoring)", driver.getAuthenticatedUsername(), path, group)
	return nil
}

func (driver *KubeDriver) ChMode(path string, mode os.FileMode) error {
	// Mode changes not supported for most backends - return success to avoid blocking operations
	log.Printf("[%s] CHMOD: %s to %v (not supported, ignoring)", driver.getAuthenticatedUsername(), path, mode)
	return nil
}
