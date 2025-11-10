# hyperfleet-adapter

HyperFleet Adapter - Event-driven adapter services for HyperFleet cluster provisioning. Handles CloudEvents consumption, AdapterConfig CRD integration, precondition evaluation, Kubernetes Job creation/monitoring, and status reporting via API. Supports GCP Pub/Sub, RabbitMQ broker abstraction.

## Introduction

This chart deploys the HyperFleet Adapter component on a Kubernetes cluster using the Helm package manager.

## Prerequisites

- Kubernetes 1.19+
- Helm 3.0+
- Broker ConfigMap (if using broker configuration from ConfigMap)

## Installing the Chart

To install the chart with the release name `hyperfleet-adapter`:

```bash
helm install hyperfleet-adapter ./charts/
```

## Uninstalling the Chart

To uninstall/delete the `hyperfleet-adapter` deployment:

```bash
helm delete hyperfleet-adapter
```

## Configuration

The following table lists the configurable parameters and their default values:

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of replicas | `1` |
| `image.registry` | Image registry | `quay.io/openshift-hyperfleet` |
| `image.repository` | Image repository | `hyperfleet-adapter` |
| `image.tag` | Image tag | `latest` |
| `image.pullPolicy` | Image pull policy | `Always` |
| `service.type` | Service type | `ClusterIP` |
| `service.port` | Service port | `8080` |
| `config.enabled` | Enable ConfigMap for adapter config | `true` |
| `config.configMapName` | Custom ConfigMap name (optional) | `""` |
| `config.adapter` | Adapter configuration (YAML) | `{}` |
| `broker.configMapName` | Broker ConfigMap name (required if using broker ConfigMap) | `""` |
| `broker.configMapKey` | Broker ConfigMap key | `broker.yaml` |
| `resources.limits.cpu` | CPU limit | `500m` |
| `resources.limits.memory` | Memory limit | `512Mi` |
| `resources.requests.cpu` | CPU request | `100m` |
| `resources.requests.memory` | Memory request | `128Mi` |

## Configuration Files

### Adapter Configuration

The adapter configuration can be provided in multiple ways:

1. **Via ConfigMap with environment variables** (recommended): Set `config.enabled: true` and provide:
   - `config.adapterType`: Adapter type identifier (e.g., "example", "gcp", "aws")
   - `config.env`: Environment variables matching the structure in `adapther-configmap-template.yaml`

2. **Via ConfigMap with YAML file**: Set `config.enabled: true` and provide `config.adapterYaml` with YAML content

3. **Packaged in image**: Set `config.enabled: false` to use the default config packaged in the image

The application checks for configuration in this order:
1. `CONFIG_FILE` environment variable (if set)
2. `/etc/adapter/config/adapter.yaml` (ConfigMap mount point, if using YAML format)
3. `/app/configs/adapter.yaml` (packaged default)

### Broker Configuration

Broker configuration must be provided via a Kubernetes ConfigMap that exists in the cluster. The ConfigMap structure should match `adapther-configmap-template.yaml` with environment variables like:
- `BROKER_TYPE`: Broker type (pubsub, awsSqs, rabbitmq, kafka)
- `BROKER_HOST`, `BROKER_PORT`: Connection details (for RabbitMQ)
- `BROKER_QUEUE_NAME`, `BROKER_EXCHANGE`: Queue/exchange names
- `BROKER_MAX_CONCURRENCY`: Concurrency settings
- And other broker-specific settings

**Recommended approach** (matching template): Mount broker ConfigMap as environment variables:

```yaml
broker:
  configMapName: broker-config
  mountAsEnv: true  # Mount all keys as environment variables
  envKeys: []  # Empty = mount all keys, or specify list like [BROKER_TYPE, BROKER_HOST]
```

**Alternative approach**: Mount broker ConfigMap as file:

```yaml
broker:
  configMapName: broker-config
  mountAsEnv: false
  configMapKey: broker.yaml  # Key name in ConfigMap
```

The broker config will be available as:
- Environment variables (if `mountAsEnv: true`) - matches template structure
- File at `/etc/adapter/config/broker.yaml` (if `mountAsEnv: false`)

## Examples

### Basic Installation

```bash
helm install hyperfleet-adapter ./charts/
```

### With Custom Adapter Configuration (Environment Variables)

```bash
helm install hyperfleet-adapter ./charts/ \
  --set config.adapterType=example \
  --set config.env.BROKER_TYPE=rabbitmq \
  --set config.env.BROKER_HOST=rabbitmq.hyperfleet-system.svc.cluster.local \
  --set config.env.BROKER_PORT=5672 \
  --set config.env.BROKER_QUEUE_NAME=hyperfleet-cluster-events
```

### With Broker ConfigMap (Environment Variables - Recommended)

```bash
helm install hyperfleet-adapter ./charts/ \
  --set broker.configMapName=broker-config \
  --set broker.mountAsEnv=true
```

This will mount all keys from the broker ConfigMap as environment variables, matching the `adapther-configmap-template.yaml` structure.

### With Broker ConfigMap (File Mount)

```bash
helm install hyperfleet-adapter ./charts/ \
  --set broker.configMapName=broker-config \
  --set broker.mountAsEnv=false \
  --set broker.configMapKey=broker.yaml
```

### Using Custom Image

```bash
helm install hyperfleet-adapter ./charts/ \
  --set image.registry=my-registry.io \
  --set image.repository=my-adapter \
  --set image.tag=v1.0.0
```

### With Environment Variables

```bash
helm install hyperfleet-adapter ./charts/ \
  --set env[0].name=CONFIG_FILE \
  --set env[0].value=/custom/path/config.yaml
```

## Notes

- The chart uses a non-root user (UID 65532) for security
- Health checks are configured at `/healthz` and `/readyz` endpoints
- Both adapter and broker ConfigMaps mount to the same directory (`/etc/adapter/config`) with different file names
- The default image uses `distroless` base image for minimal attack surface

