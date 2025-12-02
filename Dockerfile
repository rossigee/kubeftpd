# Build the kubeftpd binary
FROM golang:1.25-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=v0.6.7
ARG COMMIT=unknown
ARG DATE=unknown

# Install git and ca-certificates for Go modules and TLS
RUN apk add --no-cache git ca-certificates

WORKDIR /workspace

# Copy go.mod and go.sum first for better caching
COPY go.mod go.mod
COPY go.sum go.sum

# Download dependencies
RUN go mod download

# Copy the source code
COPY cmd/main.go cmd/main.go
COPY api/ api/
COPY internal/ internal/
COPY hack/ hack/

# Build the binary with optimizations and version information
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build \
    -ldflags="-w -s -X 'main.version=${VERSION}' -X 'main.commit=${COMMIT}' -X 'main.date=${DATE}'" \
    -a -installsuffix cgo \
    -o kubeftpd \
    cmd/main.go

# Use distroless as minimal base image to package the kubeftpd binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot

WORKDIR /

# Copy the binary from the builder stage
COPY --from=builder /workspace/kubeftpd .

# Copy ca-certificates for TLS connections to backends
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Use nonroot user for security
USER 65532:65532

# Expose FTP control port
EXPOSE 21

# Expose passive port range for FTP data connections
EXPOSE 30000-30100

# Expose HTTP port (metrics, health checks, status)
EXPOSE 8080

ENTRYPOINT ["/kubeftpd"]
