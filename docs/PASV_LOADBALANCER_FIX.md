# FTP PASV Data Connection Fix - LoadBalancer Service Consolidation

## Problem

KubeFTPd was configured with separate LoadBalancer services for FTP control and passive data connections:

- `ftp-service`: Exposed port 21 (FTP control)
- `ftp-passive-service`: Exposed ports 30000-30019 (PASV data)

This caused **PASV data connection failures** because:

1. Each LoadBalancer service gets its own external IP address
2. FTP server advertises the control connection IP (from `ftp-service`) for PASV responses  
3. Clients try to connect to PASV ports on that IP, but those ports are on a different service with a different IP
4. Result: `No route to host` errors on PASV data connections

## Solution

**Combined both services into a single LoadBalancer** that exposes all required ports:

- Port 21: FTP control connection
- Ports 10000-10019: FTP passive data connections

This ensures both control and data connections use the **same external IP address**.

## Changes Made

### 1. Helm Chart Updates

**values.yaml:**
- Changed default FTP port from 2121 to 21 (standard FTP)
- Added `passivePortRange` to main service configuration
- Changed passive port range from 30000-30019 to 10000-10019
- Disabled separate passive service by default

**templates/service.yaml:**
- Combined FTP control and passive ports into single service
- Kept legacy passive service support (marked as deprecated)

### 2. Kubernetes Manifests

**config/manager/ftp_service.yaml:**
- Removed separate `ftp-passive-service`
- Combined all ports (21 + 10000-10019) into single `ftp-service`

**config/production/ftp_service_patch.yaml:**
- Updated to handle combined service configuration
- Added production-specific LoadBalancer annotations

**config/production/ftp_passive_service_patch.yaml:**
- Deprecated file (replaced with combined service)

### 3. Application Configuration

**cmd/main.go:**
- Default PASV ports changed from 30000-31000 to 10000-10020
- FTP_PUBLIC_IP environment variable support added
- PASV port range configurable via FTP_PASSIVE_PORT_MIN/MAX

## Migration Guide

### For Existing Deployments

1. **Update your deployment** with the new manifests
2. **Set FTP_PUBLIC_IP** environment variable to your LoadBalancer's external IP:
   ```bash
   kubectl get service ftp-service -o jsonpath='{.status.loadBalancer.ingress[0].ip}'
   ```
3. **Update firewall rules** if needed (new port range: 10000-10019)
4. **Remove the old passive service** after confirming the new service works

### Environment Variables

```yaml
env:
- name: FTP_PUBLIC_IP
  value: "YOUR_LOADBALANCER_EXTERNAL_IP"  # Set this to your actual LoadBalancer IP
- name: FTP_PASSIVE_PORTS
  value: "10000-10019"  # Or use FTP_PASSIVE_PORT_MIN/MAX
```

### Helm Values

```yaml
ftp:
  service:
    type: LoadBalancer
    port: 21
    passivePortRange:
      min: 10000
      max: 10019
    # Set this to ensure consistent IP allocation
    loadBalancerIP: "YOUR_RESERVED_IP"  # Optional but recommended

  # Disable legacy separate passive service
  passive:
    enabled: false
```

## Verification

After deployment, verify the fix:

1. **Check service has all ports:**
   ```bash
   kubectl get service ftp-service -o wide
   ```

2. **Confirm external IP:**
   ```bash
   kubectl get service ftp-service -o jsonpath='{.status.loadBalancer.ingress[0].ip}'
   ```

3. **Test PASV connections:**
   ```bash
   ftp YOUR_LOADBALANCER_IP
   # Enter credentials
   ftp> passive
   ftp> dir
   # Should work without "No route to host" errors
   ```

## Technical Details

### Why This Fix Works

1. **Single External IP**: Both control and data connections use the same LoadBalancer IP
2. **Proper PASV Advertising**: Server advertises the correct IP (LoadBalancer IP) for data connections
3. **Session Affinity**: LoadBalancer configuration ensures PASV connections reach the same pod
4. **Port Consistency**: All required ports are exposed on the same service

### LoadBalancer Annotations

Production deployments include optimized LoadBalancer settings:

```yaml
annotations:
  service.beta.kubernetes.io/aws-load-balancer-type: "nlb"
  service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled: "true"
spec:
  externalTrafficPolicy: Local
  sessionAffinity: ClientIP
  sessionAffinityConfig:
    clientIP:
      timeoutSeconds: 3600
```

## Related Issues

This fix resolves:
- PASV data connection timeouts
- "No route to host" errors on PASV ports  
- FTP client hanging after PASV command
- Inconsistent FTP server behavior

## Regression Prevention

Added comprehensive regression tests:
- `TestProcessEnvironmentOverrides_PublicIP`
- `TestProcessEnvironmentOverrides_PASVPorts`
- `TestKubeDriver_PutFile_OffsetForced`
- Service template validation

The combined LoadBalancer approach is now the recommended and default configuration for all KubeFTPd deployments.
