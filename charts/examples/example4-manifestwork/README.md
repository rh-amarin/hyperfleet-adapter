# Adapter example to deploy resources via Maestro ManifestWork

This `values.yaml` deploys an `adapterconfig.yaml` that creates resources on remote clusters using Maestro's ManifestWork transport instead of applying directly to the local cluster.

## Overview

This example showcases:

- **Maestro transport**: Routes resource operations through Maestro server to remote clusters
- **ManifestWork abstraction**: Same Kubernetes manifest format works for both direct and Maestro transport
- **mTLS authentication**: Secure communication with Maestro using client certificates
- **Remote status tracking**: Monitors ManifestWork conditions to track resource application on remote clusters
- **Consumer targeting**: Dynamically resolves target cluster from HyperFleet API response

## Files

| File | Description |
|------|-------------|
| `values.yaml` | Helm values with Maestro client configuration and certificate mounts |
| `adapterconfig.yaml` | Adapter configuration with Maestro transport settings and ManifestWork resources |
| `configmap.yaml` | ConfigMap manifest template deployed to remote clusters |

## Architecture

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│   HyperFleet    │     │    Maestro      │     │  Remote Cluster │
│    Adapter      │────▶│    Server       │────▶│  (via Agent)    │
└─────────────────┘     └─────────────────┘     └─────────────────┘
        │                       │                       │
        │  ManifestWork         │  CloudEvents/gRPC     │  Apply
        │  Create/Update        │  Transport            │  Resources
        │                       │                       │
        └───────────────────────┴───────────────────────┘
```

## Key Differences from Direct Examples

| Aspect | Direct Transport (Examples 1-3) | Maestro Transport (Example 4) |
|--------|--------------------------------|-------------------------------|
| Target | Local cluster | Remote clusters via Maestro |
| Authentication | ServiceAccount RBAC | mTLS certificates |
| Resource wrapper | None | ManifestWork |
| Status source | Kubernetes API | ManifestWork conditions |
| Use case | Local operations | Multi-cluster orchestration |

## Configuration

### Maestro Client Settings

The `adapterconfig.yaml` includes Maestro client configuration:

```yaml
maestro:
  enabled: true
  endpoint: "{{ .maestroApiUrl }}"
  tls:
    enabled: true
    caFile: "/etc/maestro/certs/ca.crt"
    certFile: "/etc/maestro/certs/hyperfleet-client.crt"
    keyFile: "/etc/maestro/certs/hyperfleet-client.key"
  timeout: 30s
  retryAttempts: 3
  retryBackoff: exponential
```

### Resource Transport Mode

Resources specify `transport: "maestro"` and a `consumerName` (target cluster):

```yaml
resources:
  - name: "configmap"
    transport: "maestro"
    consumerName: "{{ .consumerName }}"
    manifest:
      ref: "/etc/adapter/configmap.yaml"
```

### Certificate Secret

Create a Kubernetes Secret with Maestro client certificates:

```bash
kubectl create secret generic maestro-client-certs \
  --from-file=ca.crt=/path/to/ca.crt \
  --from-file=hyperfleet-client.crt=/path/to/client.crt \
  --from-file=hyperfleet-client.key=/path/to/client.key
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

## Prerequisites

1. **Maestro Server**: A running Maestro server accessible from the adapter
2. **Maestro Agent**: Deployed on target clusters and registered with the server
3. **Client Certificates**: Valid mTLS certificates for adapter-to-Maestro communication
4. **Consumer Registration**: Target clusters registered as "consumers" in Maestro

## Usage

```bash
# Create the certificate secret first
kubectl create secret generic maestro-client-certs \
  --from-file=ca.crt=./certs/ca.crt \
  --from-file=hyperfleet-client.crt=./certs/client.crt \
  --from-file=hyperfleet-client.key=./certs/client.key

# Install the adapter
helm install example4-manifestwork ./charts -f charts/examples/example4-manifestwork/values.yaml
```

## How It Works

1. The adapter receives a CloudEvent with a cluster ID
2. **Preconditions**: Fetches cluster info from HyperFleet API, including the `maestro_consumer_name` (target cluster identifier)
3. **Validation**: Checks that the cluster's Ready condition is "False" before proceeding
4. **ManifestWork creation**: Creates a ManifestWork containing the ConfigMap via Maestro's gRPC API
5. **Remote application**: Maestro server forwards the ManifestWork to the target cluster's agent
6. **Agent execution**: The Maestro agent applies the ConfigMap on the remote cluster
7. **Status propagation**: Agent reports status back through Maestro to the adapter
8. **Post-processing**: Adapter builds status payload from ManifestWork conditions
9. **Status reporting**: Reports the status back to the HyperFleet API

## ManifestWork Status Conditions

The adapter monitors these ManifestWork conditions:

| Condition | Meaning |
|-----------|---------|
| `Applied` | Resource successfully applied on remote cluster |
| `Available` | Resource is available and running |
| `Degraded` | Resource exists but has issues |

## Troubleshooting

### Certificate Issues

```bash
# Verify certificates are mounted correctly
kubectl exec -it <adapter-pod> -- ls -la /etc/maestro/certs/

# Check certificate validity
kubectl exec -it <adapter-pod> -- openssl x509 -in /etc/maestro/certs/hyperfleet-client.crt -text -noout
```

### Maestro Connectivity

```bash
# Test Maestro endpoint from adapter pod
kubectl exec -it <adapter-pod> -- curl -v --cacert /etc/maestro/certs/ca.crt \
  --cert /etc/maestro/certs/hyperfleet-client.crt \
  --key /etc/maestro/certs/hyperfleet-client.key \
  https://maestro-server.maestro-system.svc:8000/api/maestro/v1/consumers
```

### Consumer Not Found

Ensure the target cluster is registered as a consumer in Maestro:

```bash
# List registered consumers
curl -s https://maestro-server/api/maestro/v1/consumers | jq '.items[].name'
```
