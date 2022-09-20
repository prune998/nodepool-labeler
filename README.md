# nodepool-labeler
K8s operator to label Cloud Instances based on NodePool labels


## Creation of the Controler

```bash
kubebuilder init --domain nodepoolLabeler.lecentre.net --repo github.com/prune998/nodepool-labeler --plugins=go/v4-alpha --component-config --owner "Prune"

```