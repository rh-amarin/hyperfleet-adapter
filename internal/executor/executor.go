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
		e.log.Errorf(ctx, "Failed to parse event data: %v", err)
		return &ExecutionResult{
			Status:      StatusFailed,
			Phase:       PhaseParamExtraction,
			Error:       err,
			ErrorReason: "failed to parse event data",
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
		Status: StatusSuccess,
		Params: make(map[string]interface{}),
	}

	e.log.Info(ctx, "Processing event")

	// Phase 1: Parameter Extraction
	e.log.Info(ctx, "Phase: Parameter Extraction")
	result.Phase = PhaseParamExtraction
	if err := e.executeParamExtraction(execCtx); err != nil {
		return e.finishWithError(ctx, result, execCtx, err, fmt.Sprintf("parameter extraction failed: %v", err))
	}
	result.Params = execCtx.Params
	e.log.Infof(ctx, "Parameter extraction completed: extracted %d params", len(execCtx.Params))
	for k, v := range execCtx.Params {
		e.log.Debugf(ctx, "param[%s]=%v type=%T", k, v, v)
	}

	// Phase 2: Preconditions
	e.log.Infof(ctx, "Phase: Preconditions (%d configured)", len(e.config.AdapterConfig.Spec.Preconditions))
	result.Phase = PhasePreconditions
	precondOutcome := e.precondExecutor.ExecuteAll(ctx, e.config.AdapterConfig.Spec.Preconditions, execCtx)
	result.PreconditionResults = precondOutcome.Results

	if precondOutcome.Error != nil {
		// Process execution error: precondition evaluation failed
		result.Status = StatusFailed
		result.Error = precondOutcome.Error
		result.ErrorReason = "precondition evaluation failed"
		execCtx.SetError("PreconditionFailed", precondOutcome.Error.Error())
		e.log.Errorf(ctx, "Precondition execution failed: %v", precondOutcome.Error)
		// Continue to post actions for error reporting
	} else if !precondOutcome.AllMatched {
		// Business outcome: precondition not satisfied
		result.ResourcesSkipped = true
		result.SkipReason = precondOutcome.NotMetReason
		execCtx.SetSkipped("PreconditionNotMet", precondOutcome.NotMetReason)
		e.log.Infof(ctx, "Preconditions NOT MET - resources will be skipped: %s", precondOutcome.NotMetReason)
	} else {
		// All preconditions matched
		e.log.Infof(ctx, "Preconditions ALL MET: %d/%d passed", len(precondOutcome.Results), len(precondOutcome.Results))
	}

	// Phase 3: Resources (skip if preconditions not met or previous error)
	e.log.Infof(ctx, "Phase: Resources (%d configured)", len(e.config.AdapterConfig.Spec.Resources))
	result.Phase = PhaseResources
	if result.Status == StatusSuccess && !result.ResourcesSkipped {
		resourceResults, err := e.resourceExecutor.ExecuteAll(ctx, e.config.AdapterConfig.Spec.Resources, execCtx)
		result.ResourceResults = resourceResults

		if err != nil {
			result.Status = StatusFailed
			result.Error = err
			result.ErrorReason = "resource execution failed"
			execCtx.SetError("ResourceFailed", err.Error())
			e.log.Errorf(ctx, "Resource execution FAILED: %v", err)
			// Continue to post actions for error reporting
		} else {
			e.log.Infof(ctx, "Resources completed: %d/%d resources processed successfully", len(resourceResults), len(resourceResults))
			for _, r := range resourceResults {
				e.log.Infof(ctx, "resource[%s]: kind=%s namespace=%s name=%s operation=%s", r.Name, r.Kind, r.Namespace, r.ResourceName, r.Operation)
			}
		}
	} else if result.ResourcesSkipped {
		e.log.Infof(ctx, "Resources SKIPPED: %s", result.SkipReason)
	} else if result.Status == StatusFailed {
		e.log.Infof(ctx, "Resources SKIPPED due to previous error")
	}

	// Phase 4: Post Actions (always execute for error reporting)
	postActionCount := 0
	if e.config.AdapterConfig.Spec.Post != nil {
		postActionCount = len(e.config.AdapterConfig.Spec.Post.PostActions)
	}
	e.log.Infof(ctx, "Phase: Post Actions (%d configured)", postActionCount)
	result.Phase = PhasePostActions
	postResults, err := e.postActionExecutor.ExecuteAll(ctx, e.config.AdapterConfig.Spec.Post, execCtx)
	result.PostActionResults = postResults

	if err != nil {
		result.Status = StatusFailed
		result.Error = err
		result.ErrorReason = "post action execution failed"
		e.log.Errorf(ctx, "Post action execution FAILED: %v", err)
	} else {
		e.log.Infof(ctx, "Post actions completed: %d/%d actions executed successfully", len(postResults), postActionCount)
		for _, r := range postResults {
			if r.APICallMade {
				e.log.Infof(ctx, "action[%s]: status=%s httpStatus=%d", r.Name, r.Status, r.HTTPStatus)
			} else {
				e.log.Infof(ctx, "action[%s]: status=%s", r.Name, r.Status)
			}
		}
	}

	// Finalize
	result.ExecutionContext = execCtx

	if result.Status == StatusSuccess {
		if result.ResourcesSkipped {
			e.log.Infof(ctx, "Execution complete: status=success resources_skipped=true reason=%s", result.SkipReason)
		} else {
			e.log.Info(ctx, "Execution complete: status=success")
		}
	} else {
		e.log.Errorf(ctx, "Execution complete: status=failed phase=%s reason=%s", result.Phase, result.ErrorReason)
	}

	return result
}

// finishWithError is a helper to handle early termination with error
func (e *Executor) finishWithError(ctx context.Context, result *ExecutionResult, execCtx *ExecutionContext, err error, reason string) *ExecutionResult {
	result.Status = StatusFailed
	result.Error = err
	result.ErrorReason = reason
	result.ExecutionContext = execCtx
	result.Params = execCtx.Params
	e.log.Errorf(ctx, "Event execution failed: phase=%s reason=%s",
		result.Phase, result.ErrorReason)
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

		result := e.Execute(ctx, evt.Data())

		// Log failure but ACK the message to prevent retry loops
		// Non-recoverable errors (4xx, validation failures) should not be retried
		if result.Status == StatusFailed {
			e.log.Errorf(ctx, "Event processing failed (ACKing to prevent retry): phase=%s reason=%s error=%v",
				result.Phase, result.ErrorReason, result.Error)
		}

		// StatusSkipped is not an error - preconditions not met is expected behavior
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
			return nil, nil, fmt.Errorf("failed to marshal map data: %w", err)
		}
	default:
		// Try to marshal any other type
		jsonBytes, err = json.Marshal(v)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal data: %w", err)
		}
	}

	// Parse into structured EventData
	var eventData EventData
	if err := json.Unmarshal(jsonBytes, &eventData); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal to EventData: %w", err)
	}

	// Parse into raw map for flexible access
	var rawData map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &rawData); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal to map: %w", err)
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
