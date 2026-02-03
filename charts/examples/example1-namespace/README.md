# Adapter example to create a namespace in regional cluster

This `values.yaml` deploys an `adapterconfig.yaml` that creates a new namespace from the id of the resource in the CloudEvent.

## Overview

This example showcases:

- **Inline manifests**: Defines the Kubernetes Namespace resource directly in the adapterconfig (no external file)
- **Preconditions**: Fetches cluster status from the Hyperfleet API before proceeding
- **Resource discovery**: Finds existing namespaces using label selectors
- **Status reporting**: Builds a status payload with CEL expressions and reports back to the Hyperfleet API
- **RBAC configuration**: Demonstrates configuring additional RBAC resources in helm values

## Files

| File | Description |
|------|-------------|
| `values.yaml` | Helm values that configure the adapter, broker, and RBAC permissions |
| `adapterconfig.yaml` | Adapter configuration with inline namespace manifest, params, preconditions, and post-processing |

## Key Differences from Other Examples

This example uses an **inline manifest** instead of referencing an external file:

```yaml
resources:
  - name: "clusterNamespace"
    manifest:
      apiVersion: v1
      kind: Namespace
      metadata:
        name: "cluster-{{ .clusterId }}"
```

This approach is simpler for basic resources that don't require complex templating.

## Configuration

### RBAC Resources

The `values.yaml` configures additional RBAC permissions needed for namespace management:

```yaml
rbac:
  resources:
    - namespaces
    - serviceaccounts
    - configmaps
    - deployments
    - roles
    - rolebindings
```

### Broker Configuration

Update the `broker.googlepubsub` section in `values.yaml` with your GCP Pub/Sub settings:

```yaml
broker:
  googlepubsub:
    projectId: CHANGE_ME
    subscriptionId: CHANGE_ME
    topic: CHANGE_ME
    deadLetterTopic: CHANGE_ME
```

## Usage

```bash
helm install example1-namespace ./charts -f charts/examples/example1-namespace/values.yaml
```

## How It Works

1. The adapter receives a CloudEvent with a cluster ID and generation
2. **Preconditions**: Fetches cluster status from the Hyperfleet API and captures the cluster name, generation, and ready condition
3. **Validation**: Checks that the cluster's Ready condition is "False" before proceeding
4. **Resource creation**: Creates a Namespace named `cluster-{clusterId}` with appropriate labels and annotations
5. **Post-processing**: Builds a status payload checking if the namespace is Active
6. **Status reporting**: Reports the status back to the Hyperfleet API
