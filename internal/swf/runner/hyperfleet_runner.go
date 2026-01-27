package runner

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/swf/converter"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/swf/tasks"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	pkgotel "github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/otel"
	"github.com/serverlessworkflow/sdk-go/v3/model"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// HyperFleetRunner is the main entry point for running workflows in the HyperFleet context.
// It integrates the SWF runner with CloudEvents handling and OTel tracing.
type HyperFleetRunner struct {
	workflow  *model.Workflow
	runner    *Runner
	config    *HyperFleetConfig
	log       logger.Logger
	k8sClient k8s_client.K8sClient
	apiClient hyperfleet_api.Client
}

// HyperFleetConfig holds configuration for the HyperFleet runner.
type HyperFleetConfig struct {
	// AdapterConfig is the legacy adapter configuration (optional, will be converted)
	AdapterConfig *config_loader.AdapterConfig
	// Workflow is the native SWF workflow (takes precedence over AdapterConfig)
	Workflow *model.Workflow
	// K8sClient is the Kubernetes client
	K8sClient k8s_client.K8sClient
	// APIClient is the HyperFleet API client
	APIClient hyperfleet_api.Client
	// Logger is the logger instance
	Logger logger.Logger
}

// HyperFleetResult contains the result of processing an event.
type HyperFleetResult struct {
	// Status is the overall execution status
	Status ExecutionStatus
	// Output is the final output from the workflow
	Output map[string]any
	// Error is the error if execution failed
	Error error
	// Phases contains results of each phase
	Phases map[string]PhaseResult
}

// PhaseResult contains the result of a single workflow phase.
type PhaseResult struct {
	Name    string
	Status  ExecutionStatus
	Output  map[string]any
	Skipped bool
}

// NewHyperFleetRunner creates a new HyperFleet runner.
func NewHyperFleetRunner(config *HyperFleetConfig) (*HyperFleetRunner, error) {
	if config == nil {
		return nil, fmt.Errorf("config is required")
	}

	if config.Logger == nil {
		return nil, fmt.Errorf("logger is required")
	}

	// Determine the workflow to use
	var workflow *model.Workflow
	var err error

	if config.Workflow != nil {
		workflow = config.Workflow
	} else if config.AdapterConfig != nil {
		// Convert legacy AdapterConfig to SWF Workflow
		workflow, err = converter.ConvertAdapterConfig(config.AdapterConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to convert adapter config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("either Workflow or AdapterConfig is required")
	}

	// Create task registry with dependencies
	deps := &tasks.Dependencies{
		K8sClient: config.K8sClient,
		APIClient: config.APIClient,
		Logger:    config.Logger,
	}

	registry := tasks.NewRegistry()
	if err := tasks.RegisterAllWithDeps(registry, deps); err != nil {
		return nil, fmt.Errorf("failed to register tasks: %w", err)
	}

	// Create SWF runner
	runner, err := NewRunner(&RunnerConfig{
		Workflow:     workflow,
		TaskRegistry: registry,
		K8sClient:    config.K8sClient,
		APIClient:    config.APIClient,
		Logger:       config.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create runner: %w", err)
	}

	return &HyperFleetRunner{
		workflow:  workflow,
		runner:    runner,
		config:    config,
		log:       config.Logger,
		k8sClient: config.K8sClient,
		apiClient: config.APIClient,
	}, nil
}

// Execute processes event data according to the workflow configuration.
func (r *HyperFleetRunner) Execute(ctx context.Context, data interface{}) *HyperFleetResult {
	// Start OTel span and add trace context to logs
	ctx, span := r.startTracedExecution(ctx)
	defer span.End()

	result := &HyperFleetResult{
		Status: StatusSuccess,
		Phases: make(map[string]PhaseResult),
	}

	// Parse event data
	eventData, rawData, err := parseEventData(data)
	if err != nil {
		result.Status = StatusFailed
		result.Error = fmt.Errorf("failed to parse event data: %w", err)
		return result
	}

	// Set resource context for logging
	if eventData.OwnedReference != nil {
		ctx = logger.WithResourceType(ctx, eventData.Kind)
		ctx = logger.WithDynamicResourceID(ctx, eventData.Kind, eventData.ID)
		ctx = logger.WithDynamicResourceID(ctx, eventData.OwnedReference.Kind, eventData.OwnedReference.ID)
	} else if eventData.ID != "" {
		ctx = logger.WithDynamicResourceID(ctx, eventData.Kind, eventData.ID)
	}

	r.log.Info(ctx, "Processing event via SWF engine")

	// Build initial input for workflow
	input := map[string]any{
		"event":  rawData,
		"params": make(map[string]any),
	}

	// Run the workflow
	wfCtx, err := r.runner.Run(ctx, input)
	if err != nil {
		result.Status = StatusFailed
		result.Error = fmt.Errorf("workflow execution failed: %w", err)
		errCtx := logger.WithErrorField(ctx, err)
		r.log.Errorf(errCtx, "Workflow execution failed")
		return result
	}

	// Extract results from workflow context
	result.Output = wfCtx.GetOutput()

	// Extract phase results
	for _, phase := range []string{"phase_params", "phase_preconditions", "phase_resources", "phase_post"} {
		if phaseOutput, ok := wfCtx.GetTaskOutput(phase); ok {
			phaseResult := PhaseResult{
				Name:   phase,
				Status: StatusSuccess,
				Output: phaseOutput,
			}
			if errMsg, hasErr := phaseOutput["error"].(string); hasErr && errMsg != "" {
				phaseResult.Status = StatusFailed
				result.Status = StatusFailed
			}
			result.Phases[phase] = phaseResult
		}
	}

	if result.Status == StatusSuccess {
		r.log.Info(ctx, "Event execution finished: status=success")
	} else {
		r.log.Errorf(ctx, "Event execution finished: status=failed")
	}

	return result
}

// CreateHandler creates an event handler function for use with the broker subscriber.
// This matches the interface expected by broker_consumer.
func (r *HyperFleetRunner) CreateHandler() func(ctx context.Context, evt *event.Event) error {
	return func(ctx context.Context, evt *event.Event) error {
		// Add event ID to context for logging correlation
		ctx = logger.WithEventID(ctx, evt.ID())

		// Extract W3C trace context from CloudEvent extensions
		ctx = pkgotel.ExtractTraceContextFromCloudEvent(ctx, evt)

		// Log event metadata
		r.log.Infof(ctx, "Event received: id=%s type=%s source=%s time=%s",
			evt.ID(), evt.Type(), evt.Source(), evt.Time())

		// Execute the workflow
		_ = r.Execute(ctx, evt.Data())

		r.log.Infof(ctx, "Event processed: type=%s source=%s time=%s",
			evt.Type(), evt.Source(), evt.Time())

		return nil
	}
}

// startTracedExecution creates an OTel span and adds trace context to logs.
func (r *HyperFleetRunner) startTracedExecution(ctx context.Context) (context.Context, trace.Span) {
	componentName := r.workflow.Document.Name
	ctx, span := otel.Tracer(componentName).Start(ctx, "Execute")

	// Add trace_id and span_id to logger context
	ctx = logger.WithOTelTraceContext(ctx)

	return ctx, span
}

// GetWorkflow returns the underlying SWF workflow model.
func (r *HyperFleetRunner) GetWorkflow() *model.Workflow {
	return r.workflow
}

// ResourceRef represents a reference to a HyperFleet resource.
type ResourceRef struct {
	ID   string `json:"id,omitempty"`
	Kind string `json:"kind,omitempty"`
	Href string `json:"href,omitempty"`
}

// EventData represents the data payload of a HyperFleet CloudEvent.
type EventData struct {
	ID             string       `json:"id,omitempty"`
	Kind           string       `json:"kind,omitempty"`
	Href           string       `json:"href,omitempty"`
	Generation     int64        `json:"generation,omitempty"`
	OwnedReference *ResourceRef `json:"owned_reference,omitempty"`
}

// parseEventData parses event data from various input types.
func parseEventData(data interface{}) (*EventData, map[string]interface{}, error) {
	if data == nil {
		return &EventData{}, make(map[string]interface{}), nil
	}

	var jsonBytes []byte
	var err error

	switch v := data.(type) {
	case []byte:
		if len(v) == 0 {
			return &EventData{}, make(map[string]interface{}), nil
		}
		jsonBytes = v
	case map[string]interface{}:
		jsonBytes, err = json.Marshal(v)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal map data: %w", err)
		}
	default:
		jsonBytes, err = json.Marshal(v)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal data: %w", err)
		}
	}

	var eventData EventData
	if err := json.Unmarshal(jsonBytes, &eventData); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal to EventData: %w", err)
	}

	var rawData map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &rawData); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal to map: %w", err)
	}

	return &eventData, rawData, nil
}

// HyperFleetRunnerBuilder provides a fluent interface for building a HyperFleetRunner.
type HyperFleetRunnerBuilder struct {
	config *HyperFleetConfig
}

// NewBuilder creates a new HyperFleetRunnerBuilder.
func NewBuilder() *HyperFleetRunnerBuilder {
	return &HyperFleetRunnerBuilder{
		config: &HyperFleetConfig{},
	}
}

// WithAdapterConfig sets the legacy adapter configuration.
func (b *HyperFleetRunnerBuilder) WithAdapterConfig(config *config_loader.AdapterConfig) *HyperFleetRunnerBuilder {
	b.config.AdapterConfig = config
	return b
}

// WithWorkflow sets the native SWF workflow.
func (b *HyperFleetRunnerBuilder) WithWorkflow(workflow *model.Workflow) *HyperFleetRunnerBuilder {
	b.config.Workflow = workflow
	return b
}

// WithK8sClient sets the Kubernetes client.
func (b *HyperFleetRunnerBuilder) WithK8sClient(client k8s_client.K8sClient) *HyperFleetRunnerBuilder {
	b.config.K8sClient = client
	return b
}

// WithAPIClient sets the HyperFleet API client.
func (b *HyperFleetRunnerBuilder) WithAPIClient(client hyperfleet_api.Client) *HyperFleetRunnerBuilder {
	b.config.APIClient = client
	return b
}

// WithLogger sets the logger.
func (b *HyperFleetRunnerBuilder) WithLogger(log logger.Logger) *HyperFleetRunnerBuilder {
	b.config.Logger = log
	return b
}

// Build creates the HyperFleetRunner.
func (b *HyperFleetRunnerBuilder) Build() (*HyperFleetRunner, error) {
	return NewHyperFleetRunner(b.config)
}
