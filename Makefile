# Makefile for hyperfleet-adapter

# Project metadata
PROJECT_NAME := hyperfleet-adapter
VERSION ?= 0.1.0
IMAGE_REGISTRY ?= quay.io/openshift-hyperfleet
IMAGE_TAG ?= latest

# Build metadata
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GIT_TAG := $(shell git describe --tags --exact-match 2>/dev/null || echo "")

# Dev image configuration - set QUAY_USER to push to personal registry
# Usage: QUAY_USER=myuser make image-dev
QUAY_USER ?=
DEV_TAG ?= dev-$(GIT_COMMIT)
BUILD_DATE := $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')

# LDFLAGS for build
# Note: Variables are in package main, so use main.varName (not full import path)
LDFLAGS := -w -s
LDFLAGS += -X main.version=$(VERSION)
LDFLAGS += -X main.commit=$(GIT_COMMIT)
LDFLAGS += -X main.buildDate=$(BUILD_DATE)
ifneq ($(GIT_TAG),)
LDFLAGS += -X main.tag=$(GIT_TAG)
endif

# Go parameters
GOCMD := go
GOBUILD := $(GOCMD) build
GOTEST := $(GOCMD) test
GOMOD := $(GOCMD) mod
GOFMT := gofmt

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

# Include bingo-managed tool versions
include .bingo/Variables.mk

# Install directory (defaults to $GOPATH/bin or $HOME/go/bin)
GOPATH ?= $(shell $(GOCMD) env GOPATH)
BINDIR ?= $(GOPATH)/bin

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
lint: $(GOLANGCI_LINT) ## Run golangci-lint
	@echo "Running golangci-lint..."
	$(GOLANGCI_LINT) cache clean && $(GOLANGCI_LINT) run

.PHONY: fmt
fmt: $(GOIMPORTS) ## Format code with gofmt and goimports
	@echo "Formatting code..."
	$(GOIMPORTS) -w .

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

.PHONY: build
build: binary ## Alias for 'binary'

.PHONY: install
install: binary ## Install binary to BINDIR (default: $GOPATH/bin)
	@echo "Installing $(PROJECT_NAME) to $(BINDIR)..."
	@mkdir -p $(BINDIR)
	cp bin/$(PROJECT_NAME) $(BINDIR)/$(PROJECT_NAME)
	@echo "✅ Installed: $(BINDIR)/$(PROJECT_NAME)"

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
	$(CONTAINER_CMD) build --platform linux/amd64 --no-cache --build-arg GIT_COMMIT=$(GIT_COMMIT) -t $(IMAGE_REGISTRY)/$(PROJECT_NAME):$(IMAGE_TAG) .
	@echo "✅ Image built: $(IMAGE_REGISTRY)/$(PROJECT_NAME):$(IMAGE_TAG)"
endif

.PHONY: image-push
image-push: image ## Build and push container image to registry
ifeq ($(CONTAINER_RUNTIME),none)
	@echo "❌ ERROR: No container runtime found"
	@echo "Please install Docker or Podman"
	@exit 1
else
	@echo "Pushing image $(IMAGE_REGISTRY)/$(PROJECT_NAME):$(IMAGE_TAG)..."
	$(CONTAINER_CMD) push $(IMAGE_REGISTRY)/$(PROJECT_NAME):$(IMAGE_TAG)
	@echo "✅ Image pushed: $(IMAGE_REGISTRY)/$(PROJECT_NAME):$(IMAGE_TAG)"
endif

.PHONY: image-dev
image-dev: ## Build and push to personal Quay registry (requires QUAY_USER)
ifndef QUAY_USER
	@echo "❌ ERROR: QUAY_USER is not set"
	@echo ""
	@echo "Usage: QUAY_USER=myuser make image-dev"
	@echo ""
	@echo "This will build and push to: quay.io/$$QUAY_USER/$(PROJECT_NAME):$(DEV_TAG)"
	@exit 1
endif
ifeq ($(CONTAINER_RUNTIME),none)
	@echo "❌ ERROR: No container runtime found"
	@echo "Please install Docker or Podman"
	@exit 1
else
	@echo "Building dev image quay.io/$(QUAY_USER)/$(PROJECT_NAME):$(DEV_TAG)..."
	$(CONTAINER_CMD) build --platform linux/amd64 --build-arg BASE_IMAGE=alpine:3.21 --build-arg GIT_COMMIT=$(GIT_COMMIT) -t quay.io/$(QUAY_USER)/$(PROJECT_NAME):$(DEV_TAG) .
	@echo "Pushing dev image..."
	$(CONTAINER_CMD) push quay.io/$(QUAY_USER)/$(PROJECT_NAME):$(DEV_TAG)
	@echo ""
	@echo "✅ Dev image pushed: quay.io/$(QUAY_USER)/$(PROJECT_NAME):$(DEV_TAG)"
endif

.PHONY: test-helm
test-helm: ## 📊 Test Helm charts (lint, template, validate)
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@echo "Testing Helm charts..."
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@if ! command -v helm > /dev/null; then \
		echo "❌ ERROR: helm not found. Please install Helm:"; \
		echo "  brew install helm  # macOS"; \
		echo "  curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash  # Linux"; \
		exit 1; \
	fi
	@echo "📋 Linting Helm chart..."
	helm lint charts/
	@echo ""
	@echo "📋 Testing template rendering with default values..."
	helm template test-release charts/ > /dev/null
	@echo "✅ Default values template OK"
	@echo ""
	@echo "📋 Testing template with broker enabled..."
	helm template test-release charts/ \
		--set broker.create=true \
		--set broker.subscriptionId=test-sub \
		--set broker.topic=test-topic \
		--set broker.type=googlepubsub > /dev/null
	@echo "✅ Broker config template OK"
	@echo ""
	@echo "📋 Testing template with HyperFleet API config..."
	helm template test-release charts/ \
		--set hyperfleetApi.baseUrl=http://localhost:8000 \
		--set hyperfleetApi.version=v1 > /dev/null
	@echo "✅ HyperFleet API config template OK"
	@echo ""
	@echo "📋 Testing template with PDB enabled..."
	helm template test-release charts/ \
		--set podDisruptionBudget.enabled=true \
		--set podDisruptionBudget.minAvailable=1 > /dev/null
	@echo "✅ PDB config template OK"
	@echo ""
	@echo "📋 Testing template with adapter config..."
	helm template test-release charts/ \
		--set config.enabled=true \
		--set config.adapterType=example \
		--set config.adapterYaml="apiVersion: hyperfleet.redhat.com/v1alpha1" > /dev/null
	@echo "✅ Adapter config template OK"
	@echo ""
	@echo "📋 Testing template with autoscaling..."
	helm template test-release charts/ \
		--set autoscaling.enabled=true \
		--set autoscaling.minReplicas=2 \
		--set autoscaling.maxReplicas=5 > /dev/null
	@echo "✅ Autoscaling config template OK"
	@echo ""
	@echo "📋 Testing template with probes enabled..."
	helm template test-release charts/ \
		--set livenessProbe.enabled=true \
		--set readinessProbe.enabled=true \
		--set startupProbe.enabled=true > /dev/null
	@echo "✅ Probes config template OK"
	@echo ""
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@echo "✅ All Helm chart tests passed!"
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

.PHONY: verify
verify: lint test ## Run all verification checks (lint + test)
