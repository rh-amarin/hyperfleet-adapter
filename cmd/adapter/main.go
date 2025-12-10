package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/golang/glog"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/executor"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	"github.com/openshift-hyperfleet/hyperfleet-broker/broker"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Build-time variables set via ldflags
var (
	version   = "0.1.0"
	commit    = "none"
	buildDate = "unknown"
	tag       = "none"
)

// Command-line flags
var configPath string

// Environment variable names
const (
	EnvBrokerSubscriptionID = "BROKER_SUBSCRIPTION_ID"
	EnvBrokerTopic          = "BROKER_TOPIC"
)

func main() {
	// Set glog to log to stderr by default (required for containers with read-only filesystems)
	// This can be overridden by --logtostderr=false if needed
	_ = flag.Set("logtostderr", "true")

	// Root command
	rootCmd := &cobra.Command{
		Use:   "adapter",
		Short: "HyperFleet Adapter - event-driven Kubernetes resource manager",
		Long: `HyperFleet Adapter listens for events from a message broker and 
executes configured actions including Kubernetes resource management 
and HyperFleet API calls.`,
		// Disable default completion command
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
	}

	// Add glog flags to root command (so they work on all subcommands)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)

	// Serve command
	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the adapter and begin processing events",
		Long: `Start the HyperFleet adapter in serve mode. The adapter will:
- Connect to the configured message broker
- Subscribe to the specified topic
- Process incoming events according to the adapter configuration
- Execute Kubernetes operations and HyperFleet API calls`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe()
		},
	}

	// Add --config flag to serve command
	serveCmd.Flags().StringVarP(&configPath, "config", "c", "",
		fmt.Sprintf("Path to adapter configuration file (can also use %s env var)", config_loader.EnvConfigPath))

	// Version command
	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("HyperFleet Adapter\n")
			fmt.Printf("  Version:    %s\n", version)
			fmt.Printf("  Commit:     %s\n", commit)
			fmt.Printf("  Built:      %s\n", buildDate)
			fmt.Printf("  Tag:        %s\n", tag)
		},
	}

	// Add subcommands
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(versionCmd)

	// Execute
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// runServe contains the main application logic for the serve command
func runServe() error {
	// Flush logs when runServe() exits
	defer glog.Flush()

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
	log.Infof("HyperFleet API client configured: timeout=%s retryAttempts=%d",
		adapterConfig.Spec.HyperfleetAPI.Timeout,
		adapterConfig.Spec.HyperfleetAPI.RetryAttempts)

	// Create HyperFleet API client from config
	log.Info("Creating HyperFleet API client...")
	apiClient, err := createAPIClient(adapterConfig.Spec.HyperfleetAPI)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to create HyperFleet API client: %v", err))
		return fmt.Errorf("failed to create HyperFleet API client: %w", err)
	}

	// Create Kubernetes client
	// Uses KUBECONFIG env var if set, otherwise uses in-cluster config
	log.Info("Creating Kubernetes client...")
	k8sClient, err := k8s_client.NewClient(ctx, k8s_client.ClientConfig{}, log)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to create Kubernetes client: %v", err))
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Create the executor using the builder pattern
	log.Info("Creating event executor...")
	exec, err := executor.NewBuilder().
		WithAdapterConfig(adapterConfig).
		WithAPIClient(apiClient).
		WithK8sClient(k8sClient).
		WithLogger(log).
		Build()
	if err != nil {
		log.Error(fmt.Sprintf("Failed to create executor: %v", err))
		return fmt.Errorf("failed to create executor: %w", err)
	}

	// Create the event handler from the executor
	// This handler will:
	// 1. Extract params from event data
	// 2. Execute preconditions (API calls, condition checks)
	// 3. Create/update Kubernetes resources
	// 4. Execute post actions (status reporting)
	handler := exec.CreateHandler()

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

	// Get subscription ID from environment
	subscriptionID := os.Getenv(EnvBrokerSubscriptionID)
	if subscriptionID == "" {
		log.Error(fmt.Sprintf("%s environment variable is required", EnvBrokerSubscriptionID))
		return fmt.Errorf("%s environment variable is required", EnvBrokerSubscriptionID)
	}

	// Get topic from environment
	topic := os.Getenv(EnvBrokerTopic)
	if topic == "" {
		log.Error(fmt.Sprintf("%s environment variable is required", EnvBrokerTopic))
		return fmt.Errorf("%s environment variable is required", EnvBrokerTopic)
	}

	// Create broker subscriber
	// Configuration is loaded from environment variables by the broker library:
	//   - BROKER_TYPE: "rabbitmq" or "googlepubsub"
	//   - BROKER_GOOGLEPUBSUB_PROJECT_ID: GCP project ID (for googlepubsub)
	//   - BROKER_RABBITMQ_URL: RabbitMQ URL (for rabbitmq)
	//   - SUBSCRIBER_PARALLELISM: number of parallel workers (default: 1)
	log.Info("Creating broker subscriber...")
	subscriber, err := broker.NewSubscriber(subscriptionID)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to create subscriber: %v", err))
		return fmt.Errorf("failed to create subscriber: %w", err)
	}
	log.Info("Broker subscriber created successfully")

	// Subscribe to topic - this is NON-BLOCKING, it returns immediately after setup
	log.Info("Subscribing to broker topic...")
	err = subscriber.Subscribe(ctx, topic, handler)
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

	// Close subscriber gracefully with timeout
	log.Info("Closing broker subscriber...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Close subscriber in a goroutine with timeout
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- subscriber.Close()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			log.Error(fmt.Sprintf("Error closing subscriber: %v", err))
		} else {
			log.Info("Subscriber closed successfully")
		}
	case <-shutdownCtx.Done():
		log.Error("Subscriber close timed out after 30 seconds")
	}

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

	return hyperfleet_api.NewClient(opts...)
}
