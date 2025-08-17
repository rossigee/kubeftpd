# KubeFTPd

A Kubernetes-native FTP service that provides secure file transfer capabilities using Custom Resource Definitions (CRDs) for user and backend management.

## Overview

KubeFTPd is designed to replace traditional FTP solutions like SFTPGo with a cloud-native approach that leverages Kubernetes for configuration and management. It supports multiple storage backends including MinIO (S3-compatible) and WebDAV, making it suitable for various use cases from document scanning workflows to general file transfer needs.

### Key Features

- **Kubernetes-Native**: Uses CRDs for user and backend configuration
- **Multiple Storage Backends**: Support for MinIO/S3 and WebDAV endpoints
- **PASV Mode Support**: Currently supports passive FTP mode with active mode planned
- **RBAC Integration**: Full Kubernetes RBAC support for access control
- **Health & Metrics**: Built-in health checks, JSON logging, and metrics endpoints
- **Security First**: TLS support, dual password authentication (plaintext/secrets), webhook validation
- **Password Security**: Kubernetes Secrets integration with production restrictions and strength validation
- **Webhook Validation**: Admission controllers for password policies and production compliance

## Architecture

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   FTP Client    │────│   KubeFTPd       │────│ Storage Backend │
│                 │    │   (Controller)   │    │ (MinIO/WebDAV)  │
└─────────────────┘    └──────────────────┘    └─────────────────┘
                              │
                              ▼
                       ┌──────────────────┐
                       │ Kubernetes API   │
                       │ (User/Backend    │
                       │  CRDs)           │
                       └──────────────────┘
```

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
apiVersion: ftp.rossigee.com/v1
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
apiVersion: ftp.rossigee.com/v1
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

4. **Create FTP users:**

Option A - Using plaintext password (development only):
```yaml
apiVersion: ftp.rossigee.com/v1
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
apiVersion: ftp.rossigee.com/v1
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

Defines FTP users with their credentials, permissions, and backend configuration. Supports both plaintext passwords (development) and Kubernetes Secrets (production).

**Password Methods (mutually exclusive):**

Option A - Plaintext password:
```yaml
apiVersion: ftp.rossigee.com/v1
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
apiVersion: ftp.rossigee.com/v1
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
apiVersion: ftp.rossigee.com/v1
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
      secretName: "minio-credentials"
      accessKeyIDKey: "access-key"
      secretAccessKeyKey: "secret-key"
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
apiVersion: ftp.rossigee.com/v1
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

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `FTP_PORT` | FTP server port (configurable for non-root) | `21` |
| `FTP_PASSIVE_PORT_MIN` | Minimum passive port range | `30000` |
| `FTP_PASSIVE_PORT_MAX` | Maximum passive port range | `30100` |
| `FTP_WELCOME_MESSAGE` | FTP welcome message | `"Welcome to KubeFTPd"` |
| `FTP_IDLE_TIMEOUT` | FTP connection idle timeout (seconds) | `300` |
| `FTP_MAX_CONNECTIONS` | Maximum concurrent FTP connections | `100` |
| `LOG_LEVEL` | Logging level (debug, info, warn, error) | `info` |
| `LOG_FORMAT` | Log format (json, text) | `json` |
| `METRICS_PORT` | Metrics endpoint port | `8080` |
| `HEALTH_PORT` | Health check endpoint port | `8081` |

### Security Best Practices

1. **Use Kubernetes Secrets** for storing credentials instead of plain text
2. **Enable Webhook Validation** for password policies and production compliance:
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
  --set webhook.enabled=true \
  --set ftp.service.port=2121  # Example: non-root port
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

### Health Checks

- **Liveness**: `/healthz` on port 8081
- **Readiness**: `/readyz` on port 8081

### Metrics

Prometheus metrics available on `/metrics` endpoint (port 8080):

- `kubeftpd_active_connections` - Number of active FTP connections
- `kubeftpd_user_logins_total` - Total user login attempts  
- `kubeftpd_authentication_attempts_total` - Authentication attempts by method and result
- `kubeftpd_password_retrieval_duration_seconds` - Password retrieval latency from secrets
- `kubeftpd_backend_operations_total` - Backend operation counters
- `kubeftpd_errors_total` - Error counters by type

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

4. **Passive mode issues**
   - Ensure passive port range is accessible
   - Check NAT/firewall configuration
   - Verify client passive mode settings

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
