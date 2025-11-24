package broker_consumer

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/golang/glog"
	"github.com/openshift-hyperfleet/hyperfleet-broker/broker"
)

const (
	// maxRetries is the number of retry attempts for subscriber creation and subscription
	maxRetries = 3
	// retryDelay is the base delay between retry attempts
	retryDelay = 1 * time.Second
)

// Subscriber wraps the hyperfleet-broker Subscriber interface
type Subscriber = broker.Subscriber

// CloudEvent wraps the CloudEvents SDK Event type
type CloudEvent = event.Event

// HandlerFunc wraps the hyperfleet-broker HandlerFunc type
type HandlerFunc = broker.HandlerFunc

// NewSubscriber creates a new subscriber from environment variables
// using the hyperfleet-broker library.
//
// The subscriptionID parameter specifies which subscription/queue to consume from.
// If empty, it will read from BROKER_SUBSCRIPTION_ID environment variable.
//
// Configuration is loaded from environment variables:
//   - BROKER_SUBSCRIPTION_ID: subscription/queue name (if subscriptionID parameter is empty)
//   - BROKER_TYPE: "rabbitmq" or "googlepubsub"
//   - For RabbitMQ: BROKER_RABBITMQ_URL
//   - For Google Pub/Sub: BROKER_GOOGLEPUBSUB_PROJECT_ID
//   - SUBSCRIBER_PARALLELISM: number of parallel workers (default: 1)
//
// Example:
//   sub, err := broker_consumer.NewSubscriber("my-subscription")
//   if err != nil {
//       log.Fatal(err)
//   }
//   defer sub.Close()
//   
//   handler := func(ctx context.Context, evt *event.Event) error {
//       // Process the event
//       return nil
//   }
//   
//   err = sub.Subscribe(ctx, "my-topic", handler)

func subscriptionIDFromEnv() string {
	return os.Getenv("BROKER_SUBSCRIPTION_ID")
}
func topicFromEnv() string {
	subscriptionTopic := os.Getenv("TOPIC")
	if subscriptionTopic == "" {
		subscriptionTopic = os.Getenv("BROKER_TOPIC")
	}
	return subscriptionTopic
}
func NewSubscriber(subscriptionID string) (Subscriber, string, error) {
	if subscriptionID == "" {
		subscriptionID = subscriptionIDFromEnv()
	}
	if subscriptionID == "" {
		return nil, "", fmt.Errorf("subscriptionID is required (pass as parameter or set BROKER_SUBSCRIPTION_ID environment variable)")
	}
	brokerType := os.Getenv("BROKER_TYPE")
	glog.Infof("Creating %s subscriber for subscription: %s", brokerType, subscriptionID)

	// Use hyperfleet-broker to create the subscriber with retry logic
	// Configuration is read from environment variables by the broker library
	var subscriber Subscriber
	var err error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		subscriber, err = broker.NewSubscriber(subscriptionID)
		if err == nil {
				glog.Info("Broker subscriber created successfully")
			return subscriber, subscriptionID, nil
			}

		if attempt < maxRetries {
			glog.Warningf("Failed to create subscriber (attempt %d/%d): %v. Retrying in %v...", attempt, maxRetries, err, retryDelay)
			time.Sleep(retryDelay)
		}
	}

	return nil, subscriptionID, fmt.Errorf("failed to create broker subscriber after %d attempts: %w", maxRetries, err)
}

// Subscribe is a helper function to subscribe to a topic with a handler
func Subscribe(ctx context.Context, subscriber Subscriber, subscriptionTopic string, handler HandlerFunc) error {
	if subscriptionTopic == "" {
		subscriptionTopic = topicFromEnv()
	}
	glog.Infof("Subscribing to topic: %s", subscriptionTopic)
	
	var err error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Check if context is already cancelled before attempting
		if ctx.Err() != nil {
			return ctx.Err()
		}

		err = subscriber.Subscribe(ctx, subscriptionTopic, handler)
		if err == nil {
			glog.Infof("Successfully subscribed to topic: %s", subscriptionTopic)
			return nil
		}

		if attempt < maxRetries {
			glog.Warningf("Failed to subscribe to topic %s (attempt %d/%d): %v. Retrying in %v...", subscriptionTopic, attempt, maxRetries, err, retryDelay)
			// Use select to respect context cancellation during sleep
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryDelay):
				// Continue to next retry
			}
		}
	}

	return fmt.Errorf("failed to subscribe to topic %s after %d attempts: %w", subscriptionTopic, maxRetries, err)
}

// Close is a helper function to close a subscriber gracefully
func Close(subscriber Subscriber) error {
	glog.Info("Closing broker subscriber...")
	
	if err := subscriber.Close(); err != nil {
		return fmt.Errorf("failed to close subscriber: %w", err)
	}

	glog.Info("Broker subscriber closed successfully")
	return nil
}

