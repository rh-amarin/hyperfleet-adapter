#!/usr/bin/env bash
# Build integration test image with envtest

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/container-runtime.sh"

CONTAINER_RUNTIME=$(detect_container_runtime)

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🔨 Building Integration Test Image"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

if [ "$CONTAINER_RUNTIME" = "none" ]; then
    echo "❌ ERROR: No container runtime found (docker/podman required)"
    exit 1
fi

echo "📦 Building image: localhost/hyperfleet-integration-test:latest"
echo "   This downloads ~100MB of Kubernetes binaries (one-time operation)"
echo ""

if [ "$CONTAINER_RUNTIME" = "podman" ]; then
    PROXY_HTTP=$(get_podman_proxy "HTTP_PROXY")
    PROXY_HTTPS=$(get_podman_proxy "HTTPS_PROXY")
    
    if [ -n "$PROXY_HTTP" ] || [ -n "$PROXY_HTTPS" ]; then
        echo "   Using proxy: $PROXY_HTTP"
        $CONTAINER_RUNTIME build \
            --build-arg HTTP_PROXY="$PROXY_HTTP" \
            --build-arg HTTPS_PROXY="$PROXY_HTTPS" \
            -t localhost/hyperfleet-integration-test:latest \
            -f test/Dockerfile.integration \
            .
    else
        $CONTAINER_RUNTIME build \
            -t localhost/hyperfleet-integration-test:latest \
            -f test/Dockerfile.integration \
            .
    fi
else
    $CONTAINER_RUNTIME build \
        -t localhost/hyperfleet-integration-test:latest \
        -f test/Dockerfile.integration \
        .
fi

echo ""
echo "✅ Integration test image built successfully!"
echo "   Image: localhost/hyperfleet-integration-test:latest"
echo ""

