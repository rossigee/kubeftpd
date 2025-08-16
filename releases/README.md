# KubeFTPd v0.1.0 Release Artifacts

This directory contains the release artifacts for KubeFTPd v0.1.0.

## Installation Files

### install_v0.1.0.yaml
Complete installation manifests for KubeFTPd including:
- CustomResourceDefinitions (CRDs) for User, MinioBackend, WebDavBackend, FilesystemBackend
- RBAC configuration (ClusterRole, ClusterRoleBinding, ServiceAccount)
- Controller deployment
- Metrics service
- FTP services (LoadBalancer type)

**Installation:**
```bash
kubectl apply -f install_v0.1.0.yaml
```

### production_v0.1.0.yaml
Production-optimized deployment with:
- Enhanced resource limits and requests
- AWS Network Load Balancer annotations
- Session affinity for FTP connections
- 2 replica deployment for high availability

**Installation:**
```bash
kubectl apply -f production_v0.1.0.yaml
```

### samples_v0.1.0.yaml
Sample Custom Resource manifests:
- MinioBackend example
- WebDAVBackend example
- FilesystemBackend example with PersistentVolumeClaim
- User examples for each backend type

**Installation:**
```bash
kubectl apply -f samples_v0.1.0.yaml
```

## Quick Start

1. Install KubeFTPd:
   ```bash
   kubectl apply -f install_v0.1.0.yaml
   ```

2. Wait for deployment:
   ```bash
   kubectl wait --for=condition=Available deployment/kubeftpd-controller-manager -n kubeftpd-system --timeout=300s
   ```

3. Apply sample configurations:
   ```bash
   kubectl apply -f samples_v0.1.0.yaml
   ```

4. Check FTP service status:
   ```bash
   kubectl get svc -n kubeftpd-system
   ```

## Release Notes

See [CHANGELOG.md](../CHANGELOG.md) for detailed release notes and feature documentation.

## Support

- GitHub: https://github.com/rossigee/kubeftpd
- Issues: https://github.com/rossigee/kubeftpd/issues