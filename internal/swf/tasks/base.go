// Package tasks provides custom task runners for HyperFleet workflows.
// These task runners extend the Serverless Workflow SDK with domain-specific
// operations for Kubernetes resource management, HyperFleet API calls,
// CEL expression evaluation, and more.
package tasks

import (
	"context"
)

// TaskRunner defines the interface for custom HyperFleet task runners.
// Each task runner handles a specific type of operation (e.g., hf:extract, hf:k8s).
type TaskRunner interface {
	// Name returns the task type identifier (e.g., "hf:extract", "hf:k8s")
	Name() string

	// Run executes the task with the given arguments and input data.
	// The args map contains task-specific configuration from the workflow definition.
	// The input map contains the current workflow data/context.
	// Returns the task output and any error that occurred.
	Run(ctx context.Context, args map[string]any, input map[string]any) (map[string]any, error)
}

// TaskRunnerFactory creates a TaskRunner with the given dependencies.
// This allows task runners to be constructed with required services
// (e.g., K8s client, API client) without hardcoding dependencies.
type TaskRunnerFactory func(deps *Dependencies) (TaskRunner, error)

// Dependencies contains shared services and clients used by task runners.
// These are injected when building the workflow runner.
type Dependencies struct {
	// K8sClient is the Kubernetes client for resource operations
	K8sClient any // Will be typed as k8s_client.K8sClient

	// APIClient is the HyperFleet API client for HTTP calls
	APIClient any // Will be typed as hyperfleet_api.Client

	// Logger is the logging interface
	Logger any // Will be typed as logger.Logger
}

// TaskResult represents the outcome of a task execution.
type TaskResult struct {
	// Output contains the task's output data
	Output map[string]any

	// Error contains any error message if the task failed
	Error string

	// Metadata contains additional information about the execution
	Metadata map[string]any
}

// NewTaskResult creates a successful task result with the given output.
func NewTaskResult(output map[string]any) *TaskResult {
	return &TaskResult{
		Output:   output,
		Metadata: make(map[string]any),
	}
}

// NewTaskError creates a failed task result with the given error.
func NewTaskError(err error) *TaskResult {
	return &TaskResult{
		Output: make(map[string]any),
		Error:  err.Error(),
	}
}
