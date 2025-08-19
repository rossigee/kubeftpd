package backends

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

// minioBackendImpl implements MinioBackend interface using minio-go client
type minioBackendImpl struct {
	client     *minio.Client
	bucket     string
	pathPrefix string
}

// newMinioBackendImpl creates a new MinIO backend implementation
func newMinioBackendImpl(backend *ftpv1.MinioBackend, kubeClient client.Client) (MinioBackend, error) {
	// Get credentials
	accessKey := backend.Spec.Credentials.AccessKeyID
	secretKey := backend.Spec.Credentials.SecretAccessKey

	// If useSecret is specified, read from Kubernetes Secret
	if backend.Spec.Credentials.UseSecret != nil {
		var err error
		accessKey, secretKey, err = getMinioCredentialsFromSecret(backend.Spec.Credentials.UseSecret, backend.Namespace, kubeClient)
		if err != nil {
			return nil, fmt.Errorf("failed to get credentials from secret: %w", err)
		}
	}

	// Parse endpoint to determine if it's secure
	endpoint := strings.TrimPrefix(backend.Spec.Endpoint, "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")
	useSSL := strings.HasPrefix(backend.Spec.Endpoint, "https://")

	// Configure TLS if specified
	var transport *http.Transport
	if backend.Spec.TLS != nil {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: backend.Spec.TLS.InsecureSkipVerify, // nolint:gosec // InsecureSkipVerify is an intentional configuration option
		}

		// TODO: Add CA certificate support if backend.Spec.TLS.CACert is provided

		transport = &http.Transport{
			TLSClientConfig: tlsConfig,
		}
	}

	// Create MinIO client
	var minioClient *minio.Client
	var err error

	if transport != nil {
		minioClient, err = minio.New(endpoint, &minio.Options{
			Creds:     credentials.NewStaticV4(accessKey, secretKey, ""),
			Secure:    useSSL,
			Region:    backend.Spec.Region,
			Transport: transport,
		})
	} else {
		minioClient, err = minio.New(endpoint, &minio.Options{
			Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
			Secure: useSSL,
			Region: backend.Spec.Region,
		})
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create MinIO client: %w", err)
	}

	// Test connection
	ctx := context.Background()
	_, err = minioClient.BucketExists(ctx, backend.Spec.Bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MinIO bucket %s: %w", backend.Spec.Bucket, err)
	}

	return &minioBackendImpl{
		client:     minioClient,
		bucket:     backend.Spec.Bucket,
		pathPrefix: backend.Spec.PathPrefix,
	}, nil
}

// getMinioCredentialsFromSecret retrieves MinIO credentials from a Kubernetes Secret
func getMinioCredentialsFromSecret(secretRef *ftpv1.MinioSecretRef, backendNamespace string, kubeClient client.Client) (string, string, error) {
	if secretRef == nil {
		return "", "", fmt.Errorf("secret reference is nil")
	}

	ctx := context.TODO()
	// Default to the backend's namespace if no namespace is explicitly specified in the secret reference
	// This ensures secrets are looked up in the same namespace as the MinioBackend resource
	secretNamespace := backendNamespace
	if secretRef.Namespace != nil && *secretRef.Namespace != "" {
		secretNamespace = *secretRef.Namespace
	}

	secret := &corev1.Secret{}
	err := kubeClient.Get(ctx, client.ObjectKey{
		Name:      secretRef.Name,
		Namespace: secretNamespace,
	}, secret)
	if err != nil {
		return "", "", fmt.Errorf("failed to get secret %s/%s: %w", secretNamespace, secretRef.Name, err)
	}

	accessKeyIDKey := secretRef.AccessKeyIDKey
	if accessKeyIDKey == "" {
		accessKeyIDKey = "accessKeyID"
	}
	secretAccessKeyKey := secretRef.SecretAccessKeyKey
	if secretAccessKeyKey == "" {
		secretAccessKeyKey = "secretAccessKey"
	}

	accessKeyID, exists := secret.Data[accessKeyIDKey]
	if !exists {
		return "", "", fmt.Errorf("access key ID not found in secret with key %s", accessKeyIDKey)
	}

	secretAccessKey, exists := secret.Data[secretAccessKeyKey]
	if !exists {
		return "", "", fmt.Errorf("secret access key not found in secret with key %s", secretAccessKeyKey)
	}

	return string(accessKeyID), string(secretAccessKey), nil
}

// StatObject returns object information
func (m *minioBackendImpl) StatObject(objectName string) (*ObjectInfo, error) {
	ctx := context.Background()
	fullPath := m.getFullPath(objectName)

	objInfo, err := m.client.StatObject(ctx, m.bucket, fullPath, minio.StatObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to stat object %s: %w", objectName, err)
	}

	return &ObjectInfo{
		Key:          objectName,
		Size:         objInfo.Size,
		LastModified: objInfo.LastModified,
		ETag:         objInfo.ETag,
		ContentType:  objInfo.ContentType,
	}, nil
}

// GetObject retrieves an object with optional range
func (m *minioBackendImpl) GetObject(objectName string, offset, length int64) (io.ReadCloser, error) {
	ctx := context.Background()
	fullPath := m.getFullPath(objectName)

	opts := minio.GetObjectOptions{}
	if offset > 0 || length > 0 {
		// Set range if specified
		if length > 0 {
			err := opts.SetRange(offset, offset+length-1)
			if err != nil {
				return nil, fmt.Errorf("failed to set range: %w", err)
			}
		} else {
			err := opts.SetRange(offset, 0)
			if err != nil {
				return nil, fmt.Errorf("failed to set range: %w", err)
			}
		}
	}

	reader, err := m.client.GetObject(ctx, m.bucket, fullPath, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get object %s: %w", objectName, err)
	}

	return reader, nil
}

// PutObject uploads an object
func (m *minioBackendImpl) PutObject(objectName string, reader io.Reader, size int64) error {
	ctx := context.Background()
	fullPath := m.getFullPath(objectName)

	// Upload object and get upload info
	uploadInfo, err := m.client.PutObject(ctx, m.bucket, fullPath, reader, size, minio.PutObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to put object %s: %w", objectName, err)
	}

	// Verify the upload by checking object exists and has correct size
	objInfo, err := m.client.StatObject(ctx, m.bucket, fullPath, minio.StatObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to verify object %s after upload: %w", objectName, err)
	}

	// Verify object size matches what we uploaded
	if size > 0 && objInfo.Size != size {
		// Cleanup partial/corrupt object
		_ = m.client.RemoveObject(ctx, m.bucket, fullPath, minio.RemoveObjectOptions{})
		return fmt.Errorf("object size verification failed for %s: expected %d, got %d", objectName, size, objInfo.Size)
	}

	// For streaming uploads (size unknown), verify uploaded size matches reported upload info
	if size <= 0 && uploadInfo.Size != objInfo.Size {
		// Cleanup inconsistent object
		_ = m.client.RemoveObject(ctx, m.bucket, fullPath, minio.RemoveObjectOptions{})
		return fmt.Errorf("streaming upload verification failed for %s: upload reported %d bytes, object size %d", objectName, uploadInfo.Size, objInfo.Size)
	}

	return nil
}

// RemoveObject deletes an object
func (m *minioBackendImpl) RemoveObject(objectName string) error {
	ctx := context.Background()
	fullPath := m.getFullPath(objectName)

	err := m.client.RemoveObject(ctx, m.bucket, fullPath, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to remove object %s: %w", objectName, err)
	}

	return nil
}

// RemoveObjects deletes objects with a prefix (directory delete)
func (m *minioBackendImpl) RemoveObjects(prefix string, recursive bool) error {
	ctx := context.Background()
	fullPrefix := m.getFullPath(prefix)

	// List objects with prefix
	opts := minio.ListObjectsOptions{
		Prefix:    fullPrefix,
		Recursive: recursive,
	}

	objectsCh := m.client.ListObjects(ctx, m.bucket, opts)

	// Create channel for objects to delete
	objectNames := make(chan minio.ObjectInfo)

	// Send object names to delete
	go func() {
		defer close(objectNames)
		for objInfo := range objectsCh {
			if objInfo.Err != nil {
				continue
			}
			objectNames <- objInfo
		}
	}()

	// Remove objects
	removeOpts := minio.RemoveObjectsOptions{}
	errorCh := m.client.RemoveObjects(ctx, m.bucket, objectNames, removeOpts)

	// Check for errors
	for rmObjErr := range errorCh {
		if rmObjErr.Err != nil {
			return fmt.Errorf("failed to remove object %s: %w", rmObjErr.ObjectName, rmObjErr.Err)
		}
	}

	return nil
}

// CopyObject copies an object, optionally deleting the source
func (m *minioBackendImpl) CopyObject(srcObject, dstObject string, deleteSource bool) error {
	ctx := context.Background()
	fullSrcPath := m.getFullPath(srcObject)
	fullDstPath := m.getFullPath(dstObject)

	// Copy object
	src := minio.CopySrcOptions{
		Bucket: m.bucket,
		Object: fullSrcPath,
	}

	dst := minio.CopyDestOptions{
		Bucket: m.bucket,
		Object: fullDstPath,
	}

	_, err := m.client.CopyObject(ctx, dst, src)
	if err != nil {
		return fmt.Errorf("failed to copy object %s to %s: %w", srcObject, dstObject, err)
	}

	// Delete source if requested
	if deleteSource {
		err = m.RemoveObject(srcObject)
		if err != nil {
			return fmt.Errorf("failed to delete source object %s after copy: %w", srcObject, err)
		}
	}

	return nil
}

// ListObjects lists objects with a prefix
func (m *minioBackendImpl) ListObjects(prefix string, recursive bool) ([]*ObjectInfo, error) {
	ctx := context.Background()
	fullPrefix := m.getFullPath(prefix)

	opts := minio.ListObjectsOptions{
		Prefix:    fullPrefix,
		Recursive: recursive,
	}

	var objects []*ObjectInfo

	for objInfo := range m.client.ListObjects(ctx, m.bucket, opts) {
		if objInfo.Err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", objInfo.Err)
		}

		// Remove the full prefix to get relative path
		relativePath := strings.TrimPrefix(objInfo.Key, m.pathPrefix)
		relativePath = strings.TrimPrefix(relativePath, "/")

		objects = append(objects, &ObjectInfo{
			Key:          relativePath,
			Size:         objInfo.Size,
			LastModified: objInfo.LastModified,
			ETag:         objInfo.ETag,
			ContentType:  objInfo.ContentType,
		})
	}

	return objects, nil
}

// getFullPath combines the path prefix with the object name
func (m *minioBackendImpl) getFullPath(objectName string) string {
	if m.pathPrefix == "" {
		return objectName
	}

	// Ensure path prefix ends with /
	prefix := strings.TrimSuffix(m.pathPrefix, "/") + "/"

	// Remove leading / from object name if present
	objectName = strings.TrimPrefix(objectName, "/")

	return prefix + objectName
}
