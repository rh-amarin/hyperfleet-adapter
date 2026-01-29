# Adapter example to create a job in regional cluster

This `values.yaml` deploys an `adapterconfig.yaml` that creates a job in the same cluster and namespace that CLM is running.

## Overview

This example showcases:

- **Preconditions**: Fetches cluster status from the Hyperfleet API before proceeding
- **Resource creation**: Deploys a Kubernetes Job using templated manifests
- **Resource discovery**: Finds existing jobs using label selectors
- **Status reporting**: Builds a status payload with CEL expressions and reports back to the Hyperfleet API
- **Sidecar pattern**: Uses a status-reporter sidecar to monitor job completion and update job conditions

## Files

Note that the `job.yaml` used by the adapter is referenced as an external file usinig `ref` in the `AdapterConfig` configuration.

This file doesn't get variables resolved at Helm time but at runtime, therefore any value has to come from the adapter, e.g. using the `params` phase.

| File | Description |
|------|-------------|
| `values.yaml` | Helm values that configure the adapter, broker, and environment variables |
| `adapterconfig.yaml` | Adapter configuration defining params, preconditions, resources, and post-processing |
| `job.yaml` | Kubernetes Job manifest template with simulation logic |

## Configuration

### Environment Variables

The `SIMULATE_RESULT` environment variable controls the job's behavior:

| Value | Description |
|-------|-------------|
| `success` | Writes a successful validation result and exits 0 |
| `failure` | Writes a failure result (missing permissions) and exits 1 |
| `hang` | Sleeps indefinitely (useful for testing timeouts) |
| `crash` | Exits without writing results |
| `invalid-json` | Writes invalid JSON to the results file |
| `missing-status` | Writes JSON missing the required status field |

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
helm install example2-job ./charts -f charts/examples/example2-job/values.yaml
```

## How It Works

1. The adapter receives a CloudEvent with a cluster ID
2. **Preconditions**: Fetches cluster status from the Hyperfleet API and captures the cluster name, generation, and ready condition
3. **Validation**: Checks that the cluster's Ready condition is "False" before proceeding
4. **Resource creation**: Creates a Job in the target namespace using the templated manifest
5. **Job execution**: The job container simulates GCP validation based on `SIMULATE_RESULT`
6. **Status monitoring**: The status-reporter sidecar monitors the job container and updates the Job's conditions
7. **Post-processing**: The adapter builds a status payload using CEL expressions and reports it to the Hyperfleet API
