# Password Security Migration Guide

This guide helps you migrate from plaintext passwords to secure secret-based password management in KubeFTPd.

## Overview

KubeFTPd now supports two methods for user password configuration:

1. **Plaintext passwords** (not recommended for production)
2. **Secret-based passwords** (recommended for production)

## Why Migrate?

✅ **Security**: Passwords stored in Kubernetes Secrets are base64 encoded and can be encrypted at rest  
✅ **Compliance**: Meets security best practices for credential management  
✅ **Auditability**: Secret access can be tracked and monitored  
✅ **Rotation**: Easier password rotation without modifying User CRDs  

## Migration Steps

### Step 1: Create Secrets for Existing Users

For each user with a plaintext password, create a corresponding secret:

```bash
# Create secret using the provided script
./scripts/create-user-secret.sh user1-password "currentPlaintextPassword" default

# Or manually with kubectl
kubectl create secret generic user1-password \
  --from-literal=password="currentPlaintextPassword" \
  --namespace=default
```

### Step 2: Update User CRDs

Replace the plaintext `password` field with `passwordSecret` reference:

**Before (plaintext):**
```yaml
apiVersion: ftp.golder.org/v1
kind: User
metadata:
  name: user1
  namespace: default
spec:
  username: "user1"
  password: "currentPlaintextPassword"  # Remove this
  backend:
    kind: MinioBackend
    name: my-backend
  homeDirectory: "/home/user1"
```

**After (secret-based):**
```yaml
apiVersion: ftp.golder.org/v1
kind: User
metadata:
  name: user1
  namespace: default
spec:
  username: "user1"
  passwordSecret:                       # Add this
    name: "user1-password"
    key: "password"                     # optional, defaults to "password"
    # namespace: "default"              # optional, defaults to User namespace
  backend:
    kind: MinioBackend
    name: my-backend
  homeDirectory: "/home/user1"
```

### Step 3: Apply Changes

```bash
kubectl apply -f updated-user.yaml
```

### Step 4: Verify Authentication

Test that users can still authenticate with their existing passwords.

## Configuration Options

### Secret Reference Fields

```yaml
passwordSecret:
  name: "secret-name"        # Required: Name of the Kubernetes Secret
  key: "password"            # Optional: Key in secret (defaults to "password")
  namespace: "other-ns"      # Optional: Secret namespace (defaults to User namespace)
```

### Custom Secret Keys

You can use custom keys in your secrets:

```bash
kubectl create secret generic user-creds \
  --from-literal=ftp-password="mySecretPass123"
```

```yaml
passwordSecret:
  name: "user-creds"
  key: "ftp-password"  # Custom key name
```

## Best Practices

### 1. Use Strong Passwords
Generate strong passwords for your secrets:

```bash
# Generate a random password
openssl rand -base64 32

# Create secret with generated password
kubectl create secret generic user1-password \
  --from-literal=password="$(openssl rand -base64 32)"
```

### 2. Namespace Isolation
Keep user secrets in the same namespace as the User resources for better security boundaries.

### 3. Regular Rotation
Implement regular password rotation:

```bash
# Update secret with new password
kubectl patch secret user1-password \
  --patch='{"data":{"password":"'$(echo -n "newPassword123" | base64)'"}}'
```

### 4. RBAC Controls
Restrict access to secrets containing passwords:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  namespace: default
  name: ftp-user-secrets
rules:
- apiGroups: [""]
  resources: ["secrets"]
  resourceNames: ["user1-password", "user2-password"]
  verbs: ["get", "list"]
```

## Troubleshooting

### Common Issues

1. **Secret not found**: Ensure secret exists in the correct namespace
   ```bash
   kubectl get secrets -n <namespace>
   ```

2. **Wrong password key**: Verify the key name in your secret
   ```bash
   kubectl describe secret user1-password
   ```

3. **Authentication fails**: Check KubeFTPd logs for detailed error messages
   ```bash
   kubectl logs -l app=kubeftpd -f
   ```

### Error Messages

- `"failed to get secret"`: Secret doesn't exist or wrong namespace
- `"password not found in secret with key"`: Wrong key name specified
- `"either password or passwordSecret is required"`: Neither method specified
- `"cannot specify both password and passwordSecret"`: Both methods specified

## Security Considerations

### Encryption at Rest
Enable encryption at rest for your Kubernetes cluster to protect secret data:

```bash
# Check if encryption is enabled
kubectl get secrets user1-password -o yaml | grep -i encrypt
```

### Secret Scanning
Use tools like `kubesec` or `kube-score` to scan for security issues:

```bash
kubesec scan user.yaml
```

### Monitoring
Monitor secret access:

```bash
# Check audit logs for secret access
kubectl get events --field-selector reason=SecretGet
```

## Validation

After migration, verify:

1. ✅ Users can authenticate with existing passwords
2. ✅ No plaintext passwords in User CRDs
3. ✅ Secrets are properly created and accessible
4. ✅ RBAC controls are in place
5. ✅ Audit logging captures secret access

## Rollback Plan

If issues occur, you can temporarily rollback to plaintext:

```bash
# Emergency rollback - add plaintext password back
kubectl patch user user1 --patch='{"spec":{"password":"temporaryPassword","passwordSecret":null}}'
```

**Note**: Remove plaintext passwords as soon as the issue is resolved.