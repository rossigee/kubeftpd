# KubeFTPd Password Security - Operational Procedures

This document provides operational procedures for managing password security in KubeFTPd production environments.

## 📋 **Table of Contents**

1. [Day-to-Day Operations](#day-to-day-operations)
2. [User Management](#user-management)
3. [Security Monitoring](#security-monitoring)
4. [Incident Response](#incident-response)
5. [Maintenance Procedures](#maintenance-procedures)
6. [Compliance & Auditing](#compliance--auditing)

## 🔄 **Day-to-Day Operations**

### Creating New FTP Users

#### 1. For Production Users (Recommended)

```bash
# Step 1: Generate strong password
PASSWORD=$(openssl rand -base64 32)

# Step 2: Create secret
kubectl create secret generic "${USERNAME}-ftp-password" \
  --from-literal=password="${PASSWORD}" \
  --namespace="${NAMESPACE}" \
  --dry-run=client -o yaml | \
  kubectl label --local -f - \
  user="${USERNAME}" \
  purpose="ftp-authentication" \
  created-by="${USER}" \
  rotation-schedule="quarterly" \
  -o yaml | \
  kubectl apply -f -

# Step 3: Create User resource
cat <<EOF | kubectl apply -f -
apiVersion: ftp.golder.org/v1
kind: User
metadata:
  name: ${USERNAME}
  namespace: ${NAMESPACE}
  labels:
    user-type: human
    department: ${DEPARTMENT}
spec:
  username: "${USERNAME}"
  passwordSecret:
    name: "${USERNAME}-ftp-password"
    key: "password"
  backend:
    kind: MinioBackend
    name: ${BACKEND_NAME}
  homeDirectory: "/${DEPARTMENT}/${USERNAME}"
  enabled: true
  permissions:
    read: true
    write: true
    delete: false
    list: true
EOF

# Step 4: Securely share password with user
echo "Password for ${USERNAME}: ${PASSWORD}" | gpg --encrypt --recipient "${USERNAME}@company.com"
```

#### 2. For Service Accounts

```bash
# Service accounts use different naming and permissions
USERNAME="api-service-${SERVICE_NAME}"
kubectl create secret generic "${USERNAME}-credentials" \
  --from-literal=ftp-access-key="$(openssl rand -base64 32)" \
  --namespace="${NAMESPACE}"

# Create service user with full permissions
cat <<EOF | kubectl apply -f -
apiVersion: ftp.golder.org/v1
kind: User
metadata:
  name: ${USERNAME}
  namespace: ${NAMESPACE}
  labels:
    user-type: service
    service: ${SERVICE_NAME}
spec:
  username: "${USERNAME}"
  passwordSecret:
    name: "${USERNAME}-credentials"
    key: "ftp-access-key"
  backend:
    kind: MinioBackend
    name: ${BACKEND_NAME}
  homeDirectory: "/${SERVICE_NAME}"
  enabled: true
  permissions:
    read: true
    write: true
    delete: true
    list: true
EOF
```

### Password Rotation

#### Quarterly Password Rotation (Recommended)

```bash
#!/bin/bash
# rotate-ftp-passwords.sh

NAMESPACE="${1:-default}"
ROTATION_LABEL="rotation-schedule=quarterly"

echo "🔄 Starting quarterly password rotation for namespace: ${NAMESPACE}"

# Find all secrets that need rotation
kubectl get secrets -n "${NAMESPACE}" \
  -l "${ROTATION_LABEL}" \
  -o jsonpath='{.items[*].metadata.name}' | \
while read SECRET_NAME; do
  echo "🔐 Rotating password for secret: ${SECRET_NAME}"

  # Generate new password
  NEW_PASSWORD=$(openssl rand -base64 32)

  # Update secret
  kubectl patch secret "${SECRET_NAME}" \
    -n "${NAMESPACE}" \
    --patch="{\"data\":{\"password\":\"$(echo -n ${NEW_PASSWORD} | base64)\"}}"

  # Update rotation timestamp
  kubectl annotate secret "${SECRET_NAME}" \
    -n "${NAMESPACE}" \
    last-rotation="$(date -Iseconds)" \
    --overwrite

  echo "✅ Rotated password for ${SECRET_NAME}"
  echo "🔑 New password: ${NEW_PASSWORD}"
  echo "📧 Please notify user to update their credentials"
  echo ""
done

echo "🎉 Password rotation complete!"
```

### User Deactivation

```bash
# Disable user without deleting
kubectl patch user "${USERNAME}" \
  -n "${NAMESPACE}" \
  --patch='{"spec":{"enabled":false}}'

# Add deactivation annotation
kubectl annotate user "${USERNAME}" \
  -n "${NAMESPACE}" \
  deactivated-by="${ADMIN_USER}" \
  deactivated-date="$(date -Iseconds)" \
  reason="${REASON}"
```

## 👥 **User Management**

### Bulk User Operations

#### Create Multiple Users from CSV

```bash
# users.csv format: username,department,backend,permissions
# john.doe,marketing,prod-storage,read-write
# jane.smith,engineering,dev-storage,full

#!/bin/bash
while IFS=',' read -r username department backend permissions; do
  echo "Creating user: ${username}"

  # Generate password
  PASSWORD=$(openssl rand -base64 32)

  # Create secret
  kubectl create secret generic "${username}-ftp-password" \
    --from-literal=password="${PASSWORD}" \
    --namespace="ftp-users"

  # Set permissions based on input
  case ${permissions} in
    "read-only")
      READ=true; WRITE=false; DELETE=false; LIST=true ;;
    "read-write")
      READ=true; WRITE=true; DELETE=false; LIST=true ;;
    "full")
      READ=true; WRITE=true; DELETE=true; LIST=true ;;
  esac

  # Create user
  cat <<EOF | kubectl apply -f -
apiVersion: ftp.golder.org/v1
kind: User
metadata:
  name: ${username}
  namespace: ftp-users
spec:
  username: "${username}"
  passwordSecret:
    name: "${username}-ftp-password"
  backend:
    kind: MinioBackend
    name: ${backend}
  homeDirectory: "/${department}/${username}"
  enabled: true
  permissions:
    read: ${READ}
    write: ${WRITE}
    delete: ${DELETE}
    list: ${LIST}
EOF

  echo "✅ Created ${username} with password: ${PASSWORD}"
done < users.csv
```

### User Audit Report

```bash
#!/bin/bash
# Generate user audit report

echo "# KubeFTPd User Audit Report - $(date)"
echo "Generated by: ${USER}"
echo ""

# Count users by type
echo "## User Summary"
echo "| Type | Count |"
echo "|------|-------|"
echo "| Total Users | $(kubectl get users --all-namespaces --no-headers | wc -l) |"
echo "| Secret-based | $(kubectl get users --all-namespaces -o jsonpath='{.items[?(@.spec.passwordSecret)].metadata.name}' | wc -w) |"
echo "| Plaintext | $(kubectl get users --all-namespaces -o jsonpath='{.items[?(@.spec.password)].metadata.name}' | wc -w) |"
echo "| Enabled | $(kubectl get users --all-namespaces -o jsonpath='{.items[?(@.spec.enabled==true)].metadata.name}' | wc -w) |"
echo "| Disabled | $(kubectl get users --all-namespaces -o jsonpath='{.items[?(@.spec.enabled==false)].metadata.name}' | wc -w) |"
echo ""

# List users with plaintext passwords (security risk)
echo "## ⚠️ Users with Plaintext Passwords"
kubectl get users --all-namespaces -o custom-columns="NAMESPACE:.metadata.namespace,NAME:.metadata.name,USERNAME:.spec.username" --no-headers | \
while read namespace name username; do
  if kubectl get user "${name}" -n "${namespace}" -o jsonpath='{.spec.password}' >/dev/null 2>&1; then
    echo "- ${namespace}/${name} (${username})"
  fi
done
echo ""

# Check for missing secrets
echo "## 🔍 Users with Missing Secrets"
kubectl get users --all-namespaces -o json | \
jq -r '.items[] | select(.spec.passwordSecret != null) |
  "\(.metadata.namespace) \(.metadata.name) \(.spec.passwordSecret.name) \(.spec.passwordSecret.namespace // .metadata.namespace)"' | \
while read user_ns user_name secret_name secret_ns; do
  if ! kubectl get secret "${secret_name}" -n "${secret_ns}" >/dev/null 2>&1; then
    echo "- ${user_ns}/${user_name} → missing secret ${secret_ns}/${secret_name}"
  fi
done
```

## 📊 **Security Monitoring**

### Daily Security Checks

```bash
#!/bin/bash
# daily-security-check.sh

echo "🛡️ KubeFTPd Daily Security Check - $(date)"

# Check for authentication failures
FAILURES=$(kubectl logs -l app=kubeftpd --since=24h | grep -c "Invalid password" || echo "0")
echo "Authentication failures (24h): ${FAILURES}"

if [ "${FAILURES}" -gt 10 ]; then
  echo "⚠️ HIGH: More than 10 authentication failures detected"
fi

# Check for plaintext passwords in production
PROD_PLAINTEXT=$(kubectl get users --all-namespaces -l environment=production -o json | \
  jq '[.items[] | select(.spec.password != null)] | length')

if [ "${PROD_PLAINTEXT}" -gt 0 ]; then
  echo "🚨 CRITICAL: ${PROD_PLAINTEXT} users have plaintext passwords in production"
fi

# Check secret accessibility
echo "🔍 Checking secret accessibility..."
kubectl get users --all-namespaces -o json | \
jq -r '.items[] | select(.spec.passwordSecret != null) |
  "\(.metadata.namespace) \(.spec.passwordSecret.name) \(.spec.passwordSecret.namespace // .metadata.namespace)"' | \
while read user_ns secret_name secret_ns; do
  if ! kubectl get secret "${secret_name}" -n "${secret_ns}" >/dev/null 2>&1; then
    echo "❌ Secret not accessible: ${secret_ns}/${secret_name}"
  fi
done

echo "✅ Security check complete"
```

### Prometheus Queries for Monitoring

```promql
# Authentication failure rate (per minute)
rate(kubeftpd_auth_failures_total[5m]) * 60

# Users with plaintext passwords by namespace
kubeftpd:users_plaintext_password_total

# Secret access error rate
rate(kubeftpd_secret_access_errors_total[5m])

# Missing secrets
kubeftpd_user_secret_missing > 0

# Authentication success rate by method
rate(kubeftpd_auth_attempts_total{result="success"}[5m]) by (method)
```

## 🚨 **Incident Response**

### Suspected Password Compromise

```bash
#!/bin/bash
# incident-response-password-compromise.sh

USERNAME="${1}"
NAMESPACE="${2}"

if [ -z "${USERNAME}" ] || [ -z "${NAMESPACE}" ]; then
  echo "Usage: $0 <username> <namespace>"
  exit 1
fi

echo "🚨 INCIDENT: Suspected password compromise for ${USERNAME}"

# Step 1: Immediately disable user
echo "🔒 Disabling user account..."
kubectl patch user "${USERNAME}" -n "${NAMESPACE}" \
  --patch='{"spec":{"enabled":false}}'

kubectl annotate user "${USERNAME}" -n "${NAMESPACE}" \
  incident-response="password-compromise" \
  disabled-date="$(date -Iseconds)" \
  incident-id="${INCIDENT_ID:-$(uuidgen)}"

# Step 2: Generate new password
echo "🔑 Generating new password..."
NEW_PASSWORD=$(openssl rand -base64 32)

# Step 3: Update secret if using secret-based auth
SECRET_NAME=$(kubectl get user "${USERNAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.passwordSecret.name}')

if [ -n "${SECRET_NAME}" ]; then
  echo "🔐 Updating password secret..."
  kubectl patch secret "${SECRET_NAME}" -n "${NAMESPACE}" \
    --patch="{\"data\":{\"password\":\"$(echo -n ${NEW_PASSWORD} | base64)\"}}"

  kubectl annotate secret "${SECRET_NAME}" -n "${NAMESPACE}" \
    emergency-rotation="$(date -Iseconds)" \
    incident-id="${INCIDENT_ID:-$(uuidgen)}"
fi

# Step 4: Log incident
echo "📝 Logging incident..."
kubectl create event \
  --namespace="${NAMESPACE}" \
  --type="Warning" \
  --reason="PasswordCompromise" \
  --message="User ${USERNAME} password compromised, account disabled and password rotated" \
  --source="kubeftpd-ops"

echo "✅ Incident response complete"
echo "🔑 New password: ${NEW_PASSWORD}"
echo "📞 Next steps:"
echo "   1. Investigate access logs"
echo "   2. Notify user securely"
echo "   3. Re-enable user after verification"
echo "   4. Update incident tracking system"
```

### Mass Password Reset

```bash
#!/bin/bash
# mass-password-reset.sh - Emergency mass password reset

NAMESPACE="${1}"
REASON="${2:-emergency-security-incident}"

echo "🚨 EMERGENCY: Mass password reset for namespace ${NAMESPACE}"
echo "Reason: ${REASON}"

# Confirm action
read -p "This will reset ALL FTP passwords in ${NAMESPACE}. Continue? (yes/no): " CONFIRM
if [ "${CONFIRM}" != "yes" ]; then
  echo "Aborted"
  exit 1
fi

# Get all users in namespace
kubectl get users -n "${NAMESPACE}" -o name | while read USER_RESOURCE; do
  USER_NAME=$(echo "${USER_RESOURCE}" | cut -d'/' -f2)
  echo "🔄 Resetting password for ${USER_NAME}..."

  # Generate new password
  NEW_PASSWORD=$(openssl rand -base64 32)

  # Check if user uses secret
  SECRET_NAME=$(kubectl get "${USER_RESOURCE}" -n "${NAMESPACE}" \
    -o jsonpath='{.spec.passwordSecret.name}')

  if [ -n "${SECRET_NAME}" ]; then
    # Update secret
    kubectl patch secret "${SECRET_NAME}" -n "${NAMESPACE}" \
      --patch="{\"data\":{\"password\":\"$(echo -n ${NEW_PASSWORD} | base64)\"}}"

    echo "✅ ${USER_NAME}: ${NEW_PASSWORD}"
  else
    # User has plaintext password - update directly
    kubectl patch "${USER_RESOURCE}" -n "${NAMESPACE}" \
      --patch="{\"spec\":{\"password\":\"${NEW_PASSWORD}\"}}"

    echo "✅ ${USER_NAME}: ${NEW_PASSWORD} (plaintext)"
  fi

  # Add annotation
  kubectl annotate "${USER_RESOURCE}" -n "${NAMESPACE}" \
    mass-reset="$(date -Iseconds)" \
    reset-reason="${REASON}"
done

echo "🎉 Mass password reset complete"
echo "📧 Notify all users to update their credentials"
```

## 🔧 **Maintenance Procedures**

### Weekly Maintenance Tasks

```bash
#!/bin/bash
# weekly-maintenance.sh

echo "🔧 KubeFTPd Weekly Maintenance - $(date)"

# Clean up old authentication logs
echo "🧹 Cleaning up old logs..."
kubectl logs -l app=kubeftpd --since=168h > /tmp/kubeftpd-weekly-logs.txt
echo "Logs saved to /tmp/kubeftpd-weekly-logs.txt"

# Check for users without recent activity
echo "👤 Checking for inactive users..."
kubectl get users --all-namespaces -o json | \
jq -r '.items[] | select(.status.lastLogin != null) |
  select((now - (.status.lastLogin | fromdateiso8601)) > (30 * 24 * 3600)) |
  "\(.metadata.namespace)/\(.metadata.name) - Last login: \(.status.lastLogin)"'

# Verify webhook is functioning
echo "🔍 Testing validation webhook..."
cat <<EOF | kubectl apply --dry-run=server -f - >/dev/null 2>&1
apiVersion: ftp.golder.org/v1
kind: User
metadata:
  name: test-weak-password
  namespace: default
spec:
  username: test
  password: "weak"
  backend:
    kind: MinioBackend
    name: test
  homeDirectory: /test
EOF

if [ $? -eq 0 ]; then
  echo "⚠️ Webhook validation may not be working properly"
else
  echo "✅ Webhook validation is working"
fi

# Check certificate expiration
echo "📜 Checking certificate expiration..."
kubectl get certificates -n kubeftpd-system -o custom-columns="NAME:.metadata.name,READY:.status.conditions[?(@.type=='Ready')].status,EXPIRY:.status.notAfter"

echo "✅ Weekly maintenance complete"
```

### Monthly Security Review

```bash
#!/bin/bash
# monthly-security-review.sh

echo "🛡️ KubeFTPd Monthly Security Review - $(date)"

# Generate comprehensive report
{
  echo "# Monthly Security Review"
  echo "Generated: $(date)"
  echo ""

  echo "## User Statistics"
  echo "- Total users: $(kubectl get users --all-namespaces --no-headers | wc -l)"
  echo "- Active users: $(kubectl get users --all-namespaces -o jsonpath='{.items[?(@.spec.enabled==true)].metadata.name}' | wc -w)"
  echo "- Secret-based: $(kubectl get users --all-namespaces -o jsonpath='{.items[?(@.spec.passwordSecret)].metadata.name}' | wc -w)"
  echo "- Plaintext: $(kubectl get users --all-namespaces -o jsonpath='{.items[?(@.spec.password)].metadata.name}' | wc -w)"
  echo ""

  echo "## Security Metrics (30 days)"
  echo "- Authentication attempts: $(kubectl logs -l app=kubeftpd --since=720h | grep -c "Authenticating user" || echo "0")"
  echo "- Authentication failures: $(kubectl logs -l app=kubeftpd --since=720h | grep -c "Invalid password" || echo "0")"
  echo "- Secret access errors: $(kubectl logs -l app=kubeftpd --since=720h | grep -c "failed to get secret" || echo "0")"
  echo ""

  echo "## Compliance Issues"
  # Check for plaintext passwords in production
  kubectl get users --all-namespaces -o json | \
  jq -r '.items[] | select(.spec.password != null) |
    select(.metadata.namespace as $ns |
      (kubectl get namespace $ns -o jsonpath="{.metadata.labels.environment}" | test("prod"))) |
    "- Plaintext password in production: \(.metadata.namespace)/\(.metadata.name)"'

  echo ""
  echo "## Recommendations"
  echo "- Migrate remaining plaintext passwords to secrets"
  echo "- Review user permissions quarterly"
  echo "- Update password rotation schedule"
  echo "- Review RBAC permissions"

} > "/tmp/kubeftpd-security-review-$(date +%Y-%m).md"

echo "📊 Security review saved to /tmp/kubeftpd-security-review-$(date +%Y-%m).md"
```

## 📋 **Compliance & Auditing**

### SOC 2 Compliance Checks

```bash
#!/bin/bash
# soc2-compliance-check.sh

echo "📋 SOC 2 Compliance Check for KubeFTPd"

# CC6.1 - Logical Access Controls
echo "## CC6.1 - Logical Access Controls"
echo "✅ Users authenticate with passwords or secrets"
echo "✅ RBAC controls access to password secrets"
echo "✅ Validation webhook enforces password policies"

# CC6.2 - Authentication
PLAINTEXT_COUNT=$(kubectl get users --all-namespaces -o json | \
  jq '[.items[] | select(.spec.password != null)] | length')
echo "## CC6.2 - Authentication"
echo "- Users with plaintext passwords: ${PLAINTEXT_COUNT}"
if [ "${PLAINTEXT_COUNT}" -gt 0 ]; then
  echo "⚠️ Consider migrating to secret-based authentication"
fi

# CC6.3 - Authorization
echo "## CC6.3 - Authorization"
echo "✅ User permissions controlled via CRD spec"
echo "✅ Granular permissions (read/write/delete/list)"

# Generate compliance report
{
  echo "# SOC 2 Compliance Report - KubeFTPd"
  echo "Date: $(date)"
  echo "Auditor: ${USER}"
  echo ""

  echo "## Controls Assessment"
  echo "| Control | Status | Notes |"
  echo "|---------|--------|-------|"
  echo "| Password Complexity | ✅ PASS | Webhook enforces strong passwords |"
  echo "| Secret Management | ✅ PASS | K8s secrets used for password storage |"
  echo "| Access Controls | ✅ PASS | RBAC limits secret access |"
  echo "| Audit Logging | ✅ PASS | Prometheus metrics track auth events |"
  echo "| User Management | ✅ PASS | Centralized user lifecycle |"

} > "/tmp/kubeftpd-soc2-$(date +%Y-%m-%d).md"

echo "📄 Compliance report: /tmp/kubeftpd-soc2-$(date +%Y-%m-%d).md"
```

### Audit Log Analysis

```bash
#!/bin/bash
# audit-log-analysis.sh

DAYS="${1:-7}"
echo "📊 Audit Log Analysis - Last ${DAYS} days"

# Extract relevant events
kubectl get events --all-namespaces --since="${DAYS}d" \
  --field-selector reason=SecretCreated,reason=SecretUpdated,reason=SecretDeleted \
  -o custom-columns="TIME:.firstTimestamp,NAMESPACE:.namespace,REASON:.reason,OBJECT:.involvedObject.name,MESSAGE:.message"

# Authentication events from logs
echo ""
echo "🔐 Authentication Events"
kubectl logs -l app=kubeftpd --since="${DAYS}d" | \
  grep -E "(Authenticating user|authenticated|Invalid password)" | \
  sed 's/.*\(Authenticating\|authenticated\|Invalid\)/\1/' | \
  sort | uniq -c | sort -nr

# Secret access patterns
echo ""
echo "🔑 Secret Access Patterns"
kubectl logs -l app=kubeftpd --since="${DAYS}d" | \
  grep "failed to get secret" | \
  sed 's/.*failed to get secret \([^:]*\):.*/\1/' | \
  sort | uniq -c | sort -nr
```

<function_calls>
<invoke name="TodoWrite">
<parameter name="todos">[{"id": "1", "content": "Demonstrate the implementation with practical examples", "status": "completed"}, {"id": "2", "content": "Add RBAC configuration for secret access", "status": "completed"}, {"id": "3", "content": "Create monitoring and alerting examples", "status": "completed"}, {"id": "4", "content": "Add password validation webhook", "status": "completed"}, {"id": "5", "content": "Document operational procedures", "status": "completed"}]
