# HyperFleet Adapter Helm Chart

HyperFleet Adapter - Event-driven adapter for HyperFleet cluster provisioning. Consumes CloudEvents from message brokers (GCP Pub/Sub, RabbitMQ), processes AdapterConfig, manages Kubernetes resources, and reports status via API.

## Prerequisites

- Kubernetes 1.19+
- Helm 3.0+
- GCP Workload Identity (for Pub/Sub access)

## Installing the Chart

```bash
helm install hyperfleet-adapter ./charts/ -f custom-values-file.yaml
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
| `log.level` | Adapter log level | `"info"` |

### ServiceAccount & Workload Identity

If the adapter requires creating kubernetes objects in the cluster, it needs to create a serviceAccount with proper rbac permissions

| Parameter | Description | Default |
|-----------|-------------|---------|
| `serviceAccount.create` | Create ServiceAccount | `true` |
| `serviceAccount.name` | ServiceAccount name (auto-generated if empty) | `""` |
| `rbac.resources` | Helper property to give CRUD permission for resources (pods, jobs...)| `""` |
| `rbac.rules` | Fine grained permissions for the service account | `""` |

### Adapter + Task Configuration

An adapter instance configures its behavior using a config file of `kind: AdapterConfig`, which can be:

1. An existing configmap (using `adapterConfig.configMapName`), or it can be created by the Helm chart.
1. Created via the helm chart, the `AdapterConfig` can be embedded in the same file as the `AdapterConfig` or providing an object of `files` referencing local files that will be added to a `ConfigMap`

In both cases the `ConfigMap` will be mounted in the adapter pod at `/etc/adapter/adapterconfig.yaml`
The purpose of adding more entries to the `files` object is for the `AdapterConfig` to reference external YAML files, so the whole `AdapterConfig` doesn't grow massively.

Beware of template resolution within files referenced in an `AdapterConfig`. These files are not processed by Helm, but you can use go templates to resolve dynamic values (e.g. `property: "{{ .paramFromAdapterConfig }}"`)

| Parameter | Description | Default |
|-----------|-------------|---------|
| `adapterConfig.create` | Enable adapter ConfigMap | `true` |
| `adapterConfig.configMapName` | Custom ConfigMap name | `""` |
| `adapterConfig.yaml` | Adapter YAML config content | `""` |
| `adapterConfig.files` | Task YAML files packaged with chart | `{}` |

### Broker Configuration

An adapter uses the hyperfleet-broker library to interact with a message broker, so the code in the adapter framework is broker agnostic.
This `ConfigMap` can be:

1. An existing `ConfigMap` referenced by the `broker.configMapName` property
2. An embedded YAML file using `broker.yaml`
3. Created out of individual properties that are broker specific (e.g. googlepubsub, rabbitmq)

The `ConfigMap` will be:

- Mounted at `/etc/broker/broker.yaml`
- The library needs the environment variable  `BROKER_CONFIG_FILE=/etc/broker/broker.yaml`

| Parameter | Description | Default |
|-----------|-------------|---------|
| `broker.create` | Create broker ConfigMap | `true` |
| `broker.configMapName` | Broker ConfigMap name | `""` |
| `broker.googlepubsub.projectId` |   Google Cloud project ID | `""` |
| `broker.googlepubsub.subscriptionId` | Subscription ID (BROKER_SUBSCRIPTION_ID) | `""` |
| `broker.googlepubsub.topic` | Topic name (BROKER_TOPIC) | `""` |
| `broker.yaml` | Broker YAML config content | `""` |

When `broker.yaml` is set:

- Creates `broker.yaml` key in ConfigMap
- Mounts at `/etc/broker/broker.yaml`
- Sets `BROKER_CONFIG_FILE=/etc/broker/broker.yaml`

### HyperFleet API Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `hyperfleetApi.baseUrl` | HyperFleet API base URL (HYPERFLEET_API_BASE_URL) | `"http://hyperfleet-api:8000"` |
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

### Health Probes (Enabled by Default)

The adapter is a message consumer but exposes some HTTP endpoints by default.

| Parameter | Description | Default |
|-----------|-------------|---------|
| `livenessProbe.enabled` | Enable liveness probe | `true` |
| `readinessProbe.enabled` | Enable readiness probe | `true` |
| `startupProbe.enabled` | Enable startup probe | `false` |

### Pod Disruption Budget

| Parameter | Description | Default |
|-----------|-------------|---------|
| `podDisruptionBudget.enabled` | Enable PDB | `false` |
| `podDisruptionBudget.minAvailable` | Minimum available pods | - |
| `podDisruptionBudget.maxUnavailable` | Maximum unavailable pods | - |

## Examples

### Basic Installation

```bash
helm install hyperfleet-adapter ./charts/ \
  -f my-values.yaml \
  --set image.registry=quay.io/my-quay-registry \
  --set broker.create=true \
  --set broker.googlepubsub.projectId=my-gcp-project \
  --set broker.googlepubsub.subscriptionId=my-subscription \
  --set broker.googlepubsub.topic=my-topic
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

To use GCP Pub/Sub with Workload Identity, a `principal` to a Kubernetes service account in the namespace is allowed the required roles (e.g. pubsub)

```bash
# 1. Create Google Service Account
gcloud iam service-accounts create hyperfleet-adapter \
  --project=MY_PROJECT

# 2. Grant Pub/Sub permissions
gcloud projects add-iam-policy-binding MY_PROJECT \
  --member="principal://iam.googleapis.com/projects/275239757837/locations/global/workloadIdentityPools/hcm-hyperfleet.svc.id.goog/subject/ns/amarin/sa/landing-zone" \
  --role="roles/pubsub.subscriber"

