# Custom Resources

KubeFTPd is configured with four custom resources in the `ftp.golder.org/v1` API group:

| Kind | Purpose |
|------|---------|
| `User` | An FTP account: credentials, home directory, backend and permissions |
| `MinioBackend` | An S3-compatible (MinIO) bucket |
| `WebDavBackend` | A WebDAV endpoint |
| `FilesystemBackend` | A directory on a Kubernetes volume |

## User

A `User` binds a username and password to a backend.

```yaml
apiVersion: ftp.golder.org/v1
kind: User
metadata:
  name: scanner
  namespace: default
spec:
  username: scanner
  passwordSecret:            # or `password:` for development only
    name: scanner-ftp-password
    key: password           # optional, defaults to "password"
  homeDirectory: /receipts
  enabled: true
  backend:
    kind: MinioBackend      # or WebDavBackend, FilesystemBackend
    name: minio-backend
    namespace: default       # optional, defaults to the User's namespace
  permissions:
    read: true
    write: true
    delete: false
    list: true
```

Exactly one of `password` or `passwordSecret` must be set. Use `passwordSecret` in any environment that matters. See [passwords.md](passwords.md) for the rules and the production restrictions.

The `spec.type` field selects an account type:

- `regular` (default): a normal FTP user
- `anonymous`: RFC 1635 anonymous access, read-only by default
- `admin`: full access

## Storage backends

### MinioBackend

```yaml
apiVersion: ftp.golder.org/v1
kind: MinioBackend
metadata:
  name: minio-backend
  namespace: default
spec:
  endpoint: https://minio.example.com
  bucket: ftp-storage
  region: us-east-1
  pathPrefix: ftp-data/        # optional
  credentials:
    # Either inline (development only) ...
    accessKeyID: "<access-key-id>"
    secretAccessKey: "<secret-access-key>"
    # ... or read from a Secret:
    # useSecret:
    #   name: minio-credentials
    #   namespace: custom-namespace      # optional, defaults to the backend's namespace
    #   accessKeyIDKey: access-key       # optional, defaults to "accessKeyID"
    #   secretAccessKeyKey: secret-key   # optional, defaults to "secretAccessKey"
  tls:
    insecureSkipVerify: false          # never set to true in production
    caCert: |                          # inline CA bundle, or:
    # caSecretRef:
    #   name: minio-ca
    #   namespace: certs                # optional
    #   key: ca.crt                     # optional, defaults to "ca.crt"
```

### WebDavBackend

```yaml
apiVersion: ftp.golder.org/v1
kind: WebDavBackend
metadata:
  name: webdav-backend
  namespace: default
spec:
  endpoint: https://webdav.example.com
  basePath: /ftp-data            # optional
  credentials:
    username: "<username>"
    password: "<password>"       # or use useSecret:
    # useSecret:
    #   secretName: webdav-credentials
    #   usernameKey: username    # optional
    #   passwordKey: password    # optional
  tls:
    insecureSkipVerify: false
    caSecretRef:
      name: webdav-ca
      key: ca.crt
```

**Server requirements.** kubeftpd checks whether a path exists with a `PROPFIND` (`Depth: 0`). For a path that does not exist, the server must answer `404 Not Found`, or `207 Multi-Status` with an entry whose `href` is that path. A `207` that only describes other resources is treated as "not found". A server that reports a missing path as an existing file makes FTP clients fail with `550 ... is a file`.

### FilesystemBackend

Stores files on a PersistentVolumeClaim mounted into the controller pod.

```yaml
apiVersion: ftp.golder.org/v1
kind: FilesystemBackend
metadata:
  name: filesystem-backend
  namespace: default
spec:
  basePath: /data/ftp
  readOnly: false
  fileMode: "0644"
  dirMode: "0755"
  maxFileSize: 0                 # bytes; 0 = no limit
  volumeClaimRef:
    name: ftp-storage
    namespace: default          # optional, defaults to the same namespace
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
  # storageClassName: fast-ssd    # optional
```

## Built-in users

The controller can create and maintain an anonymous user and an admin user from command-line flags. See the flag table in [configuration.md](configuration.md#built-in-users).

```bash
# Anonymous access on a filesystem backend
kubeftpd --enable-anonymous \
  --anonymous-home-dir=/pub \
  --anonymous-backend-kind=FilesystemBackend \
  --anonymous-backend-name=anonymous-backend

# Admin user, password read from a Secret
kubectl create secret generic admin-secret --from-literal=password="<choose-a-strong-password>"
kubeftpd --enable-admin \
  --admin-password-secret=admin-secret \
  --admin-home-dir=/ \
  --admin-backend-kind=FilesystemBackend \
  --admin-backend-name=admin-backend
```

| Account | Username | User resource | Access |
|---------|----------|---------------|--------|
| Anonymous | `anonymous` (any password accepted) | `builtin-anonymous` | read and list only |
| Admin | `admin` | `builtin-admin` | read, write, delete and list |

The controller labels these resources with `kubeftpd.golder.org/builtin: "true"`. It creates them when the flag is set, updates them when the configuration changes, and deletes them when the flag is cleared.
