# 🔐 KubeFTPd Password Security Features

## Overview

KubeFTPd now provides enterprise-grade password security management with flexible configuration options, comprehensive monitoring, and production-ready security controls.

## ✨ **Key Features**

### 🔑 **Dual Password Management**
- **Secret-based passwords** (recommended for production)
- **Plaintext passwords** (backwards compatible, development only)
- **Mutual exclusivity** validation - users cannot specify both methods

### 🛡️ **Security Validation**
- **Admission webhook** with password strength validation
- **Production environment restrictions** - no plaintext passwords allowed
- **Password complexity requirements** - minimum 8 chars, mixed case, numbers, special chars
- **Weak pattern detection** - blocks common weak passwords

### 📊 **Monitoring & Alerting**
- **Prometheus metrics** for authentication events
- **Grafana dashboards** for security monitoring
- **Automated alerts** for security violations
- **Audit trail** for compliance reporting

### 🔧 **Operational Tools**
- **Password rotation scripts** with automated generation
- **Bulk user management** from CSV files
- **Incident response procedures** for compromise scenarios
- **Compliance reporting** for SOC 2 and other frameworks

## 🚀 **Quick Start**

### Create a User with Secret-based Password

```bash
# 1. Generate strong password
PASSWORD=$(openssl rand -base64 32)

# 2. Create secret
kubectl create secret generic user1-ftp-password \
  --from-literal=password="${PASSWORD}" \
  --namespace=default

# 3. Create user
kubectl apply -f - <<EOF
apiVersion: ftp.golder.org/v1
kind: User
metadata:
  name: user1
  namespace: default
spec:
  username: "user1"
  passwordSecret:
    name: "user1-ftp-password"
    key: "password"
  backend:
    kind: MinioBackend
    name: my-backend
  homeDirectory: "/home/user1"
  enabled: true
EOF

echo "Password: ${PASSWORD}"
```

### Environment Variable Configuration

```bash
# Run FTP server on non-privileged port for non-root execution
export FTP_PORT=2121
export FTP_PASSIVE_PORT_MIN=31000
export FTP_PASSIVE_PORT_MAX=32000
./kubeftpd
```

## 📋 **Configuration Examples**

### Secret-based User (Production)
```yaml
apiVersion: ftp.golder.org/v1
kind: User
metadata:
  name: prod-user
  namespace: production
spec:
  username: "prod.user"
  passwordSecret:
    name: "prod-user-ftp-password"
    key: "password"
  backend:
    kind: MinioBackend
    name: prod-storage
  homeDirectory: "/prod/user"
  enabled: true
  permissions:
    read: true
    write: true
    delete: false  # Safety in production
    list: true
```

### Development User (Plaintext)
```yaml
apiVersion: ftp.golder.org/v1
kind: User
metadata:
  name: dev-user
  namespace: development
spec:
  username: "dev.user"
  password: "DevPassword123!"  # Only for development
  backend:
    kind: FilesystemBackend
    name: dev-storage
  homeDirectory: "/dev/user"
  enabled: true
```

### Service Account User
```yaml
apiVersion: ftp.golder.org/v1
kind: User
metadata:
  name: api-service
  namespace: default
spec:
  username: "api-service"
  passwordSecret:
    name: "api-service-credentials"
    key: "ftp-access-key"  # Custom key name
  backend:
    kind: MinioBackend
    name: api-storage
  homeDirectory: "/api"
  enabled: true
  permissions:
    read: true
    write: true
    delete: true  # Services may need delete permissions
    list: true
```

## 🔐 **Security Controls**

### Password Strength Requirements
- **Minimum 8 characters**
- **Mixed case letters** (upper and lower)
- **At least one digit**
- **At least one special character**
- **No weak patterns** (password, 123456, qwerty, etc.)
- **No sequential characters** (abc, 123, etc.)

### Production Environment Restrictions
- **No plaintext passwords** in namespaces labeled `environment=production`
- **Secret naming conventions** enforced: `*-ftp-password` or `*-ftp-credentials`
- **Enhanced monitoring** and alerting for production environments

### RBAC Security
- **Least privilege access** to password secrets
- **Namespace isolation** for secret access
- **Service account restrictions** for webhook and controller operations

## 📊 **Monitoring & Metrics**

### Available Metrics
```promql
# Authentication attempts by method and result
kubeftpd_auth_attempts_total{method="secret|plaintext", result="success|failure"}

# Authentication failures by reason
kubeftpd_auth_failures_total{reason="invalid_password|user_disabled|user_not_found"}

# Secret access errors
kubeftpd_secret_access_errors_total{error_type="not_found|key_not_found"}

# Missing secrets gauge
kubeftpd_user_secret_missing{namespace, username, secret_name}
```

### Sample Alerts
```yaml
# Critical: Plaintext passwords in production
- alert: KubeFTPdPlaintextPasswordsInProduction
  expr: kubeftpd:users_plaintext_password_total > 0
  for: 0m
  labels:
    severity: critical

# High authentication failure rate
- alert: KubeFTPdHighAuthFailureRate  
  expr: rate(kubeftpd_auth_failures_total[5m]) > 0.1
  for: 2m
  labels:
    severity: warning
```

## 🛠️ **Operational Procedures**

### Password Rotation
```bash
# Rotate passwords quarterly
./scripts/rotate-ftp-passwords.sh production quarterly

# Emergency mass password reset
./scripts/mass-password-reset.sh production "security-incident-2024-001"
```

### Security Monitoring
```bash
# Daily security check
./scripts/daily-security-check.sh

# Generate audit report
./scripts/user-audit-report.sh > audit-$(date +%Y-%m-%d).md
```

### Incident Response
```bash
# Respond to suspected password compromise
./scripts/incident-response-password-compromise.sh username namespace
```

## 📁 **File Structure**

```
kubeftpd/
├── examples/
│   ├── user-password-methods.yaml        # Examples of both password methods
│   └── production-users-demo.yaml        # Production-ready configuration
├── config/
│   ├── rbac/password-security-rbac.yaml  # RBAC for secret access
│   ├── monitoring/                       # Prometheus rules & Grafana dashboards
│   └── webhook/                          # Validation webhook configuration
├── scripts/
│   └── create-user-secret.sh            # Helper script for secret creation
├── docs/
│   ├── PASSWORD-MIGRATION.md            # Migration from plaintext to secrets
│   └── OPERATIONAL-PROCEDURES.md        # Day-to-day operations guide
└── internal/
    ├── ftp/auth.go                      # Enhanced authentication with metrics
    └── webhook/user_validator.go        # Password validation webhook
```

## 🔄 **Migration Path**

1. **Assessment**: Audit existing users with plaintext passwords
2. **Secret Creation**: Generate secrets for existing users
3. **User Updates**: Update User CRDs to use passwordSecret
4. **Validation**: Test authentication with new secrets
5. **Production Deployment**: Deploy webhook and monitoring
6. **Cleanup**: Remove plaintext passwords when migration complete

## 🎯 **Best Practices**

### For Administrators
- ✅ Use secret-based passwords for all production users
- ✅ Implement regular password rotation (quarterly recommended)
- ✅ Monitor authentication metrics daily
- ✅ Enable webhook validation to enforce policies
- ✅ Use strong, randomly generated passwords

### For Developers
- ✅ Test with plaintext passwords in development environments only
- ✅ Follow naming conventions for production secrets
- ✅ Include proper labels and annotations on secrets
- ✅ Use principle of least privilege for user permissions

### For Security Teams
- ✅ Implement audit logging and review procedures
- ✅ Set up automated alerting for security violations
- ✅ Conduct regular security reviews and compliance checks
- ✅ Maintain incident response procedures

## 🔗 **Related Documentation**

- [Password Migration Guide](PASSWORD-MIGRATION.md) - Step-by-step migration from plaintext
- [Operational Procedures](OPERATIONAL-PROCEDURES.md) - Day-to-day operations and maintenance
- [Main README](../README.md) - General KubeFTPd documentation

## 🆘 **Support & Troubleshooting**

### Common Issues
1. **"Secret not found"** - Verify secret exists in correct namespace
2. **"Password key not found"** - Check key name in secret (default: "password")
3. **"Weak password"** - Use stronger password meeting complexity requirements
4. **"Cannot specify both"** - Use either password OR passwordSecret, not both

### Getting Help
- Check logs: `kubectl logs -l app=kubeftpd -f`
- Review metrics: Access Grafana dashboard or Prometheus directly
- Run diagnostics: Use operational scripts in `/scripts` directory
- Security issues: Follow incident response procedures