/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// MinioBackendSpec defines the desired state of MinioBackend
type MinioBackendSpec struct {
	// Endpoint is the MinIO server endpoint URL
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern="^https?://.*"
	Endpoint string `json:"endpoint"`

	// Bucket is the MinIO bucket name for storage
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern="^[a-z0-9.-]+$"
	Bucket string `json:"bucket"`

	// Region is the MinIO bucket region (optional)
	// +optional
	Region string `json:"region,omitempty"`

	// PathPrefix is the prefix path within the bucket for file storage
	// +optional
	PathPrefix string `json:"pathPrefix,omitempty"`

	// Credentials specify how to authenticate with MinIO
	// +kubebuilder:validation:Required
	Credentials MinioCredentials `json:"credentials"`

	// TLS configuration for MinIO connection
	// +optional
	TLS *MinioTLSConfig `json:"tls,omitempty"`
}

// MinioCredentials define authentication for MinIO
type MinioCredentials struct {
	// AccessKeyID for MinIO authentication
	// +kubebuilder:validation:Required
	AccessKeyID string `json:"accessKeyID"`

	// SecretAccessKey for MinIO authentication
	// +kubebuilder:validation:Required
	SecretAccessKey string `json:"secretAccessKey"`

	// UseSecret indicates credentials should be read from a Secret
	// +optional
	UseSecret *MinioSecretRef `json:"useSecret,omitempty"`
}

// MinioSecretRef references a Kubernetes Secret for credentials
type MinioSecretRef struct {
	// Name of the secret
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace of the secret (defaults to same namespace)
	// +optional
	Namespace *string `json:"namespace,omitempty"`

	// AccessKeyIDKey is the key in the secret containing the access key ID
	// +kubebuilder:default="accessKeyID"
	AccessKeyIDKey string `json:"accessKeyIDKey,omitempty"`

	// SecretAccessKeyKey is the key in the secret containing the secret access key
	// +kubebuilder:default="secretAccessKey"
	SecretAccessKeyKey string `json:"secretAccessKeyKey,omitempty"`
}

// MinioTLSConfig defines TLS settings for MinIO connection
type MinioTLSConfig struct {
	// Enabled controls whether to use TLS
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`

	// InsecureSkipVerify controls whether to skip certificate verification
	// +kubebuilder:default=false
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`

	// CACert is the CA certificate for verifying the MinIO server
	// +optional
	CACert string `json:"caCert,omitempty"`
}

// MinioBackendStatus defines the observed state of MinioBackend.
type MinioBackendStatus struct {
	// Ready indicates if the backend is accessible and ready for use
	// +optional
	Ready bool `json:"ready,omitempty"`

	// LastChecked timestamp of the last connectivity check
	// +optional
	LastChecked *metav1.Time `json:"lastChecked,omitempty"`

	// ActiveConnections tracks the number of active FTP connections using this backend
	// +optional
	ActiveConnections int32 `json:"activeConnections,omitempty"`

	// Conditions represent the latest available observations of the backend's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Message provides additional status information
	// +optional
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// MinioBackend is the Schema for the miniobackends API
type MinioBackend struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of MinioBackend
	// +required
	Spec MinioBackendSpec `json:"spec"`

	// status defines the observed state of MinioBackend
	// +optional
	Status MinioBackendStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// MinioBackendList contains a list of MinioBackend
type MinioBackendList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MinioBackend `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MinioBackend{}, &MinioBackendList{})
}
