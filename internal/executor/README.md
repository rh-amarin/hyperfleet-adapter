# Executor Package

The `executor` package is the core event processing engine for the HyperFleet Adapter. It orchestrates the execution of CloudEvents according to the adapter configuration, coordinating parameter extraction, precondition evaluation, Kubernetes resource management, and post-action execution.

## Key Concepts

### Execution Status vs Business Outcomes

The executor separates **process execution status** from **business outcomes**:

- **Process Execution Status**: Did the adapter execute successfully? (`success` or `failed`)
  - `success`: Adapter ran without process execution errors
  - `failed`: Process execution error occurred (API timeout, K8s error, parse error, etc.)

- **Business Outcomes**: What did the adapter decide to do?
  - Resources executed: Preconditions met, resources created/updated
  - Resources skipped: Preconditions not met (valid business decision)

**Important**: Precondition not met is a **successful execution** with resources skipped. It's not a failure!

## Overview

The executor implements a four-phase execution pipeline:

```
┌──────────────────────────────────────────────────────────────────────┐
│                        Event Processing Pipeline                     │
├──────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  CloudEvent ──► Phase 1 ──► Phase 2 ──► Phase 3 ──► Phase 4 ──► Done │
│                Extract    Precond.   Resources   Post-Act.           │
│                Params     Eval.      Create      Execute             │
│                                                                      │
└──────────────────────────────────────────────────────────────────────┘
```

## Components

### Main Components

| Component | File | Description |
|-----------|------|-------------|
| `Executor` | `executor.go` | Main orchestrator that coordinates all phases |
| `ParamExtractor` | `param_extractor.go` | Extracts parameters from events and environment |
| `PreconditionExecutor` | `precondition_executor.go` | Evaluates preconditions with API calls and CEL |
| `ResourceExecutor` | `resource_executor.go` | Creates/updates Kubernetes resources |
| `PostActionExecutor` | `post_action_executor.go` | Executes post-processing actions |

### Type Definitions

| Type | Description |
|------|-------------|
| `ExecutionResult` | Contains the result of processing an event |
| `PreconditionResult` | Result of a single precondition evaluation |
| `ResourceResult` | Result of a single resource operation |
| `PostActionResult` | Result of a single post-action execution |
| `ExecutionContext` | Process execution context during execution |

## Usage

### Basic Usage

<details>
<summary>Click to see basic usage example</summary>

```go
import (
    "github.com/openshift-hyperfleet/hyperfleet-adapter/internal/executor"
)

// Create executor using builder
exec, err := executor.NewBuilder().
    WithAdapterConfig(adapterConfig).
    WithAPIClient(apiClient).
    WithK8sClient(k8sClient).
    WithLogger(log).
    Build()
if err != nil {
    return err
}

// Create handler for broker subscription
handler := exec.CreateHandler()

// Or execute directly
result := exec.Execute(ctx, cloudEvent)
if result.Status == executor.StatusFailed {
    log.Errorf("Execution failed: %v", result.Error)
} else if result.ResourcesSkipped {
    log.Infof("Execution succeeded, resources skipped: %s", result.SkipReason)
} else {
    log.Infof("Execution succeeded")
}
```

</details>

### Mock K8s Client for Testing

For unit tests, use a mock K8s client implementation instead of a real Kubernetes cluster:

<details>
<summary>Click to see mock K8s client example</summary>

```go
// Create a mock K8s client that implements k8s_client.K8sClient interface
mockK8s := &mockK8sClient{
    // Configure mock responses as needed
}

exec, err := executor.NewBuilder().
    WithAdapterConfig(adapterConfig).
    WithAPIClient(apiClient).
    WithK8sClient(mockK8s).  // Use mock instead of real client
    WithLogger(log).
    Build()
```

</details>

## Execution Phases

### Phase 1: Parameter Extraction

Extracts parameters from various sources:

- **Environment Variables**: `source: "env.VARIABLE_NAME"`
- **Event Data**: `source: "event.field.path"`
- **Secrets**: `source: "secret.namespace.name.key"` (requires K8s client)
- **ConfigMaps**: `source: "configmap.namespace.name.key"` (requires K8s client)

<details>
<summary>Parameter extraction example</summary>

```yaml
params:
  - name: "clusterId"
    source: "event.cluster_id"
    type: "string"
    required: true
  - name: "apiToken"
    source: "env.API_TOKEN"
    required: true
```

</details>

### Phase 2: Precondition Evaluation

Executes preconditions with optional API calls and condition evaluation:

<details>
<summary>Precondition with API call example</summary>

```yaml
preconditions:
  - name: "checkClusterStatus"
    apiCall:
      method: "GET"
      url: "{{ .apiBaseUrl }}/clusters/{{ .clusterId }}"
    capture:
      # Simple dot notation
      - name: "clusterPhase"
        field: "status.phase"
      
      # JSONPath for complex extraction
      - name: "lzStatus"
        field: "{.items[?(@.adapter=='landing-zone-adapter')].data.namespace.status}"
      
      # CEL expression for computed values
      - name: "activeCount"
        expression: "items.filter(i, i.status == 'active').size()"
    conditions:
      # Access captured values
      - field: "clusterPhase"
        operator: "in"
        value: ["Ready", "Provisioning"]
      
      # Or dig directly into API response using precondition name
      - field: "checkClusterStatus.status.nodeCount"
        operator: "greaterThan"
        value: 0
```

**Capture modes:**
- `field`: Simple dot notation (`status.phase`) or JSONPath (`{.items[*].name}`)
- `expression`: CEL expression for computed values

Only one of `field` or `expression` can be set per capture.

</details>

#### Data Scopes

Preconditions have **two different data scopes** for capture and conditions:

| Operation | Data Scope | Available Variables |
|-----------|------------|---------------------|
| **Capture** (`field`/`expression`) | API Response only | Only the parsed JSON response (e.g., `status.phase`, `items[0].name`) |
| **Conditions** (`conditions`/`expression`) | Full execution context | `params.*`, `<precondition-name>.*`, `adapter.*`, `resources.*` |

**Conditions scope details:**

| Variable | Source |
|----------|--------|
| `params.*` | Original extracted params |
| `<precondition-name>.*` | Full API response from that precondition (e.g., `checkClusterStatus.status.phase`) |
| `capturedField` | Explicitly captured fields (added to params) |
| `adapter.*` | Adapter metadata |
| `resources.*` | Created resources (empty during preconditions) |

<details>
<summary>Example: Digging into API response in conditions</summary>

```yaml
preconditions:
  - name: "getCluster"
    apiCall:
      url: "{{ .apiBaseUrl }}/clusters/{{ .clusterId }}"
      method: GET
    # No need to capture everything - conditions can access full response
    conditions:
      # Access response directly via precondition name
      - field: "getCluster.status.phase"
        operator: "equals"
        value: "Ready"
      - field: "getCluster.spec.nodeCount"
        operator: "greaterThan"
        value: 0
    # Or use CEL expression with full access
    expression: |
      getCluster.status.phase == "Ready" && 
      size(getCluster.spec.nodes) > 0
```

</details>

#### Supported Condition Operators

| Operator | Description |
|----------|-------------|
| `equals` | Exact equality |
| `notEquals` | Not equal |
| `in` | Value in list |
| `notIn` | Value not in list |
| `contains` | String/array contains |
| `greaterThan` | Numeric comparison |
| `lessThan` | Numeric comparison |
| `exists` | Field exists and is not empty |

#### CEL Expressions

For complex conditions, use CEL expressions:

<details>
<summary>CEL expression example</summary>

```yaml
preconditions:
  - name: "complexCheck"
    expression: |
      clusterPhase == "Ready" && nodeCount >= 3
```

</details>

### Phase 3: Resource Management

Creates or updates Kubernetes resources from manifests:

<details>
<summary>Resource management example</summary>

```yaml
resources:
  - name: "clusterNamespace"
    manifest:
      apiVersion: v1
      kind: Namespace
      metadata:
        name: "cluster-{{ .clusterId }}"
    discovery:
      byName: "cluster-{{ .clusterId }}"
  
  - name: "externalTemplate"
    manifest:
      ref: "templates/deployment.yaml"
    discovery:
      namespace: "cluster-{{ .clusterId }}"
      bySelectors:
        labelSelector:
          app: "myapp"
```

</details>

#### Resource Operations

| Operation | When | Description |
|-----------|------|-------------|
| `create` | Resource doesn't exist | Creates new resource |
| `update` | Resource exists | Updates existing resource |
| `recreate` | `recreateOnChange: true` | Deletes and recreates |
| `skip` | No changes needed | No operation performed |
| `dry_run` | Dry run mode | Simulated operation |

### Phase 4: Post-Actions

Executes post-processing actions like status reporting:

<details>
<summary>Post-action example</summary>

```yaml
post:
  payloads:
    - name: "statusPayload"
      build:
        status:
          expression: "resources.clusterController.status.readyReplicas > 0"
        message: "Deployment successful"  # Direct string (Go template supported)
        errorMessage:
          field: "adapter.errorMessage"   # JSONPath extraction
          default: ""                     # Fallback if field not found
  
  postActions:
    - name: "reportStatus"
      apiCall:
        method: "POST"
        url: "{{ .apiBaseUrl }}/clusters/{{ .clusterId }}/status"
        body: "{{ .statusPayload }}"
```

**Payload value types:**
- Direct string: `message: "Success"` (Go template rendered)
- Field extraction: `{ field: "path.to.field", default: "fallback" }`
- CEL expression: `{ expression: "items.size() > 0", default: false }`

</details>

## Execution Results

### ExecutionResult

<details>
<summary>ExecutionResult structure</summary>

```go
type ExecutionResult struct {
    Status              ExecutionStatus  // success, failed (process execution perspective)
    Phase               ExecutionPhase   // where execution ended
    Params              map[string]interface{}
    PreconditionResults []PreconditionResult
    ResourceResults     []ResourceResult
    PostActionResults   []PostActionResult
    Error               error            // process execution error only
    ErrorReason         string           // process execution error reason
    ResourcesSkipped    bool             // business outcome: resources were skipped
    SkipReason          string           // why resources were skipped
}
```

</details>

### Status Values

| Status | Description |
|--------|-------------|
| `success` | Execution completed successfully (adapter process execution) |
| `failed` | Execution failed with process execution error (API timeout, K8s error, etc.) |

**Note**: Precondition not met is a **successful execution** with `ResourcesSkipped=true`. This is a valid business outcome, not a process execution failure.

## Error Handling

### Execution Status vs Business Outcomes

The executor distinguishes between **process execution status** and **business outcomes**:

| Scenario | `Status` | `ResourcesSkipped` | `SkipReason` | Meaning |
|----------|----------|-------------------|--------------|---------|
| **Success** | `success` | `false` | `""` | Adapter executed successfully, all phases completed |
| **Precondition Not Met** | `success` | `true` | `"precondition..."` | Adapter executed successfully, business logic decided to skip resources |
| **Process Execution Error** | `failed` | `false` | `""` | API timeout, K8s error, parse error, etc. |

### Precondition Not Met (Business Outcome)

When preconditions are not satisfied, the executor:
1. Sets status to `success` (adapter executed successfully)
2. Sets `ResourcesSkipped = true` (business outcome)
3. Sets `SkipReason` with detailed explanation
4. Skips resource creation phase
5. Still executes post-actions (for status reporting)

**This is a valid business outcome, not an error!**

### Process Execution Errors

Process execution errors are captured in `ExecutionResult` with:
- `Status`: `failed`
- `Error`: The actual error
- `ErrorReason`: Human-readable reason
- `Phase`: Phase where error occurred

### Error and Status Reporting

Post-actions always execute (even on failure) to allow comprehensive status reporting:

<details>
<summary>Comprehensive status reporting example</summary>

```yaml
post:
  payloads:
    - name: "statusPayload"
      build:
        status:
          expression: "adapter.executionStatus == 'success' && !adapter.resourcesSkipped"
        reason:
          expression: "adapter.resourcesSkipped ? 'PreconditionNotMet' : (adapter.errorReason != '' ? adapter.errorReason : 'Healthy')"
          default: "Unknown"
        message:
          expression: "adapter.skipReason != '' ? adapter.skipReason : (adapter.errorMessage != '' ? adapter.errorMessage : 'Success')"
          default: "No message"
        observed_time: "{{ now | date \"2006-01-02T15:04:05Z07:00\" }}"
  postActions:
    - name: "reportStatus"
      apiCall:
        method: "POST"
        url: "{{ .apiBaseUrl }}/clusters/{{ .clusterId }}/status"
        body: "{{ .statusPayload }}"
```

</details>

### Available CEL Variables in Post-Actions

| Variable | Type | Description |
|----------|------|-------------|
| `adapter.executionStatus` | string | `"success"` or `"failed"` (process execution status) |
| `adapter.resourcesSkipped` | bool | Resources were skipped (business outcome) |
| `adapter.skipReason` | string | Why resources were skipped |
| `adapter.errorReason` | string | Process execution error reason (if failed) |
| `adapter.errorMessage` | string | Process execution error message (if failed) |
| `adapter.executionError` | object | Detailed error information (if failed) |

## Template Rendering

All string values in the configuration support Go templates:

```yaml
url: "{{ .apiBaseUrl }}/api/{{ .apiVersion }}/clusters/{{ .clusterId }}"
```

### Available Template Variables

| Source | Example |
|--------|---------|
| Extracted params | `{{ .clusterId }}` |
| Captured fields | `{{ .clusterPhase }}` |
| Adapter metadata | `{{ .metadata.name }}` |
| Event metadata | `{{ .eventMetadata.id }}` |

## Integration

### With Broker Consumer

<details>
<summary>Broker integration example</summary>

```go
// Create executor
exec, _ := executor.NewBuilder().
    WithAdapterConfig(config).
    WithAPIClient(apiClient).
    WithK8sClient(k8sClient).
    WithLogger(log).
    Build()

// Subscribe with executor handler
broker_consumer.Subscribe(ctx, subscriber, topic, exec.CreateHandler())
```

</details>

### Environment Variables

| Variable | Description |
|----------|-------------|
| `KUBECONFIG` | Path to kubeconfig (for local dev) |

## Testing

The executor can be tested with mock API and K8s clients:

<details>
<summary>Testing example</summary>

```go
// Create mock API client
mockAPIClient := &MockAPIClient{...}

// Create mock K8s client (implements k8s_client.K8sClient interface)
mockK8s := &MockK8sClient{...}

// Create executor with mock clients
exec, _ := executor.NewBuilder().
    WithAdapterConfig(config).
    WithAPIClient(mockAPIClient).
    WithK8sClient(mockK8s).
    WithLogger(testLogger).
    Build()

// Execute test event
result := exec.Execute(ctx, testEvent)
assert.Equal(t, executor.StatusSuccess, result.Status)
```

</details>

