# Gateway API Support for KubeFTPd

KubeFTPd now supports [Gateway API](https://gateway-api.sigs.k8s.io/) as an alternative to LoadBalancer services for FTP traffic routing. This provides more flexible, standardized, and vendor-neutral load balancing capabilities.

## Overview

Gateway API support enables:
- **Standardized Configuration**: Vendor-neutral configuration across different Gateway implementations
- **Advanced Traffic Management**: More sophisticated routing and load balancing features
- **TCP Support**: Direct TCP routing for FTP control and passive data connections
- **Multi-tenancy**: Better isolation and resource sharing capabilities
- **Enhanced Security**: Fine-grained access control and policy enforcement

## ⚠️ Important Limitations

**Port Range Limitation**: Gateway API does **NOT** support port ranges. Each port requires:
- Individual listener in Gateway resource
- Separate TCPRoute resource

For FTP passive mode (e.g., ports 10000-10019), this creates:
- **20 Gateway listeners**
- **20 TCPRoute resources**

**Resource Impact**: Large port ranges can overwhelm Kubernetes API server and Gateway controllers.

## Prerequisites

1. **Gateway API CRDs**: Ensure Gateway API CRDs are installed in your cluster
   ```bash
   kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.0.0/standard-install.yaml
   ```

2. **Gateway Controller**: Install a Gateway API controller (e.g., Istio, Nginx Gateway, Envoy Gateway)
   ```bash
   # Example: Istio Gateway
   istioctl install --set values.pilot.env.EXTERNAL_ISTIOD=false

   # Example: Nginx Gateway
   helm install nginx-gateway oci://ghcr.io/nginxinc/charts/nginx-gateway-fabric
   ```

3. **TCP Support**: Verify your Gateway implementation supports TCP routing
   - ✅ Istio Gateway (full TCP support)
   - ✅ Envoy Gateway (TCP support)
   - ⚠️ Nginx Gateway (limited TCP support)
   - ❓ Check your specific implementation's documentation

## Configuration

### Port Strategy Configuration

KubeFTPd provides three strategies to handle Gateway API port limitations:

#### Strategy 1: Limited (Recommended)
Exposes only a subset of passive ports to minimize resource usage:

```yaml
ftp:
  service:
    port: 21
    passivePortRange:
      min: 10000
      max: 10019

  gateway:
    enabled: true
    create: true
    config:
      gatewayClassName: "istio"
      passivePortStrategy: "limited"  # Only expose first N ports
      maxPassivePorts: 5              # Creates listeners for 10000-10004
```

#### Strategy 2: Single (Control Port Only)
Only exposes FTP control port, requires active FTP clients:

```yaml
ftp:
  gateway:
    enabled: true
    create: true
    config:
      gatewayClassName: "istio"
      passivePortStrategy: "single"  # Only port 21, no passive ports
```

#### Strategy 3: Full (Use with Small Ranges)
Exposes all passive ports - use only with small port ranges:

```yaml
ftp:
  service:
    passivePortRange:
      min: 10000
      max: 10004  # SMALL range recommended

  gateway:
    enabled: true
    create: true
    config:
      gatewayClassName: "istio"
      passivePortStrategy: "full"  # Creates listeners for ALL ports
```

### Advanced Configuration

#### Using Existing Gateway

```yaml
ftp:
  gateway:
    enabled: true
    create: false
    gatewayName: "shared-infrastructure-gateway"
```

#### External IP Assignment

```yaml
ftp:
  gateway:
    enabled: true
    config:
      addresses:
        - type: IPAddress
          value: "203.0.113.100"
```

#### Cross-Namespace Access

```yaml
ftp:
  gateway:
    enabled: true
    config:
      listeners:
        - name: ftp-control
          port: 21
          protocol: TCP
          allowedRoutes:
            namespaces:
              from: All  # Allow access from all namespaces
```

## Gateway Implementations

### Istio Gateway

**Features**: Full TCP support, advanced traffic management, mTLS, observability
```yaml
ftp:
  gateway:
    enabled: true
    config:
      gatewayClassName: "istio"
      addresses:
        - type: IPAddress
          value: "YOUR_EXTERNAL_IP"
```

**Benefits**:
- Native TCP routing support
- Advanced security features (mTLS, RBAC)
- Comprehensive observability and tracing
- Production-ready at scale

### Envoy Gateway

**Features**: High-performance TCP routing, rate limiting, authentication
```yaml
ftp:
  gateway:
    enabled: true
    config:
      gatewayClassName: "envoy-gateway"
```

**Benefits**:
- High-performance TCP proxy
- Built-in rate limiting and authentication
- Cloud-native architecture

### Nginx Gateway

**Features**: HTTP/HTTPS focus, limited TCP support
```yaml
ftp:
  gateway:
    enabled: true
    config:
      gatewayClassName: "nginx-gateway"
      # Note: TCP support may be limited
```

**Limitations**:
- Primary focus on HTTP/HTTPS traffic
- TCP support varies by version
- May require additional configuration

## Deployment Examples

### Example 1: New Gateway with Istio

```bash
# Install with Gateway API support
helm install kubeftpd ./chart/kubeftpd \
  --values chart/kubeftpd/examples/values-gateway.yaml \
  --set ftp.gateway.config.gatewayClassName=istio \
  --set ftp.gateway.config.addresses[0].value="203.0.113.100"
```

### Example 2: Use Existing Gateway

```bash
# Use shared infrastructure Gateway
helm install kubeftpd ./chart/kubeftpd \
  --set ftp.gateway.enabled=true \
  --set ftp.gateway.create=false \
  --set ftp.gateway.gatewayName="shared-gateway"
```

### Example 3: Development Setup

```bash
# Simple Gateway without external IP
helm install kubeftpd ./chart/kubeftpd \
  --set ftp.gateway.enabled=true \
  --set ftp.gateway.config.gatewayClassName="envoy-gateway"
```

## Resource Architecture

When Gateway API is enabled, KubeFTPd creates:

1. **Service (ClusterIP)**: Internal service for pod communication
2. **Gateway**: Traffic entry point with listeners for FTP ports
3. **TCPRoutes**: Routing rules connecting Gateway listeners to Service

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   FTP Client    │────│    Gateway       │────│   TCPRoute      │
│                 │    │  (Port 21 +      │    │  (Routing       │
└─────────────────┘    │   10000-10019)   │    │   Rules)        │
                       └──────────────────┘    └─────────────────┘
                                                        │
                                                        ▼
                                               ┌─────────────────┐
                                               │  Service        │
                                               │  (ClusterIP)    │
                                               └─────────────────┘
                                                        │
                                                        ▼
                                               ┌─────────────────┐
                                               │  KubeFTPd Pod   │
                                               └─────────────────┘
```

## Migration from LoadBalancer

### Step 1: Prepare Gateway Infrastructure

```bash
# Install Gateway API CRDs
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.0.0/standard-install.yaml

# Install Gateway controller (example: Istio)
istioctl install
```

### Step 2: Update Configuration

```yaml
# Before (LoadBalancer)
ftp:
  service:
    type: LoadBalancer
    port: 21

# After (Gateway API)
ftp:
  service:
    port: 21
  gateway:
    enabled: true
    config:
      gatewayClassName: "istio"
```

### Step 3: Deploy and Verify

```bash
# Update deployment
helm upgrade kubeftpd ./chart/kubeftpd -f values-gateway.yaml

# Verify Gateway resources
kubectl get gateway
kubectl get tcproute
kubectl get service

# Test FTP connectivity
ftp YOUR_GATEWAY_IP 21
```

## Troubleshooting

### Common Issues

1. **TCP Support Missing**
   ```bash
   # Check Gateway class capabilities
   kubectl describe gatewayclass YOUR_GATEWAY_CLASS
   ```

2. **TCPRoute Not Working**
   ```bash
   # Check TCPRoute status
   kubectl describe tcproute kubeftpd-ftp-control-route
   ```

3. **Gateway Not Ready**
   ```bash
   # Check Gateway status
   kubectl describe gateway kubeftpd-gateway
   ```

### Debugging Commands

```bash
# Check all Gateway API resources
kubectl get gateway,tcproute,httproute -A

# Verify Gateway controller logs
kubectl logs -n istio-system -l app=istiod

# Test connectivity
telnet GATEWAY_IP 21
```

## Benefits vs LoadBalancer

| Feature | LoadBalancer | Gateway API |
|---------|-------------|------------|
| **Standardization** | Vendor-specific | Vendor-neutral |
| **TCP Support** | Native | Implementation-dependent |
| **Multi-tenancy** | Limited | Advanced |
| **Traffic Management** | Basic | Advanced |
| **Policy Enforcement** | External | Built-in |
| **Observability** | Basic | Rich |
| **Resource Efficiency** | One per service | Shared infrastructure |

## Best Practices

1. **Gateway Class Selection**: Choose implementation based on TCP support and features needed
2. **Resource Sharing**: Use shared Gateway resources for better resource efficiency
3. **Security Configuration**: Enable appropriate allowedRoutes restrictions and use `CAP_NET_BIND_SERVICE` for secure port 21 binding
4. **External IP Management**: Use static IP addresses for production deployments
5. **Monitoring**: Leverage Gateway controller's observability features
6. **Testing**: Thoroughly test TCP routing with your specific Gateway implementation

## Future Enhancements

- **Policy Integration**: Support for Gateway API policy attachments
- **TLS Termination**: SSL/TLS support for secure FTP (FTPS)
- **Rate Limiting**: Built-in rate limiting configuration
- **Multi-Gateway**: Support for multiple Gateway attachments
- **Health Checks**: Integration with Gateway health checking mechanisms

Gateway API support provides a modern, standardized approach to FTP traffic management in Kubernetes environments, offering enhanced flexibility and capabilities over traditional LoadBalancer services.
