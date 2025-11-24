package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/broker_consumer"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
)

// Build-time variables set via ldflags
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
	tag       = "none"
)

const shutdownTimeout = 30 * time.Second

func main() {
	// Initialize glog flags
	flag.Parse()

	// Run the application - logger.Flush() is deferred inside run()
	if err := run(); err != nil {
		// Error already logged in run(), exit with error code
		os.Exit(1)
	}
}

// run contains the main application logic and returns an error if the adapter fails.
// Separating this from main() allows defers to run properly before os.Exit().
func run() error {
	// Flush logs when run() exits - this runs before returning to main()
	defer logger.Flush()

	// Create context that cancels on system signals
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create logger with context
	log := logger.NewLogger(ctx)

	log.Infof("Starting Hyperfleet Adapter version=%s commit=%s built=%s tag=%s", version, commit, buildDate, tag)

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Infof("Received signal %s, initiating graceful shutdown...", sig)
		cancel()

		// Second signal forces immediate exit
		sig = <-sigCh
		log.Infof("Received second signal %s, forcing immediate exit", sig)
		os.Exit(1)
	}()

	// Create broker subscriber
	// This will automatically read BROKER_SUBSCRIPTION_ID and broker config from env vars
	subscriber, subscriptionID, err := broker_consumer.NewSubscriber("")
	if err != nil {
		log.Error(fmt.Sprintf("Failed to create subscriber: %v for subscription: %s", err, subscriptionID))
		return err
	}
	if subscriber == nil {
		log.Error(fmt.Sprintf("Subscriber is nil after creation for subscription: %s", subscriptionID))
		return fmt.Errorf("subscriber is nil for subscription: %s", subscriptionID)
	}

	defer func() {
		// Use a timeout for closing to prevent hanging forever
		closeCtx, closeCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer closeCancel()

		done := make(chan struct{})
		go func() {
			subscriber.Close()
			close(done)
		}()

		select {
		case <-done:
			log.Info("Subscriber closed successfully")
		case <-closeCtx.Done():
			log.Warning("Subscriber close timed out")
		}
	}()

	// Define event handler
	handler := func(ctx context.Context, evt *event.Event) error {
		log.Infof("Received event: id=%s type=%s source=%s data=%s", evt.ID(), evt.Type(), evt.Source(), string(evt.Data()))

		// TODO: Add your event processing logic here

		log.Info("Event processed successfully")
		return nil
	}

	// Subscribe and block until context is cancelled
	// Let the broker consumer determine the topic to subscribe to from BROKER_TOPIC environment variable
	if err := broker_consumer.Subscribe(ctx, subscriber, "", handler); err != nil {
		// Context cancellation is expected during graceful shutdown, not an error
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			log.Info("Subscription stopped due to context cancellation")
			// Not an error - graceful shutdown
		} else {
			// Actual error (e.g. connection failed)
			log.Error(fmt.Sprintf("Subscription failed: %v", err))
			return err
		}
	}

	log.Info("Waiting for graceful shutdown...")

	// Give a small grace period for in-flight messages to complete
	time.Sleep(time.Second)
	log.Info("Adapter shutdown complete")

	return nil
}
