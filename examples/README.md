# Examples

Manifests you can adapt. Every `<change-me>` value is a placeholder: replace it before applying, and don't commit real credentials.

| File | Shows |
|------|-------|
| [`user-password-methods.yaml`](user-password-methods.yaml) | Secret-based and plaintext passwords, and a custom Secret key |
| [`production-users-demo.yaml`](production-users-demo.yaml) | A production-style namespace with RBAC for Secret access, human and service users, and a ServiceMonitor |

Both files need a backend named `my-minio-backend` or `production-storage`. See [custom-resources.md](../docs/custom-resources.md#storage-backends) for backend examples.

For the rules these examples follow, see [passwords.md](../docs/passwords.md).
