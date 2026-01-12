ARG BASE_IMAGE=gcr.io/distroless/static-debian12:nonroot

# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /build

# Copy source code
COPY . .

# Tidy and verify Go module dependencies
RUN go mod tidy && go mod verify

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o adapter ./cmd/adapter

# Runtime stage
FROM ${BASE_IMAGE}

WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/adapter /app/adapter

# Copy default config (fallback if ConfigMap is not mounted)
# Default config location: /app/configs/adapter.yaml
COPY configs/adapter.yaml /app/configs/adapter.yaml

# Config file resolution order (application should implement):
# 1. CONFIG_FILE environment variable (if set) - highest priority
# 2. /etc/adapter/config/adapter.yaml (ConfigMap mount point)
# 3. /app/configs/adapter.yaml (default packaged config) - fallback
#
# To use ConfigMap in Kubernetes:
#   volumeMounts:
#   - name: config
#     mountPath: /etc/adapter/config
#   volumes:
#   - name: config
#     configMap:
#       name: adapter-config
#
# To override with environment variable:
#   env:
#   - name: CONFIG_FILE
#     value: /path/to/custom/config.yaml

ENTRYPOINT ["/app/adapter"]
CMD ["serve"]

LABEL name="hyperfleet-adapter" \
      vendor="Red Hat" \
      version="0.1.0" \
      summary="HyperFleet Adapter - Event-driven adapter services for HyperFleet cluster provisioning" \
      description="Handles CloudEvents consumption, AdapterConfig CRD integration, precondition evaluation, Kubernetes Job creation/monitoring, and status reporting via API"
