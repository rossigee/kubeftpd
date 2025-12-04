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

// WebDavBackendSpec defines the desired state of WebDavBackend
type WebDavBackendSpec struct {
	// Endpoint is the WebDAV server URL
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern="^https?://.*"
	Endpoint string `json:"endpoint"`

	// BasePath is the base path on the WebDAV server for file storage
	// +optional
	BasePath string `json:"basePath,omitempty"`

	// Credentials specify how to authenticate with the WebDAV server
	// +kubebuilder:validation:Required
	Credentials WebDavCredentials `json:"credentials"`

	// TLS configuration for WebDAV connection
	// +optional
	TLS *WebDavTLSConfig `json:"tls,omitempty"`
}

// WebDavCredentials define authentication for WebDAV
type WebDavCredentials struct {
	// Username for WebDAV authentication
	// +kubebuilder:validation:Required
	Username string `json:"username"`

	// Password for WebDAV authentication
	// +kubebuilder:validation:Required
	Password string `json:"password"`

	// UseSecret indicates credentials should be read from a Secret
	// +optional
	UseSecret *WebDavSecretRef `json:"useSecret,omitempty"`
}

// WebDavSecretRef references a Kubernetes Secret for credentials
type WebDavSecretRef struct {
	// Name of the secret
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace of the secret (defaults to same namespace)
	// +optional
	Namespace *string `json:"namespace,omitempty"`

	// UsernameKey is the key in the secret containing the username
	// +kubebuilder:default="username"
	UsernameKey string `json:"usernameKey,omitempty"`

	// PasswordKey is the key in the secret containing the password
	// +kubebuilder:default="password"
	PasswordKey string `json:"passwordKey,omitempty"`
}

// WebDavTLSConfig defines TLS settings for WebDAV connection
type WebDavTLSConfig struct {
	// Enabled controls whether to use TLS
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`

	// InsecureSkipVerify controls whether to skip certificate verification
	// +kubebuilder:default=false
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`

	// CACert is the CA certificate for verifying the WebDAV server
	// +optional
	CACert string `json:"caCert,omitempty"`
}

// WebDavBackendStatus defines the observed state of WebDavBackend.
type WebDavBackendStatus struct {
	// Ready indicates if the backend is accessible and ready for use
	// +optional
	Ready bool `json:"ready,omitempty"`

	// LastChecked timestamp of the last connectivity check
	// +optional
	LastChecked *metav1.Time `json:"lastChecked,omitempty"`

	// LastConnectivityTest timestamp of the last connectivity test
	// +optional
	LastConnectivityTest metav1.Time `json:"lastConnectivityTest,omitempty"`

	// ObservedGeneration represents the generation observed by the controller
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

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

// WebDavBackend is the Schema for the webdavbackends API
type WebDavBackend struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of WebDavBackend
	// +required
	Spec WebDavBackendSpec `json:"spec"`

	// status defines the observed state of WebDavBackend
	// +optional
	Status WebDavBackendStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// WebDavBackendList contains a list of WebDavBackend
type WebDavBackendList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WebDavBackend `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WebDavBackend{}, &WebDavBackendList{})
}
