#!/bin/bash

# Script to create a Kubernetes secret for FTP user password
# Usage: ./create-user-secret.sh <secret-name> <password> [namespace]

set -e

SECRET_NAME="${1}"
PASSWORD="${2}"
NAMESPACE="${3:-default}"

if [ -z "$SECRET_NAME" ] || [ -z "$PASSWORD" ]; then
    echo "Usage: $0 <secret-name> <password> [namespace]"
    echo "Example: $0 user1-password mySecretPass123 default"
    exit 1
fi

echo "Creating secret '$SECRET_NAME' in namespace '$NAMESPACE'..."

kubectl create secret generic "$SECRET_NAME" \
    --from-literal=password="$PASSWORD" \
    --namespace="$NAMESPACE"

echo "Secret created successfully!"
echo ""
echo "To use this secret in a User resource:"
echo "spec:"
echo "  username: \"myuser\""
echo "  passwordSecret:"
echo "    name: \"$SECRET_NAME\""
echo "    key: \"password\"  # optional, defaults to 'password'"
echo "    namespace: \"$NAMESPACE\"  # optional, defaults to User namespace"