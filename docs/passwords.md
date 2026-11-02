# Passwords

A `User` can get its password in one of two ways. Use a Secret for anything beyond development.

| Method | Field | Use |
|--------|-------|-----|
| Kubernetes Secret | `spec.passwordSecret` | Production (recommended) |
| Plaintext | `spec.password` | Development only |

The two are mutually exclusive. Setting both, or neither, is rejected by the admission webhook.

## Validation rules

The validation webhook (enabled with `webhook.enabled=true`) checks every password, both plaintext values and values read from Secrets. A password must:

- be at least `webhook.validation.passwordStrength.minLength` characters long
- contain upper- and lowercase letters, a digit and a special character
- not contain common weak patterns such as `password`, `123456` or `qwerty`
- not contain sequential characters such as `abc` or `123`

## Production restrictions

Namespaces labelled `environment=production` are held to stricter rules:

- plaintext passwords are rejected
- the Secret name must end in `-ftp-password` or `-ftp-credentials`

```bash
kubectl label namespace production environment=production
```

## Creating a user with a Secret

Enter the password interactively so it doesn't end up in your shell history or in process listings:

```bash
read -rs -p "FTP password for alice: " PASSWORD && echo
kubectl create secret generic alice-ftp-password \
  --namespace=ftp-users \
  --from-literal=password="$PASSWORD"
unset PASSWORD
```

Then reference the Secret from the User:

```yaml
apiVersion: ftp.golder.org/v1
kind: User
metadata:
  name: alice
  namespace: ftp-users
spec:
  username: alice
  passwordSecret:
    name: alice-ftp-password
    key: password
  homeDirectory: /alice
  enabled: true
  backend:
    kind: MinioBackend
    name: my-backend
  permissions:
    read: true
    write: true
    delete: false
    list: true
```

`scripts/create-user-secret.sh <secret-name> <password> [namespace]` wraps the `kubectl create secret` step. It takes the password as a positional argument, so the password lands in your shell history; prefer the interactive commands above.

## Migrating from plaintext

1. **Create a Secret** for each user that has a plaintext password (see above).
2. **Replace the field** in the User manifest. Remove `password` and add `passwordSecret`:

   ```yaml
   spec:
     # password: "<remove this>"
     passwordSecret:
       name: alice-ftp-password
       key: password        # optional, defaults to "password"
       # namespace: other-ns  # optional, defaults to the User's namespace
   ```

3. **Apply the manifest** with `kubectl apply -f`.
4. **Check authentication** with an FTP client using the existing password.
5. **Remove the plaintext value** from git and from any other copies of the manifest.

If a migration breaks logins, restore the previous User manifest from version control. Don't put the plaintext password back into the User.

## Rotation

Update the Secret, then tell the user. The User resource doesn't change.

```bash
read -rs -p "New FTP password for alice: " PASSWORD && echo
kubectl create secret generic alice-ftp-password \
  --namespace=ftp-users \
  --from-literal=password="$PASSWORD" \
  --dry-run=client -o yaml | kubectl apply -f -
unset PASSWORD
kubectl annotate secret alice-ftp-password --namespace=ftp-users \
  last-rotation="$(date -Iseconds)" --overwrite
```

Rotate every password on a schedule, quarterly for most accounts. Record the schedule with a label, for example `rotation-schedule=quarterly`, so it can be queried.

## Deactivating a user

Disable the account without deleting it:

```bash
kubectl patch user alice -n ftp-users --type=merge -p '{"spec":{"enabled":false}}'
```

## Incident response: suspected compromise

1. **Disable the user** (command above) so new logins are refused.
2. **Rotate the Secret** (see Rotation) with a new password.
3. **Review logs** for the user's sessions and file operations (see [monitoring.md](monitoring.md#logs)).
4. **Tell the user** the new password through a secure channel.
5. **Re-enable the user** once you've confirmed the account is safe:

   ```bash
   kubectl patch user alice -n ftp-users --type=merge -p '{"spec":{"enabled":true}}'
   ```

## Auditing

List users that still have plaintext passwords:

```bash
kubectl get users --all-namespaces -o json | jq -r \
  '.items[] | select(.spec.password != null) | "\(.metadata.namespace)/\(.metadata.name)"'
```

List users whose Secret is missing:

```bash
kubectl get users --all-namespaces -o json | jq -r \
  '.items[] | select(.spec.passwordSecret != null) |
   "\(.metadata.namespace) \(.metadata.name) \(.spec.passwordSecret.name)"' |
while read -r ns name secret; do
  kubectl get secret "$secret" -n "$ns" >/dev/null 2>&1 || echo "$ns/$name: missing secret $secret"
done
```

## Metrics and alerts

Password-related metrics exported on `/metrics`:

| Metric | Meaning |
|--------|---------|
| `kubeftpd_auth_attempts_total` | Authentication attempts, by method and result |
| `kubeftpd_auth_failures_total` | Authentication failures, by reason |
| `kubeftpd_auth_lockouts_total` | Accounts locked by brute-force protection |
| `kubeftpd_auth_locked_accounts` | Accounts currently locked |
| `kubeftpd_secret_access_errors_total` | Failed reads of password Secrets |
| `kubeftpd_user_secret_missing` | Users whose referenced Secret is missing |

`config/monitoring/password-security-monitoring.yaml` defines a recording rule (`kubeftpd:users_plaintext_password_total`) and alerts for plaintext passwords in production and for high failure rates.

## Security notes

- Enable encryption at rest for Secrets in your cluster.
- Restrict who can read password Secrets with RBAC. `config/rbac/password-security-rbac.yaml` is a starting point.
- Never print generated passwords to a terminal, log or file that others can read.
