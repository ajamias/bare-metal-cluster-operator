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

package controller

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	hostv1alpha1 "github.com/DanNiESh/host-operator/api/v1alpha1"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/rand"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/ajamias/bare-metal-operator/api/v1alpha1"
	"github.com/ajamias/bare-metal-operator/internal/inventory"
	"github.com/ajamias/bare-metal-operator/internal/profile"
)

// BareMetalPoolReconciler reconciles a BareMetalPool object
type BareMetalPoolReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	InventoryClient *inventory.InventoryClient
}

const BareMetalPoolFinalizer = "osac.openshift.io/cluster-request"

type contextKey string

type hostOperationResult struct {
	Host   inventory.Host
	Result ctrl.Result
	Err    error
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=baremetalpools,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=baremetalpools/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=baremetalpools/finalizers,verbs=update
// +kubebuilder:rbac:groups=osac.openshift.io,resources=hosts,verbs=get;list;watch;create;delete;deletecollection
// +kubebuilder:rbac:groups=tekton.dev,resources=pipelineruns,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *BareMetalPoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	bareMetalPool := &v1alpha1.BareMetalPool{}
	err := r.Get(ctx, req.NamespacedName, bareMetalPool)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	log.Info(
		"Starting reconcile",
		"namespacedName",
		req.NamespacedName,
		"name",
		bareMetalPool.Name,
	)

	oldstatus := bareMetalPool.Status.DeepCopy()

	var result ctrl.Result
	if !bareMetalPool.DeletionTimestamp.IsZero() {
		err = r.handleDeletion(ctx, bareMetalPool)
		result = ctrl.Result{}
	} else {
		result, err = r.handleUpdate(ctx, bareMetalPool)
	}

	if !equality.Semantic.DeepEqual(bareMetalPool.Status, *oldstatus) {
		status := metav1.ConditionFalse
		hostsReason := meta.FindStatusCondition(
			bareMetalPool.Status.Conditions,
			v1alpha1.BareMetalPoolConditionTypeHostsReady,
		).Reason

		var reason string
		switch hostsReason {
		case v1alpha1.BareMetalPoolReasonHostsAvailable:
			status = metav1.ConditionTrue
			reason = v1alpha1.BareMetalPoolReasonReady
		case v1alpha1.BareMetalPoolReasonHostsProgressing:
			reason = v1alpha1.BareMetalPoolReasonProgressing
		case v1alpha1.BareMetalPoolReasonHostsDeleting:
			reason = v1alpha1.BareMetalPoolReasonDeleting
		default:
			reason = v1alpha1.BareMetalPoolReasonFailed
		}

		bareMetalPool.SetStatusCondition(
			v1alpha1.BareMetalPoolConditionTypeReady,
			status,
			reason,
			hostsReason,
		)

		statusErr := r.Status().Update(ctx, bareMetalPool)
		if statusErr != nil {
			return result, statusErr
		}
	}

	return result, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *BareMetalPoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.BareMetalPool{}).
		Named("baremetalpool").
		Complete(r)
}

// handleUpdate processes BareMetalPool creation or specification updates.
func (r *BareMetalPoolReconciler) handleUpdate(ctx context.Context, bareMetalPool *v1alpha1.BareMetalPool) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Handling update", "name", bareMetalPool.Name)

	bareMetalPool.InitializeStatusConditions()

	if controllerutil.AddFinalizer(bareMetalPool, BareMetalPoolFinalizer) {
		// Update should fire another reconcile event, so just return
		err := r.Update(ctx, bareMetalPool)
		return ctrl.Result{}, err
	}

	if bareMetalPool.Status.MatchType == "" {
		bareMetalPool.Status.MatchType = bareMetalPool.Spec.MatchType
	}

	if bareMetalPool.Status.HostSets == nil {
		bareMetalPool.Status.HostSets = []v1alpha1.HostSet{}
	}

	// positive delta means add hosts of the HostClass, negative means remove
	hostClassToHostSetDelta, hostClassToCurrentHostSetSize, hostClassToHostsToDetach, requiresUpdate, err := r.syncWithInventory(ctx, bareMetalPool)
	if err != nil {
		log.Error(err, "Failed to sync status with inventory")
		return ctrl.Result{}, err
	}

	if !requiresUpdate {
		log.Info("No update required")
		bareMetalPool.SetStatusCondition(
			v1alpha1.BareMetalPoolConditionTypeHostsReady,
			metav1.ConditionTrue,
			v1alpha1.BareMetalPoolReasonHostsAvailable,
			"Successfully reconciled hosts",
		)
		return ctrl.Result{}, nil
	}

	hostClassToAvailableHosts, err := r.verifyAvailableHosts(ctx, bareMetalPool, hostClassToHostSetDelta)
	if err != nil {
		if err.Error() == "insufficient hosts" {
			log.Info("Insufficient hosts available")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		log.Error(err, "Failed to verify host availability")
		return ctrl.Result{}, err
	}

	// attach or detach hosts in parallel for each host class
	var wg sync.WaitGroup
	chanLen := 0
	for _, delta := range hostClassToHostSetDelta {
		if delta > 0 {
			chanLen += delta
		} else {
			chanLen -= delta
		}
	}
	results := make(chan hostOperationResult, chanLen)
	ctx = context.WithValue(ctx, contextKey("mutex"), &sync.Mutex{})

	for hostClass, delta := range hostClassToHostSetDelta {
		if delta > 0 {
			for _, host := range hostClassToAvailableHosts[hostClass] {
				wg.Add(1)
				go func(host inventory.Host) {
					defer wg.Done()
					// TODO: distributedMutex.Lock(host)
					// TODO: defer distributedMutex.Unlock(host)
					result, err := r.markAndAttachHost(
						ctx,
						bareMetalPool,
						host,
						hostClassToCurrentHostSetSize,
					)
					results <- hostOperationResult{
						Host:   host,
						Result: result,
						Err:    err,
					}
				}(host)
			}
		} else if delta < 0 {
			for _, host := range hostClassToHostsToDetach[hostClass] {
				wg.Add(1)
				go func(host inventory.Host) {
					defer wg.Done()
					// TODO: distributedMutex.Lock(host)
					// TODO: defer distributedMutex.Unlock(host)
					err := r.unmarkAndDetachHost(
						ctx,
						bareMetalPool,
						host,
						hostClassToCurrentHostSetSize,
					)
					results <- hostOperationResult{
						Host:   host,
						Result: ctrl.Result{},
						Err:    err,
					}
				}(host)
			}
		} else if delta == 0 {
			continue
		}
	}
	wg.Wait()

	// need to update HostSets even on failure
	updatedHostSets := make([]v1alpha1.HostSet, 0, len(hostClassToCurrentHostSetSize))
	for hostClass, replicas := range hostClassToCurrentHostSetSize {
		if replicas <= 0 {
			continue
		}
		updatedHostSets = append(updatedHostSets, v1alpha1.HostSet{
			HostClass: hostClass,
			Replicas:  replicas,
		})
	}
	bareMetalPool.Status.HostSets = updatedHostSets

	close(results)
	for result := range results {
		if !result.Result.IsZero() || result.Err != nil {
			log.Info("Unsuccessful in updating host-" + result.Host.NodeId)
			bareMetalPool.SetStatusCondition(
				v1alpha1.BareMetalPoolConditionTypeHostsReady,
				metav1.ConditionTrue,
				v1alpha1.BareMetalPoolReasonHostsAvailable,
				"Failed to attach/detach host",
			)
			return result.Result, result.Err
		}
	}

	log.Info("Successfully reconciled hosts")
	bareMetalPool.SetStatusCondition(
		v1alpha1.BareMetalPoolConditionTypeHostsReady,
		metav1.ConditionTrue,
		v1alpha1.BareMetalPoolReasonHostsAvailable,
		"Successfully reconciled hosts",
	)

	registeredProfile, ok := profile.Get(bareMetalPool.Spec.Profile.Name)
	if ok {
		err = r.runWorkflow(ctx, bareMetalPool, registeredProfile.ClusterSetUpWorkflow)
		if err != nil {
			log.Error(err, "Failed to set up cluster")
			bareMetalPool.SetStatusCondition(
				v1alpha1.BareMetalPoolConditionTypeHostsReady,
				metav1.ConditionFalse,
				v1alpha1.BareMetalPoolReasonHostOperationFailed,
				"Failed to set up cluster",
			)
			return ctrl.Result{}, err
		}
	}

	log.Info("Successfully set up cluster")

	return ctrl.Result{}, nil
}

// handleDeletion handles the cleanup when a BareMetalPool is being deleted
func (r *BareMetalPoolReconciler) handleDeletion(ctx context.Context, bareMetalPool *v1alpha1.BareMetalPool) error {
	log := logf.FromContext(ctx)
	log.Info("Handling delete", "name", bareMetalPool.Name)

	bareMetalPool.SetStatusCondition(
		v1alpha1.BareMetalPoolConditionTypeHostsReady,
		metav1.ConditionFalse,
		v1alpha1.BareMetalPoolReasonHostsDeleting,
		"BareMetalPool's hosts are being freed",
	)

	registeredProfile, ok := profile.Get(bareMetalPool.Spec.Profile.Name)
	if ok {
		err := r.runWorkflow(ctx, bareMetalPool, registeredProfile.ClusterTearDownWorkflow)
		if err != nil {
			log.Error(err, "Failed to tear down cluster")
			bareMetalPool.SetStatusCondition(
				v1alpha1.BareMetalPoolConditionTypeHostsReady,
				metav1.ConditionFalse,
				v1alpha1.BareMetalPoolReasonHostOperationFailed,
				"Failed to tear down cluster",
			)
			return err
		}
	}

	// Get all hosts currently assigned to this cluster
	currentHosts, err := r.InventoryClient.GetHosts(ctx, string(bareMetalPool.UID))
	if err != nil {
		log.Error(err, "Failed to get hosts from inventory during deletion")
		bareMetalPool.SetStatusCondition(
			v1alpha1.BareMetalPoolConditionTypeHostsReady,
			metav1.ConditionFalse,
			v1alpha1.BareMetalPoolReasonInventoryServiceFailed,
			"Failed to get hosts from inventory during deletion",
		)
		return err
	}

	// Detach all hosts in parallel
	var wg sync.WaitGroup
	results := make(chan hostOperationResult, len(currentHosts))
	ctx = context.WithValue(ctx, contextKey("mutex"), &sync.Mutex{})
	hostClassToCurrentHostSetSize := map[string]int{}

	for _, host := range currentHosts {
		hostClassToCurrentHostSetSize[host.HostClass]++
		wg.Add(1)
		go func(host inventory.Host) {
			defer wg.Done()
			err := r.unmarkAndDetachHost(
				ctx,
				bareMetalPool,
				host,
				hostClassToCurrentHostSetSize,
			)
			results <- hostOperationResult{
				Host:   host,
				Result: ctrl.Result{},
				Err:    err,
			}
		}(host)
	}
	wg.Wait()

	// need to update HostSets even on failure
	updatedHostSets := make([]v1alpha1.HostSet, 0, len(hostClassToCurrentHostSetSize))
	for hostClass, replicas := range hostClassToCurrentHostSetSize {
		if replicas <= 0 {
			continue
		}
		updatedHostSets = append(updatedHostSets, v1alpha1.HostSet{
			HostClass: hostClass,
			Replicas:  replicas,
		})
	}
	bareMetalPool.Status.HostSets = updatedHostSets

	close(results)
	for result := range results {
		if result.Err != nil {
			log.Info("Failed to delete/detach host-" + result.Host.NodeId)
			bareMetalPool.SetStatusCondition(
				v1alpha1.BareMetalPoolConditionTypeHostsReady,
				metav1.ConditionFalse,
				v1alpha1.BareMetalPoolReasonHostOperationFailed,
				"Failed to delete/detach host",
			)
			return result.Err
		}
	}

	log.Info("Successfully deleted cluster")
	if controllerutil.RemoveFinalizer(bareMetalPool, BareMetalPoolFinalizer) {
		// Update should fire another reconcile event, so just return
		err := r.Update(ctx, bareMetalPool)
		return err
	}

	return nil
}

// syncWithInventory compares the desired and current host allocations and returns the deltas
func (r *BareMetalPoolReconciler) syncWithInventory(
	ctx context.Context,
	bareMetalPool *v1alpha1.BareMetalPool,
) (map[string]int, map[string]int, map[string][]inventory.Host, bool, error) {
	log := logf.FromContext(ctx)

	currentHosts, err := r.InventoryClient.GetHosts(ctx, string(bareMetalPool.UID))
	if err != nil {
		log.Error(err, "Failed to get hosts from inventory")
		bareMetalPool.SetStatusCondition(
			v1alpha1.BareMetalPoolConditionTypeHostsReady,
			metav1.ConditionFalse,
			v1alpha1.BareMetalPoolReasonInventoryServiceFailed,
			"Failed to get hosts from inventory",
		)
		return nil, nil, nil, false, err
	}

	// Group current hosts by hostClass for detachment later
	hostClassToCurrentHosts := map[string][]inventory.Host{}
	hostClassToCurrentHostSetSize := map[string]int{}
	for _, host := range currentHosts {
		hostClassToCurrentHosts[host.HostClass] = append(hostClassToCurrentHosts[host.HostClass], host)
		hostClassToCurrentHostSetSize[host.HostClass] += 1
	}

	// hostClassToHostSetDelta is never written to after init-ing it, so we dont need sync.Map
	hostClassToHostSetDelta := map[string]int{}
	hostClassToHostsToDetach := map[string][]inventory.Host{}
	requiresUpdate := false

	// Calculate delta for each hostSet in spec
	for _, hostSet := range bareMetalPool.Spec.HostSets {
		currentCount := hostClassToCurrentHostSetSize[hostSet.HostClass]
		delta := hostSet.Replicas - currentCount
		hostClassToHostSetDelta[hostSet.HostClass] = delta
		if delta != 0 {
			requiresUpdate = true
		}
		if delta < 0 {
			hostsToDetach := hostClassToCurrentHosts[hostSet.HostClass][hostSet.Replicas:]
			hostClassToHostsToDetach[hostSet.HostClass] = hostsToDetach
		}
	}

	// Set negative delta for hostSets not in spec
	for hostClass, hosts := range hostClassToCurrentHosts {
		if _, ok := hostClassToHostSetDelta[hostClass]; ok {
			continue
		}
		hostClassToHostSetDelta[hostClass] = -len(hosts)
		hostClassToHostsToDetach[hostClass] = hosts
		requiresUpdate = true
	}

	return hostClassToHostSetDelta, hostClassToCurrentHostSetSize, hostClassToHostsToDetach, requiresUpdate, nil
}

func (r *BareMetalPoolReconciler) verifyAvailableHosts(
	ctx context.Context,
	bareMetalPool *v1alpha1.BareMetalPool,
	hostClassToHostSetDelta map[string]int,
) (map[string][]inventory.Host, error) {
	log := logf.FromContext(ctx).V(1)
	ctx = logf.IntoContext(ctx, log)

	hostClassToAvailableHosts := map[string][]inventory.Host{}
	// TODO: can use goroutines
	for hostClass, delta := range hostClassToHostSetDelta {
		if delta <= 0 {
			continue
		}

		hosts, err := r.InventoryClient.GetHosts(
			ctx,
			"",
			inventory.WithHostClass(hostClass),
			inventory.WithCount(delta),
			inventory.WithMatchType(bareMetalPool.Spec.MatchType),
		)
		if err != nil {
			log.Error(err, "Failed to get hosts from inventory")
			bareMetalPool.SetStatusCondition(
				v1alpha1.BareMetalPoolConditionTypeHostsReady,
				metav1.ConditionFalse,
				v1alpha1.BareMetalPoolReasonInventoryServiceFailed,
				"Failed to get hosts from inventory",
			)
			return nil, err
		}
		if len(hosts) < delta {
			err := errors.New("insufficient hosts")
			log.Info(
				"There are not enough available hosts in the inventory",
				"host class", hostClass,
				"desired additional hosts", delta,
				"current additional hosts", len(hosts),
			)
			bareMetalPool.SetStatusCondition(
				v1alpha1.BareMetalPoolConditionTypeHostsReady,
				metav1.ConditionFalse,
				v1alpha1.BareMetalPoolReasonInsufficientHosts,
				"There are not enough available hosts in the inventory",
			)
			return nil, err
		}

		hostClassToAvailableHosts[hostClass] = append(hostClassToAvailableHosts[hostClass], hosts...)
	}

	log.Info("Successfully verified available hosts")

	return hostClassToAvailableHosts, nil
}

func (r *BareMetalPoolReconciler) markAndAttachHost(
	ctx context.Context,
	bareMetalPool *v1alpha1.BareMetalPool,
	inventoryHost inventory.Host,
	hostClassToCurrentHostSetSize map[string]int,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx).V(1)
	ctx = logf.IntoContext(ctx, log)

	clusterId := string(bareMetalPool.UID)
	matchType, _ := inventoryHost.Extra["matchType"].(string)

	// check if another cluster currently has this host
	hostName := fmt.Sprintf("host-%s", inventoryHost.NodeId)
	hostCR := &hostv1alpha1.Host{}
	err := r.Get(
		ctx,
		client.ObjectKey{
			Namespace: bareMetalPool.Namespace,
			Name:      hostName,
		},
		hostCR,
	)
	if client.IgnoreNotFound(err) != nil {
		log.Error(err, "Failed to get Host CR", "hostName", hostName)
		return ctrl.Result{}, err
	} else if err == nil {
		if hostCR.Labels["osac.openshift.io/cluster-id"] == clusterId {
			log.Info("Host " + hostName + " is already acquired by this cluster")
			return ctrl.Result{}, nil
		}
		err = errors.New("host is already acquired by another cluster")
		log.Error(err, "Failed to acquire Host", "hostName", hostName)
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	// mark inventory host as attached
	err = r.InventoryClient.PatchInventoryHostClusterId(ctx, inventoryHost.NodeId, clusterId)
	if err != nil {
		log.Error(err, "Failed to mark host as attached", "NodeId", inventoryHost.NodeId)
		return ctrl.Result{}, err
	}

	/*
		hostSetUpWorkflow := ""
		hostTearDownWorkflow := ""
		registeredProfile, ok := profile.Get(bareMetalPool.Spec.Profile.Name)
		if ok {
			hostSetUpWorkflow = registeredProfile.HostSetUpWorkflow.String()
			hostTearDownWorkflow = registeredProfile.HostTearDownWorkflow.String()
		}
	*/

	// create Host CR
	hostCR = &hostv1alpha1.Host{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hostName,
			Namespace: bareMetalPool.Namespace,
			Labels: client.MatchingLabels{
				"osac.openshift.io/cluster-id": clusterId,
				"osac.openshift.io/host-class": inventoryHost.HostClass,
			},
		},
		Spec: hostv1alpha1.HostSpec{
			NodeID:    inventoryHost.NodeId,
			ManagedBy: matchType,
			HostClass: inventoryHost.HostClass,
			Online:    false,
		},
	}
	err = controllerutil.SetControllerReference(bareMetalPool, hostCR, r.Scheme)
	if err != nil {
		log.Error(err, "Failed to set controller reference to Host CR", "NodeId", inventoryHost.NodeId)
		return ctrl.Result{}, err
	}
	err = r.Create(ctx, hostCR)
	if err != nil {
		log.Error(err, "Failed to create Host CR", "NodeId", inventoryHost.NodeId)
		return ctrl.Result{}, err
	}

	mutex := ctx.Value(contextKey("mutex")).(*sync.Mutex)
	mutex.Lock()
	hostClassToCurrentHostSetSize[inventoryHost.HostClass]++
	mutex.Unlock()

	log.Info("Successfully acquired host", "NodeId", inventoryHost.NodeId)

	return ctrl.Result{}, nil
}

func (r *BareMetalPoolReconciler) unmarkAndDetachHost(
	ctx context.Context,
	bareMetalPool *v1alpha1.BareMetalPool,
	inventoryHost inventory.Host,
	hostClassToCurrentHostSetSize map[string]int,
) error {
	log := logf.FromContext(ctx).V(1)
	ctx = logf.IntoContext(ctx, log)

	clusterId := string(bareMetalPool.UID)

	// check if another cluster currently has this host
	hostName := fmt.Sprintf("host-%s", inventoryHost.NodeId)
	hostCR := &hostv1alpha1.Host{}
	err := r.Get(
		ctx,
		client.ObjectKey{
			Namespace: bareMetalPool.Namespace,
			Name:      hostName,
		},
		hostCR,
	)
	if client.IgnoreNotFound(err) != nil {
		log.Error(err, "Failed to get Host CR", "hostName", hostName)
		return err
	} else if err == nil && hostCR.Labels["osac.openshift.io/cluster-id"] != clusterId {
		log.Info("Host is attached to a different cluster")
		return nil
	}

	// delete Host CR
	err = r.Delete(ctx, hostCR)
	if client.IgnoreNotFound(err) != nil {
		log.Error(err, "Failed to delete Host CR", "hostName", hostName)
		return err
	}

	// mark inventory host as detached
	err = r.InventoryClient.PatchInventoryHostClusterId(ctx, inventoryHost.NodeId, "")
	if err != nil {
		log.Error(err, "Failed to free host", "NodeId", inventoryHost.NodeId)
		bareMetalPool.SetStatusCondition(
			v1alpha1.BareMetalPoolConditionTypeHostsReady,
			metav1.ConditionFalse,
			v1alpha1.BareMetalPoolReasonInventoryServiceFailed,
			"Failed to free host",
		)
		return err
	}

	mutex := ctx.Value(contextKey("mutex")).(*sync.Mutex)
	mutex.Lock()
	hostClassToCurrentHostSetSize[inventoryHost.HostClass]--
	mutex.Unlock()

	log.Info("Successfully detached host", "NodeId", inventoryHost.NodeId)

	return nil
}

// runWorkflow runs a workflow by creating a Tekton PipelineRun
func (r *BareMetalPoolReconciler) runWorkflow(ctx context.Context, bareMetalPool *v1alpha1.BareMetalPool, workflowReference profile.WorkflowReference) error {
	log := logf.FromContext(ctx)
	pfName := bareMetalPool.Spec.Profile.Name
	pfParams := bareMetalPool.Spec.Profile.Input

	if workflowReference.String() == "" {
		log.Info("Skipping workflow")
		return nil
	}

	// Create PipelineRun
	pipelineRunName := fmt.Sprintf("%s-%s", workflowReference.String(), rand.String(8))
	pipelineRun := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pipelineRunName,
			Namespace: bareMetalPool.Namespace,
			Labels: map[string]string{
				"osac.openshift.io/cluster-id": string(bareMetalPool.UID),
				"osac.openshift.io/profile":    pfName,
			},
		},
		Spec: tektonv1.PipelineRunSpec{
			PipelineRef: &workflowReference.WorkflowRef,
			Params:      pfParams,
		},
	}

	// Set owner reference so PipelineRun is deleted when BareMetalPool is deleted
	err := controllerutil.SetControllerReference(bareMetalPool, pipelineRun, r.Scheme)
	if err != nil {
		return err
	}

	// Create the PipelineRun
	err = r.Create(ctx, pipelineRun)
	if err != nil {
		return err
	}

	log.Info("Created PipelineRun", "name", pipelineRunName)

	return nil
}
