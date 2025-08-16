package ftp

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"

	"github.com/goftp/server"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
	"github.com/rossigee/kubeftpd/internal/storage"
)

// Server represents the KubeFTPd server
type Server struct {
	Port      int
	PasvPorts string
	client    client.Client
	server    *server.Server
}

// NewServer creates a new FTP server instance
func NewServer(port int, pasvPorts string, kubeClient client.Client) *Server {
	return &Server{
		Port:      port,
		PasvPorts: pasvPorts,
		client:    kubeClient,
	}
}

// Start initializes and starts the FTP server
func (s *Server) Start(ctx context.Context) error {
	log.Printf("Starting KubeFTPd server on port %d with PASV ports %s", s.Port, s.PasvPorts)

	// Create auth instance
	auth := NewKubeAuth(s.client)

	// Create FTP server configuration
	factory := &KubeDriverFactory{client: s.client, auth: auth}

	opts := &server.ServerOpts{
		Factory:      factory,
		Port:         s.Port,
		Hostname:     "",
		Auth:         auth,
		Logger:       &KubeLogger{},
		PassivePorts: s.PasvPorts,
	}

	ftpServer := server.NewServer(opts)
	s.server = ftpServer

	// Start the server
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", s.Port))
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}

	go func() {
		<-ctx.Done()
		log.Println("Shutting down FTP server...")
		listener.Close()
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
	conn              *server.Conn
	user              *ftpv1.User
	storageImpl       storage.Storage
	authenticatedUser string // Track the authenticated username
}

func (driver *KubeDriver) Init(conn *server.Conn) {
	log.Printf("Initializing driver for connection")
	// Store connection reference to get authenticated user later
	driver.conn = conn
}

func (driver *KubeDriver) ChangeDir(path string) error {
	log.Printf("ChangeDir: %s", path)
	if err := driver.ensureUserInitialized(); err != nil {
		return err
	}
	return driver.storageImpl.ChangeDir(path)
}

func (driver *KubeDriver) Stat(path string) (server.FileInfo, error) {
	log.Printf("Stat: %s", path)
	if err := driver.ensureUserInitialized(); err != nil {
		return nil, err
	}
	return driver.storageImpl.Stat(path)
}

func (driver *KubeDriver) ListDir(path string, callback func(server.FileInfo) error) error {
	log.Printf("ListDir: %s", path)
	if err := driver.ensureUserInitialized(); err != nil {
		return err
	}
	return driver.storageImpl.ListDir(path, callback)
}

func (driver *KubeDriver) DeleteDir(path string) error {
	log.Printf("DeleteDir: %s", path)
	if err := driver.ensureUserInitialized(); err != nil {
		return err
	}
	return driver.storageImpl.DeleteDir(path)
}

func (driver *KubeDriver) DeleteFile(path string) error {
	log.Printf("DeleteFile: %s", path)
	if err := driver.ensureUserInitialized(); err != nil {
		return err
	}
	return driver.storageImpl.DeleteFile(path)
}

func (driver *KubeDriver) Rename(fromPath, toPath string) error {
	log.Printf("Rename: %s -> %s", fromPath, toPath)
	if err := driver.ensureUserInitialized(); err != nil {
		return err
	}
	return driver.storageImpl.Rename(fromPath, toPath)
}

func (driver *KubeDriver) MakeDir(path string) error {
	log.Printf("MakeDir: %s", path)
	if err := driver.ensureUserInitialized(); err != nil {
		return err
	}
	return driver.storageImpl.MakeDir(path)
}

func (driver *KubeDriver) GetFile(path string, offset int64) (int64, io.ReadCloser, error) {
	log.Printf("GetFile: %s (offset: %d)", path, offset)
	if err := driver.ensureUserInitialized(); err != nil {
		return 0, nil, err
	}
	return driver.storageImpl.GetFile(path, offset)
}

func (driver *KubeDriver) PutFile(path string, reader io.Reader, append bool) (int64, error) {
	log.Printf("PutFile: %s (append: %v)", path, append)
	if err := driver.ensureUserInitialized(); err != nil {
		return 0, err
	}
	return driver.storageImpl.PutFile(path, reader, append)
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
