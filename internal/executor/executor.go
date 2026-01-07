package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
)

// NewExecutor creates a new Executor with the given configuration
func NewExecutor(config *ExecutorConfig) (*Executor, error) {
	if err := validateExecutorConfig(config); err != nil {
		return nil, err
	}

	return &Executor{
		config:             config,
		precondExecutor:    newPreconditionExecutor(config),
		resourceExecutor:   newResourceExecutor(config),
		postActionExecutor: newPostActionExecutor(config),
		log:                config.Logger,
	}, nil
}
func validateExecutorConfig(config *ExecutorConfig) error {
	if config == nil {
		return fmt.Errorf("config is required")
	}

	requiredFields := []string{
		"AdapterConfig",
		"APIClient",
		"Logger",
		"K8sClient"}

	for _, field := range requiredFields {
		if reflect.ValueOf(config).Elem().FieldByName(field).IsNil() {
			return fmt.Errorf("field %s is required", field)
		}
	}
	return nil
}

// Execute processes event data according to the adapter configuration
// The caller is responsible for:
// - Adding event ID to context for logging correlation using logger.WithEventID()
func (e *Executor) Execute(ctx context.Context, data interface{}) *ExecutionResult {

	// Parse event data
	eventData, rawData, err := ParseEventData(data)
	if err != nil {
		parseErr := fmt.Errorf("failed to parse event data: %w", err)
		e.log.Errorf(ctx, "Failed to parse event data: error=%v", parseErr)
		return &ExecutionResult{
			Status:       StatusFailed,
			CurrentPhase: PhaseParamExtraction,
			Errors:       map[ExecutionPhase]error{PhaseParamExtraction: parseErr},
		}
	}

	// This is intended to set OwnerReference and ResourceID for the event when it exist
	// For example, when a NodePool event arrived
	// the logger will set the cluster_id:owner_id , resource_id: nodepool_id and resource_type: nodepool
	// but when a resource is cluster type, it will just record cluster_id:resource_id
	if eventData.OwnedReference != nil {
		ctx = logger.WithResourceID(
			logger.WithResourceType(ctx, eventData.Kind), eventData.ID)
		ctx = logger.WithDynamicResourceID(ctx, eventData.OwnedReference.Kind, eventData.OwnedReference.ID)
	} else {
		ctx = logger.WithDynamicResourceID(ctx, eventData.Kind, eventData.ID)
	}

	execCtx := NewExecutionContext(ctx, rawData)

	// Initialize execution result
	result := &ExecutionResult{
		Status:       StatusSuccess,
		Params:       make(map[string]interface{}),
		Errors:       make(map[ExecutionPhase]error),
		CurrentPhase: PhaseParamExtraction,
	}

	e.log.Info(ctx, "Processing event")

	// Phase 1: Parameter Extraction
	e.log.Infof(ctx, "Phase %s: RUNNING", result.CurrentPhase)
	if err := e.executeParamExtraction(execCtx); err != nil {
		result.Status = StatusFailed
		result.Errors[PhaseParamExtraction] = err
		execCtx.SetError("ParameterExtractionFailed", err.Error())
		return result
	}
	result.Params = execCtx.Params
	e.log.Debugf(ctx, "Parameter extraction completed: extracted %d params", len(execCtx.Params))

	// Phase 2: Preconditions
	result.CurrentPhase = PhasePreconditions
	e.log.Infof(ctx, "Phase %s: RUNNING - %d configured", result.CurrentPhase, len(e.config.AdapterConfig.Spec.Preconditions))
	precondOutcome := e.precondExecutor.ExecuteAll(ctx, e.config.AdapterConfig.Spec.Preconditions, execCtx)
	result.PreconditionResults = precondOutcome.Results

	if precondOutcome.Error != nil {
		// Process execution error: precondition evaluation failed
		result.Status = StatusFailed
		precondErr := fmt.Errorf("precondition evaluation failed: error=%w", precondOutcome.Error)
		result.Errors[result.CurrentPhase] = precondErr
		execCtx.SetError("PreconditionFailed", precondOutcome.Error.Error())
		e.log.Errorf(ctx, "Phase %s: FAILED - error=%v", result.CurrentPhase, precondOutcome.Error)
		result.ResourcesSkipped = true
		result.SkipReason = "PreconditionFailed"
		execCtx.SetSkipped("PreconditionFailed", precondOutcome.Error.Error())
		// Continue to post actions for error reporting
	} else if !precondOutcome.AllMatched {
		// Business outcome: precondition not satisfied
		result.ResourcesSkipped = true
		result.SkipReason = precondOutcome.NotMetReason
		execCtx.SetSkipped("PreconditionNotMet", precondOutcome.NotMetReason)
		e.log.Infof(ctx, "Phase %s: SUCCESS - NOT_MET - %s", result.CurrentPhase, precondOutcome.NotMetReason)
	} else {
		// All preconditions matched
		e.log.Infof(ctx, "Phase %s: SUCCESS - MET - %d passed", result.CurrentPhase, len(precondOutcome.Results))
	}

	// Phase 3: Resources (skip if preconditions not met or previous error)
	result.CurrentPhase = PhaseResources
	e.log.Infof(ctx, "Phase %s: RUNNING - %d configured", result.CurrentPhase, len(e.config.AdapterConfig.Spec.Resources))
	if !result.ResourcesSkipped {
		resourceResults, err := e.resourceExecutor.ExecuteAll(ctx, e.config.AdapterConfig.Spec.Resources, execCtx)
		result.ResourceResults = resourceResults

		if err != nil {
			result.Status = StatusFailed
			resErr := fmt.Errorf("resource execution failed: %w", err)
			result.Errors[result.CurrentPhase] = resErr
			execCtx.SetError("ResourceFailed", err.Error())
			e.log.Errorf(ctx, "Phase %s: FAILED - error=%v", result.CurrentPhase, err)
			// Continue to post actions for error reporting
		} else {
			e.log.Infof(ctx, "Phase %s: SUCCESS - %d processed", result.CurrentPhase, len(resourceResults))
		}
	} else {
		e.log.Infof(ctx, "Phase %s: SKIPPED - %s", result.CurrentPhase, result.SkipReason)
	}

	// Phase 4: Post Actions (always execute for error reporting)
	result.CurrentPhase = PhasePostActions
	postActionCount := 0
	if e.config.AdapterConfig.Spec.Post != nil {
		postActionCount = len(e.config.AdapterConfig.Spec.Post.PostActions)
	}
	e.log.Infof(ctx, "Phase %s: RUNNING - %d configured", result.CurrentPhase, postActionCount)
	postResults, err := e.postActionExecutor.ExecuteAll(ctx, e.config.AdapterConfig.Spec.Post, execCtx)
	result.PostActionResults = postResults

	if err != nil {
		result.Status = StatusFailed
		postErr := fmt.Errorf("post action execution failed: %w", err)
		result.Errors[result.CurrentPhase] = postErr
		e.log.Errorf(ctx, "Phase %s: FAILED - error=%v", result.CurrentPhase, err)
	} else {
		e.log.Infof(ctx, "Phase %s: SUCCESS - %d executed", result.CurrentPhase, len(postResults))
	}

	// Finalize
	result.ExecutionContext = execCtx

	if result.Status == StatusSuccess {
		e.log.Infof(ctx, "Event execution finished: event_execution_status=success resources_skipped=%t reason=%s", result.ResourcesSkipped, result.SkipReason)
	} else {
		e.log.Errorf(ctx, "Event execution finished: event_execution_status=failed event_execution_errors=%v", result.Errors)
	}
	return result
}

// executeParamExtraction extracts parameters from the event and environment
func (e *Executor) executeParamExtraction(execCtx *ExecutionContext) error {
	// Extract configured parameters
	if err := extractConfigParams(e.config.AdapterConfig, execCtx, e.config.K8sClient); err != nil {
		return err
	}

	// Add metadata params
	addMetadataParams(e.config.AdapterConfig, execCtx)

	return nil
}

// CreateHandler creates an event handler function that can be used with the broker subscriber
// This is a convenience method for integrating with the broker_consumer package
//
// Error handling strategy:
// - All failures are logged but the message is ACKed (return nil)
// - This prevents infinite retry loops for non-recoverable errors (e.g., 400 Bad Request, invalid data)
func (e *Executor) CreateHandler() func(ctx context.Context, evt *event.Event) error {
	return func(ctx context.Context, evt *event.Event) error {
		// Add event ID to context for logging correlation
		ctx = logger.WithEventID(ctx, evt.ID())

		// Log event metadata
		e.log.Infof(ctx, "Event received: id=%s type=%s source=%s time=%s",
			evt.ID(), evt.Type(), evt.Source(), evt.Time())

		_ = e.Execute(ctx, evt.Data())

		e.log.Infof(ctx, "Event processed: id=%s type=%s source=%s time=%s",
			evt.ID(), evt.Type(), evt.Source(), evt.Time())

		return nil
	}
}

// ParseEventData parses event data from various input types into structured EventData and raw map.
// Accepts: []byte (JSON), map[string]interface{}, or any JSON-serializable type.
// Returns: structured EventData, raw map for flexible access, and any error.
func ParseEventData(data interface{}) (*EventData, map[string]interface{}, error) {
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
		// Already a map, marshal to JSON for struct conversion
		jsonBytes, err = json.Marshal(v)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal map data: error=%w", err)
		}
	default:
		// Try to marshal any other type
		jsonBytes, err = json.Marshal(v)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal data: error=%w", err)
		}
	}

	// Parse into structured EventData
	var eventData EventData
	if err := json.Unmarshal(jsonBytes, &eventData); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal to EventData: error=%w", err)
	}

	// Parse into raw map for flexible access
	var rawData map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &rawData); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal to map: error=%w", err)
	}

	return &eventData, rawData, nil
}

// ExecutorBuilder provides a fluent interface for building an Executor
type ExecutorBuilder struct {
	config *ExecutorConfig
}

// NewBuilder creates a new ExecutorBuilder
func NewBuilder() *ExecutorBuilder {
	return &ExecutorBuilder{
		config: &ExecutorConfig{},
	}
}

// WithAdapterConfig sets the adapter configuration
func (b *ExecutorBuilder) WithAdapterConfig(config *config_loader.AdapterConfig) *ExecutorBuilder {
	b.config.AdapterConfig = config
	return b
}

// WithAPIClient sets the HyperFleet API client
func (b *ExecutorBuilder) WithAPIClient(client hyperfleet_api.Client) *ExecutorBuilder {
	b.config.APIClient = client
	return b
}

// WithK8sClient sets the Kubernetes client
func (b *ExecutorBuilder) WithK8sClient(client k8s_client.K8sClient) *ExecutorBuilder {
	b.config.K8sClient = client
	return b
}

// WithLogger sets the logger
func (b *ExecutorBuilder) WithLogger(log logger.Logger) *ExecutorBuilder {
	b.config.Logger = log
	return b
}

// Build creates the Executor
func (b *ExecutorBuilder) Build() (*Executor, error) {
	return NewExecutor(b.config)
}
