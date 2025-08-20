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

// UserSpec defines the desired state of User
type UserSpec struct {
	// Username is the FTP username for authentication
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern="^[a-zA-Z0-9_-]+$"
	Username string `json:"username"`

	// Type indicates the type of user (regular, anonymous, admin)
	// +kubebuilder:default="regular"
	// +kubebuilder:validation:Enum=regular;anonymous;admin
	// +optional
	Type string `json:"type,omitempty"`

	// Password is the FTP password (plaintext, not recommended for production)
	// +optional
	Password string `json:"password,omitempty"`

	// PasswordSecret references a Kubernetes Secret containing the password
	// +optional
	PasswordSecret *UserSecretRef `json:"passwordSecret,omitempty"`

	// Backend specifies which backend storage to use
	// +kubebuilder:validation:Required
	Backend BackendReference `json:"backend"`

	// HomeDirectory is the virtual home directory path for the user
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern="^/.*"
	HomeDirectory string `json:"homeDirectory"`

	// Chroot restricts user access to their home directory (jail)
	// When enabled, users cannot navigate outside their home directory
	// +kubebuilder:default=true
	// +optional
	Chroot bool `json:"chroot,omitempty"`

	// Enabled controls whether the user account is active
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`

	// Permissions define what the user can do
	// +optional
	Permissions UserPermissions `json:"permissions,omitempty"`
}

// BackendReference refers to a backend storage resource
type BackendReference struct {
	// Kind specifies the backend type (MinioBackend, WebDavBackend, FilesystemBackend)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=MinioBackend;WebDavBackend;FilesystemBackend
	Kind string `json:"kind"`

	// Name of the backend resource
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace of the backend resource (defaults to same namespace)
	// +optional
	Namespace *string `json:"namespace,omitempty"`
}

// UserSecretRef references a Kubernetes Secret for user password
type UserSecretRef struct {
	// Name of the secret
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace of the secret (defaults to same namespace as User)
	// +optional
	Namespace *string `json:"namespace,omitempty"`

	// Key is the key in the secret containing the password
	// +kubebuilder:default="password"
	Key string `json:"key,omitempty"`
}

// UserPermissions define what operations a user can perform
type UserPermissions struct {
	// Read permission for downloading files
	// +kubebuilder:default=true
	Read bool `json:"read,omitempty"`

	// Write permission for uploading files
	// +kubebuilder:default=true
	Write bool `json:"write,omitempty"`

	// Delete permission for removing files
	// +kubebuilder:default=false
	Delete bool `json:"delete,omitempty"`

	// List permission for listing directories
	// +kubebuilder:default=true
	List bool `json:"list,omitempty"`
}

// UserStatus defines the observed state of User.
type UserStatus struct {
	// Ready indicates if the user is properly configured and available
	// +optional
	Ready bool `json:"ready,omitempty"`

	// LastLogin timestamp of the user's last successful login
	// +optional
	LastLogin *metav1.Time `json:"lastLogin,omitempty"`

	// ConnectionCount tracks active connections for this user
	// +optional
	ConnectionCount int32 `json:"connectionCount,omitempty"`

	// Conditions represent the latest available observations of the user's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Message provides additional status information
	// +optional
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// User is the Schema for the users API
type User struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of User
	// +required
	Spec UserSpec `json:"spec"`

	// status defines the observed state of User
	// +optional
	Status UserStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// UserList contains a list of User
type UserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []User `json:"items"`
}

func init() {
	SchemeBuilder.Register(&User{}, &UserList{})
}
