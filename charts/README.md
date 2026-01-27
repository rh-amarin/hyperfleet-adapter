# HyperFleet Adapter Helm Chart

HyperFleet Adapter - Event-driven adapter for HyperFleet cluster provisioning. Consumes CloudEvents from message brokers (GCP Pub/Sub, RabbitMQ), processes AdapterConfig, manages Kubernetes resources, and reports status via API.

## Prerequisites

- Kubernetes 1.19+
- Helm 3.0+
- GCP Workload Identity (for Pub/Sub access)

## Installing the Chart

```bash
helm install hyperfleet-adapter ./charts/
```

## Uninstalling the Chart

```bash
helm delete hyperfleet-adapter
```

## Configuration

### Key Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of replicas | `1` |
| `image.registry` | Image registry | `quay.io/openshift-hyperfleet` |
| `image.repository` | Image repository | `hyperfleet-adapter` |
| `image.tag` | Image tag | `latest` |
| `image.pullPolicy` | Image pull policy | `Always` |
| `command` | Container command | `["/app/adapter"]` |
| `args` | Container arguments | `["serve"]` |

### ServiceAccount & Workload Identity

| Parameter | Description | Default |
|-----------|-------------|---------|
| `serviceAccount.create` | Create ServiceAccount | `true` |
| `serviceAccount.name` | ServiceAccount name (auto-generated if empty) | `""` |
| `serviceAccount.annotations` | ServiceAccount annotations (for Workload Identity) | `{}` |

### Adapter Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `config.enabled` | Enable adapter ConfigMap | `true` |
| `config.configMapName` | Custom ConfigMap name | `""` |
| `config.adapterType` | Adapter type identifier | `""` |
| `config.env` | Environment variables for ConfigMap | `{}` |
| `config.adapterYaml` | Adapter YAML config content | `""` |

When `config.adapterYaml` is set:

- Creates `adapter.yaml` key in ConfigMap
- Mounts at `/etc/adapter/adapter.yaml`
- Sets `ADAPTER_CONFIG_PATH=/etc/adapter/adapter.yaml`

### Broker Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `broker.create` | Create broker ConfigMap | `false` |
| `broker.configMapName` | Broker ConfigMap name | `""` |
| `broker.type` | Broker type (googlepubsub, rabbitmq) | `""` |
| `broker.subscriptionId` | Subscription ID (BROKER_SUBSCRIPTION_ID) | `""` |
| `broker.topic` | Topic name (BROKER_TOPIC) | `""` |
| `broker.env` | Additional broker env vars | `{}` |
| `broker.yaml` | Broker YAML config content | `""` |
| `broker.mountAsEnv` | Mount ConfigMap as env vars via envFrom | `true` |
| `broker.envKeys` | Specific keys to mount (empty = all via envFrom) | `[]` |

When `broker.yaml` is set:

- Creates `broker.yaml` key in ConfigMap
- Mounts at `/etc/broker/broker.yaml`
- Sets `BROKER_CONFIG_FILE=/etc/broker/broker.yaml`

### HyperFleet API Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `hyperfleetApi.baseUrl` | HyperFleet API base URL (HYPERFLEET_API_BASE_URL) | `""` |
| `hyperfleetApi.version` | API version (HYPERFLEET_API_VERSION) | `"v1"` |

### Resources

| Parameter | Description | Default |
|-----------|-------------|---------|
| `resources.limits.cpu` | CPU limit | `500m` |
| `resources.limits.memory` | Memory limit | `512Mi` |
| `resources.requests.cpu` | CPU request | `100m` |
| `resources.requests.memory` | Memory request | `128Mi` |

### Pod Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `podAnnotations` | Pod annotations | `{}` |
| `podLabels` | Additional pod labels | `{}` |
| `nodeSelector` | Node selector | `{}` |
| `tolerations` | Tolerations | `[]` |
| `affinity` | Affinity rules | `{}` |
| `topologySpreadConstraints` | Topology spread constraints | `[]` |
| `terminationGracePeriodSeconds` | Termination grace period | `30` |

### Health Probes (Disabled by Default)

The adapter is a message consumer and doesn't expose HTTP endpoints by default.

| Parameter | Description | Default |
|-----------|-------------|---------|
| `livenessProbe.enabled` | Enable liveness probe | `false` |
| `readinessProbe.enabled` | Enable readiness probe | `false` |
| `startupProbe.enabled` | Enable startup probe | `false` |

### Pod Disruption Budget

| Parameter | Description | Default |
|-----------|-------------|---------|
| `podDisruptionBudget.enabled` | Enable PDB | `false` |
| `podDisruptionBudget.minAvailable` | Minimum available pods | - |
| `podDisruptionBudget.maxUnavailable` | Maximum unavailable pods | - |

## Examples

### Basic Installation with Broker ConfigMap

```bash
helm install hyperfleet-adapter ./charts/ \
  --set broker.create=true \
  --set broker.type=googlepubsub \
  --set broker.subscriptionId=my-subscription \
  --set broker.topic=my-topic
```

### With HyperFleet API Configuration

```bash
helm install hyperfleet-adapter ./charts/ \
  --set hyperfleetApi.baseUrl=https://api.hyperfleet.example.com \
  --set broker.create=true \
  --set broker.subscriptionId=my-subscription \
  --set broker.topic=my-topic
```

### With Full Configuration (Adapter + Broker)

```bash
helm install hyperfleet-adapter ./charts/ \
  --set broker.create=true \
  --set broker.type=googlepubsub \
  --set broker.subscriptionId=my-subscription \
  --set broker.topic=my-topic \
  --set-file config.adapterYaml=./my-adapter-config.yaml \
  --set-file broker.yaml=./my-broker-config.yaml
```

### With GCP Workload Identity

```bash
helm install hyperfleet-adapter ./charts/ \
  --set broker.create=true \
  --set broker.type=googlepubsub \
  --set broker.subscriptionId=my-subscription \
  --set broker.topic=my-topic
```

### Using Existing ServiceAccount

```bash
helm install hyperfleet-adapter ./charts/ \
  --set serviceAccount.create=false \
  --set serviceAccount.name=my-existing-sa
```

### Using Existing Broker ConfigMap

```bash
helm install hyperfleet-adapter ./charts/ \
  --set broker.configMapName=existing-broker-config
```

### With Values File

Create `my-values.yaml`:

```yaml
replicaCount: 2

serviceAccount:
  create: true
  name: my-adapter

hyperfleetApi:
  baseUrl: https://api.hyperfleet.example.com
  version: v1

broker:
  create: true
  type: googlepubsub
  subscriptionId: hyperfleet-adapter-subscription
  topic: hyperfleet-events
  yaml: |
    broker:
      type: googlepubsub
      googlepubsub:
        project_id: my-gcp-project
        max_outstanding_messages: 1000
        num_goroutines: 10
    subscriber:
      parallelism: 10

config:
  enabled: true
  adapterYaml: |
    apiVersion: hyperfleet.redhat.com/v1alpha1
    kind: AdapterConfig
    metadata:
      name: my-adapter
    spec:
      adapter:
        version: "1.0.0"
      # ... rest of adapter config

resources:
  limits:
    cpu: 1000m
    memory: 1Gi
  requests:
    cpu: 200m
    memory: 256Mi

podDisruptionBudget:
  enabled: true
  minAvailable: 1
```

Install with values file:

```bash
helm install hyperfleet-adapter ./charts/ -f my-values.yaml
```

## Environment Variables

The deployment sets these environment variables automatically:

| Variable | Value | Condition |
|----------|-------|-----------|
| `HYPERFLEET_API_BASE_URL` | From `hyperfleetApi.baseUrl` | When `hyperfleetApi.baseUrl` is set |
| `HYPERFLEET_API_VERSION` | From `hyperfleetApi.version` | Always (default: v1) |
| `ADAPTER_CONFIG_PATH` | `/etc/adapter/adapter.yaml` | When `config.adapterYaml` is set |
| `BROKER_CONFIG_FILE` | `/etc/broker/broker.yaml` | When `broker.yaml` is set |
| `BROKER_SUBSCRIPTION_ID` | From ConfigMap | When `broker.subscriptionId` is set |
| `BROKER_TOPIC` | From ConfigMap | When `broker.topic` is set |

Additional env vars from `broker.env` are also loaded via `envFrom` when `broker.mountAsEnv: true`.

## GCP Workload Identity Setup

To use GCP Pub/Sub with Workload Identity:

```bash
# 1. Create Google Service Account
gcloud iam service-accounts create hyperfleet-adapter \
  --project=MY_PROJECT

# 2. Grant Pub/Sub permissions
gcloud projects add-iam-policy-binding MY_PROJECT \
  --member="serviceAccount:hyperfleet-adapter@MY_PROJECT.iam.gserviceaccount.com" \
  --role="roles/pubsub.subscriber"

# 3. Allow KSA to impersonate GSA
gcloud iam service-accounts add-iam-policy-binding \
  hyperfleet-adapter@MY_PROJECT.iam.gserviceaccount.com \
  --role="roles/iam.workloadIdentityUser" \
  --member="serviceAccount:MY_PROJECT.svc.id.goog[NAMESPACE/RELEASE-hyperfleet-adapter]"
```

## Notes

- The adapter runs as non-root user (UID 65532) with read-only filesystem
- Health probes are disabled by default (adapter is a message consumer, not HTTP server)
- Uses `distroless` base image for minimal attack surface
- Config checksum annotation triggers pod restart on ConfigMap changes
