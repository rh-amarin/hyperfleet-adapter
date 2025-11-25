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
    version: "0.0.1"
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

See `types.go` for complete definitions.


## Related

- `internal/criteria` - Evaluates conditions
- `internal/k8s_client` - Manages K8s resources
- `configs/adapter-config-template.yaml` - Full config template
