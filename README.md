# nodepool-labeler
K8s operator to label Cloud Instances based on NodePool labels

## Architecture

This operator is pretty simple.
It monitors the `creation` events for `Nodes`. When a new node is added, it wait (reconcile loop) for the `nodePool` label to be added.
Then, it connect to GCE API and copy a list of Kubernetes labels from the K8s Node to the GCE Instance.
Finally, it adds a label to the K8s Node so we can track that this node was correctly labeled in GCE.


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


# Build Docker image 
make docker-build docker-push IMG=prune/nodepool-labeler:dev

# build manifests
make manifests
kustomize build config/default|less

# install
make deploy
```