# Exoscale Block Storage CSI Driver

Exoscale Block Storage Container Storage Interface Driver.

## Prerequisite

* Make sure you have the [CCM](https://github.com/exoscale/exoscale-cloud-controller-manager) deployed and running in your cluster.

* Create secret with [exoscale-secret.sh](./deployment/exoscale-secret.sh).
    ```Bash
    export EXOSCALE_API_KEY=EXOxxxxx
    export EXOSCALE_API_SECRET=xxxxx
    deployment/exoscale-secret.sh
    ```

## Deployment

```
kubectl apply -f deployment/exoscale-csi.yaml
```

## Using it

You should see your `exoscale-csi-controller` and `exoscale-csi-node` pods running in the `kube-system` namespace.
```Bash
kubectl -n kube-system get pods
...
exoscale-cloud-controller-manager-c8dc6b785-fgcbq   1/1     Running   0          25d
exoscale-csi-controller-5df549794-7ptgw             7/7     Running   0          5d1h
exoscale-csi-node-nkbzw                             3/3     Running   0          5d1h
exoscale-csi-node-v8skv                             3/3     Running   0          5d1h
...
```

Since one `exoscale-csi-node` is deployed per node, you should see one pod per node.

### Example

You can deploy a test App example to try it out.
```Bash
# You should see an Exoscale Block Storage Volume created in your Organization.
kubectl apply -f doc/examples/namespace.yaml
kubectl apply -f doc/examples/pvc.yaml
# You should see your example App pod running and the Volume attached to one of your nodes.
kubectl apply -f doc/examples/deployment.yaml
```

## Building from source

You can build a binary
```Bash
make build
```

Or you can build a container image
```Bash
make docker
```

## Versioning and compatibility policy

The Exoscale CSI adheres to [Semantic Versioning](https://semver.org/).

Minor versions are aligned with the minor version number of the latest Kubernetes release.
The aligned version `0.y` is compatible with Kubernetes version `1.y` and unless specified otherwise in the compatibility matrix below, CSI `0.y` is also compatible with the two most recent Kubernetes minor versions before `1.y`. Even older versions may work but without guarantee.
For example, CSI `0.99` would be compatible with Kubernetes versions `1.99`, `1.98` and `1.97`.

### Compatibility Matrix

| CSI version | supported Kubernetes versions |
|-------------|-------------------------------|
| 0.29        | 1.29, 1.28, 1.27              |

## Project status

We consider the CSI to be in Beta phase. Although it reliably performs it's essential functions, missing features and bugs have to be expected.
