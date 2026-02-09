package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/client_factory"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/executor"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/maestro_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/health"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/otel"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/version"
	"github.com/openshift-hyperfleet/hyperfleet-broker/broker"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

// Command-line flags
var (
	configPath     string // Path to deployment config (adapter-config.yaml)
	taskConfigPath string // Path to task config (adapter-task-config.yaml)
	logLevel       string
	logFormat      string
	logOutput      string
	serveFlags     *pflag.FlagSet
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

	// Add config flags to serve command
	serveCmd.Flags().StringVarP(&configPath, "config", "c", "",
		fmt.Sprintf("Path to adapter deployment config file (can also use %s env var)", config_loader.EnvAdapterConfig))
	serveCmd.Flags().StringVarP(&taskConfigPath, "task-config", "t", "",
		fmt.Sprintf("Path to adapter task config file (can also use %s env var)", config_loader.EnvTaskConfigPath))
	serveFlags = serveCmd.Flags()

	// Add Maestro override flags
	serveCmd.Flags().String("maestro-grpc-server-address", "", "Maestro gRPC server address")
	serveCmd.Flags().String("maestro-http-server-address", "", "Maestro HTTP server address")
	serveCmd.Flags().String("maestro-source-id", "", "Maestro source ID")
	serveCmd.Flags().String("maestro-client-id", "", "Maestro client ID")
	serveCmd.Flags().String("maestro-ca-file", "", "Maestro CA certificate file")
	serveCmd.Flags().String("maestro-cert-file", "", "Maestro client certificate file")
	serveCmd.Flags().String("maestro-key-file", "", "Maestro client key file")
	serveCmd.Flags().String("maestro-timeout", "", "Maestro client timeout")
	serveCmd.Flags().Bool("maestro-insecure", false, "Use insecure connection to Maestro")

	// Add HyperFleet API override flags
	serveCmd.Flags().String("hyperfleet-api-timeout", "", "HyperFleet API timeout")
	serveCmd.Flags().Int("hyperfleet-api-retry", 0, "HyperFleet API retry attempts")

	// Add config debug override flags
	serveCmd.Flags().Bool("debug-config", false,
		"Log the full merged configuration after load. Env: HYPERFLEET_DEBUG_CONFIG")

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
			info := version.Info()
			fmt.Printf("HyperFleet Adapter\n")
			fmt.Printf("  Version:    %s\n", info.Version)
			fmt.Printf("  Commit:     %s\n", info.Commit)
			fmt.Printf("  Built:      %s\n", info.BuildDate)
			fmt.Printf("  Tag:        %s\n", info.Tag)
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
	cfg.Version = version.Version

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

	log.Infof(ctx, "Starting Hyperfleet Adapter version=%s commit=%s built=%s tag=%s", version.Version, version.Commit, version.BuildDate, version.Tag)

	// Load unified configuration (deployment + task configs)
	log.Info(ctx, "Loading adapter configuration...")
	config, err := config_loader.LoadConfig(
		config_loader.WithAdapterConfigPath(configPath),
		config_loader.WithTaskConfigPath(taskConfigPath),
		config_loader.WithAdapterVersion(version.Version),
		config_loader.WithFlags(serveFlags),
	)
	if err != nil {
		errCtx := logger.WithErrorField(ctx, err)
		log.Errorf(errCtx, "Failed to load adapter configuration")
		return fmt.Errorf("failed to load adapter configuration: %w", err)
	}

	// Recreate logger with component name from config
	log, err = logger.NewLogger(buildLoggerConfig(config.Metadata.Name))
	if err != nil {
		return fmt.Errorf("failed to create logger with adapter config: %w", err)
	}

	log.Infof(ctx, "Adapter configuration loaded successfully: name=%s ",
		config.Metadata.Name)
	log.Infof(ctx, "HyperFleet API client configured: timeout=%s retryAttempts=%d",
		config.Spec.Clients.HyperfleetAPI.Timeout.String(),
		config.Spec.Clients.HyperfleetAPI.RetryAttempts)
	if config.Spec.DebugConfig {
		configBytes, err := yaml.Marshal(config)
		if err != nil {
			errCtx := logger.WithErrorField(ctx, err)
			log.Warnf(errCtx, "Failed to marshal adapter configuration for logging")
		} else {
			log.Infof(ctx, "Loaded adapter configuration:\n%s", string(configBytes))
		}
	}

	// Get trace sample ratio from environment (default: 10%)
	sampleRatio := otel.GetTraceSampleRatio(log, ctx)

	// Initialize OpenTelemetry for trace_id/span_id generation and HTTP propagation
	tp, err := otel.InitTracer(config.Metadata.Name, version.Version, sampleRatio)
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
	healthServer := health.NewServer(log, HealthServerPort, config.Metadata.Name)
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
		Component: config.Metadata.Name,
		Version:   version.Version,
		Commit:    version.Commit,
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
	apiClient, err := client_factory.CreateAPIClient(config.Spec.Clients.HyperfleetAPI, log)
	if err != nil {
		errCtx := logger.WithErrorField(ctx, err)
		log.Errorf(errCtx, "Failed to create HyperFleet API client")
		return fmt.Errorf("failed to create HyperFleet API client: %w", err)
	}

	// Create Kubernetes client
	log.Info(ctx, "Creating Kubernetes client...")
	k8sClient, err := client_factory.CreateK8sClient(ctx, config.Spec.Clients.Kubernetes, log)
	if err != nil {
		errCtx := logger.WithErrorField(ctx, err)
		log.Errorf(errCtx, "Failed to create Kubernetes client")
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Create Maestro client if configured
	var maestroClient maestro_client.ManifestWorkClient
	if config.Spec.Clients.Maestro != nil {
		log.Info(ctx, "Creating Maestro client...")
		maestroClient, err = client_factory.CreateMaestroClient(ctx, config.Spec.Clients.Maestro, log)
		if err != nil {
			errCtx := logger.WithErrorField(ctx, err)
			log.Errorf(errCtx, "Failed to create Maestro client")
			return fmt.Errorf("failed to create Maestro client: %w", err)
		}
		log.Info(ctx, "Maestro client created successfully")
	}

	// Create the executor using the builder pattern
	log.Info(ctx, "Creating event executor...")
	execBuilder := executor.NewBuilder().
		WithConfig(config).
		WithAPIClient(apiClient).
		WithK8sClient(k8sClient).
		WithLogger(log)

	if maestroClient != nil {
		execBuilder = execBuilder.WithMaestroClient(maestroClient)
	}

	exec, err := execBuilder.Build()
	if err != nil {
		errCtx := logger.WithErrorField(ctx, err)
		log.Errorf(errCtx, "Failed to create executor")
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

	// Get broker subscription ID from config
	subscriptionID := config.Spec.Clients.Broker.SubscriptionID
	if subscriptionID == "" {
		err := fmt.Errorf("spec.clients.broker.subscriptionId is required")
		errCtx := logger.WithErrorField(ctx, err)
		log.Errorf(errCtx, "Missing required broker configuration")
		return err
	}

	// Get broker topic from config
	topic := config.Spec.Clients.Broker.Topic
	if topic == "" {
		err := fmt.Errorf("spec.clients.broker.topic is required")
		errCtx := logger.WithErrorField(ctx, err)
		log.Errorf(errCtx, "Missing required broker configuration")
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
