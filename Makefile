# Makefile for hyperfleet-adapter

# Project metadata
PROJECT_NAME := hyperfleet-adapter
VERSION ?= 0.0.1
IMAGE_REGISTRY ?= quay.io/openshift-hyperfleet
IMAGE_TAG ?= latest

# Build metadata
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GIT_TAG := $(shell git describe --tags --exact-match 2>/dev/null || echo "")
BUILD_DATE := $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')

# LDFLAGS for build
LDFLAGS := -w -s
LDFLAGS += -X github.com/openshift-hyperfleet/hyperfleet-adapter/cmd/adapter.version=$(VERSION)
LDFLAGS += -X github.com/openshift-hyperfleet/hyperfleet-adapter/cmd/adapter.commit=$(GIT_COMMIT)
LDFLAGS += -X github.com/openshift-hyperfleet/hyperfleet-adapter/cmd/adapter.buildDate=$(BUILD_DATE)
ifneq ($(GIT_TAG),)
LDFLAGS += -X github.com/openshift-hyperfleet/hyperfleet-adapter/cmd/adapter.tag=$(GIT_TAG)
endif

# Go parameters
GOCMD := go
GOBUILD := $(GOCMD) build
GOTEST := $(GOCMD) test
GOMOD := $(GOCMD) mod
GOFMT := gofmt
GOIMPORTS := goimports

# Test parameters
TEST_TIMEOUT := 30m
RACE_FLAG := -race
COVERAGE_OUT := coverage.out
COVERAGE_HTML := coverage.html

# Container runtime detection
DOCKER_AVAILABLE := $(shell if docker info >/dev/null 2>&1; then echo "true"; else echo "false"; fi)
PODMAN_AVAILABLE := $(shell if podman info >/dev/null 2>&1; then echo "true"; else echo "false"; fi)

ifeq ($(DOCKER_AVAILABLE),true)
    CONTAINER_RUNTIME := docker
    CONTAINER_CMD := docker
else ifeq ($(PODMAN_AVAILABLE),true)
    CONTAINER_RUNTIME := podman
    CONTAINER_CMD := podman
    # Find Podman socket for testcontainers compatibility
    PODMAN_SOCK := $(shell find /var/folders -name "podman-machine-*-api.sock" 2>/dev/null | head -1)
    ifeq ($(PODMAN_SOCK),)
        PODMAN_SOCK := $(shell find ~/.local/share/containers/podman/machine -name "*.sock" 2>/dev/null | head -1)
    endif
    ifneq ($(PODMAN_SOCK),)
        export DOCKER_HOST := unix://$(PODMAN_SOCK)
        export TESTCONTAINERS_RYUK_DISABLED := true
    endif
else
    CONTAINER_RUNTIME := none
    CONTAINER_CMD := sh -c 'echo "No container runtime found. Please install Docker or Podman." && exit 1'
endif

# Directories
# Find all Go packages, excluding vendor and test directories
PKG_DIRS := $(shell $(GOCMD) list ./... 2>/dev/null | grep -v /vendor/ | grep -v /test/)

.PHONY: help
help: ## Display this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

.PHONY: container-info
container-info: ## Show detected container runtime information
	@bash scripts/show-container-info.sh

.PHONY: test
test: ## Run unit tests with race detection
	@echo "Running unit tests..."
	$(GOTEST) -v $(RACE_FLAG) -timeout $(TEST_TIMEOUT) $(PKG_DIRS)

.PHONY: test-coverage
test-coverage: ## Run unit tests with coverage report
	@echo "Running unit tests with coverage..."
	$(GOTEST) -v $(RACE_FLAG) -timeout $(TEST_TIMEOUT) -coverprofile=$(COVERAGE_OUT) -covermode=atomic $(PKG_DIRS)
	@echo "Coverage report generated: $(COVERAGE_OUT)"
	@echo "To view HTML coverage report, run: make test-coverage-html"

.PHONY: test-coverage-html
test-coverage-html: test-coverage ## Generate HTML coverage report
	@echo "Generating HTML coverage report..."
	$(GOCMD) tool cover -html=$(COVERAGE_OUT) -o $(COVERAGE_HTML)
	@echo "HTML coverage report generated: $(COVERAGE_HTML)"

.PHONY: image-integration-test
image-integration-test: ## 🔨 Build integration test image with envtest
	@bash scripts/build-integration-image.sh

.PHONY: test-integration
test-integration: ## 🐳 Run integration tests (requires Docker/Podman)
	@TEST_TIMEOUT=$(TEST_TIMEOUT) bash scripts/run-integration-tests.sh

# Run integration tests using K3s strategy (privileged, more realistic Kubernetes)
# This uses testcontainers to spin up a real K3s cluster
# NOTE: Requires privileged containers, may not work in all CI/CD environments
.PHONY: test-integration-k3s
test-integration-k3s: ## 🚀 Run integration tests with K3s (faster, may need privileges)
	@TEST_TIMEOUT=$(TEST_TIMEOUT) bash scripts/run-k3s-tests.sh

.PHONY: test-all
test-all: test test-integration lint ## ✅ Run ALL tests (unit + integration + lint)
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@echo "✅ All tests completed successfully!"
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

.PHONY: lint
lint: ## Run golangci-lint
	@echo "Running golangci-lint..."
	@if command -v golangci-lint > /dev/null; then \
		golangci-lint cache clean && golangci-lint run; \
	else \
		echo "Error: golangci-lint not found. Please install it:"; \
		echo "  go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
		exit 1; \
	fi

.PHONY: fmt
fmt: ## Format code with gofmt and goimports
	@echo "Formatting code..."
	@if command -v $(GOIMPORTS) > /dev/null; then \
		$(GOIMPORTS) -w .; \
	else \
		$(GOFMT) -w .; \
	fi

.PHONY: mod-tidy
mod-tidy: ## Tidy Go module dependencies
	@echo "Tidying Go modules..."
	$(GOMOD) tidy
	$(GOMOD) verify

.PHONY: binary
binary: ## Build binary
	@echo "Building $(PROJECT_NAME)..."
	@echo "Version: $(VERSION), Commit: $(GIT_COMMIT), BuildDate: $(BUILD_DATE)"
	@mkdir -p bin
	CGO_ENABLED=0 $(GOBUILD) -ldflags="$(LDFLAGS)" -o bin/$(PROJECT_NAME) ./cmd/adapter

.PHONY: clean
clean: ## Clean build artifacts and test coverage files
	@echo "Cleaning..."
	rm -rf bin/
	rm -f $(COVERAGE_OUT) $(COVERAGE_HTML)

.PHONY: image
image: ## Build container image with Docker or Podman
ifeq ($(CONTAINER_RUNTIME),none)
	@echo "❌ ERROR: No container runtime found"
	@echo "Please install Docker or Podman"
	@exit 1
else
	@echo "Building container image with $(CONTAINER_RUNTIME)..."
	$(CONTAINER_CMD) build -t $(PROJECT_NAME):$(VERSION) .
	@echo "✅ Image built: $(PROJECT_NAME):$(VERSION)"
endif

.PHONY: verify
verify: lint test ## Run all verification checks (lint + test)
