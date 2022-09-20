# nodepool-labeler
K8s operator to label Cloud Instances based on NodePool labels


## Creation of the Controler

```bash
# Create the Operator
kubebuilder init --domain nodepoollabeler.lecentre.net --repo github.com/prune998/nodepool-labeler --plugins=go/v4-alpha --component-config --owner "Prune"

# Create the API WITHOUT A CRD (as we are reconciling native resources)
# https://github.com/kubernetes-sigs/kubebuilder/issues/2398#issuecomment-952709317
kubebuilder create api --controller=true --resource=false   --group batch --version v1 --kind Labels --namespaced=false

# Build Docker image 
make docker-build docker-push IMG=prune/nodepool-labeler:dev

# build manifests
make manifests
kustomize build config/default|less

# install
make deploy
```