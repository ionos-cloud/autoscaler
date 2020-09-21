# Deployment for cluster autosacler deployment in IEC

The cluster autoscaler for IEC

## Helm chart dependencies

* ionos-secret

## Deployment

By default the chart installs Deployment, ServiceAccount and Role, ClusterRole and RoleBindings.
This is recommended when working with a single Kubernetes cluster.

For a **controlplane/nodepool** setup, you'll need to adjust the `installType`.

Set it to `controlplane` to install the resources inside the controlplane, to
`target` to install resources inside the target cluster.

### Requirements

The `Deployment`, which is running in the Controlplane cluster, requires the kubeconfig to be present as a secret named `kubernetes-admin`. In case the deployment is also runninfg in the `target` cluster, it can use in-cluster kubeconfig.


### Deployed components

The helm chart deploys the following components:

* serviceaccount
* role
* clusterrole:
* rolebinding:
* clusterrolebinding:
* deployment:

### Example

Install controlplane components:

```console
helm install cp-123-cluster-autoscaler chartmuseum/cluster-autoscaler \
    --namespace cp-123 \
    --kubeconfig=controlplane.kubeconfig \
    --set installType=controlplane
    --values overrides.yml
```

Install target components: (This is where the customer has full control)

```console
helm install cluster-autoscaler chartmuseum/cluster-autoscaler \
    --namespace kube-system \
    --kubeconfig=customer123.kubeconfig \
    ---set installType=target \
    --values overrides.yml
```