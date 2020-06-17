# Cluster Autoscaler for IONOS Enterprise Cloud Managed Kubernetes

The cluster autoscaler for the IONOS Enterprise Cloud(IEC) scales worker nodes within IEC Managed Kubernetes cluster
node pools. This can be dynamically enabled/disabled by defining autscaler min/max for an existing IEC node pool.

## Configuration

The cluster autoscaler runs for clusters with at least one nodepool with defined autoscaling mix/max.

## Development

In the autoscaler repository [autoscaler repository](https://github.com/kubernetes/autoscaler)

1.) Build the `cluster-autoscaler` binary:


```
make build-in-docker
```

2.) Build the docker image:

```
TAG=TAG_TO_USE_FOR_IMAGE REGISTRY=DOCKER_REGISTRY_TO_USE make make-image
```


3.) Push the docker image to a docker registry

```
TAG=TAG_OF_IMAGE_TO_PUSH REGISTRY=DOCKER_REGISTRY_OF_IMAGE make push-image
```

## Deployment

### Customer side deployment

Deploy cluster autoscaler to customer cluster on worker nodes. Requires IONOS Cloud API credentials provided within a 
secret in customer cluster.

#### IONOS credentials

Cluster autoscaler needs a IONOC CloudAPI jwt token to identify himself with the IONOS Cloud API,
cf. examples/cluster-autoscaler-secret.yaml.

IONOS CloudAPI jwt token can be generates using IONOS auth api.
```shell
curl -u $IONOS_USER -p $IONOS_PASS https://api.ionos.com/auth/v1/tokens/generate
```

#### Deploy

```shell
kubectl apply -f example/cluster-autoscaler-autodiscover.yaml
```

### CPC Deployment

In CPC Deployment, the cluster-autoscaler retrieves the token from the secret in the cluster namespace.
