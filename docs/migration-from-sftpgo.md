# Migrating from SFTPGo

KubeFTPd replaces SFTPGo with Kubernetes resources: each account becomes a `User` and each storage target becomes a backend resource.

## Steps

1. **Export users** from SFTPGo.
2. **Create backends.** Define a `MinioBackend`, `WebDavBackend` or `FilesystemBackend` for each storage target. See [custom-resources.md](custom-resources.md#storage-backends).
3. **Create a Secret per user** with the existing password. See [passwords.md](passwords.md#creating-a-user-with-a-secret).
4. **Create a `User` per account**, referencing the backend and the Secret.
5. **Test with an FTP client** against the new service.
6. **Point DNS or the load balancer** at KubeFTPd, then decommission SFTPGo.

There is no automated converter yet. Steps 2 to 4 are manual, so keep the export somewhere you can check each account against.

## What changes

| Area | SFTPGo | KubeFTPd |
|------|--------|----------|
| Configuration | Config file and database | Kubernetes resources, managed with GitOps |
| Users | SFTPGo user records | `User` resources, one per account |
| Passwords | Stored by SFTPGo | Kubernetes Secrets (recommended) |
| Storage | Virtual folders and backends | Backend resources |
| Access control | SFTPGo permissions | `User.spec.permissions` and Kubernetes RBAC |

## Protocol

KubeFTPd supports FTP with passive (PASV) data connections only. Clients that need active mode must switch to passive. Explicit FTPS (RFC 4217) was added in v0.8.0; see [the changelog](../CHANGELOG.md). KubeFTPd does not serve SFTP.
