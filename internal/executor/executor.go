package executor

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
)

// NewExecutor creates a new Executor with the given configuration
func NewExecutor(config *ExecutorConfig) (*Executor, error) {
	if config == nil {
		return nil, NewExecutorError(PhaseParamExtraction, "init", "executor config is required", nil)
	}
	if config.AdapterConfig == nil {
		return nil, NewExecutorError(PhaseParamExtraction, "init", "adapter config is required", nil)
	}
	if config.APIClient == nil {
		return nil, NewExecutorError(PhaseParamExtraction, "init", "API client is required", nil)
	}
	if config.Logger == nil {
		return nil, NewExecutorError(PhaseParamExtraction, "init", "logger is required", nil)
	}

	return &Executor{
		config:             config,
		precondExecutor:    NewPreconditionExecutor(config.APIClient),
		resourceExecutor:   NewResourceExecutor(config.K8sClient),
		postActionExecutor: NewPostActionExecutor(config.APIClient),
	}, nil
}

// Execute processes a CloudEvent according to the adapter configuration
// This is the main entry point for event processing
func (e *Executor) Execute(ctx context.Context, evt *event.Event) *ExecutionResult {
	// ============================================================================
	// Setup
	// ============================================================================
	if evt == nil {
		return &ExecutionResult{
			Status:      StatusFailed,
			Error:       NewExecutorError(PhaseParamExtraction, "init", "event is required", nil),
			ErrorReason: "nil event received",
		}
	}
	ctxWithEventID := context.WithValue(ctx, logger.EvtIDKey, evt.ID())
	eventLogger := logger.WithEventID(e.config.Logger, evt.ID())

	// Parse event data at the boundary (decouples CloudEvent from parameter extraction)
	eventData, err := parseEventData(evt)
	if err != nil {
		return &ExecutionResult{
			EventID:     evt.ID(),
			Status:      StatusFailed,
			Phase:       PhaseParamExtraction,
			Error:       NewExecutorError(PhaseParamExtraction, "parse_event", "failed to parse event data", err),
			ErrorReason: "event data parsing failed",
		}
	}

	execCtx := NewExecutionContext(ctxWithEventID, evt, eventData)

	// Initialize execution result
	result := &ExecutionResult{
		EventID: evt.ID(),
		Status:  StatusSuccess,
		Params:  make(map[string]interface{}),
	}

	eventLogger.Infof("========== EVENT RECEIVED ==========")
	eventLogger.Infof("Event ID: %s", evt.ID())
	eventLogger.Infof("Event Type: %s", evt.Type())
	eventLogger.Infof("Event Source: %s", evt.Source())
	eventLogger.Infof("Event Time: %s", evt.Time())
	eventLogger.Infof("=====================================")

	// ============================================================================
	// Phase 1: Parameter Extraction
	// ============================================================================
	eventLogger.Infof(">>> PHASE: Parameter Extraction")
	result.Phase = PhaseParamExtraction
	if err := e.executeParamExtraction(execCtx); err != nil {
		return e.finishWithError(result, execCtx, err, fmt.Sprintf("parameter extraction failed: %v", err), eventLogger)
	}
	result.Params = execCtx.Params
	eventLogger.Infof("Parameter extraction completed: extracted %d params", len(execCtx.Params))
	for k, v := range execCtx.Params {
		eventLogger.V(1).Infof("  param[%s] = %v (type: %T)", k, v, v)
	}

	// ============================================================================
	// Phase 2: Preconditions
	// ============================================================================
	eventLogger.Infof(">>> PHASE: Preconditions (%d configured)", len(e.config.AdapterConfig.Spec.Preconditions))
	result.Phase = PhasePreconditions
	precondOutcome := e.precondExecutor.ExecuteAll(ctxWithEventID, e.config.AdapterConfig.Spec.Preconditions, execCtx, eventLogger)
	result.PreconditionResults = precondOutcome.Results

	if precondOutcome.Error != nil {
		// Process execution error: precondition evaluation failed
		result.Status = StatusFailed
		result.Error = precondOutcome.Error
		result.ErrorReason = "precondition evaluation failed"
		execCtx.SetError("PreconditionFailed", precondOutcome.Error.Error())
		eventLogger.Error(fmt.Sprintf("Precondition execution failed: %v", precondOutcome.Error))
		// Continue to post actions for error reporting
	} else if !precondOutcome.AllMatched {
		// Business outcome: precondition not satisfied
		result.ResourcesSkipped = true
		result.SkipReason = precondOutcome.NotMetReason
		execCtx.SetSkipped("PreconditionNotMet", precondOutcome.NotMetReason)
		eventLogger.Infof("Preconditions NOT MET - resources will be skipped: %s", precondOutcome.NotMetReason)
	} else {
		// All preconditions matched
		eventLogger.Infof("Preconditions ALL MET: %d/%d passed", len(precondOutcome.Results), len(precondOutcome.Results))
	}

	// ============================================================================
	// Phase 3: Resources (skip if preconditions not met or previous error)
	// ============================================================================
	eventLogger.Infof(">>> PHASE: Resources (%d configured)", len(e.config.AdapterConfig.Spec.Resources))
	result.Phase = PhaseResources
	if result.Status == StatusSuccess && !result.ResourcesSkipped {
		resourceResults, err := e.resourceExecutor.ExecuteAll(ctxWithEventID, e.config.AdapterConfig.Spec.Resources, execCtx, eventLogger)
		result.ResourceResults = resourceResults

		if err != nil {
			result.Status = StatusFailed
			result.Error = err
			result.ErrorReason = "resource execution failed"
			execCtx.SetError("ResourceFailed", err.Error())
			eventLogger.Error(fmt.Sprintf("Resource execution FAILED: %v", err))
			// Continue to post actions for error reporting
		} else {
			eventLogger.Infof("Resources completed: %d/%d resources processed successfully", len(resourceResults), len(resourceResults))
			for _, r := range resourceResults {
				eventLogger.Infof("  resource[%s]: %s %s/%s (operation: %s)", r.Name, r.Kind, r.Namespace, r.ResourceName, r.Operation)
			}
		}
	} else if result.ResourcesSkipped {
		eventLogger.Infof("Resources SKIPPED: %s", result.SkipReason)
	} else if result.Status == StatusFailed {
		eventLogger.Infof("Resources SKIPPED due to previous error")
	}

	// ============================================================================
	// Phase 4: Post Actions (always execute for error reporting)
	// ============================================================================
	postActionCount := 0
	if e.config.AdapterConfig.Spec.Post != nil {
		postActionCount = len(e.config.AdapterConfig.Spec.Post.PostActions)
	}
	eventLogger.Infof(">>> PHASE: Post Actions (%d configured)", postActionCount)
	result.Phase = PhasePostActions
	postResults, err := e.postActionExecutor.ExecuteAll(ctxWithEventID, e.config.AdapterConfig.Spec.Post, execCtx, eventLogger)
	result.PostActionResults = postResults

	if err != nil {
		result.Status = StatusFailed
		result.Error = err
		result.ErrorReason = "post action execution failed"
		eventLogger.Error(fmt.Sprintf("Post action execution FAILED: %v", err))
	} else {
		eventLogger.Infof("Post actions completed: %d/%d actions executed successfully", len(postResults), postActionCount)
		for _, r := range postResults {
			if r.APICallMade {
				eventLogger.Infof("  action[%s]: status=%s httpStatus=%d", r.Name, r.Status, r.HTTPStatus)
			} else {
				eventLogger.Infof("  action[%s]: status=%s", r.Name, r.Status)
			}
		}
	}

	// ============================================================================
	// Finalize
	// ============================================================================
	result.ExecutionContext = execCtx

	// Final logging
	eventLogger.Infof("========== EXECUTION COMPLETE ==========")
	if result.Status == StatusSuccess {
		if result.ResourcesSkipped {
			eventLogger.Infof("Result: SUCCESS (resources skipped)")
			eventLogger.Infof("Skip Reason: %s", result.SkipReason)
		} else {
			eventLogger.Infof("Result: SUCCESS")
		}
	} else {
		eventLogger.Error("Result: FAILED")
		eventLogger.Error(fmt.Sprintf("Failed Phase: %s", result.Phase))
		eventLogger.Error(fmt.Sprintf("Error Reason: %s", result.ErrorReason))
	}
	eventLogger.Infof("=========================================")

	return result
}

// finishWithError is a helper to handle early termination with error
func (e *Executor) finishWithError(result *ExecutionResult, execCtx *ExecutionContext, err error, reason string, eventLogger logger.Logger) *ExecutionResult {
	result.Status = StatusFailed
	result.Error = err
	result.ErrorReason = reason
	result.ExecutionContext = execCtx
	result.Params = execCtx.Params
	eventLogger.Error(fmt.Sprintf("Event execution failed: id=%s phase=%s reason=%s",
		result.EventID, result.Phase, result.ErrorReason))
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
		result := e.Execute(ctx, evt)

		// Log failure but ACK the message to prevent retry loops
		// Non-recoverable errors (4xx, validation failures) should not be retried
		if result.Status == StatusFailed {
			e.config.Logger.Error(fmt.Sprintf("Event processing failed (ACKing to prevent retry): eventId=%s phase=%s reason=%s error=%v",
				result.EventID, result.Phase, result.ErrorReason, result.Error))
		}

		// StatusSkipped is not an error - preconditions not met is expected behavior
		return nil
	}
}


// parseEventData parses the CloudEvent data payload into a map
// This is done at the boundary to decouple CloudEvent from parameter extraction
func parseEventData(evt *event.Event) (map[string]interface{}, error) {
	if evt == nil {
		return make(map[string]interface{}), nil
	}

	data := evt.Data()
	if len(data) == 0 {
		return make(map[string]interface{}), nil
	}

	var eventData map[string]interface{}
	if err := json.Unmarshal(data, &eventData); err != nil {
		return nil, fmt.Errorf("failed to parse event data as JSON: %w", err)
	}

	return eventData, nil
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

