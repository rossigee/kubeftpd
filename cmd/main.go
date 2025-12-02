/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"os"
	"path/filepath"
	"strconv"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"fmt"
	"net/http"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
	"github.com/rossigee/kubeftpd/internal/controller"
	"github.com/rossigee/kubeftpd/internal/ftp"
	// +kubebuilder:scaffold:imports
)

var (
	// Version information - set during build
	version = "v0.6.7"
	commit  = "unknown"
	date    = "unknown"

	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(ftpv1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

type appConfig struct {
	metricsAddr       string
	metricsCertPath   string
	metricsCertName   string
	metricsCertKey    string
	webhookCertPath   string
	webhookCertName   string
	webhookCertKey    string
	secureMetrics     bool
	enableHTTP2       bool
	ftpPort           int
	ftpPasvPorts      string
	ftpPublicIP       string
	ftpWelcomeMessage string
	// Built-in anonymous user settings
	enableAnonymous      bool
	anonymousHomeDir     string
	anonymousBackendKind string
	anonymousBackendName string
	// Built-in admin user settings
	enableAdmin         bool
	adminPasswordSecret string
	adminHomeDir        string
	adminBackendKind    string
	adminBackendName    string
}

func getDefaultFTPPort() int {
	// Check if running as root (UID 0) - can bind to port 21
	// Otherwise default to port 2121 for unprivileged users
	if os.Getuid() == 0 {
		return 21
	}
	return 2121
}

func parseFlags() (*appConfig, zap.Options) {
	config := &appConfig{}
	flag.StringVar(&config.metricsAddr, "http-bind-address", ":8080", "The address the HTTP server binds to (serves metrics, health, and status). "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the HTTP server.")
	flag.BoolVar(&config.secureMetrics, "metrics-secure", false,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&config.webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&config.webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&config.webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&config.metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&config.metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&config.metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&config.enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.IntVar(&config.ftpPort, "ftp-port", getDefaultFTPPort(), "The port on which the FTP server listens")
	flag.StringVar(&config.ftpPasvPorts, "ftp-pasv-ports", "10000-10020", "The range of ports for FTP passive mode")
	flag.StringVar(&config.ftpPublicIP, "ftp-public-ip", "", "The public IP address for FTP passive mode (PASV) responses")

	// Built-in anonymous user flags
	flag.BoolVar(&config.enableAnonymous, "enable-anonymous", false, "Enable anonymous FTP access (RFC 1635)")
	flag.StringVar(&config.anonymousHomeDir, "anonymous-home-dir", "/pub", "Home directory for anonymous FTP users")
	flag.StringVar(&config.anonymousBackendKind, "anonymous-backend-kind", "FilesystemBackend", "Backend kind for anonymous users")
	flag.StringVar(&config.anonymousBackendName, "anonymous-backend-name", "anonymous-backend", "Backend name for anonymous users")

	// Built-in admin user flags
	flag.BoolVar(&config.enableAdmin, "enable-admin", false, "Enable built-in admin user")
	flag.StringVar(&config.adminPasswordSecret, "admin-password-secret", "", "Name of Kubernetes Secret containing admin password")
	flag.StringVar(&config.adminHomeDir, "admin-home-dir", "/", "Home directory for admin user")
	flag.StringVar(&config.adminBackendKind, "admin-backend-kind", "FilesystemBackend", "Backend kind for admin user")
	flag.StringVar(&config.adminBackendName, "admin-backend-name", "admin-backend", "Backend name for admin user")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	return config, opts
}

func processEnvironmentOverrides(config *appConfig) {
	if envFtpPort := os.Getenv("FTP_PORT"); envFtpPort != "" {
		if port, err := strconv.Atoi(envFtpPort); err == nil {
			config.ftpPort = port
		} else {
			setupLog.Error(err, "invalid FTP_PORT environment variable", "value", envFtpPort)
			os.Exit(1)
		}
	}

	if envFtpPasvPorts := os.Getenv("FTP_PASSIVE_PORTS"); envFtpPasvPorts != "" {
		config.ftpPasvPorts = envFtpPasvPorts
	} else {
		envMinPort := os.Getenv("FTP_PASSIVE_PORT_MIN")
		envMaxPort := os.Getenv("FTP_PASSIVE_PORT_MAX")
		if envMinPort != "" && envMaxPort != "" {
			config.ftpPasvPorts = envMinPort + "-" + envMaxPort
		}
	}

	if envFtpWelcome := os.Getenv("FTP_WELCOME_MESSAGE"); envFtpWelcome != "" {
		config.ftpWelcomeMessage = envFtpWelcome
	}

	if envFtpPublicIP := os.Getenv("FTP_PUBLIC_IP"); envFtpPublicIP != "" {
		config.ftpPublicIP = envFtpPublicIP
	}
}

func setupTLSOptions(enableHTTP2 bool) []func(*tls.Config) {
	var tlsOpts []func(*tls.Config)

	if !enableHTTP2 {
		disableHTTP2 := func(c *tls.Config) {
			setupLog.Info("disabling http/2")
			c.NextProtos = []string{"http/1.1"}
		}
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	return tlsOpts
}

func setupCertWatcher(certPath, certName, certKey, watcherType string) (*certwatcher.CertWatcher, error) {
	if len(certPath) == 0 {
		return nil, nil
	}

	setupLog.Info("Initializing certificate watcher using provided certificates",
		"cert-path", certPath, "cert-name", certName, "cert-key", certKey, "type", watcherType)

	watcher, err := certwatcher.New(
		filepath.Join(certPath, certName),
		filepath.Join(certPath, certKey),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize %s certificate watcher: %w", watcherType, err)
	}

	return watcher, nil
}

func setupWebhookServer(config *appConfig, tlsOpts []func(*tls.Config)) (webhook.Server, *certwatcher.CertWatcher, error) {
	webhookCertWatcher, err := setupCertWatcher(config.webhookCertPath, config.webhookCertName, config.webhookCertKey, "webhook")
	if err != nil {
		return nil, nil, err
	}

	webhookTLSOpts := tlsOpts
	if webhookCertWatcher != nil {
		webhookTLSOpts = append(webhookTLSOpts, func(tlsConfig *tls.Config) {
			tlsConfig.GetCertificate = webhookCertWatcher.GetCertificate
		})
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: webhookTLSOpts,
	})

	return webhookServer, webhookCertWatcher, nil
}

func createHTTPHandler() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"service":"kubeftpd","version":"%s","commit":"%s","date":"%s","status":"running"}`+"\n", version, commit, date)
	})
	return mux
}

func setupMetricsServer(config *appConfig, tlsOpts []func(*tls.Config), mux *http.ServeMux) (metricsserver.Options, *certwatcher.CertWatcher, error) {
	metricsServerOptions := metricsserver.Options{
		BindAddress:   config.metricsAddr,
		SecureServing: config.secureMetrics,
		TLSOpts:       tlsOpts,
		ExtraHandlers: map[string]http.Handler{"/": mux},
	}

	if config.secureMetrics {
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	metricsCertWatcher, err := setupCertWatcher(config.metricsCertPath, config.metricsCertName, config.metricsCertKey, "metrics")
	if err != nil {
		return metricsserver.Options{}, nil, err
	}

	if metricsCertWatcher != nil {
		metricsServerOptions.TLSOpts = append(metricsServerOptions.TLSOpts, func(tlsConfig *tls.Config) {
			tlsConfig.GetCertificate = metricsCertWatcher.GetCertificate
		})
	}

	return metricsServerOptions, metricsCertWatcher, nil
}

func setupControllers(mgr ctrl.Manager, config *appConfig) error {
	// Get the operator namespace for built-in user creation
	operatorNamespace := "default"
	if ns := os.Getenv("POD_NAMESPACE"); ns != "" {
		operatorNamespace = ns
	}

	// Create built-in user manager configuration
	builtInConfig := controller.BuiltInUserConfig{
		EnableAnonymous:      config.enableAnonymous,
		AnonymousHomeDir:     config.anonymousHomeDir,
		AnonymousBackendKind: config.anonymousBackendKind,
		AnonymousBackendName: config.anonymousBackendName,
		EnableAdmin:          config.enableAdmin,
		AdminPasswordSecret:  config.adminPasswordSecret,
		AdminHomeDir:         config.adminHomeDir,
		AdminBackendKind:     config.adminBackendKind,
		AdminBackendName:     config.adminBackendName,
		Namespace:            operatorNamespace,
	}

	controllers := []struct {
		name       string
		reconciler interface{ SetupWithManager(ctrl.Manager) error }
	}{
		{"User", &controller.UserReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}},
		{"MinioBackend", &controller.MinioBackendReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}},
		{"WebDavBackend", &controller.WebDavBackendReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}},
		{"FilesystemBackend", &controller.FilesystemBackendReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}},
		{"BuiltInUserManager", &controller.BuiltInUserManager{Client: mgr.GetClient(), Scheme: mgr.GetScheme(), Config: builtInConfig}},
	}

	for _, c := range controllers {
		if err := c.reconciler.SetupWithManager(mgr); err != nil {
			return fmt.Errorf("unable to create controller %s: %w", c.name, err)
		}
	}

	return nil
}

func addCertWatchersToManager(mgr ctrl.Manager, metricsCertWatcher, webhookCertWatcher *certwatcher.CertWatcher) error {
	if metricsCertWatcher != nil {
		setupLog.Info("Adding metrics certificate watcher to manager")
		if err := mgr.Add(metricsCertWatcher); err != nil {
			return fmt.Errorf("unable to add metrics certificate watcher to manager: %w", err)
		}
	}

	if webhookCertWatcher != nil {
		setupLog.Info("Adding webhook certificate watcher to manager")
		if err := mgr.Add(webhookCertWatcher); err != nil {
			return fmt.Errorf("unable to add webhook certificate watcher to manager: %w", err)
		}
	}

	return nil
}

func setupHealthChecks(mgr ctrl.Manager) error {
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("unable to set up health check: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("unable to set up ready check: %w", err)
	}
	return nil
}

func main() {
	config, opts := parseFlags()
	processEnvironmentOverrides(config)

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog.Info("Starting KubeFTPd", "version", version, "commit", commit, "date", date)

	tlsOpts := setupTLSOptions(config.enableHTTP2)

	webhookServer, webhookCertWatcher, err := setupWebhookServer(config, tlsOpts)
	if err != nil {
		setupLog.Error(err, "Failed to setup webhook server")
		os.Exit(1)
	}

	mux := createHTTPHandler()
	metricsServerOptions, metricsCertWatcher, err := setupMetricsServer(config, tlsOpts, mux)
	if err != nil {
		setupLog.Error(err, "Failed to setup metrics server")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:        scheme,
		Metrics:       metricsServerOptions,
		WebhookServer: webhookServer,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err := setupControllers(mgr, config); err != nil {
		setupLog.Error(err, "Failed to setup controllers")
		os.Exit(1)
	}

	if err := addCertWatchersToManager(mgr, metricsCertWatcher, webhookCertWatcher); err != nil {
		setupLog.Error(err, "Failed to add certificate watchers")
		os.Exit(1)
	}

	if err := setupHealthChecks(mgr); err != nil {
		setupLog.Error(err, "Failed to setup health checks")
		os.Exit(1)
	}

	// Trigger initial built-in user reconciliation
	// This will create/update/delete built-in User CRs based on configuration
	ctx := context.Background()
	operatorNamespace := "default"
	if ns := os.Getenv("POD_NAMESPACE"); ns != "" {
		operatorNamespace = ns
	}

	builtInConfig := controller.BuiltInUserConfig{
		EnableAnonymous:      config.enableAnonymous,
		AnonymousHomeDir:     config.anonymousHomeDir,
		AnonymousBackendKind: config.anonymousBackendKind,
		AnonymousBackendName: config.anonymousBackendName,
		EnableAdmin:          config.enableAdmin,
		AdminPasswordSecret:  config.adminPasswordSecret,
		AdminHomeDir:         config.adminHomeDir,
		AdminBackendKind:     config.adminBackendKind,
		AdminBackendName:     config.adminBackendName,
		Namespace:            operatorNamespace,
	}

	builtInUserManager := &controller.BuiltInUserManager{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Config: builtInConfig,
	}

	if err := builtInUserManager.UpdateConfig(ctx, builtInConfig); err != nil {
		setupLog.Error(err, "Failed to reconcile built-in users")
		// Don't exit here as this might be a temporary error
		// The controller will retry during normal reconciliation
	} else {
		setupLog.Info("Successfully reconciled built-in users based on configuration")
	}

	// Start FTP server
	ftpServer := ftp.NewServer(config.ftpPort, config.ftpPasvPorts, config.ftpPublicIP, config.ftpWelcomeMessage, mgr.GetClient())
	ctx, cancel := context.WithCancel(ctrl.SetupSignalHandler())
	defer cancel()

	// Channel to receive FTP server errors
	ftpErrorChan := make(chan error, 1)

	go func() {
		setupLog.Info("starting FTP server", "port", config.ftpPort, "pasv-ports", config.ftpPasvPorts)
		if err := ftpServer.Start(ctx); err != nil {
			ftpErrorChan <- err
		}
	}()

	// Channel to receive manager errors
	managerErrorChan := make(chan error, 1)

	go func() {
		setupLog.Info("starting manager")
		if err := mgr.Start(ctx); err != nil {
			managerErrorChan <- err
		}
	}()

	// Wait for either component to fail or context cancellation
	select {
	case err := <-ftpErrorChan:
		setupLog.Error(err, "FTP server failed to start - shutting down")
		cancel()
		os.Exit(1)
	case err := <-managerErrorChan:
		setupLog.Error(err, "manager failed to start - shutting down")
		cancel()
		os.Exit(1)
	case <-ctx.Done():
		setupLog.Info("shutting down gracefully")
	}
}
