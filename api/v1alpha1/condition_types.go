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

// BareMetalCluster condition types
const (
	BareMetalClusterConditionTypeReady      = "Ready"
	BareMetalClusterConditionTypeHostsReady = "HostsReady"
)

// BareMetalCluster condition reasons for Ready condition
const (
	// BareMetalClusterReasonReady indicates the cluster request is fully ready
	BareMetalClusterReasonReady = "Ready"

	// BareMetalClusterReasonProgressing indicates the cluster request is being processed
	BareMetalClusterReasonProgressing = "Progressing"

	// BareMetalClusterReasonFailed indicates the cluster request has failed
	BareMetalClusterReasonFailed = "Failed"

	// BareMetalClusterReasonDeleting indicates the cluster request is being deleted
	BareMetalClusterReasonDeleting = "Deleting"
)

// BareMetalCluster condition reasons for HostsReady condition
const (
	// BareMetalClusterReasonHostsAvailable indicates all required hosts have been allocated and ready
	BareMetalClusterReasonHostsAvailable = "HostsAvailable"

	// BareMetalClusterReasonHostsProgressing indicates hosts are being processed
	BareMetalClusterReasonHostsProgressing = "HostsProgressing"

	// BareMetalClusterReasonHostsDeleting indicates hosts are being deleted/freed
	BareMetalClusterReasonHostsDeleting = "HostsDeleting"

	// BareMetalClusterReasonInventoryServiceFailed indicates communication with inventory service failed
	BareMetalClusterReasonInventoryServiceFailed = "InventoryServiceFailed"

	// BareMetalClusterReasonHostOperationFailed indicates host attach/detach operations failed
	BareMetalClusterReasonHostOperationFailed = "HostOperationFailed"

	// BareMetalClusterReasonInsufficientHosts indicates not enough hosts match the criteria
	BareMetalClusterReasonInsufficientHosts = "InsufficientHosts"

	// BareMetalClusterReasonHostsUnavailable indicates requested hosts are not available
	BareMetalClusterReasonHostsUnavailable = "HostsUnavailable"
)

// InitializeStatusConditions initializes the BareMetalCluster conditions
func (c *BareMetalCluster) InitializeStatusConditions() {
	c.initializeStatusCondition(
		BareMetalClusterConditionTypeReady,
		metav1.ConditionFalse,
		BareMetalClusterReasonProgressing,
	)
	c.initializeStatusCondition(
		BareMetalClusterConditionTypeHostsReady,
		metav1.ConditionFalse,
		BareMetalClusterReasonHostsProgressing,
	)
}

func (c *BareMetalCluster) SetStatusCondition(
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
func (c *BareMetalCluster) initializeStatusCondition(
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
