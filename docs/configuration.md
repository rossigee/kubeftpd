# Configuration

KubeFTPd is configured with environment variables and command-line flags. The Helm chart sets these for you; see [deployment.md](deployment.md).

## Environment variables

| Variable | Description | Default |
|----------|-------------|---------|
| `FTP_BIND_ADDRESS` | FTP server bind address (empty = all interfaces) | `""` |
| `FTP_PORT` | FTP server port | `21` (root), `2121` (non-root) |
| `FTP_PASSIVE_PORTS` | Passive mode port range | `10000-10020` |
| `FTP_PASSIVE_PORT_MIN` | Lowest passive port. Used with `FTP_PASSIVE_PORT_MAX` when `FTP_PASSIVE_PORTS` is unset | unset |
| `FTP_PASSIVE_PORT_MAX` | Highest passive port. Used with `FTP_PASSIVE_PORT_MIN` when `FTP_PASSIVE_PORTS` is unset | unset |
| `FTP_PUBLIC_IP` | IP advertised in PASV responses (set to the LoadBalancer IP) | `""` |
| `FTP_WELCOME_MESSAGE` | FTP welcome message | built-in message |
| `LOG_LEVEL` | Log level: `debug`, `info`, `warn`, `error` | `info` |
| `HTTP_BIND_ADDRESS` | Address for metrics, health and status (`host:port`) | `:8080` |
| `ENABLE_PROFILING` | Enable Go profiling endpoints | `false` |

Examples:

```bash
# Non-root development run on port 2121, localhost only
FTP_BIND_ADDRESS=127.0.0.1 FTP_PORT=2121 ./kubeftpd

# IPv6 on all interfaces
FTP_BIND_ADDRESS=:: FTP_PORT=2121 ./kubeftpd

# HTTP endpoints on a specific address
HTTP_BIND_ADDRESS=192.168.1.10:8080 ./kubeftpd

# HTTPS for metrics on all interfaces
./kubeftpd --http-bind-address=:8443 --metrics-secure
```

## Built-in users

These flags create and manage `User` resources for the anonymous and admin accounts. See [custom-resources.md](custom-resources.md#built-in-users).

| Flag | Description | Default |
|------|-------------|---------|
| `--enable-anonymous` | Enable anonymous FTP access (RFC 1635) | `false` |
| `--anonymous-home-dir` | Home directory for anonymous users | `/pub` |
| `--anonymous-backend-kind` | Backend kind for anonymous users | `FilesystemBackend` |
| `--anonymous-backend-name` | Backend name for anonymous users | `anonymous-backend` |
| `--enable-admin` | Enable built-in admin user | `false` |
| `--admin-password-secret` | Secret holding the admin password | `""` |
| `--admin-home-dir` | Home directory for admin user | `/` |
| `--admin-backend-kind` | Backend kind for admin user | `FilesystemBackend` |
| `--admin-backend-name` | Backend name for admin user | `admin-backend` |

## OpenTelemetry

Tracing is enabled only when an `OTEL_*` variable is set.

| Variable | Description |
|----------|-------------|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP endpoint for traces and metrics |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | OTLP endpoint for traces only |
| `OTEL_SERVICE_NAME` | Service name reported to the collector |
| `OTEL_RESOURCE_ATTRIBUTES` | Extra resource attributes (read by the OpenTelemetry SDK) |

See [monitoring.md](monitoring.md#tracing) for the spans emitted.

## Known issues

- The Helm values `ftp.settings.idleTimeout` and `ftp.settings.maxConnections` are passed to the controller as `FTP_IDLE_TIMEOUT` and `FTP_MAX_CONNECTIONS`, but the controller does not read those variables yet, so the values have no effect.
- `config/manager` sets `LOG_FORMAT`, which the controller does not read. Logs are always structured JSON.

## Security hardening

1. **Non-root with port 21.** The container runs as UID 65532 and drops all capabilities except `NET_BIND_SERVICE`, which lets it bind port 21 without root.
2. **Fail fast.** If the FTP listener cannot bind, the process exits and Kubernetes restarts it. Health checks only pass while the FTP service works.
3. **Use Kubernetes Secrets** for every password and backend credential. See [passwords.md](passwords.md).
4. **Enable webhook validation** so password policies are enforced on admission:

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

5. **Label production namespaces**: `kubectl label namespace <name> environment=production`
6. **Enable TLS** for all backend connections, using `caSecretRef` or `caCert` where the backend uses a private CA.
7. **Restrict RBAC** for the KubeFTPd service account and for who can read password secrets.
8. **Restrict network access** to the FTP and metrics ports with a `NetworkPolicy`.
9. **Rotate credentials and certificates** regularly.
