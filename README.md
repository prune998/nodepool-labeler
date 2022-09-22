# nodepool-labeler
K8s operator to label Cloud Instances based on NodePool labels

## Why ?

When using GKE. the Google managed Kubernetes cluster, each node is in a fact a GCE (Google Compute Engine) Instance. 
At the moment, the Instances contains a label pointing to the Cluster they are part of, but not the Nodepool. (well, you can still get the info some other ways, but it's not obvious).

This Operator is here to copy K8s Labels into the GCP Instance. This can be used for Cost exposure or just to link Instances to K8s cluster/nodepool.

## Architecture

This operator monitors the `create` events for `Nodes`. When a new node is added, it wait (reconcile loop) for the `cloud.google.com/gke-nodepool` label to be present on the node.
When the label is present it connect to GCE API and copy a list of labels *from* the Kubernetes Node *to* the Google Cloud Instance.
When the labels are added to GCE instance, the Operator adds a label to the K8s Node so we can track which node was already processed from K8s.

Default Labels copied:

```yaml
K8s Label                      GCE Label
service:                       service
app:                           app
team:                          team
owner:                         owner
cloud.google.com/gke-nodepool: nodepool
```

Note that if a Label already exist on the GCE Instance, **it will not be updated**. This can lead to out-of-sync labels between the two, but it is a security
to prevent k8s users to temper the Instance workflow.
Because of this behaviour, **updating** a K8s label value **will not be propagated** into the Instance labels. 

Again, this operator is here to sync the K8s labels into the GCE Instance **when it is first created** and is not a real two-way sync. 

## Creation of the Controler

```bash
# Create the Operator
kubebuilder init --domain nodepoollabeler.lecentre.net --repo github.com/prune998/nodepool-labeler --plugins=go/v4-alpha --component-config --owner "Prune"
kubebuilder edit --multigroup=true

# Create the API WITHOUT A CRD (as we are reconciling native resources)
# https://github.com/kubernetes-sigs/kubebuilder/issues/2398#issuecomment-952709317
kubebuilder create api --controller=true --resource=false   --group batch --version v1 --kind Labels --namespaced=false
mkdir api

# Create a config API to add more stuff in the config file
kubebuilder create api --group config --version v2 --kind ProjectConfig --resource --controller=false --make=false

# Build local binary
make

# Build Docker image 
make docker-build docker-push IMG=prune/nodepool-labeler:dev

# build manifests
make manifests
kustomize build config/default|less

# install
make deploy
```

# Deployment

You can use `kustomize build config/default` to generate the Yaml, or `make deploy` from the root, which is equivalent to `kustomize` + `k apply`.

## Setup

The Operator setup is defined in a `ConfigMap` named `nodepool-labeler-manager-config`. 
You can update the values there or in the local file `config/manager/controller_manager_config.yaml` that is used to generate the yaml.

At the moment only 2 values are needed:

- `projectID`: it's the `name` of the GCP Project
- `labels`: it's the list of labels to copy over, formated as `name of the K8s label` -> `name of the new GCE label to create on the instance`