# Adapter example to create a deployment in regional cluster

This `values.yaml` deploys an `adapterconfig.yaml` that creates a Deployment in the same cluster and namespace that CLM is running.

## Overview

This example showcases:

- **Preconditions**: Fetches cluster status from the Hyperfleet API before proceeding
- **Resource creation**: Deploys a Kubernetes Deployment using templated manifests
- **Resource discovery**: Finds existing deployments using label selectors
- **Status reporting**: Builds a status payload with CEL expressions and reports back to the Hyperfleet API
- **Long-running workloads**: Unlike Jobs, Deployments maintain running pods

## Files

| File | Description |
|------|-------------|
| `values.yaml` | Helm values that configure the adapter, broker, and environment variables |
| `adapterconfig.yaml` | Adapter configuration defining params, preconditions, resources, and post-processing |
| `deployment.yaml` | Kubernetes Deployment manifest template |

## Configuration

### Environment Variables

The `SIMULATE_RESULT` environment variable is available but primarily used for testing purposes:

| Value | Description |
|-------|-------------|
| `success` | Default behavior |
| `failure` | Simulates failure scenario |

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
helm install example3-deployment ./charts -f charts/examples/example3-deployment/values.yaml
```

## How It Works

1. The adapter receives a CloudEvent with a cluster ID
2. **Preconditions**: Fetches cluster status from the Hyperfleet API and captures the cluster name, generation, and ready condition
3. **Validation**: Checks that the cluster's Ready condition is "False" before proceeding
4. **Resource creation**: Creates a Deployment named `test-deployment-{clusterId}` in the target namespace
5. **Status monitoring**: The adapter monitors the Deployment's Available condition
6. **Post-processing**: Builds a status payload using CEL expressions
7. **Status reporting**: Reports the status back to the Hyperfleet API

## Comparison with Example 2 (Job)

| Aspect | Example 2 (Job) | Example 3 (Deployment) |
|--------|-----------------|------------------------|
| Resource type | Kubernetes Job | Kubernetes Deployment |
| Lifecycle | Runs to completion | Long-running |
| Status monitoring | Sidecar container | Deployment conditions |
| Use case | One-time validation tasks | Persistent workloads |
