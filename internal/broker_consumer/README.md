# Broker Consumer

This package provides a simple wrapper around the `hyperfleet-broker` library for consuming messages from Google Pub/Sub.

## Overview

The broker_consumer package integrates with the [hyperfleet-broker](https://github.com/openshift-hyperfleet/hyperfleet-broker) library to provide a unified interface for consuming CloudEvents.

## Features

- **Broker Abstraction**: Wraps hyperfleet-broker for easy usage.
- **CloudEvents 1.0**: Consumes CloudEvents-compliant messages.
- **Environment Configuration**: Configured via environment variables and config file.
- **Graceful Shutdown**: Supports context-based cancellation and graceful shutdown.

## Usage

### Basic Example

```go
package main

import (
    "context"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/cloudevents/sdk-go/v2/event"
    "github.com/golang/glog"
    brokerconsumer "github.com/openshift-hyperfleet/hyperfleet-adapter/internal/broker_consumer"
)

func main() {
    // Create message handler
    handler := func(ctx context.Context, evt *event.Event) error {
        glog.Infof("Received event: type=%s, source=%s, id=%s",
            evt.Type(), evt.Source(), evt.ID())
        
        // Return nil to acknowledge successful processing
        // Return error to nack and potentially requeue
        return nil
    }

    // Create subscriber from environment variables
    // Automatically reads BROKER_SUBSCRIPTION_ID from env if empty string passed
    subscriber, err := brokerconsumer.NewSubscriber("")
    if err != nil {
        glog.Fatalf("Failed to create subscriber: %v", err)
    }
    defer subscriber.Close()

    // Setup graceful shutdown
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Subscribe to topic/subscription
    // In Google Pub/Sub, the topic argument corresponds to the subscription name
    subscriptionID := os.Getenv("BROKER_SUBSCRIPTION_ID")
    
    if err := brokerconsumer.Subscribe(ctx, subscriber, subscriptionID, handler); err != nil {
        glog.Fatalf("Failed to subscribe: %v", err)
    }

    glog.Info("Subscriber started, waiting for messages...")

    // Wait for shutdown signal
    // ...
}
```

## Configuration

The broker consumer is configured via **environment variables** and a **configuration file**.

**Important**: The `hyperfleet-broker` library currently requires `BROKER_CONFIG_FILE` environment variable to point to a valid YAML configuration file, even if you override all values via environment variables.

### Environment Variables

**Required:**
- `BROKER_CONFIG_FILE`: Path to config file (e.g., `/app/config/broker.yaml`)
- `BROKER_TYPE`: `googlepubsub`
- `BROKER_GOOGLEPUBSUB_PROJECT_ID`: GCP project ID
- `BROKER_SUBSCRIPTION_ID`: The subscription ID to consume from

**Optional:**
- `SUBSCRIBER_PARALLELISM`: Number of parallel message handlers (default: 1)

## CloudEvent Format

Messages are expected to be CloudEvents 1.0 compliant:

```json
{
  "specversion": "1.0",
  "type": "com.hyperfleet.cluster.provisioning.requested",
  "source": "https://hyperfleet.redhat.com/api/v1/clusters",
  "id": "A234-1234-1234",
  "time": "2025-01-21T12:00:00Z",
  "datacontenttype": "application/json",
  "data": {
    "clusterId": "cluster-123",
    "action": "provision"
  }
}
```

## Dependencies

This package depends on:

- [hyperfleet-broker](https://github.com/openshift-hyperfleet/hyperfleet-broker)
- [CloudEvents SDK](https://github.com/cloudevents/sdk-go)
