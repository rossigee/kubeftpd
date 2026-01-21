# Cozystack Integration for KubeFTPd

KubeFTPd provides native support for deployment on [Cozystack](https://cozystack.io/), a free and open-source PaaS platform built on Kubernetes. This integration leverages Cozystack's FluxCD-based deployment model and multi-tenant architecture.

## Overview

Cozystack integration provides:
- **FluxCD Deployment**: Native HelmRelease resources for GitOps workflows
- **Multi-tenant Support**: Security and resource configurations optimized for shared environments
- **Cozystack Networking**: Integration with Cozystack's networking layer and service exposure
- **Storage Integration**: Compatible with Cozystack's storage classes and persistent volumes
- **Security Hardening**: Enhanced security configurations for multi-tenant environments

## Prerequisites

1. **Cozystack Cluster**: Ensure you have access to a Cozystack cluster
2. **Namespace Access**: Have a tenant namespace where you can deploy resources
3. **FluxCD**: Cozystack's FluxCD should be running and accessible
4. **Storage Class**: Cozystack storage class should be available for persistent volumes

## Quick Start

### Option 1: Direct HelmRelease Deployment

```bash
# 1. Create the HelmRepository in flux-system namespace
kubectl apply -f examples/cozystack/helmrepository.yaml

# 2. Deploy KubeFTPd using HelmRelease
kubectl apply -f examples/cozystack/helmrelease.yaml

# 3. Create demo credentials secret
kubectl apply -f examples/cozystack/secret-demo-password.yaml
```

### Option 2: Kustomize Deployment

```bash
# Deploy everything with Kustomize
kubectl apply -k examples/cozystack/
```

### Option 3: Using Cozystack Values

```bash
# Deploy with Helm using Cozystack-optimized values
helm install kubeftpd ./chart/kubeftpd \
  --values chart/kubeftpd/examples/values-cozystack.yaml \
  --namespace your-tenant-namespace
```

## Configuration

### Cozystack-Specific Values

The `values-cozystack.yaml` file provides optimized configuration for Cozystack environments:

```yaml
# Key configuration highlights
ftp:
  service:
    type: ClusterIP  # Cozystack handles external access
    passivePortRange:
      min: 10000
      max: 10004  # Conservative range for multi-tenant

controller:
  resources:
    limits:
      cpu: 200m
      memory: 128Mi
    requests:
      cpu: 50m
      memory: 64Mi

networkPolicy:
  enabled: true  # Important for multi-tenancy

webhook:
  enabled: true
  validation:
    passwordStrength:
      minLength: 12  # Stronger passwords for shared environment
```

### FluxCD HelmRelease Configuration

The HelmRelease configuration includes:

- **Automated Updates**: Checks for chart updates every 5 minutes
- **Health Monitoring**: Built-in health checks and remediation
- **Security**: Enhanced security configurations for multi-tenant environments
- **Resource Management**: Conservative resource limits suitable for shared infrastructure

## Storage Configuration

### Cozystack PVC Integration

```yaml
backends:
  filesystem:
    enabled: true
    instances:
      - name: cozystack-storage
        basePath: /data
        pvc:
          enabled: true
          name: kubeftpd-storage
          size: 5Gi
          storageClass: ""  # Uses Cozystack's default storage class
```

### MinIO Integration

For Cozystack environments with MinIO:

```yaml
backends:
  minio:
    enabled: true
    instances:
      - name: cozystack-minio
        endpoint: minio.tenant-namespace.svc.cozy.local:9000
        bucket: ftp-data
        credentials:
          secretName: minio-credentials
```

## Networking

### Service Exposure

Cozystack typically handles external service exposure through its networking layer. KubeFTPd is configured with:

```yaml
ftp:
  service:
    type: ClusterIP
    annotations:
      cozystack.io/expose: "tcp"
      cozystack.io/port: "21"
      cozystack.io/service-type: "ftp"
```

### Network Policies

Multi-tenant security is enforced through NetworkPolicies:

```yaml
networkPolicy:
  enabled: true
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              name: cozystack-system
    - ports:
        - protocol: TCP
          port: 21
        - protocol: TCP
          port: 8080
```

## Security

### Enhanced Security for Multi-tenant Environments

1. **Secure Port Binding**: Uses `CAP_NET_BIND_SERVICE` capability for secure port 21 access as non-root
2. **Strong Passwords**: Minimum 12 characters with complexity requirements  
3. **Secret Management**: Mandatory use of Kubernetes Secrets for credentials
4. **Network Isolation**: NetworkPolicies restrict inter-tenant communication
5. **Non-root Execution**: All containers run as non-root users with minimal capabilities
6. **Resource Limits**: Conservative resource limits prevent resource exhaustion

### Credential Management

```bash
# Create FTP user credentials
kubectl create secret generic kubeftpd-user-password \
  --from-literal=password="YourSecurePassword123!" \
  --namespace your-tenant-namespace

# Reference in User resource
apiVersion: ftp.golder.org/v1
kind: User
metadata:
  name: secure-user
spec:
  username: "ftpuser"
  passwordSecret:
    name: "kubeftpd-user-password"
    key: "password"
```

## GitOps Workflow

### Directory Structure

```
your-gitops-repo/
├── clusters/
│   └── cozystack-prod/
│       └── kubeftpd/
│           ├── kustomization.yaml
│           ├── helmrelease.yaml
│           ├── helmrepository.yaml
│           └── secrets/
│               └── sealed-secrets.yaml
```

### Automated Deployment

1. **Commit Changes**: Push configuration changes to your GitOps repository
2. **FluxCD Sync**: Cozystack's FluxCD automatically detects and applies changes
3. **Health Monitoring**: FluxCD monitors deployment health and performs remediation
4. **Rollback**: Automatic rollback on deployment failures

## Monitoring and Observability

### Metrics Integration

KubeFTPd metrics are available on port 8080 and can be scraped by Cozystack's monitoring stack:

```yaml
controller:
  http:
    serviceMonitor:
      enabled: true
      additionalLabels:
        cozystack.io/monitoring: "true"
```

### Logging

Structured JSON logging integrates with Cozystack's logging infrastructure:

```json
{
  "timestamp": "2024-01-15T10:30:00Z",
  "level": "info",
  "msg": "User authenticated successfully",
  "username": "demo",
  "backend": "cozystack-storage",
  "tenant": "your-namespace"
}
```

## Troubleshooting

### Common Issues

1. **Service Not Accessible**
   ```bash
   # Check Cozystack networking configuration
   kubectl get service kubeftpd-ftp-service
   kubectl describe service kubeftpd-ftp-service
   ```

2. **PVC Issues**
   ```bash
   # Check storage class and PVC status
   kubectl get storageclass
   kubectl get pvc kubeftpd-storage
   ```

3. **FluxCD Deployment Issues**
   ```bash
   # Check HelmRelease status
   kubectl get helmrelease kubeftpd
   kubectl describe helmrelease kubeftpd
   ```

4. **Network Policy Blocks**
   ```bash
   # Check network policies
   kubectl get networkpolicy
   kubectl describe networkpolicy kubeftpd
   ```

### Debug Commands

```bash
# Check all KubeFTPd resources
kubectl get all -l cozystack.io/application=kubeftpd

# View controller logs
kubectl logs -l app.kubernetes.io/name=kubeftpd-controller

# Check FluxCD status
kubectl get helmrelease,helmrepository -A

# Test FTP connectivity from within cluster
kubectl run ftp-test --rm -it --image=alpine/curl -- \
  ftp kubeftpd-ftp-service.your-namespace.svc.cozy.local
```

## Best Practices

### Resource Management

1. **CPU/Memory Limits**: Set conservative limits for multi-tenant environments
2. **Storage Quotas**: Use appropriate storage sizes for your use case
3. **Connection Limits**: Configure `maxConnections` based on expected load

### Security

1. **Secret Management**: Use proper secret management (Sealed Secrets, SOPS)
2. **Network Policies**: Always enable NetworkPolicies in multi-tenant environments
3. **RBAC**: Follow principle of least privilege for service accounts
4. **Regular Updates**: Keep charts and images updated for security patches

### GitOps

1. **Version Pinning**: Pin chart versions for production deployments
2. **Environment Separation**: Use separate branches/directories for different environments
3. **Change Review**: Implement proper review processes for configuration changes
4. **Backup**: Ensure configuration and secrets are properly backed up

## Migration from Standalone Kubernetes

If migrating from a standalone Kubernetes deployment:

1. **Export Configuration**: Export your current Helm values
2. **Adapt for Cozystack**: Apply Cozystack-specific modifications
3. **Update Networking**: Change from LoadBalancer to ClusterIP
4. **Add Security**: Enable NetworkPolicies and enhanced security
5. **Test Deployment**: Deploy in development environment first
6. **Migrate Data**: Plan data migration if using different storage

## Support

For Cozystack-specific issues:
- **KubeFTPd Issues**: [GitHub Issues](https://github.com/rossigee/kubeftpd/issues)
- **Cozystack Platform**: [Cozystack Documentation](https://cozystack.io/docs/)
- **FluxCD**: [FluxCD Documentation](https://fluxcd.io/docs/)

## Examples

Complete examples are available in the `examples/cozystack/` directory:
- `helmrelease.yaml`: FluxCD HelmRelease configuration
- `helmrepository.yaml`: Helm repository for FluxCD
- `kustomization.yaml`: Kustomize configuration for GitOps
- `values-cozystack.yaml`: Cozystack-optimized Helm values
- `secret-demo-password.yaml`: Example secret configuration

These examples provide a complete foundation for deploying KubeFTPd on Cozystack with proper security, networking, and multi-tenant considerations.
