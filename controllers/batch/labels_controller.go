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
	"strconv"

	"github.com/mitchellh/hashstructure/v2"
	"github.com/prometheus/client_golang/prometheus"
	configv2 "github.com/prune998/nodepool-labeler/apis/config/v2"
	"google.golang.org/api/compute/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
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
	Config configv2.ProjectConfig
}

var (
	totalNodes = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "node_total",
			Help: "Number of node proccessed",
		},
	)
	labeledNodes = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "node_labeled",
			Help: "Number of node that had labels added",
		},
	)
	totalFailures = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "label_failure_total",
			Help: "Number of falures while labeling nodes",
		},
	)
)

func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(totalNodes, labeledNodes, totalFailures)
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

	// node will hold the current node that triggered the reconcile
	var node corev1.Node
	if err := r.Get(ctx, req.NamespacedName, &node); err != nil {

		// log.Error(err, "unable to fetch node")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// for now, skip all nodes based on name
	// this is just for the demo, then we'll remove this to watch all nodes
	if match, _ := regexp.MatchString("gke-wk-qa-us-central--label-test-pool.*", node.Name); !match {
		return ctrl.Result{}, nil
	}

	// inc total count of processed nodes
	totalNodes.Inc()

	// if the "cloud.google.com/gke-nodepool" label is not set, re-queue the node
	// Nodes seems to join the cluster before they are labeled (by the node-autoscaler ?)
	if _, ok := node.Labels["cloud.google.com/gke-nodepool"]; !ok {
		return ctrl.Result{}, errors.New("node does not have a 'cloud.google.com/gke-nodepool' label set, re-conciling")
	}

	//  stop here if the node is not one of ours
	// this is goging to be removed when the operator is ready to handle all the nodes of the cluster = prod ready
	if node.Labels["cloud.google.com/gke-nodepool"] != "label-test-pool" {
		log.Info("skipping node which is not ours")
		return ctrl.Result{}, nil
	}

	// labelsToAdd is the list of labels to add to the GCP instance
	// it is a map of k8s labels -> GCP labels (a-z-_ only)
	// each label to copy is added to this list
	labelsToAdd := make(map[string]string)
	for labelKey, labelValue := range r.Config.Labels {
		if val, ok := node.Labels[labelKey]; ok {
			if validateLabelFormat(val) {
				labelsToAdd[labelValue] = val
			}
		} else {
			labelsToAdd[labelValue] = "unknown"
		}
	}

	// if node is labeled, compute checksum to see if we need to re-apply

	hash, err := hashstructure.Hash(labelsToAdd, hashstructure.FormatV2, nil)
	if err != nil {
		log.Error(err, "unable to compute the Labels hash")
	}
	stringHash := strconv.FormatUint(hash, 10)

	// if hash is the same, skip re-labelling
	if nodeIsLabeled(node.Labels) && stringHash == node.Labels[labelKey] {
		log.Info("node is already labeled and labels are the same, skipping", "hash", hash)
		return ctrl.Result{}, nil
	}

	// check if the node has a label telling that we already labeled it
	log.Info("node was never processed for labels or labels differs, working on it...")

	// label the GCP Instance
	err = addCloudNodeLabels(node.Name, r.Config.ProjectID, node.Labels["topology.kubernetes.io/zone"], labelsToAdd)
	if err != nil {
		log.Error(err, "error adding labels to the CLOUD resource, retrying", "node", node.Name, "labels", labelsToAdd)
		totalFailures.Inc()
		return ctrl.Result{}, errors.New("error adding labels to the CLOUD resource")
	}

	// add the status labels in K8s node
	patch := client.MergeFrom(node.DeepCopy())
	node.Labels[labelKey] = stringHash
	err = r.Patch(ctx, &node, patch)
	if err != nil {
		log.Error(err, "error patching K8s Node labels, retrying", "node", node.Name)
		totalFailures.Inc()
		return ctrl.Result{}, errors.New("error adding labels to the K8s resource")
	}
	labeledNodes.Inc()

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
		if key == labelKey && val != "" {
			return true
		}
	}
	return false
}

// NewNodePredicate this predicate will only match on Create events as we don't care when nodes are updated or removed
func NewNodePredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			// reconcile all the nodes at start
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			// Generation is only updated when something changes in the spec
			// If it's a metadata change, nothing changes, so it's what we want here
			// but generation only exist for CRDs, not for nodes, so we have to deal with all updates :/
			// newGeneration := e.ObjectNew.GetGeneration()
			// oldGeneration := e.ObjectOld.GetGeneration()
			// return oldGeneration == newGeneration

			// here we also reconcile on any node change
			// and expect the logic of reconcile to not hammer the GCE API
			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
	}
}

// AddCloudNodeLabels connect to GCE API and add labels to the instance
func addCloudNodeLabels(instanceName, project, zone string, labels map[string]string) error {

	ctx := context.Background()
	service, err := compute.NewService(ctx)
	if err != nil {
		return err
	}

	// search for the instance
	instance, err := service.Instances.Get(project, zone, instanceName).Do()
	if err != nil {
		return err
	}

	// compute labels
	// merge original labels with new ones - old instance labels always win just in case
	for k, v := range instance.Labels {
		labels[k] = v
	}

	// Ensure we're not over the 64 max labels
	if len(labels) > 64 {
		// this is a silent return BUT we need to return a specific error that will be logged
		// we don't want to trigger a reconsile (else we'll loop reconcile forever)
		return nil
	}

	NewLabels := &compute.InstancesSetLabelsRequest{
		LabelFingerprint: instance.LabelFingerprint,
		Labels:           labels,
	}

	_, err = service.Instances.SetLabels(project, zone, instanceName, NewLabels).Do()
	if err != nil {
		return err
	}

	return nil
}

func validateLabelFormat(value string) bool {
	// Validate the labels
	// maximum of 64 labels
	// Keys have a minimum length of 1 character and a maximum length of 63
	// Value have maximum length of 63 characters.
	// Keys and values can contain only lowercase letters, numeric characters, underscores, and dashes
	// Keys must start with a lowercase letter or international character.
	r, _ := regexp.Compile("(^[a-z][a-z0-9_-]{1,62}$)")
	return r.MatchString(value)
}
