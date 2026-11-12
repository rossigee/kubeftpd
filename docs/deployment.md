# Deployment

## Container images

Images are published to GitHub Container Registry for `linux/amd64` and `linux/arm64`:

```text
ghcr.io/rossigee/kubeftpd:v0.10.3   # a release
ghcr.io/rossigee/kubeftpd:latest   # the most recent release
```

## Helm (recommended)

The chart is published as an OCI artifact alongside the container image.

```bash
kubectl create namespace kubeftpd-system

helm install kubeftpd oci://ghcr.io/rossigee/kubeftpd/charts/kubeftpd \
  --version 0.10.3 \
  --namespace kubeftpd-system \
  --set webhook.enabled=true
```

Customise with a values file:

```yaml
# values.yaml
controller:
  image:
    tag: v0.10.3          # pin the version; the chart defaults to the chart's appVersion
  replicas: 1
  logLevel: info

ftp:
  service:
    type: LoadBalancer   # or ClusterIP with Gateway API (see gateway-api.md)
    port: 21
    passivePortRange:
      min: 10000
      max: 10019         # one Service port per passive port

webhook:
  enabled: true          # enforce password policy on admission
  validation:
    passwordStrength:
      enabled: true
      minLength: 8
      requireComplexity: true
    production:
      enabled: true
      requireSecrets: true
```

Run `helm show values oci://ghcr.io/rossigee/kubeftpd/charts/kubeftpd --version 0.10.3` for the full list of options.

## kubectl

Each release attaches two manifests:

```bash
kubectl apply -f https://github.com/rossigee/kubeftpd/releases/download/v0.10.3/crds.yaml
kubectl apply -f https://github.com/rossigee/kubeftpd/releases/download/v0.10.3/kubeftpd.yaml
```

Use `releases/latest/download/` in place of `download/v0.10.3/` to track the newest release.

## Load balancer

Expose the FTP control port and the passive data ports from **one** LoadBalancer Service. Splitting them across Services gives them different external IPs, and passive-mode clients then fail with "No route to host". The chart creates this Service when `ftp.service.type` is `LoadBalancer`.
For a hand-written Service, match the chart's labels and list every port:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: kubeftpd-ftp
  namespace: kubeftpd-system
spec:
  type: LoadBalancer
  externalTrafficPolicy: Local
  sessionAffinity: ClientIP
  selector:
    app.kubernetes.io/name: kubeftpd
    app.kubernetes.io/instance: kubeftpd
    app.kubernetes.io/component: manager
  ports:
    - name: ftp
      port: 21
      targetPort: ftp
      protocol: TCP
    # One entry per passive port, 10000 to 10019 inclusive
    - name: passive-10000
      port: 10000
      targetPort: passive-10000
      protocol: TCP
    # ... passive-10001 through passive-10019
```

Set `FTP_PUBLIC_IP` to the Service's external IP so passive responses advertise an address clients can reach:

```bash
kubectl get service kubeftpd-ftp -n kubeftpd-system \
  -o jsonpath='{.status.loadBalancer.ingress[0].ip}'
```

Reserve the IP with `loadBalancerIP` (where your provider supports it) so it doesn't change on re-creation. Cloud-specific annotations, for example for AWS Network Load Balancers, belong on the Service and depend on your provider.

## Gateway API

For clusters with a Gateway API implementation, use `ftp.gateway.enabled=true` instead of a LoadBalancer. See [gateway-api.md](gateway-api.md).

## Migrating from separate passive services

Earlier releases used a separate passive Service with a different external IP. The combined Service above replaces it. After switching, remove the old Service and check that passive connections work with `ftp> passive` followed by `ftp> ls`. See [troubleshooting.md](troubleshooting.md#passive-data-connections-fail-with-no-route-to-host).
