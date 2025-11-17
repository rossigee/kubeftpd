# KubeFTPd

[![Build Status](https://github.com/rossigee/kubeftpd/workflows/Clean%20CI/badge.svg)](https://github.com/rossigee/kubeftpd/actions/workflows/clean-ci.yml)
[![codecov](https://codecov.io/gh/rossigee/kubeftpd/branch/master/graph/badge.svg)](https://codecov.io/gh/rossigee/kubeftpd)
[![Go Report Card](https://goreportcard.com/badge/github.com/rossigee/kubeftpd)](https://goreportcard.com/report/github.com/rossigee/kubeftpd)
[![Container Images](https://img.shields.io/badge/container-ghcr.io%2Frossigee%2Fkubeftpd-blue)](https://github.com/rossigee/kubeftpd/pkgs/container/kubeftpd)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

A Kubernetes-native FTP service that provides secure file transfer capabilities using Custom Resource Definitions (CRDs) for user and backend management.

## Overview

KubeFTPd is designed to replace traditional FTP solutions like SFTPGo with a cloud-native approach that leverages Kubernetes for configuration and management. It supports multiple storage backends including MinIO (S3-compatible), WebDAV, and local filesystem storage, making it suitable for various use cases from document scanning workflows to general file transfer needs.

### Key Features

- **Kubernetes-Native**: Uses CRDs for user and backend configuration
- **Multiple Storage Backends**: Support for MinIO/S3, WebDAV endpoints, and local filesystem storage
- **Built-in User Types**: Anonymous FTP (RFC 1635) and admin users with automatic User CR management
- **PASV Mode Support**: Currently supports passive FTP mode with active mode planned
- **Gateway API Support**: Modern alternative to LoadBalancer with standardized TCP routing
- **Cozystack Integration**: Native support for Cozystack PaaS platform with FluxCD deployment
- **RBAC Integration**: Full Kubernetes RBAC support for access control
- **Health & Metrics**: Built-in health checks, JSON logging, and metrics endpoints
- **Security First**: TLS support, dual password authentication (plaintext/secrets), webhook validation
- **Password Security**: Kubernetes Secrets integration with production restrictions and strength validation
- **Webhook Validation**: Admission controllers for password policies and production compliance

## Architecture

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   FTP Client    │────│   KubeFTPd       │────│ Storage Backend │
│                 │    │   (Controller)   │    │ (MinIO/WebDAV/  │
└─────────────────┘    └──────────────────┘    │  Filesystem)    │
                                               └─────────────────┘
                              │
                              ▼
                       ┌──────────────────┐
                       │ Kubernetes API   │
                       │ (User/Backend    │
                       │  CRDs)           │
                       └──────────────────┘
```

## Container Images

Pre-built container images are available from GitHub Container Registry:

```bash
# Latest release
ghcr.io/rossigee/kubeftpd:latest

# Specific version
ghcr.io/rossigee/kubeftpd:v0.6.6
```

**Supported architectures:**
- `linux/amd64`
- `linux/arm64`

## Quick Start

### Prerequisites

- Kubernetes cluster (v1.25+)
- kubectl configured
- Go 1.25+ (for development)

### Installation

1. **Install CRDs:**
```bash
kubectl apply -f config/crd/bases/
```

2. **Deploy the controller:**
```bash
kubectl apply -f config/rbac/
kubectl apply -f config/manager/
```

3. **Create a storage backend:**

For MinIO:
```yaml
apiVersion: ftp.golder.org/v1
kind: MinioBackend
metadata:
  name: minio-backend
  namespace: default
spec:
  endpoint: "https://minio.example.com"
  bucket: "ftp-storage"
  region: "us-east-1"
  credentials:
    accessKeyID: "admin"
    secretAccessKey: "password123"
  tls:
    insecureSkipVerify: false
```

For WebDAV:
```yaml
apiVersion: ftp.golder.org/v1
kind: WebDavBackend
metadata:
  name: webdav-backend
  namespace: default
spec:
  endpoint: "https://webdav.example.com"
  basePath: "/ftp-data"
  credentials:
    username: "ftpuser"
    password: "password123"
  tls:
    insecureSkipVerify: false
```

For Filesystem:
```yaml
apiVersion: ftp.golder.org/v1
kind: FilesystemBackend
metadata:
  name: filesystem-backend
  namespace: default
spec:
  basePath: "/data/ftp"
  readOnly: false
  fileMode: "0644"
  dirMode: "0755"
  maxFileSize: 0
  volumeClaimRef:
    name: ftp-storage
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ftp-storage
  namespace: default
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
```

4. **Create FTP users:**

Option A - Using plaintext password (development only):
```yaml
apiVersion: ftp.golder.org/v1
kind: User
metadata:
  name: scanner-receipts
  namespace: default
spec:
  username: "scanner"
  password: "secure-password"
  homeDirectory: "/receipts"
  enabled: true
  backend:
    kind: "MinioBackend"
    name: "minio-backend"
  permissions:
    read: true
    write: true
    delete: false
```

Option B - Using Kubernetes Secret (recommended):
```bash
# Create secret first
kubectl create secret generic scanner-password \
  --from-literal=password="MySecurePassword123!"
```

```yaml
apiVersion: ftp.golder.org/v1
kind: User
metadata:
  name: scanner-receipts
  namespace: default
spec:
  username: "scanner"
  passwordSecret:
    name: "scanner-password"
    key: "password"
  homeDirectory: "/receipts"
  enabled: true
  backend:
    kind: "MinioBackend"
    name: "minio-backend"
  permissions:
    read: true
    write: true
    delete: false
```

5. **Connect with FTP client:**
```bash
ftp ftp.example.com 21
# Use passive mode (PASV)
# Login with username/password from User CRD
```

## Custom Resources

### User CRD

Defines FTP users with their credentials, permissions, and backend configuration. Supports both plaintext passwords (development) and Kubernetes Secrets (production). Three user types are supported:

- **regular**: Standard FTP users (default)
- **anonymous**: RFC 1635 compliant anonymous FTP access
- **admin**: Administrative users with full permissions

## Built-in Users

KubeFTPd supports automatic management of built-in users through configuration flags. These users are created as User CRs and managed by the BuiltInUserManager controller.

### Anonymous User

Enable RFC 1635 compliant anonymous FTP access:

```bash
# Enable anonymous user with filesystem backend
kubeftpd --enable-anonymous \
  --anonymous-home-dir="/pub" \
  --anonymous-backend-kind="FilesystemBackend" \
  --anonymous-backend-name="anonymous-backend"
```

**Anonymous User Characteristics:**
- Username: `anonymous`
- Password: Any password accepted (RFC 1635 compliance)
- Permissions: Read-only access
- Created as User CR: `builtin-anonymous`

### Admin User

Enable built-in admin user with secret-based authentication:

```bash
# First create the admin password secret
kubectl create secret generic admin-secret \
  --from-literal=password="AdminPassword123!"

# Enable admin user
kubeftpd --enable-admin \
  --admin-password-secret="admin-secret" \
  --admin-home-dir="/" \
  --admin-backend-kind="FilesystemBackend" \
  --admin-backend-name="admin-backend"
```

**Admin User Characteristics:**
- Username: `admin`
- Password: Retrieved from Kubernetes Secret
- Permissions: Full access (read, write, delete, list)
- Created as User CR: `builtin-admin`

### Lifecycle Management

Built-in users are automatically:
- **Created** when enabled via configuration flags
- **Updated** when configuration changes
- **Deleted** when disabled
- **Labeled** with `kubeftpd.golder.org/builtin: true`

Example of automatically created User CR:

```yaml
apiVersion: ftp.golder.org/v1
kind: User
metadata:
  name: builtin-anonymous
  namespace: default
  labels:
    kubeftpd.golder.org/builtin: "true"
    kubeftpd.golder.org/type: "anonymous"
spec:
  type: "anonymous"
  username: "anonymous"
  homeDirectory: "/pub"
  enabled: true
  backend:
    kind: "FilesystemBackend"
    name: "anonymous-backend"
  permissions:
    read: true
    write: false
    delete: false
    list: true
```

**Password Methods (mutually exclusive):**

Option A - Plaintext password:
```yaml
apiVersion: ftp.golder.org/v1
kind: User
metadata:
  name: example-user
  namespace: default
spec:
  username: "ftpuser"
  password: "secure-password"  # Not recommended for production
  homeDirectory: "/home/ftpuser"
  enabled: true
  backend:
    kind: "MinioBackend"  # or "WebDavBackend"
    name: "my-backend"
    namespace: "default"  # optional, defaults to User namespace
  permissions:
    read: true
    write: true
    delete: false
status:
  ready: true
  message: "User configured successfully"
```

Option B - Kubernetes Secret (recommended):
```yaml
apiVersion: ftp.golder.org/v1
kind: User
metadata:
  name: example-user
  namespace: default
spec:
  username: "ftpuser"
  passwordSecret:
    name: "user-credentials"
    key: "password"
  homeDirectory: "/home/ftpuser"
  enabled: true
  backend:
    kind: "MinioBackend"
    name: "my-backend"
  permissions:
    read: true
    write: true
    delete: false
status:
  ready: true
  message: "User configured successfully"
```

**Security Notes:**
- Production environments with `environment: production` namespace labels require secret-based passwords
- Webhook validation enforces password strength requirements
- Secret names in production must follow pattern: `.*-ftp-(password|credentials)$`

### MinioBackend CRD

Configures MinIO/S3-compatible storage backends.

```yaml
apiVersion: ftp.golder.org/v1
kind: MinioBackend
metadata:
  name: example-minio
  namespace: default
spec:
  endpoint: "https://minio.example.com"
  bucket: "ftp-storage"
  region: "us-east-1"
  pathPrefix: "ftp-data/"  # optional
  credentials:
    accessKeyID: "minioadmin"
    secretAccessKey: "minioadmin"
    # Or use Kubernetes Secret:
    useSecret:
      name: "minio-credentials"
      namespace: "custom-namespace"  # optional, defaults to MinioBackend's namespace
      accessKeyIDKey: "access-key"      # optional, defaults to "accessKeyID"
      secretAccessKeyKey: "secret-key"  # optional, defaults to "secretAccessKey"
  tls:
    insecureSkipVerify: false
    # caCert: "..."  # TODO: CA certificate support
status:
  ready: true
  message: "Backend connection established"
```

### WebDavBackend CRD

Configures WebDAV storage backends.

```yaml
apiVersion: ftp.golder.org/v1
kind: WebDavBackend
metadata:
  name: example-webdav
  namespace: default
spec:
  endpoint: "https://webdav.example.com"
  basePath: "/ftp-storage"  # optional
  credentials:
    username: "webdavuser"
    password: "webdavpass"
    # Or use Kubernetes Secret:
    useSecret:
      secretName: "webdav-credentials"
      usernameKey: "username"
      passwordKey: "password"
  tls:
    insecureSkipVerify: false
    # caCert: "..."  # TODO: CA certificate support
status:
  ready: true
  message: "Backend connection established"
```

### FilesystemBackend CRD

Configures local filesystem storage backends with Kubernetes persistent volumes.

```yaml
apiVersion: ftp.golder.org/v1
kind: FilesystemBackend
metadata:
  name: example-filesystem
  namespace: default
spec:
  basePath: "/data/ftp"
  readOnly: false
  fileMode: "0644"        # File permissions (octal)
  dirMode: "0755"         # Directory permissions (octal)
  maxFileSize: 0          # Maximum file size in bytes (0 = no limit)
  volumeClaimRef:         # Optional PVC reference
    name: "ftp-storage"
    namespace: "default"  # defaults to same namespace
status:
  ready: true
  message: "Filesystem backend ready"
  mountPath: "/data/ftp"
```

**Required PersistentVolumeClaim:**
```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ftp-storage
  namespace: default
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
  # storageClassName: fast-ssd  # specify storage class if needed
```

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `FTP_PORT` | FTP server port | `21` (root), `2121` (non-root) |
| `FTP_PASSIVE_PORT_MIN` | Minimum passive port range | `30000` |
| `FTP_PASSIVE_PORT_MAX` | Maximum passive port range | `30100` |
| `FTP_WELCOME_MESSAGE` | FTP welcome message | `"Welcome to KubeFTPd"` |
| `FTP_IDLE_TIMEOUT` | FTP connection idle timeout (seconds) | `300` |
| `FTP_MAX_CONNECTIONS` | Maximum concurrent FTP connections | `100` |
| `LOG_LEVEL` | Logging level (debug, info, warn, error) | `info` |
| `LOG_FORMAT` | Log format (json, text) | `json` |
| `HTTP_PORT` | HTTP server port (metrics, health, status) | `8080` |

### Built-in User Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `--enable-anonymous` | Enable anonymous FTP access (RFC 1635) | `false` |
| `--anonymous-home-dir` | Home directory for anonymous users | `/pub` |
| `--anonymous-backend-kind` | Backend kind for anonymous users | `FilesystemBackend` |
| `--anonymous-backend-name` | Backend name for anonymous users | `anonymous-backend` |
| `--enable-admin` | Enable built-in admin user | `false` |
| `--admin-password-secret` | Kubernetes Secret name for admin password | `""` |
| `--admin-home-dir` | Home directory for admin user | `/` |
| `--admin-backend-kind` | Backend kind for admin user | `FilesystemBackend` |
| `--admin-backend-name` | Backend name for admin user | `admin-backend` |

### OpenTelemetry Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP endpoint for traces and metrics | `""` (disabled) |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | OTLP endpoint for traces only | `""` (disabled) |
| `OTEL_SERVICE_NAME` | Service name for telemetry | `""` (disabled) |
| `OTEL_RESOURCE_ATTRIBUTES` | Additional resource attributes | `""` |

**Note**: OpenTelemetry tracing is automatically enabled when any `OTEL_*` environment variables are configured.

### Security Best Practices

1. **Secure Port 21 Binding**: KubeFTPd uses `CAP_NET_BIND_SERVICE` capability for secure port binding:
   - **Non-root Execution**: Runs as non-root user (UID 65532) for enhanced security
   - **Privileged Port Access**: `CAP_NET_BIND_SERVICE` allows binding to port 21 without root privileges
   - **Minimal Capabilities**: Drops all capabilities except `NET_BIND_SERVICE` for least privilege principle
   - **Standard FTP Port**: Uses port 21 by default while maintaining security best practices

2. **Fail-Fast Architecture**: KubeFTPd implements fail-fast startup behavior:
   - FTP server binding failures cause immediate application termination
   - Kubernetes will restart the pod, providing clear feedback about configuration issues
   - Health checks only pass when FTP service is actually functional

3. **Use Kubernetes Secrets** for storing credentials instead of plain text
4. **Enable Webhook Validation** for password policies and production compliance:
   ```yaml
   webhook:
     enabled: true
     validation:
       passwordStrength:
         enabled: true
         minLength: 8
         requireComplexity: true
       production:
         enabled: true
         requireSecrets: true
   ```
3. **Production Environment Setup**:
   - Label production namespaces: `kubectl label namespace production environment=production`
   - Use strong password patterns avoiding common words
   - Follow secret naming convention: `*-ftp-password` or `*-ftp-credentials`
4. **Enable TLS** for all backend connections
5. **Set appropriate RBAC** permissions for the KubeFTPd service account
6. **Use NetworkPolicies** to restrict FTP traffic
7. **Regularly rotate** credentials and certificates

## Development

### Prerequisites

- Go 1.25+
- Docker
- Kubernetes cluster (kind/minikube for local development)
- kubebuilder v3.0+

### Setup

1. **Clone the repository:**
```bash
git clone https://github.com/rossigee/kubeftpd.git
cd kubeftpd
```

2. **Install dependencies:**
```bash
go mod download
```

3. **Set up pre-commit hooks:**
```bash
make setup-pre-commit
```

4. **Run tests:**
```bash
make test
make test-coverage
```

5. **Lint code:**
```bash
make lint              # Basic linting with go vet, gofmt
make lint-advanced     # Comprehensive linting (requires golangci-lint Go 1.25+ support)
```

6. **Build and run locally:**
```bash
make build
make run
```

### Available Make Targets

| Target | Description |
|--------|-------------|
| `make build` | Build the kubeftpd binary |
| `make run` | Run the controller locally |
| `make test` | Run unit tests |
| `make test-coverage` | Run tests with coverage report |
| `make lint` | Run golangci-lint |
| `make security-scan` | Run gosec security scanner |
| `make manifests` | Generate CRD manifests |
| `make generate` | Generate code |
| `make docker-build` | Build Docker image |
| `make docker-push` | Push Docker image |
| `make install` | Install CRDs to cluster |
| `make uninstall` | Remove CRDs from cluster |
| `make deploy` | Deploy controller to cluster |
| `make undeploy` | Remove controller from cluster |

### Testing

The project includes comprehensive testing:

- **Unit Tests**: Test individual components and functions
- **Integration Tests**: Test CRD controllers and storage backends
- **E2E Tests**: Test complete FTP workflows
- **Security Tests**: Scan for security vulnerabilities

Run all tests:
```bash
make test-all
```

### Code Quality

We maintain high code quality standards:

- **golangci-lint**: Comprehensive linting
- **gosec**: Security vulnerability scanning
- **Pre-commit hooks**: Automated quality checks
- **Code coverage**: Minimum 80% coverage requirement

## Deployment

### Production Deployment

1. **Create namespace:**
```bash
kubectl create namespace kubeftpd-system
```

2. **Install CRDs:**
```bash
kubectl apply -f https://github.com/rossigee/kubeftpd/releases/latest/download/crds.yaml
```

3. **Deploy using Helm (recommended):**
```bash
helm repo add kubeftpd https://rossigee.github.io/kubeftpd
helm install kubeftpd kubeftpd/kubeftpd -n kubeftpd-system \
  --set controller.image.tag=v0.6.6 \
  --set webhook.enabled=true \
  --set ftp.service.port=2121  # Example: override default port
```

**Helm Configuration Options:**
```yaml
# values.yaml
ftp:
  service:
    port: 21  # Configurable for non-root deployment
  passive:
    service:
      portRange:
        min: 30000
        max: 30100

webhook:
  enabled: true  # Enable password validation
  validation:
    passwordStrength:
      enabled: true
      minLength: 8
      requireComplexity: true
    production:
      enabled: true
      requireSecrets: true

security:
  passwordPolicy:
    enforceStrong: true
    minLength: 8
    requireComplexity: true
```

4. **Or deploy using kubectl:**
```bash
kubectl apply -f https://github.com/rossigee/kubeftpd/releases/latest/download/kubeftpd.yaml
```

### Load Balancer Configuration

For production use, configure a LoadBalancer service:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: kubeftpd-ftp
  namespace: kubeftpd-system
spec:
  type: LoadBalancer
  ports:
  - name: ftp
    port: 21
    targetPort: 21
  - name: ftp-passive
    port: 30000
    targetPort: 30000
    # Add range for passive ports 30000-30100
  selector:
    app: kubeftpd
```

### Gateway API Configuration

For modern Kubernetes deployments, use Gateway API as an alternative to LoadBalancer:

```yaml
# Enable Gateway API support
ftp:
  service:
    port: 21
    passivePortRange:
      min: 10000
      max: 10019

  gateway:
    enabled: true
    config:
      gatewayClassName: "cilium"  # or istio, nginx-gateway, etc.
```

**⚠️ Important**: Gateway API requires individual listeners per port. Each passive port creates separate Gateway listeners and TCPRoute resources.

**Benefits of Gateway API:**
- **Standardized**: Vendor-neutral configuration across different Gateway implementations
- **Advanced**: Rich traffic management and policy capabilities
- **Secure**: Fine-grained access control and multi-tenancy support
- **Efficient**: Shared infrastructure reduces resource overhead

**See [GATEWAY_API_SUPPORT.md](GATEWAY_API_SUPPORT.md) for detailed configuration and examples.**

### Cozystack Platform Deployment

For [Cozystack](https://cozystack.io/) PaaS platform deployment with FluxCD GitOps:

```bash
# Deploy with FluxCD HelmRelease
kubectl apply -f examples/cozystack/helmrelease.yaml

# Or deploy with Kustomize
kubectl apply -k examples/cozystack/

# Or use Cozystack-optimized values
helm install kubeftpd ./chart/kubeftpd \
  --values chart/kubeftpd/examples/values-cozystack.yaml
```

**Cozystack Features:**
- **FluxCD Integration**: Native HelmRelease resources for GitOps workflows
- **Multi-tenant Security**: Enhanced security configurations for shared environments
- **Resource Optimization**: Conservative resource limits suitable for PaaS platforms
- **Network Policies**: Automatic network isolation for multi-tenant deployments

**See [COZYSTACK_INTEGRATION.md](COZYSTACK_INTEGRATION.md) for detailed deployment guide and examples.**

## Migration from SFTPGo

### Migration Steps

1. **Export existing users** from SFTPGo configuration
2. **Create equivalent User CRDs** using the migration script:
   ```bash
   scripts/migrate-from-sftpgo.sh users.json
   ```
3. **Update backend configurations** to use MinioBackend/WebDavBackend CRDs
4. **Test connections** with existing FTP clients
5. **Update DNS/load balancer** to point to KubeFTPd service
6. **Decommission SFTPGo** after validation

### Compatibility Notes

- **Protocol**: KubeFTPd currently supports PASV mode only (EPSV planned)
- **Authentication**: Migrates to Kubernetes-native user management
- **Storage**: Direct compatibility with existing MinIO/S3 buckets
- **Permissions**: Enhanced permission model with Kubernetes RBAC integration

## Monitoring and Observability

### Structured Logging

KubeFTPd provides comprehensive structured logging for all FTP operations with detailed success/failure information:

**Log Format Examples:**
```
[testuser] UPLOAD SUCCESS: /docs/file.pdf (1024 bytes, 450ms)
[testuser] DOWNLOAD SUCCESS: /images/photo.jpg (2560 bytes, 120ms)
[testuser] DELETE FAILED: /protected/secret.txt - permission denied
[testuser] LIST SUCCESS: /documents/
[testuser] MKDIR SUCCESS: /newfolder/
```

**Logged Operations:**
- **File Operations**: UPLOAD, DOWNLOAD, DELETE with size, duration, and status
- **Directory Operations**: LIST, MKDIR, RMDIR with success/failure status  
- **Authentication**: User login/logout with session duration
- **Errors**: Detailed error information with context

### OpenTelemetry Tracing

Distributed tracing support for FTP operations when OpenTelemetry is configured:

**Traced Operations:**
- `ftp.upload` - File uploads with size, duration, backend type
- `ftp.download` - File downloads with offset, size, timing
- `ftp.append` - File append operations
- `ftp.delete` - File and directory deletions

**Trace Attributes:**
- `ftp.user` - Authenticated username
- `ftp.path` - File/directory path
- `ftp.backend` - Storage backend type
- `ftp.bytes` - Transfer size in bytes
- `ftp.duration_ms` - Operation duration

### Health Checks

- **Liveness**: `/healthz` on port 8080
- **Readiness**: `/readyz` on port 8080
- **Status**: `/` on port 8080 (service information)

### Metrics

Prometheus metrics available on `/metrics` endpoint (port 8080):

**Connection & Session Metrics:**
- `kubeftpd_active_connections` - Number of active FTP connections
- `kubeftpd_connections_total` - Total FTP connections (by username, client_ip)
- `kubeftpd_connection_duration_seconds` - Duration of FTP connections (histogram)
- `kubeftpd_user_session_duration_seconds` - Duration of user sessions (histogram)

**Authentication Metrics:**
- `kubeftpd_user_logins_total` - Total user login attempts (by username, result)
- `kubeftpd_authentication_attempts_total` - Authentication attempts by method and result
- `kubeftpd_password_retrieval_duration_seconds` - Password retrieval latency from secrets

**File Operation Metrics:**
- `kubeftpd_file_operations_total` - Total file operations (by username, operation, backend_type, result)
- `kubeftpd_file_transfer_bytes_total` - Total bytes transferred (by username, direction, backend_type)
- `kubeftpd_file_transfer_duration_seconds` - Duration of file transfers (histogram)

**Backend Performance Metrics:**
- `kubeftpd_backend_operations_total` - Backend operations (by backend_name, backend_type, operation, result)
- `kubeftpd_backend_response_time_seconds` - Backend operation response times (histogram)

**System Metrics:**
- `kubeftpd_errors_total` - Error counters by type and component
- `kubeftpd_config_reloads_total` - Configuration reload events

### Logging

Structured JSON logging with configurable levels:

```json
{
  "timestamp": "2024-01-15T10:30:00Z",
  "level": "info",
  "msg": "User authenticated successfully",
  "username": "scanner",
  "backend": "minio-backend",
  "client_ip": "192.168.1.100"
}
```

## Troubleshooting

### Common Issues

1. **Connection refused**
   - Check FTP port (21) is accessible
   - Verify LoadBalancer/NodePort configuration
   - Check firewall rules

2. **Authentication failures**
   - Verify User CRD exists and is enabled
   - Check credentials in User spec (password or passwordSecret)
   - For secret-based auth: verify secret exists and contains correct key
   - Check webhook validation logs if enabled
   - Review controller logs for errors

3. **Backend connection errors**
   - Verify Backend CRD status
   - Check network connectivity to storage backend
   - Validate credentials and permissions

4. **PASV data connection failures** (`No route to host` on passive mode)
   - **Problem**: Separate LoadBalancer services for control/data ports get different external IPs
   - **Solution**: Use combined LoadBalancer service (default in v0.5.0+)
   - **Check**: Verify both port 21 and passive ports (10000-10019) are on same service
   - **Configuration**: Set `FTP_PUBLIC_IP` environment variable to LoadBalancer external IP
   - **Legacy**: Ensure passive port range is accessible and check NAT/firewall configuration
   - See [PASV_LOADBALANCER_FIX.md](PASV_LOADBALANCER_FIX.md) for detailed migration instructions

5. **Webhook validation issues**
   - Check webhook pod status: `kubectl get pods -l app.kubernetes.io/component=webhook`
   - Review webhook logs: `kubectl logs -l app.kubernetes.io/component=webhook`
   - Verify webhook configuration: `kubectl get validatingadmissionwebhook`
   - Test user creation with detailed error messages

### Debug Mode

Enable debug logging:
```bash
kubectl set env deployment/kubeftpd-controller LOG_LEVEL=debug -n kubeftpd-system
```

View controller logs:
```bash
kubectl logs -f deployment/kubeftpd-controller -n kubeftpd-system
```

## Contributing

We welcome contributions! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

### Development Workflow

1. Fork the repository
2. Create a feature branch
3. Make changes with tests
4. Run quality checks: `make lint test security-scan`
5. Submit a pull request

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## Support

- **Issues**: [GitHub Issues](https://github.com/rossigee/kubeftpd/issues)
- **Discussions**: [GitHub Discussions](https://github.com/rossigee/kubeftpd/discussions)
- **Documentation**: [Wiki](https://github.com/rossigee/kubeftpd/wiki)

## Roadmap

- [ ] Active FTP mode support (PORT command)
- [ ] Extended Passive mode (EPSV) support
- [ ] FTPS (FTP over TLS/SSL) support
- [ ] SFTP protocol support
- [ ] Multi-tenancy with namespace isolation
- [ ] Advanced user quota management
- [ ] Audit logging and compliance features
- [ ] Integration with external identity providers (LDAP, OIDC)

## License

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
