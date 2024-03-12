/*
Copyright 2024.

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
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

type SnapshotRetentionSpec struct {
	Expires  string `json:"expires,omitempty"`
	MaxCount *int32 `json:"maxCount,omitempty"`
}

// SnapshotScheduleSpec defines the desired state of SnapshotSchedule
type SnapshotScheduleSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	ClaimSelector    metav1.LabelSelector  `json:"claimSelector,omitempty"`
	Retention        SnapshotRetentionSpec `json:"retention,omitempty"`
	Schedule         string                `json:"schedule,omitempty"`
	Disabled         bool                  `json:"disabled,omitempty"`
	SnapshotTemplate *SnapshotTemplateSpec `json:"snapshotTemplate,omitempty"`
}

type SnapshotTemplateSpec struct {
	Labels            map[string]string `json:"labels,omitempty"`
	SnapshotClassName *string           `json:"snapshotClassName,omitempty"`
}

// SnapshotScheduleStatus defines the observed state of SnapshotSchedule
type SnapshotScheduleStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Conditions       []conditionsv1.Condition `json:"conditions,omitempty"`
	LastSnapshotTime *metav1.Time             `json:"lastSnapshotTime,omitempty"`
	NextSnapshotTime *metav1.Time             `json:"nextSnapshotTime,omitempty"`
}

const (
	ConditionReconciled     conditionsv1.ConditionType = "Reconciled"
	ReconciledReasonError                              = "ReconcileError"
	ReconciledReasonSuccess                            = "ReconcileSuccess"
)

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// SnapshotSchedule is the Schema for the snapshotschedules API
type SnapshotSchedule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SnapshotScheduleSpec   `json:"spec,omitempty"`
	Status SnapshotScheduleStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// SnapshotScheduleList contains a list of SnapshotSchedule
type SnapshotScheduleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SnapshotSchedule `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SnapshotSchedule{}, &SnapshotScheduleList{})
}
