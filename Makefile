# Makefile for hyperfleet-adapter

# Project metadata
PROJECT_NAME := hyperfleet-adapter
VERSION ?= 0.0.1
IMAGE_REGISTRY ?= quay.io/openshift-hyperfleet
IMAGE_TAG ?= latest

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

# Directories
# Find all Go packages, excluding vendor and test directories
PKG_DIRS := $(shell $(GOCMD) list ./... 2>/dev/null | grep -v /vendor/ | grep -v /test/ || echo "./...")

.PHONY: help
help: ## Display this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

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

.PHONY: test-integration
test-integration: ## Run integration tests with testcontainers (works in both Prow and local environments)
	@echo "Running integration tests with testcontainers..."
	@if command -v podman > /dev/null && [ -z "$$DOCKER_HOST" ]; then \
		echo "Detected podman environment (likely Prow), setting up podman system service..."; \
		podman system service --time=0 & || true; \
		sleep 2; \
		DOCKER_HOST=$$(podman info --format 'unix://{{.Host.RemoteSocket.Path}}'); \
		export DOCKER_HOST; \
		export TESTCONTAINERS_RYUK_DISABLED=true; \
		echo "DOCKER_HOST set to: $$DOCKER_HOST"; \
		$(GOTEST) -v -tags=integration ./test/integration/... -timeout $(TEST_TIMEOUT); \
	else \
		echo "Using existing Docker/Podman setup..."; \
		$(GOTEST) -v -tags=integration ./test/integration/... -timeout $(TEST_TIMEOUT); \
	fi

.PHONY: test-all
test-all: test test-integration ## Run both unit and integration tests

.PHONY: lint
lint: ## Run golangci-lint
	@echo "Running golangci-lint..."
	@if command -v golangci-lint > /dev/null; then \
		golangci-lint run; \
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

.PHONY: build
build: ## Build binary
	@echo "Building $(PROJECT_NAME)..."
	@mkdir -p bin
	CGO_ENABLED=0 $(GOBUILD) -ldflags="-w -s" -o bin/$(PROJECT_NAME) ./cmd/adapter

.PHONY: clean
clean: ## Clean build artifacts and test coverage files
	@echo "Cleaning..."
	rm -rf bin/
	rm -f $(COVERAGE_OUT) $(COVERAGE_HTML)

.PHONY: docker-build
docker-build: ## Build Docker image
	@echo "Building Docker image..."
	docker build -t $(IMAGE_REGISTRY)/$(PROJECT_NAME):$(IMAGE_TAG) .
	@echo "Docker image built: $(IMAGE_REGISTRY)/$(PROJECT_NAME):$(IMAGE_TAG)"

.PHONY: docker-push
docker-push: ## Push Docker image
	@echo "Pushing Docker image..."
	docker push $(IMAGE_REGISTRY)/$(PROJECT_NAME):$(IMAGE_TAG)
	@echo "Docker image pushed: $(IMAGE_REGISTRY)/$(PROJECT_NAME):$(IMAGE_TAG)"

.PHONY: verify
verify: lint test ## Run all verification checks (lint + test)

