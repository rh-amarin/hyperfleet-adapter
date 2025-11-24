# Integration Testing Guide

## Overview
This directory contains integration tests for the **adapter-specific** functionality of the broker_consumer wrapper in HyperFleet Adapter.

### ⚠️ Important: Test Scope
These tests focus **ONLY on adapter-specific logic**, not broker functionality:
- ✅ Environment variable configuration (`BROKER_SUBSCRIPTION_ID`)
- ✅ Error wrapping and propagation
- ✅ Basic smoke test that the wrapper delegates correctly

**Comprehensive broker testing** (publish/subscribe, acknowledgement, shared subscriptions, error handling, performance, etc.) is done in the [hyperfleet-broker](https://github.com/openshift-hyperfleet/hyperfleet-broker) library. We do NOT duplicate those tests here.

## File Structure

The test suite is organized into logical components:

### Core Test Files
- **`adapter_integration_test.go`**: Main entry point with adapter-specific integration tests
  - Documentation about test scope and coverage
  - Environment variable fallback logic tests
  - Basic smoke test for wrapper functionality

### Test Infrastructure
- **`setup_test.go`**: Test fixtures and setup utilities
  - `TestMain`: Pre-test validation and environment setup
  - `setupTestEnvironment`: Config file and env var setup
- **`testutil_container.go`**: Pub/Sub emulator container management
- **`testutil_publisher.go`**: Test message publishing utilities

### Documentation
- **`README.md`**: This file - testing guide and troubleshooting

## Prerequisites
- Docker or Podman running locally.
- Internet connection (for first run to download emulator images).

## Running Tests
Use the Makefile target:
```bash
make test-integration
```
Or run specific tests:
```bash
go test -v -tags=integration ./test/integration/broker_consumer/...
```

## Configuration
Tests use `testcontainers-go` to spin up a Google Pub/Sub emulator.
- **Image**: `gcr.io/google.com/cloudsdktool/cloud-sdk:emulators` (~2GB)
- **Timeout**: Startup timeout is set to 120s to allow for image download.

## Troubleshooting
If tests hang on "Creating container...", ensure your container runtime is healthy.
If you see `unsupported broker type`, ensure `BROKER_CONFIG_FILE` points to a valid file (even if dummy) because the library currently requires it for initialization.

## Notes on Env Vars
The `hyperfleet-broker` library requires a config file to be present to initialize correctly. Environment variables (e.g. `BROKER_GOOGLEPUBSUB_PROJECT_ID`) successfully override values in the config file, but cannot currently replace the file entirely.

## What We Test vs What Broker Tests

### Adapter Tests (this directory)
- ✅ Environment variable configuration: `BROKER_SUBSCRIPTION_ID`
- ✅ Error wrapping and message formatting
- ✅ Basic smoke test that wrapper works

### Broker Library Tests (hyperfleet-broker)
- ✅ Publish/Subscribe functionality
- ✅ Message acknowledgement
- ✅ Multiple events
- ✅ Shared subscriptions (load balancing)
- ✅ Fanout subscriptions
- ✅ Error handling
- ✅ Configuration mechanisms
- ✅ Performance testing
- ✅ Slow subscriber handling
- ✅ Panic recovery

**The adapter is a thin wrapper** - we trust the broker library's comprehensive tests and only test our specific additions.
