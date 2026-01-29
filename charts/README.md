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

### Adapter + Task Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `adapterConfig.create` | Enable adapter ConfigMap | `true` |
| `adapterConfig.configMapName` | Custom ConfigMap name | `""` |
| `adapterConfig.yaml` | Adapter YAML config content | `""` |
| `adapterConfig.files` | Task YAML files packaged with chart | `{}` |

When `adapterConfig.yaml` is set:
- Creates `adapterconfig.yaml` key in ConfigMap
- Mounts at `/etc/adapter/adapterconfig.yaml`

### Broker Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `broker.create` | Create broker ConfigMap | `false` |
| `broker.configMapName` | Broker ConfigMap name | `""` |
| `broker.type` | Broker type (googlepubsub, rabbitmq) | `""` |
| `broker.googlepubsub.subscriptionId` | Subscription ID (BROKER_SUBSCRIPTION_ID) | `""` |
| `broker.googlepubsub.topic` | Topic name (BROKER_TOPIC) | `""` |
| `broker.yaml` | Broker YAML config content | `""` |
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
| `terminationGracePeriodSeconds` | Termination grace period | `30` |

### Health Probes (Disabled by Default)

The adapter is a message consumer and doesn't expose HTTP endpoints by default.

| Parameter | Description | Default |
|-----------|-------------|---------|
| `livenessProbe.enabled` | Enable liveness probe | `true` |
| `readinessProbe.enabled` | Enable readiness probe | `true` |
| `startupProbe.enabled` | Enable startup probe | `true` |

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
  --set broker.googlepubsub.subscriptionId=my-subscription \
  --set broker.googlepubsub.topic=my-topic
```

### With HyperFleet API Configuration

```bash
helm install hyperfleet-adapter ./charts/ \
  --set hyperfleetApi.baseUrl=https://api.hyperfleet.example.com \
  --set broker.create=true \
  --set broker.googlepubsub.subscriptionId=my-subscription \
  --set broker.googlepubsub.topic=my-topic
```

### With Full Configuration (Adapter + Broker)

```bash
helm install hyperfleet-adapter ./charts/ \
  --set broker.create=true \
  --set broker.type=googlepubsub \
  --set broker.googlepubsub.subscriptionId=my-subscription \
  --set broker.googlepubsub.topic=my-topic \
  --set-file adapterConfig.yaml=./my-adapter-config.yaml \
  --set-file broker.yaml=./my-broker-config.yaml
```

### With GCP Workload Identity

```bash
helm install hyperfleet-adapter ./charts/ \
  --set broker.create=true \
  --set broker.type=googlepubsub \
  --set broker.googlepubsub.subscriptionId=my-subscription \
  --set broker.googlepubsub.topic=my-topic \
  --set 'serviceAccount.annotations.iam\.gke\.io/gcp-service-account=adapter@PROJECT.iam.gserviceaccount.com'
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
  annotations:
    iam.gke.io/gcp-service-account: adapter@my-project.iam.gserviceaccount.com

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

adapterConfig:
  create: true
  yaml: |
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
| `BROKER_CONFIG_FILE` | `/etc/broker/broker.yaml` | When `broker.yaml` is set |
| `BROKER_SUBSCRIPTION_ID` | From ConfigMap | When `broker.googlepubsub.subscriptionId` is set |
| `BROKER_TOPIC` | From ConfigMap | When `broker.googlepubsub.topic` is set |

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