/*
Copyright 2026.

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

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterRequest condition types
const (
	ClusterRequestConditionTypeReady      = "Ready"
	ClusterRequestConditionTypeHostsReady = "HostsReady"
)

// ClusterRequest condition reasons for Ready condition
const (
	// ClusterRequestReasonReady indicates the cluster request is fully ready
	ClusterRequestReasonReady = "Ready"

	// ClusterRequestReasonProgressing indicates the cluster request is being processed
	ClusterRequestReasonProgressing = "Progressing"

	// ClusterRequestReasonFailed indicates the cluster request has failed
	ClusterRequestReasonFailed = "Failed"

	// ClusterRequestReasonDeleting indicates the cluster request is being deleted
	ClusterRequestReasonDeleting = "Deleting"
)

// ClusterRequest condition reasons for HostsReady condition
const (
	// ClusterRequestReasonHostsAvailable indicates all required hosts have been allocated and ready
	ClusterRequestReasonHostsAvailable = "HostsAvailable"

	// ClusterRequestReasonHostsProgressing indicates hosts are being processed
	ClusterRequestReasonHostsProgressing = "HostsProgressing"

	// ClusterRequestReasonHostsDeleting indicates hosts are being deleted/freed
	ClusterRequestReasonHostsDeleting = "HostsDeleting"

	// ClusterRequestReasonInventoryServiceFailed indicates communication with inventory service failed
	ClusterRequestReasonInventoryServiceFailed = "InventoryServiceFailed"

	// ClusterRequestReasonHostOperationFailed indicates host attach/detach operations failed
	ClusterRequestReasonHostOperationFailed = "HostOperationFailed"

	// ClusterRequestReasonInsufficientHosts indicates not enough hosts match the criteria
	ClusterRequestReasonInsufficientHosts = "InsufficientHosts"

	// ClusterRequestReasonHostsUnavailable indicates requested hosts are not available
	ClusterRequestReasonHostsUnavailable = "HostsUnavailable"
)

// InitializeStatusConditions initializes the ClusterRequest conditions
func (c *ClusterRequest) InitializeStatusConditions() {
	c.initializeStatusCondition(
		ClusterRequestConditionTypeReady,
		metav1.ConditionFalse,
		ClusterRequestReasonProgressing,
	)
	c.initializeStatusCondition(
		ClusterRequestConditionTypeHostsReady,
		metav1.ConditionFalse,
		ClusterRequestReasonHostsProgressing,
	)
}

func (c *ClusterRequest) SetStatusCondition(
	conditionType string,
	status metav1.ConditionStatus,
	reason string,
	message string,
) {
	meta.SetStatusCondition(&c.Status.Conditions, metav1.Condition{
		Type:    conditionType,
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}

// initializeStatusCondition initializes a single condition if it doesn't already exist
func (c *ClusterRequest) initializeStatusCondition(
	conditionType string,
	status metav1.ConditionStatus,
	reason string,
) {
	if c.Status.Conditions == nil {
		c.Status.Conditions = []metav1.Condition{}
	}

	// If condition already exists, don't overwrite
	if meta.FindStatusCondition(c.Status.Conditions, conditionType) != nil {
		return
	}

	c.SetStatusCondition(conditionType, status, reason, "Initialized")
}
