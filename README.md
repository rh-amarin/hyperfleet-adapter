# HyperFleet Adapter

HyperFleet Adapter Framework - Event-driven adapter services for HyperFleet cluster provisioning. Handles CloudEvents consumption, AdapterConfig CRD integration, precondition evaluation, Kubernetes Job creation/monitoring, and status reporting via API. Supports GCP Pub/Sub, RabbitMQ broker abstraction.

## Features

- **CloudEvents Processing**: Consumes and processes CloudEvents from message brokers
- **Broker Agnostic**: Supports multiple message brokers (GCP Pub/Sub, RabbitMQ)
- **Kubernetes Integration**: Creates and monitors Kubernetes Jobs for cluster provisioning
- **AdapterConfig CRD**: Integrates with Kubernetes Custom Resource Definitions
- **Precondition Evaluation**: Evaluates preconditions before cluster provisioning
- **Status Reporting**: Provides API endpoints for status reporting
- **Structured Logging**: Context-aware logging with operation IDs and transaction tracking
- **Error Handling**: Comprehensive error handling with error codes and API references

## Prerequisites

- Go 1.24.6 or later
- Docker (for building Docker images)
- Kubernetes 1.19+ (for deployment)
- Helm 3.0+ (for Helm chart deployment)
- `golangci-lint` (for linting, optional)

## Getting Started

### Clone the Repository

```bash
git clone https://github.com/openshift-hyperfleet/hyperfleet-adapter.git
cd hyperfleet-adapter
```

### Install Dependencies

```bash
make mod-tidy
```

### Build

```bash
# Build the binary
make build

# The binary will be created at: bin/hyperfleet-adapter
```

### Run Tests

```bash
# Run unit tests
make test

# Run integration tests
make test-integration

# Run all tests
make test-all
```

### Linting

```bash
# Run linter
make lint

# Format code
make fmt
```

## Development

### Project Structure

```
hyperfleet-adapter/
├── cmd/
│   └── adapter/          # Main application entry point
├── pkg/
│   ├── errors/           # Error handling utilities
│   └── logger/           # Structured logging with context support
├── internal/
│   ├── broker-consumer/  # Message broker consumer implementations
│   ├── config-loader/    # Configuration loading logic
│   ├── criteria/         # Precondition evaluation
│   ├── hyperfleet-api/   # HyperFleet API client
│   └── k8s-objects/      # Kubernetes object management
├── test/                 # Integration tests
├── data/                 # Configuration templates
├── charts/               # Helm chart for Kubernetes deployment
├── Dockerfile            # Multi-stage Docker build
├── Makefile              # Build and test automation
├── go.mod                # Go module dependencies
└── README.md             # This file
```

### Available Make Targets

| Target | Description |
|--------|-------------|
| `make build` | Build binary |
| `make test` | Run unit tests |
| `make lint` | Run golangci-lint |
| `make docker-build` | Build Docker image |
| `make docker-push` | Push Docker image |
| `make test-integration` | Run integration tests |
| `make test-coverage` | Generate test coverage report |
| `make fmt` | Format code |
| `make mod-tidy` | Tidy Go module dependencies |
| `make clean` | Clean build artifacts |
| `make verify` | Run lint and test |

### Configuration

The adapter supports multiple configuration sources with the following priority order:

1. **Environment Variable** (`CONFIG_FILE`) - Highest priority
2. **ConfigMap Mount** (`/etc/adapter/config/adapter.yaml`)
3. **Packaged Config** (`/app/configs/adapter.yaml`) - Fallback

See `data/adapter-config-template.yaml` for configuration template.

### Broker Configuration

Broker configuration is managed separately and can be provided via:

- **ConfigMap**: Mounted at deployment time
- **Environment Variables**: For broker-specific settings

See the Helm chart documentation for broker configuration options.

## Deployment

### Using Helm Chart

The project includes a Helm chart for Kubernetes deployment.

```bash
# Install the chart
helm install hyperfleet-adapter ./charts/

# Install with custom values
helm install hyperfleet-adapter ./charts/ -f custom-values.yaml

# Upgrade deployment
helm upgrade hyperfleet-adapter ./charts/

# Uninstall
helm delete hyperfleet-adapter
```

For detailed Helm chart documentation, see [charts/README.md](./charts/README.md).

### Docker Image

Build and push Docker images:

```bash
# Build Docker image
make docker-build

# Build with custom tag
make docker-build IMAGE_TAG=v1.0.0

# Push Docker image
make docker-push
```

Default image: `quay.io/openshift-hyperfleet/hyperfleet-adapter:latest`

## Testing

### Unit Tests

```bash
make test
```

Unit tests include:
- Logger functionality and context handling
- Error handling and error codes
- Operation ID middleware

### Integration Tests

```bash
make test-integration
```

Integration tests use `testcontainers` and work in both local and Prow CI environments.

### Test Coverage

```bash
# Generate coverage report
make test-coverage

# Generate HTML coverage report
make test-coverage-html
```

## Logging

The adapter uses structured logging with context-aware fields:

- **Transaction ID** (`txid`): Request transaction identifier
- **Operation ID** (`opid`): Unique operation identifier
- **Adapter ID** (`adapter_id`): Adapter instance identifier
- **Cluster ID** (`cluster_id`): Cluster identifier

Logs are formatted with prefixes like: `[opid=abc123][adapter_id=adapter-1] message`

## Error Handling

The adapter uses a structured error handling system:

- **Error Codes**: Standardized error codes with prefixes
- **Error References**: API references for error documentation
- **Error Types**: Common error types (NotFound, Validation, Conflict, etc.)

See `pkg/errors/error.go` for error handling implementation.

## Contributing

We welcome contributions! Please see [CONTRIBUTING.md](./CONTRIBUTING.md) for guidelines on:

- Code style and standards
- Testing requirements
- Pull request process
- Commit message guidelines

## Repository Access

All members of the **hyperfleet** team have write access to this repository.

### Steps to Apply for Repository Access

If you're a team member and need access to this repository:

1. **Verify Organization Membership**: Ensure you're a member of the `openshift-hyperfleet` organization
2. **Check Team Assignment**: Confirm you're added to the hyperfleet team within the organization
3. **Repository Permissions**: All hyperfleet team members automatically receive write access
4. **OWNERS File**: Code reviews and approvals are managed through the OWNERS file

For access issues, contact a repository administrator or organization owner.

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](./LICENSE) file for details.

## Related Documentation

- [Helm Chart Documentation](./charts/README.md)
- [Contributing Guidelines](./CONTRIBUTING.md)
- [Configuration Template](./data/adapter-config-template.yaml)

## Support

For issues, questions, or contributions, please open an issue on GitHub.
