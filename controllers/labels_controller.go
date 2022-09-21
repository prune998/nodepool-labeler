/*
Copyright 2022 Prune.

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

package controllers

import (
	"context"
	"errors"
	"regexp"

	"google.golang.org/api/compute/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	labelKey   = "k8s-nodepool-labeler"
	labelValue = "true"
)

// LabelsReconciler reconciles a Labels object
type LabelsReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=batch,resources=labels,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=batch,resources=labels/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=batch,resources=labels/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=core,resources=nodes/status,verbs=get;list;watch;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Labels object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.13.0/pkg/reconcile
func (r *LabelsReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var node corev1.Node
	// log.Info("reconciling", "data", req.NamespacedName)
	if err := r.Get(ctx, req.NamespacedName, &node); err != nil {

		// log.Error(err, "unable to fetch node")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// now we have the node data
	// log.Info("node", "data", node)

	// for now, skip all nodes based on name
	if match, _ := regexp.MatchString("gke-wk-qa-us-central--label-test-pool.*", node.Name); !match {
		return ctrl.Result{}, nil
	}

	// log.Info("node", "data", node)

	// if the "cloud.google.com/gke-nodepool" label is not set, re-queue the node
	if _, ok := node.Labels["cloud.google.com/gke-nodepool"]; !ok {
		return ctrl.Result{}, errors.New("node does not have a 'cloud.google.com/gke-nodepool' label set, re-conciling")
	}

	//  stop here if the node is not one of ours
	if node.Labels["cloud.google.com/gke-nodepool"] != "label-test-pool" {
		log.Info("skipping node which is not ours")
		return ctrl.Result{}, nil
	}

	// check if the node has a label telling that we already labeled it
	if !nodeIsLabeled(node.Labels) {
		log.Info("node does not have the right labels, adding them", "data", node.Name)

		// add labels in GCP
		labelsToAdd := make(map[string]string)

		// this is a map of k8s labels -> GCP labels (a-z-_ only)
		labelsList := map[string]string{
			"service":                       "service",
			"app":                           "app",
			"team":                          "team",
			"cloud.google.com/gke-nodepool": "nodepool",
		}

		for labelKey, labelValue := range labelsList {
			if _, ok := node.Labels[labelKey]; ok {
				labelsToAdd[labelKey] = labelValue
			} else {
				labelsToAdd[labelKey] = "unknown"
			}
		}
		err := addCloudNodeLabels(node.Name, node.Labels["topology.kubernetes.io/zone"], labelsToAdd)
		if err != nil {
			log.Error(err, "error adding labels to the CLOUD resource, retrying", "node", node.Name, "labels", labelsToAdd)
			return ctrl.Result{}, errors.New("error adding labels to the CLOUD resource")
		}

		// add labels in K8s
		patch := client.MergeFrom(node.DeepCopy())
		node.Labels[labelKey] = labelValue
		err = r.Patch(ctx, &node, patch)
		if err != nil {
			log.Error(err, "error patching the resource", node.Name)
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *LabelsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		WithEventFilter(NewNodePredicate()).
		Complete(r)
}

func nodeIsLabeled(labels map[string]string) bool {
	for key, val := range labels {
		if key == labelKey && val == labelValue {
			return true
		}
	}
	return false
}

// NewNodePredicate this predicate will only match on Create events as we don't care when nodes are updated or removed
func NewNodePredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			// we are kipping this event so we reconcile all the nodes at start
			// not sure we need it in fact... to be tested
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			// Generation is only updated when something changes in the spec
			// If it's a metadata change, nothing changes, so it's what we want here
			// but generation only exist for CRDs, not for nodes, so we have to deal with all updates :/
			// newGeneration := e.ObjectNew.GetGeneration()
			// oldGeneration := e.ObjectOld.GetGeneration()
			// return oldGeneration == newGeneration
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
	}
}

// AddCloudNodeLabels connect to GCE API and add labels to the instance
func addCloudNodeLabels(instanceName, zone string, labels map[string]string) error {

	// Set static values for now
	projectID := "wk-quality-assurance"

	ctx := context.Background()
	service, err := compute.NewService(ctx)
	if err != nil {
		return err
	}

	// search for the instance
	instance, err := service.Instances.Get(projectID, zone, instanceName).Do()
	if err != nil {
		return err
	}

	// compute labels
	NewLabels := &compute.InstancesSetLabelsRequest{
		LabelFingerprint: instance.LabelFingerprint,
		Labels:           labels,
	}
	_, err = service.Instances.SetLabels(projectID, zone, instanceName, NewLabels).Do()
	if err != nil {
		return err
	}

	return nil
}
