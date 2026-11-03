# KubeFTPd

[![CI](https://github.com/rossigee/kubeftpd/actions/workflows/ci.yml/badge.svg)](https://github.com/rossigee/kubeftpd/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/rossigee/kubeftpd/branch/master/graph/badge.svg)](https://codecov.io/gh/rossigee/kubeftpd)
[![Go Report Card](https://goreportcard.com/badge/github.com/rossigee/kubeftpd)](https://goreportcard.com/report/github.com/rossigee/kubeftpd)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

A Kubernetes-native FTP server. Users and storage backends are Kubernetes custom resources, so access is managed the same way as the rest of your cluster.

## Features

- **Kubernetes-native**: users and backends are `User`, `MinioBackend`, `WebDavBackend` and `FilesystemBackend` resources
- **Storage backends**: S3-compatible (MinIO), WebDAV and local filesystem volumes
- **Passwords from Secrets**: production namespaces can require Secret-based passwords, enforced by an admission webhook
- **Built-in accounts**: optional anonymous (RFC 1635) and admin users
- **Exposure**: a LoadBalancer Service or Gateway API
- **Observability**: structured JSON logs, Prometheus metrics, health endpoints and OpenTelemetry tracing

Passive (PASV) mode is supported. Active mode is planned.

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

## Quick start

**Requirements:** a Kubernetes cluster (1.25 or later), `kubectl` and `helm`.

1. **Install the chart:**

   ```bash
   kubectl create namespace kubeftpd-system
   helm install kubeftpd oci://ghcr.io/rossigee/kubeftpd/charts/kubeftpd \
     --version 0.10.0 \
     --namespace kubeftpd-system \
     --set webhook.enabled=true
   ```

2. **Create a storage backend and a user.** This example uses a filesystem volume:

   ```yaml
   apiVersion: ftp.golder.org/v1
   kind: FilesystemBackend
   metadata:
     name: filesystem-backend
     namespace: default
   spec:
     basePath: /data/ftp
     volumeClaimRef:
       name: ftp-storage
   ---
   apiVersion: v1
   kind: PersistentVolumeClaim
   metadata:
     name: ftp-storage
     namespace: default
   spec:
     accessModes: [ReadWriteOnce]
     resources:
       requests:
         storage: 10Gi
   ```

   Create the user's password Secret interactively (so the password isn't in your shell history), then the User:

   ```bash
   read -rs -p "FTP password for alice: " PASSWORD && echo
   kubectl create secret generic alice-ftp-password -n default --from-literal=password="$PASSWORD"
   unset PASSWORD
   ```

   ```yaml
   apiVersion: ftp.golder.org/v1
   kind: User
   metadata:
     name: alice
     namespace: default
   spec:
     username: alice
     passwordSecret:
       name: alice-ftp-password
     homeDirectory: /alice
     enabled: true
     backend:
       kind: FilesystemBackend
       name: filesystem-backend
     permissions:
       read: true
       write: true
       delete: false
       list: true
   ```

3. **Connect:**

   ```bash
   ftp <external-ip-or-hostname>
   ```

Other install methods, load balancer setup and Gateway API are in [docs/deployment.md](docs/deployment.md).

## Documentation

Start at the [documentation index](docs/README.md). The main pages are:

- [Deployment](docs/deployment.md): Helm, kubectl, load balancer, Gateway API
- [Custom resources](docs/custom-resources.md): users and backends, with full field examples
- [Passwords](docs/passwords.md): Secrets, validation rules, migration and incident response
- [Configuration](docs/configuration.md): environment variables and built-in users
- [Monitoring](docs/monitoring.md): logs, metrics, health and tracing
- [Troubleshooting](docs/troubleshooting.md)
- [Migration from SFTPGo](docs/migration-from-sftpgo.md)
- [Development](docs/development.md)
- [Examples](examples/README.md)

## Contributing

Open a pull request against `master`. CI must pass before merge. See [docs/development.md](docs/development.md#contributing).

## Roadmap

- [ ] Active FTP mode (PORT command)
- [ ] Extended passive mode (EPSV)
- [ ] SFTP protocol support
- [ ] Multi-tenancy with namespace isolation
- [ ] Advanced user quota management
- [ ] Audit logging and compliance features
- [ ] Integration with external identity providers (LDAP, OIDC)

## Support

- [Issues](https://github.com/rossigee/kubeftpd/issues)
- [Discussions](https://github.com/rossigee/kubeftpd/discussions)

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
