#!/bin/bash
# Development environment setup script for KubeFTPd

set -e

echo "🚀 Setting up KubeFTPd development environment..."

# Check if Docker is running
if ! docker info > /dev/null 2>&1; then
    echo "❌ Docker is not running. Please start Docker and try again."
    exit 1
fi

# Check if docker-compose is available
if ! command -v docker-compose > /dev/null 2>&1; then
    echo "❌ docker-compose is not installed. Please install docker-compose and try again."
    exit 1
fi

# Create required directories
echo "📁 Creating required directories..."
mkdir -p logs tmp bin dev

# Set up Git hooks (if not already set up)
if [ ! -f .git/hooks/pre-commit ]; then
    echo "🔗 Setting up Git hooks..."
    make setup-pre-commit
fi

# Build development environment
echo "🔨 Building development containers..."
docker-compose -f docker-compose.yml build

# Start infrastructure services (MinIO, PostgreSQL)
echo "🏗️ Starting infrastructure services..."
docker-compose -f docker-compose.yml up -d minio postgres minio-setup

# Wait for services to be ready
echo "⏳ Waiting for services to be ready..."
sleep 10

# Check MinIO health
echo "🔍 Checking MinIO health..."
until curl -f http://localhost:9000/minio/health/live > /dev/null 2>&1; do
    echo "Waiting for MinIO..."
    sleep 5
done
echo "✅ MinIO is ready"

# Check PostgreSQL health
echo "🔍 Checking PostgreSQL health..."
until docker-compose exec postgres pg_isready -U kubeftpd > /dev/null 2>&1; do
    echo "Waiting for PostgreSQL..."
    sleep 5
done
echo "✅ PostgreSQL is ready"

# Create example CRD manifests for testing
echo "📄 Creating example manifests..."
mkdir -p examples

cat > examples/minio-backend.yaml << EOF
apiVersion: ftp.rossigee.com/v1
kind: MinioBackend
metadata:
  name: dev-minio-backend
  namespace: default
spec:
  endpoint: "http://localhost:9000"
  bucket: "ftp-storage"
  region: "us-east-1"
  credentials:
    accessKeyID: "minioadmin"
    secretAccessKey: "minioadmin123"
  tls:
    insecureSkipVerify: true
EOF

cat > examples/test-user.yaml << EOF
apiVersion: ftp.rossigee.com/v1
kind: User
metadata:
  name: test-user
  namespace: default
spec:
  username: "testuser"
  password: "testpass"
  homeDirectory: "/test"
  enabled: true
  backend:
    kind: "MinioBackend"
    name: "dev-minio-backend"
  permissions:
    read: true
    write: true
    delete: true
EOF

# Create a simple test script
cat > scripts/test-ftp.sh << 'EOF'
#!/bin/bash
# Simple FTP test script

echo "🧪 Testing FTP connection..."

# Test FTP connection using lftp
lftp -c "
set ftp:passive-mode true
set ftp:ssl-allow false
open ftp://testuser:testpass@localhost:21
ls
mkdir test-dir || true
cd test-dir
put /etc/hosts
ls
get hosts hosts-downloaded
rm hosts
cd ..
rmdir test-dir
quit
"

echo "✅ FTP test completed"
EOF

chmod +x scripts/test-ftp.sh

echo "🎉 Development environment setup complete!"
echo ""
echo "📋 Next steps:"
echo "  1. Start development environment:"
echo "     docker-compose -f docker-compose.yml -f docker-compose.dev.yml --profile dev up"
echo ""
echo "  2. Run tests:"
echo "     make test"
echo ""
echo "  3. Test FTP connection:"
echo "     ./scripts/test-ftp.sh"
echo ""
echo "  4. View logs:"
echo "     docker-compose logs -f kubeftpd"
echo ""
echo "  5. Access services:"
echo "     - MinIO Console: http://localhost:9001 (minioadmin/minioadmin123)"
echo "     - KubeFTPd Metrics: http://localhost:8080/metrics"
echo "     - KubeFTPd Health: http://localhost:8081/healthz"
echo ""
echo "  6. Optional observability stack:"
echo "     docker-compose -f docker-compose.yml -f docker-compose.dev.yml --profile observability up"
echo "     - Prometheus: http://localhost:9090"
echo "     - Grafana: http://localhost:3000 (admin/admin)"
echo "     - Jaeger: http://localhost:16686"
