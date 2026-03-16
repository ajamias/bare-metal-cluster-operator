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

// BareMetalPool condition types
const (
	BareMetalPoolConditionTypeReady      = "Ready"
	BareMetalPoolConditionTypeHostsReady = "HostsReady"
)

// BareMetalPool condition reasons for Ready condition
const (
	// BareMetalPoolReasonReady indicates the cluster request is fully ready
	BareMetalPoolReasonReady = "Ready"

	// BareMetalPoolReasonProgressing indicates the cluster request is being processed
	BareMetalPoolReasonProgressing = "Progressing"

	// BareMetalPoolReasonFailed indicates the cluster request has failed
	BareMetalPoolReasonFailed = "Failed"

	// BareMetalPoolReasonDeleting indicates the cluster request is being deleted
	BareMetalPoolReasonDeleting = "Deleting"
)

// BareMetalPool condition reasons for HostsReady condition
const (
	// BareMetalPoolReasonHostsAvailable indicates all required hosts have been allocated and ready
	BareMetalPoolReasonHostsAvailable = "HostsAvailable"

	// BareMetalPoolReasonHostsProgressing indicates hosts are being processed
	BareMetalPoolReasonHostsProgressing = "HostsProgressing"

	// BareMetalPoolReasonHostsDeleting indicates hosts are being deleted/freed
	BareMetalPoolReasonHostsDeleting = "HostsDeleting"

	// BareMetalPoolReasonInventoryServiceFailed indicates communication with inventory service failed
	BareMetalPoolReasonInventoryServiceFailed = "InventoryServiceFailed"

	// BareMetalPoolReasonHostOperationFailed indicates host attach/detach operations failed
	BareMetalPoolReasonHostOperationFailed = "HostOperationFailed"

	// BareMetalPoolReasonInsufficientHosts indicates not enough hosts match the criteria
	BareMetalPoolReasonInsufficientHosts = "InsufficientHosts"

	// BareMetalPoolReasonHostsUnavailable indicates requested hosts are not available
	BareMetalPoolReasonHostsUnavailable = "HostsUnavailable"
)

// InitializeStatusConditions initializes the BareMetalPool conditions
func (c *BareMetalPool) InitializeStatusConditions() {
	c.initializeStatusCondition(
		BareMetalPoolConditionTypeReady,
		metav1.ConditionFalse,
		BareMetalPoolReasonProgressing,
	)
	c.initializeStatusCondition(
		BareMetalPoolConditionTypeHostsReady,
		metav1.ConditionFalse,
		BareMetalPoolReasonHostsProgressing,
	)
}

func (c *BareMetalPool) SetStatusCondition(
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
func (c *BareMetalPool) initializeStatusCondition(
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
