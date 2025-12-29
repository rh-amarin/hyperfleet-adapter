# Config Loader

The `config_loader` package loads and validates HyperFleet Adapter configuration files (YAML format).

## Features

- **YAML Parsing**: Load configurations from files or bytes
- **Validation**: Required fields, structure, CEL expressions, K8s manifests
- **Type Safety**: Strongly-typed Go structs
- **Helper Methods**: Query params, resources, preconditions by name

## Usage

```go
import "github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"

// Load from file (or set ADAPTER_CONFIG_PATH env var)
config, err := config_loader.Load("path/to/config.yaml")

// With adapter version validation
config, err := config_loader.Load("config.yaml", config_loader.WithAdapterVersion("1.0.0"))
```

### Accessing Configuration

```go
// Metadata
config.Metadata.Name
config.Metadata.Namespace

// API config
timeout, _ := config.Spec.HyperfleetAPI.ParseTimeout()

// Query helpers
config.GetRequiredParams()
config.GetResourceByName("clusterNamespace")
config.GetPreconditionByName("clusterStatus")
config.GetPostActionByName("reportStatus")
```

## Configuration Structure

```yaml
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: example-adapter
  namespace: hyperfleet-system
spec:
  adapter:
    version: "0.1.0"
  hyperfleetApi:
    timeout: 2s
    retryAttempts: 3
    retryBackoff: exponential
  params: [...]
  preconditions: [...]
  resources: [...]
  post: {...}
```

See `configs/adapter-config-template.yaml` for the complete configuration reference.

## Validation

The loader validates:
- Required fields (`apiVersion`, `kind`, `metadata.name`, `adapter.version`)
- HTTP methods in API calls (`GET`, `POST`, `PUT`, `PATCH`, `DELETE`)
- Parameters have `source`
- File references exist (`buildRef`, `manifest.ref`)
- CEL expressions are syntactically valid
- K8s manifests have required fields
- CaptureField has either `field` or `expression` (not both, not neither)

Validation errors are descriptive:
```
spec.params[0].name is required
spec.preconditions[1].apiCall.method must be one of: GET, POST, PUT, PATCH, DELETE
```

## Types

| Type | Description |
|------|-------------|
| `AdapterConfig` | Top-level configuration |
| `Parameter` | Parameter extraction config |
| `Precondition` | Pre-check with API call and conditions |
| `Resource` | K8s resource with manifest and discovery |
| `PostConfig` | Post-processing actions |
| `APICall` | HTTP request configuration |
| `Condition` | Field/operator/value condition |
| `CaptureField` | Field capture from API response (see below) |
| `ValueDef` | Dynamic value definition in payload builds (see below) |

### CaptureField

Captures values from API responses. Supports two modes (mutually exclusive):

| Field | Description |
|-------|-------------|
| `name` | Variable name for captured value (required) |
| `field` | Simple dot notation or JSONPath expression |
| `expression` | CEL expression for computed values |

```yaml
capture:
  # Simple dot notation
  - name: "clusterPhase"
    field: "status.phase"
  
  # JSONPath for complex extraction
  - name: "lzStatus"
    field: "{.items[?(@.adapter=='landing-zone-adapter')].data.namespace.status}"
  
  # CEL expression
  - name: "activeCount"
    expression: "items.filter(i, i.status == 'active').size()"
```

### ValueDef

Dynamic value definition for payload builds. Used when a field should be computed via field extraction (JSONPath) or CEL expression.

| Field | Description |
|-------|-------------|
| `field` | JSONPath/dot notation to extract value |
| `expression` | CEL expression to evaluate |
| `default` | Default value if extraction fails or returns nil |

```yaml
build:
  # Direct string (Go template supported)
  message: "Deployment successful"
  
  # Field extraction with default
  errorMessage:
    field: "adapter.errorMessage"
    default: ""
  
  # CEL expression with default
  isHealthy:
    expression: "resources.deployment.status.readyReplicas > 0"
    default: false
```

See `types.go` for complete definitions.


## Related

- `internal/criteria` - Evaluates conditions
- `internal/k8s_client` - Manages K8s resources
- `configs/adapter-config-template.yaml` - Full config template
