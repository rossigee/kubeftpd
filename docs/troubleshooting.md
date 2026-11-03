# Troubleshooting

Start with the logs and the User or Backend status, then work through the matching section below.

```bash
kubectl logs -f deployment/kubeftpd-controller-manager -n kubeftpd-system
kubectl get users,miniobackends,webdavbackends,filesystembackends -A
```

The controller deployment name depends on your Helm release. Run `kubectl get deploy -n kubeftpd-system` to find it.

To get more detail, set the log level and restart:

```bash
kubectl set env deployment/<controller-deployment> LOG_LEVEL=debug -n kubeftpd-system
```

## Connection refused

- Confirm port 21 is reachable through the LoadBalancer or Gateway.
- Check the Service has an external IP: `kubectl get svc -n kubeftpd-system`.
- Check network policies and firewalls between the client and the cluster.
- If the controller logs show a bind error on the FTP port, the process exits and restarts. Fix the bind address or port in [configuration.md](configuration.md).

## Authentication fails

- Confirm the User exists and `spec.enabled` is `true`.
- For Secret-based users, confirm the Secret exists in the User's namespace (or the namespace given in `passwordSecret.namespace`) and contains the key named in `passwordSecret.key`.
- Look for `Invalid password` and `failed to get secret` in the logs.
- If the webhook is enabled, check its logs for rejected passwords. See [passwords.md](passwords.md#validation-rules).

## Backend connection errors

- Check the backend's status: `kubectl describe miniobackend <name>` (or the matching kind).
- Confirm the endpoint is reachable from the controller pod.
- Check the credentials. For a Secret reference, confirm the key names match.
- For TLS errors with a private CA, provide the CA with `caSecretRef` or `caCert`. Don't disable verification.

## Passive data connections fail with "No route to host"

Passive mode needs the control port and the passive data ports to share one external IP:

1. **One Service.** Put port 21 and every passive port in the same LoadBalancer Service. Splitting them gives each a different IP, and clients then try to reach the data ports on the wrong address.
2. **Advertise the right address.** Set `FTP_PUBLIC_IP` to the Service's external IP:

   ```bash
   kubectl get service <ftp-service> -n kubeftpd-system \
     -o jsonpath='{.status.loadBalancer.ingress[0].ip}'
   ```

3. **Open the passive range.** Allow the full range (10000-10019 by default) through any firewall or NAT in front of the cluster.
4. **Check from a client:**

   ```text
   ftp <external-ip>
   ftp> passive
   ftp> ls
   ```

If the Service has the right ports but passive transfers still hang, see the known issue on passive port declarations in [configuration.md](configuration.md#known-issues).

## Webhook rejects or doesn't validate

- Check the webhook pod: `kubectl get pods -l app.kubernetes.io/component=webhook -n kubeftpd-system`.
- Check the webhook configuration: `kubectl get validatingwebhookconfigurations`. The chart creates it only when `webhook.enabled=true`.
- Test a user with a weak password with `kubectl apply --dry-run=server`. The webhook should reject it.

## Common errors

| Error | Cause |
|-------|-------|
| `password in secret <ns>/<name> is weak` | The Secret's password fails the strength rules |
| `production password secrets must follow naming convention` | Production Secret name doesn't end in `-ftp-password` or `-ftp-credentials` |
| `either password or passwordSecret is required` | Neither field set |
| `cannot specify both password and passwordSecret` | Both fields set |
| `password not found in secret with key` | Wrong `passwordSecret.key` |
