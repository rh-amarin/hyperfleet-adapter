// Package runner provides the HyperFleet workflow runner that extends
// the Serverless Workflow SDK with custom task execution capabilities.
package runner

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ExecutionPhase represents which phase of execution
type ExecutionPhase string

const (
	PhaseParamExtraction ExecutionPhase = "param_extraction"
	PhasePreconditions   ExecutionPhase = "preconditions"
	PhaseResources       ExecutionPhase = "resources"
	PhasePostActions     ExecutionPhase = "post_actions"
)

// ExecutionStatus represents the status of execution
type ExecutionStatus string

const (
	StatusSuccess ExecutionStatus = "success"
	StatusFailed  ExecutionStatus = "failed"
)

// WorkflowContext holds runtime context during workflow execution.
// This bridges the SWF SDK execution with HyperFleet-specific state.
type WorkflowContext struct {
	// Ctx is the Go context for cancellation and deadlines
	Ctx context.Context

	// EventData is the original CloudEvent data payload
	EventData map[string]any

	// Params holds extracted parameters and captured fields from preconditions
	Params map[string]any

	// Resources holds created/updated K8s resources keyed by resource name
	Resources map[string]*unstructured.Unstructured

	// PreconditionResponses holds full API responses from preconditions
	// keyed by precondition name (for use in CEL expressions)
	PreconditionResponses map[string]any

	// Adapter holds adapter execution metadata
	Adapter AdapterMetadata

	// CurrentPhase tracks the current execution phase
	CurrentPhase ExecutionPhase

	// Errors tracks errors by phase
	Errors map[ExecutionPhase]error

	// ResourcesSkipped indicates if resources were skipped
	ResourcesSkipped bool

	// SkipReason explains why resources were skipped
	SkipReason string
}

// AdapterMetadata holds adapter execution metadata for CEL expressions
type AdapterMetadata struct {
	ExecutionStatus  string `json:"executionStatus"`
	ErrorReason      string `json:"errorReason,omitempty"`
	ErrorMessage     string `json:"errorMessage,omitempty"`
	ResourcesSkipped bool   `json:"resourcesSkipped,omitempty"`
	SkipReason       string `json:"skipReason,omitempty"`
}

// NewWorkflowContext creates a new workflow execution context.
func NewWorkflowContext(ctx context.Context, eventData map[string]any) *WorkflowContext {
	return &WorkflowContext{
		Ctx:                   ctx,
		EventData:             eventData,
		Params:                make(map[string]any),
		Resources:             make(map[string]*unstructured.Unstructured),
		PreconditionResponses: make(map[string]any),
		Errors:                make(map[ExecutionPhase]error),
		CurrentPhase:          PhaseParamExtraction,
		Adapter: AdapterMetadata{
			ExecutionStatus: string(StatusSuccess),
		},
	}
}

// SetParam sets a parameter value.
func (wc *WorkflowContext) SetParam(name string, value any) {
	wc.Params[name] = value
}

// GetParam retrieves a parameter value.
func (wc *WorkflowContext) GetParam(name string) (any, bool) {
	v, ok := wc.Params[name]
	return v, ok
}

// SetResource stores a created/updated Kubernetes resource.
func (wc *WorkflowContext) SetResource(name string, resource *unstructured.Unstructured) {
	wc.Resources[name] = resource
}

// GetResource retrieves a stored Kubernetes resource.
func (wc *WorkflowContext) GetResource(name string) (*unstructured.Unstructured, bool) {
	r, ok := wc.Resources[name]
	return r, ok
}

// SetPreconditionResponse stores the full API response from a precondition.
func (wc *WorkflowContext) SetPreconditionResponse(name string, response any) {
	wc.PreconditionResponses[name] = response
}

// SetError marks the execution as failed with an error.
func (wc *WorkflowContext) SetError(phase ExecutionPhase, reason, message string, err error) {
	wc.Adapter.ExecutionStatus = string(StatusFailed)
	wc.Adapter.ErrorReason = reason
	wc.Adapter.ErrorMessage = message
	wc.Errors[phase] = err
}

// SetSkipped marks resources as skipped (not an error).
func (wc *WorkflowContext) SetSkipped(reason, message string) {
	wc.ResourcesSkipped = true
	wc.SkipReason = reason
	wc.Adapter.ResourcesSkipped = true
	wc.Adapter.SkipReason = message
}

// GetCELVariables returns all variables for CEL expression evaluation.
// This includes params, adapter metadata, resources, and precondition responses.
func (wc *WorkflowContext) GetCELVariables() map[string]any {
	result := make(map[string]any)

	// Copy all params
	for k, v := range wc.Params {
		result[k] = v
	}

	// Add adapter metadata
	result["adapter"] = map[string]any{
		"executionStatus":  wc.Adapter.ExecutionStatus,
		"errorReason":      wc.Adapter.ErrorReason,
		"errorMessage":     wc.Adapter.ErrorMessage,
		"resourcesSkipped": wc.Adapter.ResourcesSkipped,
		"skipReason":       wc.Adapter.SkipReason,
	}

	// Add resources (convert unstructured to maps)
	resources := make(map[string]any)
	for name, resource := range wc.Resources {
		if resource != nil {
			resources[name] = resource.Object
		}
	}
	result["resources"] = resources

	// Add precondition responses
	for name, response := range wc.PreconditionResponses {
		result[name] = response
	}

	return result
}

// ToWorkflowInput creates the input data structure for the SWF workflow.
// This combines event data, config, and context into a single map.
func (wc *WorkflowContext) ToWorkflowInput(config map[string]any) map[string]any {
	return map[string]any{
		"event":   wc.EventData,
		"config":  config,
		"params":  wc.Params,
		"adapter": wc.adapterToMap(),
	}
}

func (wc *WorkflowContext) adapterToMap() map[string]any {
	return map[string]any{
		"executionStatus":  wc.Adapter.ExecutionStatus,
		"errorReason":      wc.Adapter.ErrorReason,
		"errorMessage":     wc.Adapter.ErrorMessage,
		"resourcesSkipped": wc.Adapter.ResourcesSkipped,
		"skipReason":       wc.Adapter.SkipReason,
	}
}

// EvaluationRecord tracks a single condition evaluation during execution
type EvaluationRecord struct {
	Phase          ExecutionPhase
	Name           string
	EvaluationType string
	Expression     string
	Matched        bool
	Timestamp      time.Time
}

// WorkflowResult contains the result of workflow execution.
type WorkflowResult struct {
	// Status is the overall execution status
	Status ExecutionStatus

	// CurrentPhase is the phase where execution ended
	CurrentPhase ExecutionPhase

	// Params contains the extracted parameters
	Params map[string]any

	// PreconditionResults contains results of precondition evaluations
	PreconditionResults []PreconditionResult

	// ResourceResults contains results of resource operations
	ResourceResults []ResourceResult

	// PostActionResults contains results of post-action executions
	PostActionResults []PostActionResult

	// Errors contains errors keyed by the phase where they occurred
	Errors map[ExecutionPhase]error

	// ResourcesSkipped indicates if resources were skipped
	ResourcesSkipped bool

	// SkipReason is why resources were skipped
	SkipReason string

	// Output is the final workflow output
	Output any
}

// PreconditionResult contains the result of a single precondition evaluation.
type PreconditionResult struct {
	Name           string
	Matched        bool
	APICallMade    bool
	CapturedFields map[string]any
	Error          error
}

// ResourceResult contains the result of a single resource operation.
type ResourceResult struct {
	Name            string
	Kind            string
	Namespace       string
	ResourceName    string
	Operation       string // create, update, recreate, skip
	OperationReason string
	Resource        *unstructured.Unstructured
	Error           error
}

// PostActionResult contains the result of a single post-action execution.
type PostActionResult struct {
	Name        string
	APICallMade bool
	HTTPStatus  int
	Error       error
}

// GetOutput returns the workflow output as a map.
// If the output is not a map, returns an empty map.
func (wr *WorkflowResult) GetOutput() map[string]any {
	if wr.Output == nil {
		return make(map[string]any)
	}
	if m, ok := wr.Output.(map[string]any); ok {
		return m
	}
	return make(map[string]any)
}

// GetTaskOutput retrieves the output of a specific task phase.
// This is a simplified implementation that looks for task outputs in the main output.
func (wr *WorkflowResult) GetTaskOutput(taskName string) (map[string]any, bool) {
	output := wr.GetOutput()
	if taskOutput, ok := output[taskName].(map[string]any); ok {
		return taskOutput, true
	}
	// Return the full output if task-specific output not found
	return output, true
}
