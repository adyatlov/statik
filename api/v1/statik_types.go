package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// StatikSpec defines the desired state of Statik
type StatikSpec struct {
	RepoURL    string `json:"repoURL"`    // URL of the GitHub repository
	CommitHash string `json:"commitHash"` // GitHub commit hash to deploy
	DomainName string `json:"domainName"` // Domain where the site will be deployed
}

// StatikStatus defines the observed state of Statik
type StatikStatus struct {
	DeployedCommitHash string       `json:"deployedCommitHash,omitempty"` // Hash of the deployed commit
	DeploymentStatus   string       `json:"deploymentStatus,omitempty"`   // Status of the deployment
	LastUpdated        *metav1.Time `json:"lastUpdated,omitempty"`        // Last successful deployment time
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Statik is the Schema for the statiks API
type Statik struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   StatikSpec   `json:"spec,omitempty"`
	Status StatikStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// StatikList contains a list of Statik
type StatikList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Statik `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Statik{}, &StatikList{})
}
