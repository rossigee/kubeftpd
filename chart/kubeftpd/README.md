# KubeFTPd Helm Chart

This Helm chart deploys KubeFTPd, a Kubernetes-native FTP server with support for multiple storage backends.

## Features

- **Multiple Storage Backends**: Filesystem, MinIO (S3), and WebDAV support
- **Kubernetes Native**: Full CRD integration with controller pattern
- **Security**: RBAC, network policies, and secure defaults
- **Monitoring**: Prometheus metrics and ServiceMonitor support
- **High Availability**: Pod disruption budgets and configurable replicas
- **Streaming**: Constant memory usage file uploads
- **Production Ready**: Comprehensive configuration options

## Prerequisites

- Kubernetes 1.19+
- Helm 3.0+

## Installation

### Add the Helm Repository

```bash
# If you have a chart repository
helm repo add kubeftpd https://charts.example.com/kubeftpd
helm repo update
```

### Install from Local Chart

```bash
# Clone the repository
git clone https://github.com/rossigee/kubeftpd.git
cd kubeftpd

# Install the chart
helm install my-kubeftpd ./chart/kubeftpd
```

### Install with Custom Values

```bash
helm install my-kubeftpd ./chart/kubeftpd -f my-values.yaml
```

## Configuration

### Basic Configuration

The most basic configuration requires setting up at least one storage backend and one user:

```yaml
# values.yaml
backends:
  filesystem:
    enabled: true
    instances:
      - name: local-storage
        basePath: /data
        readOnly: false
        pvc:
          enabled: true
          name: kubeftpd-storage
          size: 10Gi

users:
  admin:
    enabled: true
    username: "admin"
    password: "secure-password"  # Change this!
    backend:
      kind: "FilesystemBackend"
      name: "local-storage"
```

### Storage Backend Examples

#### Filesystem Backend with PVC

```yaml
backends:
  filesystem:
    enabled: true
    instances:
      - name: local-storage
        basePath: /data
        readOnly: false
        fileMode: "0644"
        dirMode: "0755"
        maxFileSize: 104857600  # 100MB
        pvc:
          enabled: true
          name: kubeftpd-storage
          size: 10Gi
          storageClass: "fast-ssd"
          accessModes:
            - ReadWriteOnce
```

#### MinIO Backend

```yaml
backends:
  minio:
    enabled: true
    instances:
      - name: minio-backend
        endpoint: minio.default.svc.cluster.local:9000
        bucket: ftp-data
        region: us-east-1
        useSSL: false
        credentials:
          secretName: minio-credentials
          accessKeyKey: accessKey
          secretKeyKey: secretKey
```

#### WebDAV Backend

```yaml
backends:
  webdav:
    enabled: true
    instances:
      - name: webdav-backend
        baseURL: https://webdav.example.com/dav
        credentials:
          secretName: webdav-credentials
          usernameKey: username
          passwordKey: password
```

### User Configuration

#### Multiple Users with Different Permissions

```yaml
users:
  admin:
    enabled: true
    username: "admin"
    password: "admin-password"
    homeDirectory: "/admin"
    backend:
      kind: "FilesystemBackend"
      name: "local-storage"
    permissions:
      read: true
      write: true
      delete: true
      list: true

  additional:
    - name: readonly-user
      username: "readonly"
      password: "readonly-password"
      homeDirectory: "/readonly"
      backend:
        kind: "FilesystemBackend"
        name: "local-storage"
      permissions:
        read: true
        write: false
        delete: false
        list: true

    - name: upload-user
      username: "uploader"
      password: "upload-password"
      homeDirectory: "/uploads"
      backend:
        kind: "MinioBackend"
        name: "minio-backend"
      permissions:
        read: true
        write: true
        delete: false
        list: true
```

### Service Configuration

#### LoadBalancer with External IP

```yaml
ftp:
  service:
    type: LoadBalancer
    loadBalancerIP: "203.0.113.10"
    annotations:
      service.beta.kubernetes.io/aws-load-balancer-type: "nlb"

  passive:
    enabled: true
    service:
      type: LoadBalancer
      loadBalancerIP: "203.0.113.10"
      portRange:
        min: 30000
        max: 30100
```

#### NodePort Configuration

```yaml
ftp:
  service:
    type: NodePort
    port: 21

  passive:
    enabled: true
    service:
      type: NodePort
      portRange:
        min: 30000
        max: 30010
```

### Monitoring Configuration

#### Enable Prometheus Monitoring

```yaml
controller:
  metrics:
    enabled: true
    serviceMonitor:
      enabled: true
      additionalLabels:
        prometheus: kube-prometheus
      interval: 30s
      scrapeTimeout: 10s
```

### Security Configuration

#### Network Policies

```yaml
networkPolicy:
  enabled: true
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              name: monitoring
      ports:
        - protocol: TCP
          port: 8443
```

#### Pod Security

```yaml
controller:
  securityContext:
    runAsNonRoot: true
    runAsUser: 65532
    fsGroup: 65532
    capabilities:
      drop:
        - ALL
    seccompProfile:
      type: RuntimeDefault
```

### High Availability

#### Pod Disruption Budget

```yaml
podDisruptionBudget:
  enabled: true
  minAvailable: 1
```

## Parameters

### Global Parameters

| Name | Description | Value |
|------|-------------|-------|
| `global.imageRegistry` | Global Docker image registry | `""` |

### Controller Parameters

| Name | Description | Value |
|------|-------------|-------|
| `controller.image.registry` | Controller image registry | `ghcr.io` |
| `controller.image.repository` | Controller image repository | `rossigee/kubeftpd` |
| `controller.image.tag` | Controller image tag | `v0.1.0` |
| `controller.image.pullPolicy` | Controller image pull policy | `IfNotPresent` |
| `controller.resources.limits` | Resource limits for controller | `{cpu: 500m, memory: 128Mi}` |
| `controller.resources.requests` | Resource requests for controller | `{cpu: 100m, memory: 64Mi}` |

### FTP Service Parameters

| Name | Description | Value |
|------|-------------|-------|
| `ftp.service.type` | FTP service type | `LoadBalancer` |
| `ftp.service.port` | FTP service port | `21` |
| `ftp.passive.enabled` | Enable passive FTP | `true` |
| `ftp.passive.service.portRange.min` | Passive port range minimum | `30000` |
| `ftp.passive.service.portRange.max` | Passive port range maximum | `30100` |

### Storage Backend Parameters

| Name | Description | Value |
|------|-------------|-------|
| `backends.filesystem.enabled` | Enable filesystem backend | `false` |
| `backends.minio.enabled` | Enable MinIO backend | `false` |
| `backends.webdav.enabled` | Enable WebDAV backend | `false` |

### User Parameters

| Name | Description | Value |
|------|-------------|-------|
| `users.admin.enabled` | Enable admin user | `true` |
| `users.admin.username` | Admin username | `admin` |
| `users.admin.password` | Admin password | `changeme` |

For a complete list of parameters, see the [values.yaml](values.yaml) file.

## Examples

### Simple Local Storage Setup

```yaml
# simple-local.yaml
backends:
  filesystem:
    enabled: true
    instances:
      - name: local-storage
        basePath: /data
        pvc:
          enabled: true
          name: kubeftpd-storage
          size: 5Gi

users:
  admin:
    password: "my-secure-password"
    backend:
      name: "local-storage"

ftp:
  service:
    type: NodePort
```

```bash
helm install kubeftpd ./chart/kubeftpd -f simple-local.yaml
```

### Production MinIO Setup

```yaml
# production-minio.yaml
backends:
  minio:
    enabled: true
    instances:
      - name: production-minio
        endpoint: minio.storage.svc.cluster.local:9000
        bucket: ftp-production
        useSSL: true
        credentials:
          secretName: minio-production-credentials

users:
  admin:
    password: "very-secure-admin-password"
    backend:
      kind: "MinioBackend"
      name: "production-minio"

  additional:
    - name: app-upload
      username: "app-uploader"
      password: "app-upload-password"
      homeDirectory: "/uploads"
      backend:
        kind: "MinioBackend"
        name: "production-minio"
      permissions:
        write: true
        delete: false

controller:
  resources:
    limits:
      cpu: 1000m
      memory: 256Mi
    requests:
      cpu: 200m
      memory: 128Mi

  metrics:
    enabled: true
    serviceMonitor:
      enabled: true

podDisruptionBudget:
  enabled: true
  minAvailable: 1

networkPolicy:
  enabled: true
```

```bash
helm install kubeftpd ./chart/kubeftpd -f production-minio.yaml
```

## Upgrading

### To a New Chart Version

```bash
helm upgrade my-kubeftpd ./chart/kubeftpd
```

### With Value Changes

```bash
helm upgrade my-kubeftpd ./chart/kubeftpd -f new-values.yaml
```

## Uninstallation

```bash
helm uninstall my-kubeftpd
```

**Note**: This will not delete PVCs created by the chart. You may need to delete them manually:

```bash
kubectl delete pvc kubeftpd-storage
```

## Troubleshooting

### Common Issues

#### PVC Not Binding

If your PVC is not binding, check:
1. Storage class exists and is default
2. Sufficient storage available
3. Access modes are supported

```bash
kubectl get pvc
kubectl describe pvc kubeftpd-storage
```

#### FTP Connection Issues

1. Check service type and external IP:
```bash
kubectl get svc
```

2. Verify pod is running:
```bash
kubectl get pods
kubectl logs deployment/kubeftpd-controller-manager
```

3. Check user credentials:
```bash
kubectl get users
kubectl describe user kubeftpd-admin
```

#### Backend Not Ready

Check backend status:
```bash
kubectl get filesystembackends
kubectl describe filesystembackend local-storage
```

### Debugging

Enable debug logging:

```yaml
controller:
  args:
    - --v=2  # Increase verbosity
```

Check controller logs:
```bash
kubectl logs deployment/kubeftpd-controller-manager -f
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Test with different configurations
5. Submit a pull request

## License

This Helm chart is licensed under the Apache License 2.0.
