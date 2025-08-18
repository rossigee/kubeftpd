# Changelog

All notable changes to KubeFTPd will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [v0.4.1] - 2025-08-18

### Fixed
- **HTTPS/HTTP health probe mismatch**: Fixed health probe failures caused by secureMetrics defaulting to true
  - Health probes were failing with "Client sent an HTTP request to an HTTPS server"
  - Changed secureMetrics default from true to false in main.go:85
  - Controller-runtime was auto-generating TLS certificates when secureMetrics=true but no cert path provided
  - Internal metrics/health endpoints don't need HTTPS for cluster-internal communication
  - Pods now reach 1/1 Ready status with proper HTTP health check responses

## [v0.4.0] - 2025-08-18

### Removed
- **Leader election**: Removed leader election mechanism to simplify deployment and reduce complexity
  - Controllers only validate configurations and update status with no shared state coordination required
  - Eliminates potential port conflicts and simplifies RBAC requirements
  - Multiple replicas can now safely run without coordination overhead
  - Removed `--leader-elect` flag and all associated leader election RBAC resources

### Fixed
- **HTTP port binding conflict**: Fixed port conflict between metrics server and health probe endpoints
  - Both services now properly share the same HTTP server on port 8080
  - Eliminates startup failures caused by port binding conflicts

## [v0.3.1] - 2025-08-18

### Fixed
- **MinioBackend secret namespace bug**: Fixed critical namespace lookup bug where MinioBackend was looking for secrets in 'default' namespace instead of the backend's namespace
  - Secret references without explicit namespace now correctly default to the backend's namespace
  - Added comprehensive unit tests to prevent regression of this issue
  - Updated documentation to clarify namespace behavior for secret references

### Added
- **MinioBackend namespace tests**: Comprehensive unit test suite for namespace secret lookup behavior
  - Regression test to ensure 'default' namespace is never used unless explicitly specified
  - Integration-style test simulating real-world scanner-receipts scenario
  - Tests for custom key names and error handling

### Changed
- **Documentation**: Updated MinioBackend documentation to clarify optional namespace parameter in useSecret configuration

## [v0.3.0] - 2025-08-18

### Changed
- **Port consolidation**: Consolidated all HTTP endpoints onto single port 8080
  - Health checks (`/healthz`, `/readyz`) moved from port 8081 to port 8080
  - Prometheus metrics endpoint remains on port 8080
  - Added service status endpoint (`/`) on port 8080 with version information
- **Configuration simplification**: Removed `--health-probe-bind-address` flag, renamed `--metrics-bind-address` to `--http-bind-address`, and renamed `METRICS_PORT` to `HTTP_PORT` environment variable
- **Container deployment**: Updated all Kubernetes manifests and Docker configurations for single-port setup
- **Service naming**: Renamed metrics service to http service to reflect consolidated endpoints
- **Helm values**: **BREAKING CHANGE** - Renamed `controller.metrics.*` to `controller.http.*` in values.yaml

### Fixed
- **FilesystemBackend validation**: Add missing FilesystemBackend validation support in user controller
- **Chart template**: Correct FTP port configuration in deployment template
- **Chart CRD**: Add passwordSecret field to User CRD and make password optional
- **Chart versioning**: Update default image tag from v0.2.6 to v0.3.0
- **Volume mounting**: Add /data directory volume mount for FilesystemBackend

### Added
- **Status endpoint**: New JSON status endpoint at `/` showing service name, version, commit, date, and status

## [v0.2.6] - Previous Release

### Changed
- **Documentation**: Update README.md to correct API group from ftp.rossigee.com to ftp.golder.org
- **Documentation**: Add comprehensive FilesystemBackend documentation and examples

## [v0.1.1] - 2025-08-16

### Added

#### Password Security & Authentication
- **Dual password authentication system**: Support for both plaintext passwords (development) and Kubernetes Secrets (production)
- **Webhook validation system**: ValidatingAdmissionWebhook for User CRD validation
- **Password strength enforcement**: Configurable password complexity requirements
- **Production security restrictions**: Environment-based password policy enforcement
- **Secret-based authentication**: Complete integration with Kubernetes Secret management
- **Authentication metrics**: Prometheus metrics for login attempts and password retrieval performance

#### Configuration & Deployment
- **Configurable FTP port**: Environment variable support for non-root deployment (`FTP_PORT`)
- **Enhanced environment variables**: Complete FTP server configuration via environment
- **Helm chart enhancements**: Webhook configuration, security policies, flexible deployment options
- **Security context improvements**: Non-root execution with configurable port binding

#### Security Features  
- **Production environment detection**: Automatic security policy enforcement for production namespaces
- **Secret naming conventions**: Enforced naming patterns for production password secrets
- **Webhook validation**: Real-time validation of User CRD with security compliance
- **Password pattern detection**: Prevention of weak passwords with common patterns

#### Testing & Quality
- **Comprehensive test coverage**: Unit, integration, and E2E tests for all authentication methods
- **Webhook validation tests**: Complete test suite for admission controller functionality
- **E2E secret authentication**: End-to-end testing of secret-based user authentication
- **Authentication metrics testing**: Validation of monitoring and observability features

### Changed

#### Breaking Changes
- **User CRD**: Added `passwordSecret` field as alternative to `password` field
- **Mutual exclusivity**: Users must specify either `password` OR `passwordSecret`, not both
- **Production restrictions**: Production environments require secret-based passwords

#### Configuration Updates
- **Environment variables**: Added `FTP_WELCOME_MESSAGE`, `FTP_IDLE_TIMEOUT`, `FTP_MAX_CONNECTIONS`
- **Helm values**: New webhook and security configuration sections
- **Documentation**: Comprehensive updates with security best practices

### Security Enhancements

#### Vulnerability Mitigations
- **Weak password prevention**: Automated detection and rejection of common weak patterns
- **Production compliance**: Enforced secret-based authentication in production environments
- **Password complexity**: Configurable strength requirements (length, complexity, patterns)
- **Secret validation**: Real-time validation of secret existence and accessibility

#### Monitoring & Observability
- **Authentication metrics**: `kubeftpd_authentication_attempts_total`, `kubeftpd_password_retrieval_duration_seconds`
- **Security logging**: Enhanced structured logging for authentication events
- **Webhook monitoring**: Health checks and validation metrics for admission controllers

### Deployment Options

#### Non-Root Deployment
```bash
# Example: Deploy on port 2121 for non-root execution
helm install kubeftpd kubeftpd/kubeftpd \
  --set ftp.service.port=2121 \
  --set webhook.enabled=true
```

#### Production Security
```bash
# Label production namespace
kubectl label namespace production environment=production

# Production-compliant user with secret
kubectl create secret generic user-ftp-password --from-literal=password="MySecure123!"
```

### Migration Guide

#### From v0.1.0 to v0.1.1
1. **Existing users continue to work** - no breaking changes for existing deployments
2. **Optional webhook deployment** - enable with `--set webhook.enabled=true`
3. **Production upgrade path**:
   ```bash
   # Convert existing users to secrets
   kubectl create secret generic user-ftp-password --from-literal=password="$(kubectl get user myuser -o jsonpath='{.spec.password}')"

   # Update user to use secret
   kubectl patch user myuser --type='json' -p='[
     {"op": "remove", "path": "/spec/password"},
     {"op": "add", "path": "/spec/passwordSecret", "value": {"name": "user-ftp-password", "key": "password"}}
   ]'
   ```

## [v0.1.0] - 2025-08-16

### Added

#### Core Features
- **Kubernetes-native FTP service** with Custom Resource Definitions (CRDs)
- **User management** via User CRDs with configurable permissions (read, write, delete, list)
- **Multiple storage backends**:
  - MinioBackend CRD for S3-compatible storage
  - WebDavBackend CRD for HTTP-based storage
- **FTP server** with passive mode support (ports 30000-30100)
- **Health checks and metrics** endpoints for monitoring
- **Structured JSON logging** with configurable levels

#### Kubernetes Integration
- **RBAC configuration** with appropriate permissions
- **ServiceAccount** for controller operations
- **LoadBalancer Services** for FTP traffic
- **Production-ready deployment** manifests
- **Kustomization overlays** for different environments
- **Namespace isolation** support

#### Development Infrastructure
- **Comprehensive test suite** with unit, integration, and e2e tests
- **CI/CD workflows** for automated testing and quality checks
- **Code linting** with golangci-lint integration
- **Pre-commit hooks** for code quality enforcement
- **Docker support** with multi-stage builds
- **Development scripts** for local setup

#### Documentation
- **Comprehensive README** with usage examples and deployment instructions
- **API documentation** in CRD schemas
- **Sample manifests** for all resource types
- **Contributing guidelines** and development setup

### Technical Specifications

#### API Version
- **API Group**: `ftp.golder.org/v1`
- **Resources**: User, MinioBackend, WebDavBackend, FilesystemBackend

#### Supported Protocols
- **FTP**: Passive mode (PASV) support
- **Future**: Active mode and FTPS planned for future releases

#### Storage Backends
- **MinIO/S3**: Full S3-compatible API support with bucket and path prefix configuration
- **WebDAV**: HTTP-based storage with basic authentication
- **Credentials**: Kubernetes Secret integration for secure credential management

#### Security Features
- **Pod Security Standards**: Restricted pod security context
- **Non-root execution**: Runs as non-root user with read-only filesystem
- **Secret management**: Kubernetes Secret integration for credentials
- **RBAC**: Comprehensive role-based access control

#### Observability
- **Metrics**: Prometheus-compatible metrics on `/metrics` endpoint
- **Health checks**: Liveness (`/healthz`) and readiness (`/readyz`) probes
- **Logging**: Structured JSON logging with correlation IDs
- **Version information**: Runtime version reporting

### Installation

#### Quick Install
```bash
kubectl apply -f https://github.com/rossigee/kubeftpd/releases/download/v0.1.0/kubeftpd-v0.1.0.yaml
```

#### Production Install
```bash
kubectl apply -f https://github.com/rossigee/kubeftpd/releases/download/v0.1.0/kubeftpd-production-v0.1.0.yaml
```

### Configuration Examples

#### MinIO Backend
```yaml
apiVersion: ftp.golder.org/v1
kind: MinioBackend
metadata:
  name: minio-storage
spec:
  endpoint: "https://minio.example.com"
  bucket: "ftp-data"
  region: "us-east-1"
  credentials:
    useSecret:
      secretName: minio-credentials
```

#### User Configuration
```yaml
apiVersion: ftp.golder.org/v1
kind: User
metadata:
  name: ftp-user
spec:
  username: "ftpuser"
  password: "secure-password"
  homeDirectory: "/home/ftpuser"
  backend:
    kind: "MinioBackend"
    name: "minio-storage"
  permissions:
    read: true
    write: true
    delete: false
    list: true
```

### Known Limitations

- **FTP Mode**: Currently supports passive mode only (active mode planned)
- **Protocol**: FTP only (FTPS/SFTP planned for future releases)
- **Local Storage**: No local filesystem support (cloud storage only)

### System Requirements

- **Kubernetes**: v1.25 or later
- **LoadBalancer**: Support for LoadBalancer services (for FTP traffic)
- **Storage**: MinIO/S3 or WebDAV backend accessible from cluster

### Breaking Changes

None (initial release)

### Security Notes

- All container images run with restricted security context
- No privileged containers or root access required
- Credentials stored securely in Kubernetes Secrets
- Network policies can be applied for additional security

### Contributors

Initial release developed by the KubeFTPd team.

---

## Unreleased

### Planned Features
- Active FTP mode support (PORT command)
- FTPS (FTP over TLS/SSL) support
- SFTP protocol support
- Multi-tenancy with namespace isolation
- Advanced user quota management
- Audit logging and compliance features
- Integration with external identity providers (LDAP, OIDC)

---

[v0.1.1]: https://github.com/rossigee/kubeftpd/releases/tag/v0.1.1
[v0.1.0]: https://github.com/rossigee/kubeftpd/releases/tag/v0.1.0
