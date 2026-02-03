# HyperFleet Adapter Configuration Schema

This document provides a comprehensive reference for the HyperFleet Adapter Framework configuration schema.

## Table of Contents

- [Overview](#overview)
- [Configuration Structure](#configuration-structure)
- [Template Syntax](#template-syntax)
- [Parameters](#parameters)
- [Preconditions](#preconditions)
- [Resources](#resources)
- [Post-Processing](#post-processing)
- [CEL Expressions](#cel-expressions)
- [Error Handling](#error-handling)
- [Examples](#examples)

## Overview

The HyperFleet Adapter Framework uses a declarative YAML configuration to define how adapters process CloudEvents and manage Kubernetes resources. The configuration follows Kubernetes custom resource conventions.

```yaml
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: my-adapter
  namespace: hyperfleet-system
spec:
  adapter: {}
  hyperfleetApi: {}
  kubernetes: {}
  params: []
  preconditions: []
  resources: []
  post: {}
```

## Configuration Structure

### Root Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `apiVersion` | string | Yes | Must be `hyperfleet.redhat.com/v1alpha1` |
| `kind` | string | Yes | Must be `AdapterConfig` |
| `metadata` | object | Yes | Standard Kubernetes metadata |
| `spec` | object | Yes | Adapter specification |

### Metadata

```yaml
metadata:
  name: my-adapter              # Adapter identifier
  namespace: hyperfleet-system  # Deployment namespace
  labels:
    hyperfleet.io/adapter-type: namespace
    hyperfleet.io/component: adapter
```

### Spec Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `adapter` | object | Yes | Adapter version information |
| `hyperfleetApi` | object | No | HyperFleet API client settings |
| `kubernetes` | object | No | Kubernetes client settings |
| `params` | array | No | Parameter extraction definitions |
| `preconditions` | array | No | Pre-execution validations |
| `resources` | array | No | Kubernetes resources to manage |
| `post` | object | No | Post-processing configuration |

## Template Syntax

The configuration supports multiple templating syntaxes:

### Go Templates

Use `{{ .variableName }}` for variable interpolation:

```yaml
name: "cluster-{{ .clusterId | lower }}"
url: "{{ .hyperfleetApiBaseUrl }}/api/v1/clusters/{{ .clusterId }}"
```

**Available Functions:**

- `lower`, `upper` - Case conversion
- `default "value"` - Default values
- `now` - Current timestamp
- `date "format"` - Date formatting
- `quote` - Quote strings

### Simple Field Access

Use `field: "path"` for extracting values from API responses:

```yaml
capture:
  - name: "clusterPhase"
    field: "status.phase"
```

### JSONPath

Use JSONPath syntax for complex extractions:

```yaml
capture:
  - name: "readyCondition"
    field: "{.status.conditions[?(@.type=='Ready')].status}"
```

### CEL Expressions

Use `expression: "cel_code"` for computed values:

```yaml
capture:
  - name: "isHealthy"
    expression: "status.conditions.exists(c, c.type == 'Ready' && c.status == 'True')"
```

## Parameters

Parameters extract values from CloudEvents and environment variables.

### Parameter Schema

```yaml
params:
  - name: "paramName"           # Variable name (required)
    source: "event.field.path"  # Data source (required)
    type: "string"              # Type: string, int, float, bool
    default: "defaultValue"     # Default if not found
    description: "Description"  # Documentation
    required: true              # Fail if missing (default: false)
```

### Supported Sources

| Source Pattern | Description | Example |
|---------------|-------------|---------|
| `env.VAR_NAME` | Environment variable | `env.HYPERFLEET_API_BASE_URL` |
| `event.field` | CloudEvent data field | `event.cluster_id` |
| `event.spec.field` | Nested event field | `event.spec.region` |

### Supported Types

| Type | Description | Conversion |
|------|-------------|------------|
| `string` | String value (default) | Any value to string |
| `int` | Integer value | Parse string, truncate float |
| `float` | Floating point | Parse string |
| `bool` | Boolean | `true/false`, `yes/no`, `1/0` |

### Examples

```yaml
params:
  # Required environment variable
  - name: "hyperfleetApiBaseUrl"
    source: "env.HYPERFLEET_API_BASE_URL"
    type: "string"
    required: true

  # Optional with default
  - name: "region"
    source: "event.spec.region"
    type: "string"
    default: "us-east-1"

  # Integer parameter
  - name: "replicas"
    source: "event.spec.replicas"
    type: "int"
    default: 1

  # Boolean parameter
  - name: "dryRun"
    source: "env.DRY_RUN"
    type: "bool"
    default: false
```

## Preconditions

Preconditions validate system state before resource operations.

### Precondition Schema

```yaml
preconditions:
  - name: "preconditionName"    # Identifier (required)
    apiCall:                     # API call configuration
      method: "GET"
      url: "https://api.example.com/resource"
      timeout: 10s
      retryAttempts: 3
      retryBackoff: "exponential"
      headers:
        - name: "Content-Type"
          value: "application/json"
    capture:                     # Fields to capture from response
      - name: "fieldName"
        field: "path.to.field"
    conditions:                  # Conditions to validate
      - field: "fieldName"
        operator: "equals"
        value: "expectedValue"
    # OR use CEL expression
    expression: "captured.field == 'value'"
```

### API Call Configuration

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `method` | string | Yes | HTTP method (GET, POST, PUT, DELETE) |
| `url` | string | Yes | Request URL (supports templates) |
| `timeout` | duration | No | Request timeout (default: 30s) |
| `retryAttempts` | int | No | Number of retries (default: 0) |
| `retryBackoff` | string | No | Backoff strategy: exponential, linear, constant |
| `headers` | array | No | HTTP headers |
| `body` | string | No | Request body (for POST/PUT) |

### Capture Configuration

Capture extracts values from API responses for use in conditions and resources.

```yaml
capture:
  # Simple field access
  - name: "clusterName"
    field: "name"

  # Nested field
  - name: "phase"
    field: "status.phase"

  # JSONPath
  - name: "readyCondition"
    field: "{.status.conditions[?(@.type=='Ready')]}"

  # CEL expression
  - name: "isReady"
    expression: "status.phase == 'Ready'"
```

### Condition Operators

| Operator | Description | Example Value |
|----------|-------------|---------------|
| `equals` | Exact match | `"Ready"` |
| `notEquals` | Not equal | `"Failed"` |
| `in` | In list | `["Ready", "Running"]` |
| `notIn` | Not in list | `["Failed", "Error"]` |
| `contains` | String contains | `"error"` |
| `greaterThan` | Numeric > | `0` |
| `lessThan` | Numeric < | `100` |
| `exists` | Field exists | `true` |

### Data Scopes

**Capture Scope (field/expression in capture):**

- Access: API response data only
- Example: `status.phase`, `items[0].name`

**Conditions Scope (conditions/expression):**

- `params.*` - Extracted parameters
- `<precondition-name>.*` - Full API response from preconditions
- `adapter.*` - Adapter metadata
- Captured values by name

## Resources

Resources define Kubernetes manifests to create or update.

### Resource Schema

```yaml
resources:
  - name: "resourceName"         # Identifier (required)
    manifest:                     # Kubernetes manifest (inline)
      apiVersion: v1
      kind: Namespace
      metadata:
        name: "cluster-{{ .clusterId }}"
    # OR reference external template
    templateFile: "templates/resource.yaml"
    discovery:                    # Resource discovery config
      namespace: "target-ns"      # For namespaced resources
      bySelectors:
        labelSelector:
          key: value
      # OR by name
      byName:
        name: "resource-name"
```

### Manifest Configuration

Manifests support Go template interpolation:

```yaml
manifest:
  apiVersion: v1
  kind: Namespace
  metadata:
    name: "cluster-{{ .clusterId | lower }}"
    labels:
      hyperfleet.io/cluster-id: "{{ .clusterId }}"
      hyperfleet.io/generation: "{{ .generationId }}"
    annotations:
      hyperfleet.io/created-by: "hyperfleet-adapter"
```

### Discovery Configuration

Discovery finds existing resources to update instead of create.

```yaml
discovery:
  # For namespaced resources
  namespace: "target-namespace"

  # Find by labels
  bySelectors:
    labelSelector:
      hyperfleet.io/cluster-id: "{{ .clusterId }}"
      hyperfleet.io/resource-type: "namespace"

  # OR find by name
  byName:
    name: "cluster-{{ .clusterId | lower }}"
```

### Resource Types

**Cluster-Scoped Resources:**

- Namespace, ClusterRole, ClusterRoleBinding
- Omit `namespace` in discovery

**Namespaced Resources:**

- Deployment, Service, ConfigMap, Job, etc.
- Set `namespace` in discovery

## Post-Processing

Post-processing runs after resources are created/updated.

### Post Schema

```yaml
post:
  payloads:                       # Status payloads to build
    - name: "payloadName"
      build:
        adapter: "adapter-name"
        conditions: []
        observed_generation: {}
        observed_time: ""
        data: {}
  postActions:                    # Actions to execute
    - name: "actionName"
      when:                       # Optional condition
        expression: "condition"
      apiCall: {}
```

### Payload Building

Payloads construct structured status reports:

```yaml
payloads:
  - name: "clusterStatusPayload"
    build:
      adapter: "{{ .metadata.name }}"

      conditions:
        - type: "Applied"
          status:
            expression: |
              resources.?clusterNamespace.?status.?phase.orValue("") == "Active" ? "True" : "False"
          reason:
            expression: |
              resources.?clusterNamespace.?status.?phase.orValue("") == "Active"
                ? "NamespaceCreated"
                : "NamespacePending"
          message:
            expression: |
              resources.?clusterNamespace.?status.?phase.orValue("") == "Active"
                ? "Namespace created successfully"
                : "Namespace creation in progress"

      # Numeric fields - use expression to preserve type
      observed_generation:
        expression: "generationId"

      # Timestamps - use Go template
      observed_time: "{{ now | date \"2006-01-02T15:04:05Z07:00\" }}"

      # Custom data
      data:
        namespace:
          name:
            expression: |
              resources.?clusterNamespace.?metadata.?name.orValue("")
```

### Post Actions

Actions execute API calls after resource operations:

```yaml
postActions:
  # Always executed
  - name: "reportClusterStatus"
    apiCall:
      method: "POST"
      url: "{{ .hyperfleetApiBaseUrl }}/api/v1/clusters/{{ .clusterId }}/statuses"
      body: "{{ .clusterStatusPayload }}"
      timeout: 30s
      retryAttempts: 3
      retryBackoff: "exponential"
      headers:
        - name: "Content-Type"
          value: "application/json"

  # Conditional execution
  - name: "notifyReady"
    when:
      expression: |
        resources.?clusterNamespace.?status.?phase.orValue("") == "Active"
    apiCall:
      method: "POST"
      url: "{{ .hyperfleetApiBaseUrl }}/api/v1/notifications"
      body: '{"type": "cluster.ready", "cluster_id": "{{ .clusterId }}"}'
      timeout: 10s
```

## CEL Expressions

CEL (Common Expression Language) provides powerful expression evaluation.

### Optional Chaining

Use `?.` and `.orValue()` for safe field access:

```yaml
expression: |
  resources.?clusterNamespace.?status.?phase.orValue("")
```

### Condition Checks

```yaml
expression: |
  resources.?job.?status.?succeeded.orValue(0) > 0

expression: |
  resources.?manifestwork.?status.?conditions.exists(c, c.type == "Applied" && c.status == "True")
```

### Ternary Expressions

```yaml
expression: |
  condition ? "valueIfTrue" : "valueIfFalse"
```

### List Operations

```yaml
# Filter
expression: "items.filter(i, i.status == 'active')"

# Exists
expression: "conditions.exists(c, c.type == 'Ready')"

# Size
expression: "items.size()"

# Map
expression: "items.map(i, i.name)"
```

### Data Access in Post-Processing

Available variables:

- `params.*` - Extracted parameters
- `resources.*` - Created/updated resources
- `adapter.*` - Adapter execution metadata
- `<precondition-name>.*` - API responses
- Captured values by name

## Error Handling

### Retry Configuration

```yaml
apiCall:
  retryAttempts: 3
  retryBackoff: "exponential"  # exponential, linear, constant
```

**Backoff Strategies:**

- `exponential`: 1s, 2s, 4s, 8s...
- `linear`: 1s, 2s, 3s, 4s...
- `constant`: 1s, 1s, 1s, 1s...

### Timeout Configuration

```yaml
apiCall:
  timeout: 30s
```

### Error Conditions in Payloads

```yaml
conditions:
  - type: "Health"
    status:
      expression: |
        adapter.?executionStatus.orValue("") == "success" ? "True" :
        (adapter.?executionStatus.orValue("") == "failed" ? "False" : "Unknown")
    reason:
      expression: |
        adapter.?errorReason.orValue("") != "" ? adapter.?errorReason.orValue("") : "Healthy"
    message:
      expression: |
        adapter.?errorMessage.orValue("") != "" ? adapter.?errorMessage.orValue("") : "All operations completed"
```

## See Also

- [HyperFleet Adapter Framework](https://github.com/openshift-hyperfleet/hyperfleet-adapter)
- [Helm Chart README](../charts/README.md)
- [CEL Specification](https://github.com/google/cel-spec)
- [Kubernetes JSONPath](https://kubernetes.io/docs/reference/kubectl/jsonpath/)
