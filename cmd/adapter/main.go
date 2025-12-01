package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	"github.com/rh-amarin/hyperfleet-broker/broker"
)

// Build-time variables set via ldflags
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
	tag       = "none"
)

const shutdownTimeout = 30 * time.Second

// Command-line flags
var configPath string

func main() {
	// Define flags
	flag.StringVar(&configPath, "config", "", fmt.Sprintf("Path to adapter configuration file (can also use %s env var)", config_loader.EnvConfigPath))

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

	// Load adapter configuration
	// If configPath flag is empty, config_loader.Load will read from ADAPTER_CONFIG_PATH env var
	log.Info("Loading adapter configuration...")
	adapterConfig, err := config_loader.Load(configPath, config_loader.WithAdapterVersion(version))
	if err != nil {
		log.Error(fmt.Sprintf("Failed to load adapter configuration: %v", err))
		return fmt.Errorf("failed to load adapter configuration: %w", err)
	}
	log.Infof("Adapter configuration loaded successfully: name=%s namespace=%s",
		adapterConfig.Metadata.Name, adapterConfig.Metadata.Namespace)

	// Verify API base URL is configured
	apiBaseURL := hyperfleet_api.BaseURLFromEnv()
	if apiBaseURL == "" {
		log.Error(fmt.Sprintf("HyperFleet API base URL not configured. Set %s environment variable", hyperfleet_api.EnvBaseURL))
		return fmt.Errorf("HyperFleet API base URL not configured")
	}
	log.Infof("HyperFleet API client configured: baseURL=%s timeout=%s retryAttempts=%d",
		apiBaseURL, adapterConfig.Spec.HyperfleetAPI.Timeout, adapterConfig.Spec.HyperfleetAPI.RetryAttempts)

	// Create HyperFleet API client from config
	// The client is stateless and safe to reuse across messages.
	// Each API call receives the message-specific context for proper isolation.
	log.Info("Creating HyperFleet API client...")
	apiClient, err := createAPIClient(adapterConfig.Spec.HyperfleetAPI)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to create HyperFleet API client: %v", err))
		return fmt.Errorf("failed to create HyperFleet API client: %w", err)
	}

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

	log.Info("Creating broker subscriber...")
	subscriber, err := broker.NewSubscriber("subscription1")
	if err != nil {
		log.Error(fmt.Sprintf("Failed to create subscriber: %v", err))
		return fmt.Errorf("failed to create subscriber: %w", err)
	}
	log.Info("Broker subscriber created successfully")

	handler := func(ctx context.Context, evt *event.Event) error {
		log.Infof("event %v", evt)
		log.Infof("Received event: id=%s type=%s source=%s data=%s", evt.ID(), evt.Type(), evt.Source(), string(evt.Data()))

		// TODO: Process event using adapterConfig and apiClient
		// Each API call MUST use ctx (the message context) for proper isolation:
		//   resp, err := apiClient.Get(ctx, url)  // ctx ensures per-message timeout/cancellation
		//
		// 1. Extract params from event data using adapterConfig.Spec.Params
		// 2. Execute preconditions using adapterConfig.Spec.Preconditions
		//    - Make API calls using apiClient.Get(ctx, ...)/Post(ctx, ...)/etc.
		//    - Extract response fields and evaluate conditions
		// 3. Create/update Kubernetes resources using adapterConfig.Spec.Resources
		// 4. Execute post actions using adapterConfig.Spec.Post.PostActions
		//    - Report status back to HyperFleet API using apiClient

		// Reference config and client to avoid unused variable warnings
		_ = adapterConfig
		_ = apiClient

		log.Info("Event processed successfully")
		return nil
	}

	log.Info("Subscribing to broker topic...")
	err = subscriber.Subscribe(ctx, "topic-adapter-test", handler)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to subscribe to topic: %v", err))
		return fmt.Errorf("failed to subscribe to topic: %w", err)
	}
	log.Info("Successfully subscribed to broker topic")

	// Channel to signal fatal errors from the errors goroutine
	fatalErrCh := make(chan error, 1)

	// Monitor subscription errors channel in a separate goroutine
	go func() {
		for subErr := range subscriber.Errors() {
			log.Error(fmt.Sprintf("Subscription error: %v", subErr))
			// For critical errors, signal shutdown
			// You can add more sophisticated error classification here
			select {
			case fatalErrCh <- subErr:
				// Signal sent, trigger shutdown
			default:
				// Channel already has an error, don't block
			}
		}
	}()

	log.Info("Adapter started, waiting for events...")

	// Wait for shutdown signal or fatal subscription error
	select {
	case <-ctx.Done():
		log.Info("Context cancelled, shutting down...")
	case err := <-fatalErrCh:
		log.Error(fmt.Sprintf("Fatal subscription error, shutting down: %v", err))
		cancel() // Cancel context to trigger graceful shutdown
	}

	// Give a small grace period for in-flight messages to complete
	log.Info("Initiating graceful shutdown...")
	time.Sleep(time.Second)
	log.Info("Adapter shutdown complete")

	return nil
}

// createAPIClient creates a HyperFleet API client from the config
func createAPIClient(apiConfig config_loader.HyperfleetAPIConfig) (hyperfleet_api.Client, error) {
	var opts []hyperfleet_api.ClientOption

	// Parse and set timeout using the accessor method
	timeout, err := apiConfig.ParseTimeout()
	if err != nil {
		return nil, fmt.Errorf("invalid timeout %q: %w", apiConfig.Timeout, err)
	}
	if timeout > 0 {
		opts = append(opts, hyperfleet_api.WithTimeout(timeout))
	}

	// Set retry attempts
	if apiConfig.RetryAttempts > 0 {
		opts = append(opts, hyperfleet_api.WithRetryAttempts(apiConfig.RetryAttempts))
	}

	// Parse and set retry backoff strategy
	if apiConfig.RetryBackoff != "" {
		backoff := hyperfleet_api.BackoffStrategy(apiConfig.RetryBackoff)
		switch backoff {
		case hyperfleet_api.BackoffExponential, hyperfleet_api.BackoffLinear, hyperfleet_api.BackoffConstant:
			opts = append(opts, hyperfleet_api.WithRetryBackoff(backoff))
		default:
			return nil, fmt.Errorf("invalid retry backoff strategy %q (supported: exponential, linear, constant)", apiConfig.RetryBackoff)
		}
	}

	return hyperfleet_api.NewClient(opts...), nil
}
