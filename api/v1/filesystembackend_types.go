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

// FilesystemBackendSpec defines the desired state of FilesystemBackend
type FilesystemBackendSpec struct {
	// BasePath is the base directory path where files will be stored
	// This should typically be a mounted persistent volume
	// +kubebuilder:validation:Required
	BasePath string `json:"basePath"`

	// ReadOnly specifies if the filesystem should be mounted read-only
	// +kubebuilder:default:=false
	ReadOnly bool `json:"readOnly,omitempty"`

	// FileMode specifies the default file permissions for new files
	// +kubebuilder:default:="0644"
	// +kubebuilder:validation:Pattern="^0[0-7]{3}$"
	FileMode string `json:"fileMode,omitempty"`

	// DirMode specifies the default directory permissions for new directories
	// +kubebuilder:default:="0755"
	// +kubebuilder:validation:Pattern="^0[0-7]{3}$"
	DirMode string `json:"dirMode,omitempty"`

	// MaxFileSize specifies the maximum allowed file size in bytes
	// Set to 0 for no limit (default)
	// +kubebuilder:default:=0
	MaxFileSize int64 `json:"maxFileSize,omitempty"`

	// VolumeClaimRef references the PersistentVolumeClaim to use for storage
	// +optional
	VolumeClaimRef *VolumeClaimReference `json:"volumeClaimRef,omitempty"`
}

// VolumeClaimReference references a PersistentVolumeClaim
type VolumeClaimReference struct {
	// Name is the name of the PersistentVolumeClaim
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace is the namespace of the PersistentVolumeClaim
	// If not specified, uses the same namespace as the FilesystemBackend
	// +optional
	Namespace *string `json:"namespace,omitempty"`
}

// FilesystemBackendStatus defines the observed state of FilesystemBackend
type FilesystemBackendStatus struct {
	// Ready indicates if the filesystem backend is ready for use
	Ready bool `json:"ready"`

	// Message provides additional information about the backend status
	// +optional
	Message string `json:"message,omitempty"`

	// LastChecked is the timestamp of the last readiness check
	// +optional
	LastChecked *metav1.Time `json:"lastChecked,omitempty"`

	// AvailableSpace shows the available space in bytes (if determinable)
	// +optional
	AvailableSpace *int64 `json:"availableSpace,omitempty"`

	// TotalSpace shows the total space in bytes (if determinable)
	// +optional
	TotalSpace *int64 `json:"totalSpace,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Base Path",type=string,JSONPath=`.spec.basePath`
//+kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
//+kubebuilder:printcolumn:name="Read Only",type=boolean,JSONPath=`.spec.readOnly`
//+kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// FilesystemBackend is the Schema for the filesystembackends API
type FilesystemBackend struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FilesystemBackendSpec   `json:"spec,omitempty"`
	Status FilesystemBackendStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// FilesystemBackendList contains a list of FilesystemBackend
type FilesystemBackendList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FilesystemBackend `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FilesystemBackend{}, &FilesystemBackendList{})
}
