package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/swf/loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/swf/runner"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/health"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/otel"
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
var (
	configPath string
	logLevel   string
	logFormat  string
	logOutput  string
)

// Environment variable names
const (
	EnvBrokerSubscriptionID = "BROKER_SUBSCRIPTION_ID"
	EnvBrokerTopic          = "BROKER_TOPIC"
)

// Timeout constants
const (
	// OTelShutdownTimeout is the timeout for gracefully shutting down the OpenTelemetry TracerProvider
	OTelShutdownTimeout = 5 * time.Second
	// HealthServerShutdownTimeout is the timeout for gracefully shutting down the health server
	HealthServerShutdownTimeout = 5 * time.Second
)

// Server port constants
const (
	// HealthServerPort is the port for /healthz and /readyz endpoints
	HealthServerPort = "8080"
	// MetricsServerPort is the port for /metrics endpoint
	MetricsServerPort = "9090"
)

func main() {
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

	// Add flags to root command (so they work on all subcommands)
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

	// Add logging flags to serve command
	serveCmd.Flags().StringVar(&logLevel, "log-level", "",
		"Log level (debug, info, warn, error). Env: LOG_LEVEL")
	serveCmd.Flags().StringVar(&logFormat, "log-format", "",
		"Log format (text, json). Env: LOG_FORMAT")
	serveCmd.Flags().StringVar(&logOutput, "log-output", "",
		"Log output (stdout, stderr). Env: LOG_OUTPUT")

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

// buildLoggerConfig creates a logger configuration from environment variables
// and command-line flags. Flags take precedence over environment variables.
func buildLoggerConfig(component string) logger.Config {
	cfg := logger.ConfigFromEnv()

	// Override with command-line flags if provided
	if logLevel != "" {
		cfg.Level = logLevel
	}
	if logFormat != "" {
		cfg.Format = logFormat
	}
	if logOutput != "" {
		cfg.Output = logOutput
	}

	cfg.Component = component
	cfg.Version = version

	return cfg
}

// runServe contains the main application logic for the serve command
func runServe() error {
	// Create context that cancels on system signals
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create bootstrap logger (before config is loaded)
	log, err := logger.NewLogger(buildLoggerConfig("hyperfleet-adapter"))
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}

	log.Infof(ctx, "Starting Hyperfleet Adapter version=%s commit=%s built=%s tag=%s", version, commit, buildDate, tag)

	// Load workflow configuration (supports both AdapterConfig and native SWF formats)
	// If configPath flag is empty, loader will read from ADAPTER_CONFIG_PATH env var
	log.Info(ctx, "Loading workflow configuration...")
	loadResult, err := loader.Load(configPath, loader.WithAdapterVersion(version))
	if err != nil {
		errCtx := logger.WithErrorField(ctx, err)
		log.Errorf(errCtx, "Failed to load workflow configuration")
		return fmt.Errorf("failed to load workflow configuration: %w", err)
	}

	// Extract metadata based on format
	var componentName, componentNamespace string
	var apiConfig config_loader.HyperfleetAPIConfig

	if loadResult.Format == loader.FormatAdapterConfig && loadResult.AdapterConfig != nil {
		// Legacy format - extract from AdapterConfig
		componentName = loadResult.AdapterConfig.Metadata.Name
		componentNamespace = loadResult.AdapterConfig.Metadata.Namespace
		apiConfig = loadResult.AdapterConfig.Spec.HyperfleetAPI
	} else {
		// Native SWF format - extract from workflow document
		componentName = loadResult.Workflow.Document.Name
		if ns, ok := loadResult.Workflow.Document.Metadata["namespace"].(string); ok {
			componentNamespace = ns
		} else {
			componentNamespace = loadResult.Workflow.Document.Namespace
		}
		// For native SWF, use default/environment-based API config
		apiConfig = config_loader.HyperfleetAPIConfig{
			Timeout:       "30s",
			RetryAttempts: 3,
			RetryBackoff:  "exponential",
		}
	}

	// Recreate logger with component name from config
	log, err = logger.NewLogger(buildLoggerConfig(componentName))
	if err != nil {
		return fmt.Errorf("failed to create logger with config: %w", err)
	}

	log.Infof(ctx, "Workflow configuration loaded successfully: name=%s namespace=%s format=%s",
		componentName, componentNamespace, loadResult.Format)
	log.Infof(ctx, "HyperFleet API client configured: timeout=%s retryAttempts=%d",
		apiConfig.Timeout, apiConfig.RetryAttempts)

	// Get trace sample ratio from environment (default: 10%)
	sampleRatio := otel.GetTraceSampleRatio(log, ctx)

	// Initialize OpenTelemetry for trace_id/span_id generation and HTTP propagation
	tp, err := otel.InitTracer(componentName, version, sampleRatio)
	if err != nil {
		errCtx := logger.WithErrorField(ctx, err)
		log.Errorf(errCtx, "Failed to initialize OpenTelemetry")
		return fmt.Errorf("failed to initialize OpenTelemetry: %w", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), OTelShutdownTimeout)
		defer shutdownCancel()
		if err := tp.Shutdown(shutdownCtx); err != nil {
			errCtx := logger.WithErrorField(shutdownCtx, err)
			log.Warnf(errCtx, "Failed to shutdown TracerProvider")
		}
	}()

	// Start health server immediately (readiness starts as false)
	healthServer := health.NewServer(log, HealthServerPort, componentName)
	if err := healthServer.Start(ctx); err != nil {
		errCtx := logger.WithErrorField(ctx, err)
		log.Errorf(errCtx, "Failed to start health server")
		return fmt.Errorf("failed to start health server: %w", err)
	}
	// Mark config as loaded since we got here successfully
	healthServer.SetConfigLoaded()
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), HealthServerShutdownTimeout)
		defer shutdownCancel()
		if err := healthServer.Shutdown(shutdownCtx); err != nil {
			errCtx := logger.WithErrorField(shutdownCtx, err)
			log.Warnf(errCtx, "Failed to shutdown health server")
		}
	}()

	// Start metrics server with build info
	metricsServer := health.NewMetricsServer(log, MetricsServerPort, health.MetricsConfig{
		Component: componentName,
		Version:   version,
		Commit:    commit,
	})
	if err := metricsServer.Start(ctx); err != nil {
		errCtx := logger.WithErrorField(ctx, err)
		log.Errorf(errCtx, "Failed to start metrics server")
		return fmt.Errorf("failed to start metrics server: %w", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), HealthServerShutdownTimeout)
		defer shutdownCancel()
		if err := metricsServer.Shutdown(shutdownCtx); err != nil {
			errCtx := logger.WithErrorField(shutdownCtx, err)
			log.Warnf(errCtx, "Failed to shutdown metrics server")
		}
	}()

	// Create HyperFleet API client from config
	log.Info(ctx, "Creating HyperFleet API client...")
	apiClient, err := createAPIClient(apiConfig, log)
	if err != nil {
		errCtx := logger.WithErrorField(ctx, err)
		log.Errorf(errCtx, "Failed to create HyperFleet API client")
		return fmt.Errorf("failed to create HyperFleet API client: %w", err)
	}

	// Create Kubernetes client
	// Uses KUBECONFIG env var if set, otherwise uses in-cluster config
	log.Info(ctx, "Creating Kubernetes client...")
	k8sClient, err := k8s_client.NewClient(ctx, k8s_client.ClientConfig{}, log)
	if err != nil {
		errCtx := logger.WithErrorField(ctx, err)
		log.Errorf(errCtx, "Failed to create Kubernetes client")
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Create the SWF-based workflow runner
	// This replaces the legacy executor with Serverless Workflow SDK execution
	log.Info(ctx, "Creating SWF workflow runner...")
	runnerBuilder := runner.NewBuilder().
		WithAPIClient(apiClient).
		WithK8sClient(k8sClient).
		WithLogger(log)

	// Use workflow directly for native SWF, or AdapterConfig for legacy format
	if loadResult.Format == loader.FormatAdapterConfig && loadResult.AdapterConfig != nil {
		runnerBuilder = runnerBuilder.WithAdapterConfig(loadResult.AdapterConfig)
	} else {
		runnerBuilder = runnerBuilder.WithWorkflow(loadResult.Workflow)
	}

	workflowRunner, err := runnerBuilder.Build()
	if err != nil {
		errCtx := logger.WithErrorField(ctx, err)
		log.Errorf(errCtx, "Failed to create workflow runner")
		return fmt.Errorf("failed to create workflow runner: %w", err)
	}

	// Create the event handler from the workflow runner
	// The SWF runner executes the 4-phase pipeline:
	// 1. Extract params from event data (hf:extract)
	// 2. Execute preconditions (hf:preconditions)
	// 3. Create/update Kubernetes resources (hf:resources)
	// 4. Execute post actions (hf:post)
	handler := workflowRunner.CreateHandler()

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Infof(ctx, "Received signal %s, initiating graceful shutdown...", sig)
		// Mark as not ready immediately per HyperFleet Graceful Shutdown Standard
		// This must happen BEFORE context cancellation to ensure /readyz returns 503
		log.Info(ctx, "Shutdown initiated, marking not ready")
		healthServer.SetShuttingDown(true)
		cancel()

		// Second signal forces immediate exit
		sig = <-sigCh
		log.Infof(ctx, "Received second signal %s, forcing immediate exit", sig)
		os.Exit(1)
	}()

	// Get subscription ID from environment
	subscriptionID := os.Getenv(EnvBrokerSubscriptionID)
	if subscriptionID == "" {
		err := fmt.Errorf("%s environment variable is required", EnvBrokerSubscriptionID)
		errCtx := logger.WithErrorField(ctx, err)
		log.Errorf(errCtx, "Missing required environment variable")
		return err
	}

	// Get topic from environment
	topic := os.Getenv(EnvBrokerTopic)
	if topic == "" {
		err := fmt.Errorf("%s environment variable is required", EnvBrokerTopic)
		errCtx := logger.WithErrorField(ctx, err)
		log.Errorf(errCtx, "Missing required environment variable")
		return err
	}

	// Create broker subscriber
	// Configuration is loaded from environment variables by the broker library:
	//   - BROKER_TYPE: "rabbitmq" or "googlepubsub"
	//   - BROKER_GOOGLEPUBSUB_PROJECT_ID: GCP project ID (for googlepubsub)
	//   - BROKER_RABBITMQ_URL: RabbitMQ URL (for rabbitmq)
	//   - SUBSCRIBER_PARALLELISM: number of parallel workers (default: 1)
	log.Info(ctx, "Creating broker subscriber...")
	subscriber, err := broker.NewSubscriber(log, subscriptionID)
	if err != nil {
		errCtx := logger.WithErrorField(ctx, err)
		log.Errorf(errCtx, "Failed to create subscriber")
		return fmt.Errorf("failed to create subscriber: %w", err)
	}
	log.Info(ctx, "Broker subscriber created successfully")

	// Subscribe to topic - this is NON-BLOCKING, it returns immediately after setup
	log.Info(ctx, "Subscribing to broker topic...")
	err = subscriber.Subscribe(ctx, topic, handler)
	if err != nil {
		errCtx := logger.WithErrorField(ctx, err)
		log.Errorf(errCtx, "Failed to subscribe to topic")
		return fmt.Errorf("failed to subscribe to topic: %w", err)
	}
	log.Info(ctx, "Successfully subscribed to broker topic")

	// Mark as ready now that broker subscription is established
	healthServer.SetBrokerReady(true)
	log.Info(ctx, "Adapter is ready to process events")

	// Channel to signal fatal errors from the errors goroutine
	fatalErrCh := make(chan error, 1)

	// Monitor subscription errors channel in a separate goroutine.
	// Note: Error context here reflects the handler's location, not the error's origin
	// in the broker library. Stack traces (if captured) would show this goroutine's
	// call stack. For richer error context, the broker library would need to provide
	// errors with embedded stack traces or structured error details.
	go func() {
		for subErr := range subscriber.Errors() {
			errCtx := logger.WithErrorField(ctx, subErr)
			log.Errorf(errCtx, "Subscription error")
			// For critical errors, signal shutdown
			select {
			case fatalErrCh <- subErr:
				// Signal sent, trigger shutdown
			default:
				// Channel already has an error, don't block
			}
		}
	}()

	log.Info(ctx, "Adapter started, waiting for events...")

	// Wait for shutdown signal or fatal subscription error
	select {
	case <-ctx.Done():
		log.Info(ctx, "Context cancelled, shutting down...")
	case err := <-fatalErrCh:
		errCtx := logger.WithErrorField(ctx, err)
		log.Errorf(errCtx, "Fatal subscription error, shutting down")
		// Mark as not ready before shutdown per HyperFleet Graceful Shutdown Standard
		healthServer.SetShuttingDown(true)
		cancel() // Cancel context to trigger graceful shutdown
	}

	// Close subscriber gracefully with timeout
	log.Info(ctx, "Closing broker subscriber...")
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
			errCtx := logger.WithErrorField(ctx, err)
			log.Errorf(errCtx, "Error closing subscriber")
		} else {
			log.Info(ctx, "Subscriber closed successfully")
		}
	case <-shutdownCtx.Done():
		err := fmt.Errorf("subscriber close timed out after 30 seconds")
		errCtx := logger.WithErrorField(ctx, err)
		log.Error(errCtx, "Subscriber close timed out")
	}

	log.Info(ctx, "Adapter shutdown complete")

	return nil
}

// createAPIClient creates a HyperFleet API client from the config
func createAPIClient(apiConfig config_loader.HyperfleetAPIConfig, log logger.Logger) (hyperfleet_api.Client, error) {
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

	return hyperfleet_api.NewClient(log, opts...)
}
