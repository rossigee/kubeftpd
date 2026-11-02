# Monitoring

## Logs

KubeFTPd writes structured JSON logs to stdout. Set `LOG_LEVEL` to `debug`, `info`, `warn` or `error`.

Uploads are logged when they start and when they finish. A successful upload includes the username, path, size and duration:

```json
{
  "level": "info",
  "msg": "Upload operation successful",
  "username": "alice",
  "path": "/docs/file.pdf",
  "resolved_path": "/data/alice/docs/file.pdf",
  "size_bytes": 1024,
  "duration_ms": 450
}
```

Messages to search for:

| Message | Meaning |
|---------|---------|
| `FTP upload operation` | Upload started |
| `Upload operation successful` | Upload finished |
| `Upload operation failed` | Upload failed; the `error` field has the reason |
| `CHROOT VIOLATION: Attempted access outside home directory` | A user tried to leave their home directory |
| `Starting KubeFTPd server` | Process started; shows bind address, port and passive range |

Find failed authentications and secret-read errors with:

```bash
kubectl logs -l app.kubernetes.io/name=kubeftpd -n kubeftpd-system --since=1h | grep -i 'invalid password\|failed to get secret'
```

## Metrics

Prometheus metrics are served on `/metrics` on port 8080 (`HTTP_BIND_ADDRESS`).

**Connections and sessions**

| Metric | Type |
|--------|------|
| `kubeftpd_active_connections` | gauge |
| `kubeftpd_connections_total` | counter |
| `kubeftpd_connection_duration_seconds` | histogram |
| `kubeftpd_user_session_duration_seconds` | histogram |
| `kubeftpd_user_logins_total` | counter |

**File operations and transfers**

| Metric | Type |
|--------|------|
| `kubeftpd_file_operations_total` | counter |
| `kubeftpd_file_transfer_bytes_total` | counter |
| `kubeftpd_file_transfer_duration_seconds` | histogram |

**Backends**

| Metric | Type |
|--------|------|
| `kubeftpd_backend_operations_total` | counter |
| `kubeftpd_backend_response_time_seconds` | histogram |

**Authentication** (see [passwords.md](passwords.md#metrics-and-alerts))

| Metric | Type |
|--------|------|
| `kubeftpd_auth_attempts_total` | counter |
| `kubeftpd_auth_failures_total` | counter |
| `kubeftpd_auth_lockouts_total` | counter |
| `kubeftpd_auth_locked_accounts` | gauge |
| `kubeftpd_secret_access_errors_total` | counter |
| `kubeftpd_user_secret_missing` | gauge |

**Errors and configuration**

| Metric | Type |
|--------|------|
| `kubeftpd_errors_total` | counter |
| `kubeftpd_config_reloads_total` | counter |

A Prometheus Operator `ServiceMonitor` is available in the chart: set `controller.http.serviceMonitor.enabled=true`.

## Health

| Endpoint | Port | Use |
|----------|------|-----|
| `/healthz` | 8080 | Liveness (used by the chart) |
| `/readyz` | not set by the chart | Readiness (registered with the controller manager) |
| `/` | 8080 | Status information |

## Tracing

Tracing turns on when any `OTEL_*` variable is set. See [configuration.md](configuration.md#opentelemetry).

Spans:

- `ftp.upload`, `ftp.append`: uploads and appends
- `ftp.download`: downloads

Span attributes:

| Attribute | Value |
|-----------|-------|
| `ftp.user` | Authenticated username |
| `ftp.path` | File path |
| `ftp.backend` | Storage backend type |
| `ftp.bytes` | Bytes transferred |
| `ftp.duration_ms` | Operation duration in milliseconds |
| `ftp.append` | `true` for appends |
