package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/swf/tasks"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	"github.com/serverlessworkflow/sdk-go/v3/impl/expr"
	"github.com/serverlessworkflow/sdk-go/v3/model"
)

// Runner executes Serverless Workflow definitions with HyperFleet custom tasks.
// It extends the SDK's workflow execution with custom task runners for
// Kubernetes operations, API calls, CEL expressions, and more.
type Runner struct {
	workflow     *model.Workflow
	taskRegistry *tasks.Registry
	deps         *tasks.Dependencies
	log          logger.Logger
	httpClient   *http.Client
}

// RunnerConfig holds configuration for creating a Runner.
type RunnerConfig struct {
	// Workflow is the SWF workflow definition to execute
	Workflow *model.Workflow

	// TaskRegistry is the registry of custom task runners (optional, uses default if nil)
	TaskRegistry *tasks.Registry

	// K8sClient is the Kubernetes client for resource operations
	K8sClient k8s_client.K8sClient

	// APIClient is the HyperFleet API client for HTTP calls
	APIClient hyperfleet_api.Client

	// Logger is the logging interface
	Logger logger.Logger
}

// NewRunner creates a new workflow runner with the given configuration.
func NewRunner(cfg *RunnerConfig) (*Runner, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if cfg.Workflow == nil {
		return nil, fmt.Errorf("workflow is required")
	}
	if cfg.Logger == nil {
		return nil, fmt.Errorf("logger is required")
	}

	registry := cfg.TaskRegistry
	if registry == nil {
		registry = tasks.DefaultRegistry()
	}

	deps := &tasks.Dependencies{
		K8sClient: cfg.K8sClient,
		APIClient: cfg.APIClient,
		Logger:    cfg.Logger,
	}

	// Create HTTP client with sensible defaults for native SWF HTTP tasks
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	return &Runner{
		workflow:     cfg.Workflow,
		taskRegistry: registry,
		deps:         deps,
		log:          cfg.Logger,
		httpClient:   httpClient,
	}, nil
}

// Run executes the workflow with the given input.
// The input should contain event data and configuration.
func (r *Runner) Run(ctx context.Context, input map[string]any) (*WorkflowResult, error) {
	r.log.Info(ctx, "Starting workflow execution")

	// Inject environment variables into input for native SWF expression access
	// This allows workflows to use ${ .env.HYPERFLEET_API_BASE_URL } syntax
	input["env"] = r.collectEnvironmentVariables()

	// Create workflow context
	eventData, _ := input["event"].(map[string]any)
	if eventData == nil {
		eventData = make(map[string]any)
	}
	wfCtx := NewWorkflowContext(ctx, eventData)

	// Execute workflow tasks
	result, err := r.executeWorkflow(wfCtx, input)
	if err != nil {
		r.log.Errorf(ctx, "Workflow execution failed: %v", err)
		return nil, err
	}

	r.log.Info(ctx, "Workflow execution completed")
	return result, nil
}

// collectEnvironmentVariables reads HYPERFLEET_* prefixed environment variables.
// Returns a map with both the original name and a short version without prefix.
// Example: HYPERFLEET_API_BASE_URL is accessible as both
// .env.HYPERFLEET_API_BASE_URL and .env.API_BASE_URL
//
// Note: Returns map[string]any (not map[string]string) because gojq requires
// interface{} types for proper JSON-like traversal.
func (r *Runner) collectEnvironmentVariables() map[string]any {
	envVars := make(map[string]any)
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "HYPERFLEET_") {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) == 2 {
				// Store with original name
				envVars[parts[0]] = parts[1]
				// Also store without prefix for cleaner access
				key := strings.TrimPrefix(parts[0], "HYPERFLEET_")
				envVars[key] = parts[1]
			}
		}
	}
	return envVars
}

// executeWorkflow processes the workflow's task list.
func (r *Runner) executeWorkflow(wfCtx *WorkflowContext, input map[string]any) (*WorkflowResult, error) {
	result := &WorkflowResult{
		Status:       StatusSuccess,
		CurrentPhase: PhaseParamExtraction,
		Params:       make(map[string]any),
		Errors:       make(map[ExecutionPhase]error),
	}

	if r.workflow.Do == nil {
		return result, nil
	}

	// Process each task in the workflow
	currentOutput := input
	for _, taskItem := range *r.workflow.Do {
		taskOutput, err := r.executeTask(wfCtx, taskItem, currentOutput)
		if err != nil {
			result.Status = StatusFailed
			result.Errors[wfCtx.CurrentPhase] = err
			return result, err
		}

		// Update output for next task
		if taskOutput != nil {
			currentOutput = taskOutput
		}
	}

	// Populate result from context
	result.Params = wfCtx.Params
	result.ResourcesSkipped = wfCtx.ResourcesSkipped
	result.SkipReason = wfCtx.SkipReason
	result.Output = currentOutput

	return result, nil
}

// executeTask executes a single task from the workflow.
func (r *Runner) executeTask(wfCtx *WorkflowContext, taskItem *model.TaskItem, input map[string]any) (map[string]any, error) {
	taskName := taskItem.Key

	r.log.Debugf(wfCtx.Ctx, "Executing task: %s", taskName)

	// Check if condition - skip task if evaluates to false
	if taskItem.Task.GetBase() != nil && taskItem.Task.GetBase().If != nil {
		shouldExecute, err := r.evaluateIfCondition(wfCtx.Ctx, taskItem.Task.GetBase().If, input)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate if condition for task %s: %w", taskName, err)
		}
		if !shouldExecute {
			r.log.Debugf(wfCtx.Ctx, "Skipping task %s: if condition evaluated to false", taskName)
			return input, nil
		}
	}

	// Check if this is a custom HyperFleet task (CallFunction with hf: prefix)
	if callFunc, ok := taskItem.Task.(*model.CallFunction); ok && tasks.IsHyperFleetTask(callFunc.Call) {
		return r.executeCustomTask(wfCtx, taskName, callFunc, input)
	}

	// Handle other built-in task types
	switch t := taskItem.Task.(type) {
	case *model.SetTask:
		return r.executeSetTask(wfCtx, taskName, t, input)
	case *model.DoTask:
		return r.executeDoTask(wfCtx, taskName, t, input)
	case *model.ForTask:
		return r.executeForTask(wfCtx, taskName, t, input)
	case *model.SwitchTask:
		return r.executeSwitchTask(wfCtx, taskName, t, input)
	case *model.CallHTTP:
		return r.executeHTTPTask(wfCtx, taskName, t, input)
	case *model.TryTask:
		return r.executeTryTask(wfCtx, taskName, t, input)
	default:
		return nil, fmt.Errorf("unsupported task type %T for task %s", t, taskName)
	}
}

// executeCustomTask executes a HyperFleet custom task.
func (r *Runner) executeCustomTask(wfCtx *WorkflowContext, taskName string, callFunc *model.CallFunction, input map[string]any) (map[string]any, error) {
	// Get the task runner from registry
	runner, err := r.taskRegistry.Create(callFunc.Call, r.deps)
	if err != nil {
		return nil, fmt.Errorf("failed to create task runner for %s: %w", callFunc.Call, err)
	}

	// Prepare arguments from the 'with' field
	args := callFunc.With
	if args == nil {
		args = make(map[string]any)
	}

	// Execute the task
	r.log.Debugf(wfCtx.Ctx, "Executing custom task: %s (type: %s)", taskName, callFunc.Call)
	output, err := runner.Run(wfCtx.Ctx, args, input)
	if err != nil {
		return nil, fmt.Errorf("task %s failed: %w", taskName, err)
	}

	// Handle export if specified
	if callFunc.Export != nil && callFunc.Export.As != nil {
		// Export output to workflow context
		// This is simplified - full implementation would evaluate JQ expression
		r.log.Debugf(wfCtx.Ctx, "Exporting task output: %s", taskName)
	}

	return output, nil
}

// executeSetTask handles the Set task type.
// It evaluates JQ runtime expressions (e.g., ${ .env.HYPERFLEET_API_BASE_URL })
// and sets the resulting values in the workflow context.
//
// For compatibility with HyperFleet custom tasks (hf:preconditions, hf:post, etc.),
// values are also stored under output["params"] since those tasks expect params there.
func (r *Runner) executeSetTask(wfCtx *WorkflowContext, taskName string, task *model.SetTask, input map[string]any) (map[string]any, error) {
	// Copy input to output
	output := make(map[string]any)
	for k, v := range input {
		output[k] = v
	}

	// Get or create the params map for HyperFleet task compatibility
	params, _ := output["params"].(map[string]any)
	if params == nil {
		params = make(map[string]any)
		output["params"] = params
	}

	if task.Set != nil {
		for k, v := range task.Set {
			// Evaluate the value using the SDK's expression evaluator
			// This handles runtime expressions like ${ .env.VAR_NAME // "default" }
			evaluated, err := expr.TraverseAndEvaluate(v, input, wfCtx.Ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to evaluate expression for '%s': %w", k, err)
			}

			// Store at root level (standard SWF behavior)
			output[k] = evaluated
			// Also store in params for HyperFleet task compatibility
			params[k] = evaluated
			wfCtx.SetParam(k, evaluated)
			r.log.Debugf(wfCtx.Ctx, "Set %s = %v", k, evaluated)
		}
	}

	return output, nil
}

// executeDoTask handles nested task lists.
func (r *Runner) executeDoTask(wfCtx *WorkflowContext, taskName string, task *model.DoTask, input map[string]any) (map[string]any, error) {
	if task.Do == nil {
		return input, nil
	}

	currentOutput := input
	for _, nestedTask := range *task.Do {
		output, err := r.executeTask(wfCtx, nestedTask, currentOutput)
		if err != nil {
			return nil, err
		}
		if output != nil {
			currentOutput = output
		}
	}

	return currentOutput, nil
}

// executeForTask handles iteration over collections.
func (r *Runner) executeForTask(wfCtx *WorkflowContext, taskName string, task *model.ForTask, input map[string]any) (map[string]any, error) {
	// Simplified for loop implementation
	// Full implementation would evaluate 'in' expression and iterate

	r.log.Debugf(wfCtx.Ctx, "For task %s: iteration not yet implemented", taskName)
	return input, nil
}

// executeSwitchTask handles conditional branching.
func (r *Runner) executeSwitchTask(wfCtx *WorkflowContext, taskName string, task *model.SwitchTask, input map[string]any) (map[string]any, error) {
	// Simplified switch implementation
	// Full implementation would evaluate 'when' expressions

	r.log.Debugf(wfCtx.Ctx, "Switch task %s: conditional branching not yet implemented", taskName)
	return input, nil
}

// evaluateIfCondition evaluates the `if` condition for a task.
// Returns true if the task should execute, false if it should be skipped.
func (r *Runner) evaluateIfCondition(ctx context.Context, ifExpr *model.RuntimeExpression, input map[string]any) (bool, error) {
	if ifExpr == nil {
		return true, nil
	}

	result, err := expr.TraverseAndEvaluate(ifExpr.Value, input, ctx)
	if err != nil {
		return false, fmt.Errorf("if expression evaluation failed: %w", err)
	}

	// Convert result to boolean
	switch v := result.(type) {
	case bool:
		return v, nil
	case nil:
		return false, nil
	default:
		// Any non-nil, non-false value is truthy
		return true, nil
	}
}

// executeHTTPTask executes a native SWF HTTP call task.
func (r *Runner) executeHTTPTask(wfCtx *WorkflowContext, taskName string, task *model.CallHTTP, input map[string]any) (map[string]any, error) {
	r.log.Debugf(wfCtx.Ctx, "Executing HTTP task: %s", taskName)

	// Evaluate endpoint URL
	endpointStr := task.With.Endpoint.String()
	evaluatedEndpoint, err := expr.TraverseAndEvaluate(endpointStr, input, wfCtx.Ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate endpoint for task %s: %w", taskName, err)
	}

	urlStr, ok := evaluatedEndpoint.(string)
	if !ok {
		urlStr = endpointStr
	}

	r.log.Debugf(wfCtx.Ctx, "HTTP %s %s", task.With.Method, urlStr)

	// Prepare request body
	var bodyReader io.Reader
	if task.With.Body != nil {
		// First unmarshal the RawMessage to get the actual value
		var bodyValue any
		if err := json.Unmarshal(task.With.Body, &bodyValue); err != nil {
			// If unmarshal fails, use raw bytes
			bodyReader = bytes.NewReader(task.With.Body)
		} else {
			// Check if it's a string that might be a runtime expression
			if bodyStr, ok := bodyValue.(string); ok {
				// Evaluate as potential jq expression
				bodyData, err := expr.TraverseAndEvaluate(bodyStr, input, wfCtx.Ctx)
				if err != nil {
					// Not an expression, use the string as-is
					bodyReader = strings.NewReader(bodyStr)
				} else {
					switch b := bodyData.(type) {
					case string:
						bodyReader = strings.NewReader(b)
					case []byte:
						bodyReader = bytes.NewReader(b)
					default:
						jsonBody, err := json.Marshal(b)
						if err != nil {
							return nil, fmt.Errorf("failed to marshal request body: %w", err)
						}
						bodyReader = bytes.NewReader(jsonBody)
					}
				}
			} else {
				// It's already an object/array, marshal it back to JSON
				jsonBody, err := json.Marshal(bodyValue)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal request body: %w", err)
				}
				bodyReader = bytes.NewReader(jsonBody)
			}
		}
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(wfCtx.Ctx, strings.ToUpper(task.With.Method), urlStr, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	for key, value := range task.With.Headers {
		evaluatedValue, err := expr.TraverseAndEvaluate(value, input, wfCtx.Ctx)
		if err != nil {
			req.Header.Set(key, value)
		} else if strVal, ok := evaluatedValue.(string); ok {
			req.Header.Set(key, strVal)
		} else {
			req.Header.Set(key, value)
		}
	}

	// Set Content-Type if not already set and we have a body
	if bodyReader != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	// Execute request
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse response as JSON if possible
	var content any
	if err := json.Unmarshal(respBody, &content); err != nil {
		content = string(respBody)
	}

	// Build response object based on output mode
	httpResponse := map[string]any{
		"statusCode": resp.StatusCode,
		"status":     resp.Status,
		"headers":    headerToMap(resp.Header),
		"body":       string(respBody),
		"content":    content,
	}

	// Determine output based on output mode
	var output any
	switch task.With.Output {
	case "raw":
		output = string(respBody)
	case "content":
		output = content
	case "response":
		output = httpResponse
	default:
		// Default is content
		output = content
	}

	// Copy input to result
	result := make(map[string]any)
	for k, v := range input {
		result[k] = v
	}

	// Store HTTP response in result
	result["response"] = httpResponse
	result["content"] = content

	// Apply export if specified
	if task.Export != nil && task.Export.As != nil {
		exportedResult, err := r.applyExport(wfCtx.Ctx, task.Export, result, output)
		if err != nil {
			return nil, fmt.Errorf("failed to apply export for task %s: %w", taskName, err)
		}
		return exportedResult, nil
	}

	return result, nil
}

// executeTryTask handles try/catch blocks with retry support.
func (r *Runner) executeTryTask(wfCtx *WorkflowContext, taskName string, task *model.TryTask, input map[string]any) (map[string]any, error) {
	r.log.Debugf(wfCtx.Ctx, "Executing try task: %s", taskName)

	if task.Try == nil {
		return input, nil
	}

	var lastErr error
	maxAttempts := 1

	// Determine retry configuration
	if task.Catch != nil && task.Catch.Retry != nil && task.Catch.Retry.Limit.Attempt != nil {
		maxAttempts = task.Catch.Retry.Limit.Attempt.Count
		if maxAttempts < 1 {
			maxAttempts = 1
		}
	}

	currentOutput := input

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			r.log.Debugf(wfCtx.Ctx, "Try task %s: retry attempt %d/%d", taskName, attempt, maxAttempts)

			// Apply backoff delay if configured
			if task.Catch != nil && task.Catch.Retry != nil && task.Catch.Retry.Backoff != nil {
				delay := r.calculateBackoffDelay(task.Catch.Retry.Backoff, attempt)
				if delay > 0 {
					select {
					case <-time.After(delay):
					case <-wfCtx.Ctx.Done():
						return nil, wfCtx.Ctx.Err()
					}
				}
			}
		}

		// Execute try block
		var tryErr error
		tryOutput := currentOutput
		for _, tryTask := range *task.Try {
			output, err := r.executeTask(wfCtx, tryTask, tryOutput)
			if err != nil {
				tryErr = err
				break
			}
			if output != nil {
				tryOutput = output
			}
		}

		if tryErr == nil {
			// Success
			return tryOutput, nil
		}

		lastErr = tryErr
		r.log.Debugf(wfCtx.Ctx, "Try task %s: attempt %d failed: %v", taskName, attempt, tryErr)
	}

	// All retries exhausted, execute catch.do if present
	if task.Catch != nil && task.Catch.Do != nil {
		r.log.Debugf(wfCtx.Ctx, "Try task %s: executing catch block", taskName)

		// Set error information in context
		catchInput := make(map[string]any)
		for k, v := range currentOutput {
			catchInput[k] = v
		}
		if task.Catch.As != "" {
			catchInput[task.Catch.As] = map[string]any{
				"message": lastErr.Error(),
			}
		}

		for _, catchTask := range *task.Catch.Do {
			output, err := r.executeTask(wfCtx, catchTask, catchInput)
			if err != nil {
				return nil, fmt.Errorf("catch block failed: %w", err)
			}
			if output != nil {
				catchInput = output
			}
		}
		return catchInput, nil
	}

	return nil, fmt.Errorf("try task %s failed after %d attempts: %w", taskName, maxAttempts, lastErr)
}

// calculateBackoffDelay calculates the delay for retry backoff.
func (r *Runner) calculateBackoffDelay(backoff *model.RetryBackoff, attempt int) time.Duration {
	baseDelay := 1 * time.Second

	if backoff.Constant != nil {
		return baseDelay
	}

	if backoff.Linear != nil {
		return baseDelay * time.Duration(attempt)
	}

	if backoff.Exponential != nil {
		// Exponential backoff: base * 2^(attempt-1)
		return baseDelay * time.Duration(1<<uint(attempt-1))
	}

	return baseDelay
}

// applyExport applies the export transformation to the task output.
func (r *Runner) applyExport(ctx context.Context, export *model.Export, input map[string]any, taskOutput any) (map[string]any, error) {
	if export == nil || export.As == nil {
		return input, nil
	}

	// The export.As contains a jq expression that transforms the current state
	// For our use case, we need to merge taskOutput into the input based on the expression
	exportExpr := export.As.AsStringOrMap()

	switch e := exportExpr.(type) {
	case string:
		// Evaluate the jq expression
		// The expression typically looks like: ${ . + { key: .content.value } }
		// We need to make taskOutput available as the evaluation context
		evalContext := make(map[string]any)
		for k, v := range input {
			evalContext[k] = v
		}
		// Make task output available under common keys
		if content, ok := taskOutput.(map[string]any); ok {
			evalContext["content"] = content
		} else {
			evalContext["content"] = taskOutput
		}

		result, err := expr.TraverseAndEvaluate(e, evalContext, ctx)
		if err != nil {
			return nil, fmt.Errorf("export expression evaluation failed: %w", err)
		}

		if resultMap, ok := result.(map[string]any); ok {
			return resultMap, nil
		}

		// If result is not a map, merge it into input
		output := make(map[string]any)
		for k, v := range input {
			output[k] = v
		}
		output["exported"] = result
		return output, nil

	case map[string]any:
		// Export is a structured object, evaluate each field
		output := make(map[string]any)
		for k, v := range input {
			output[k] = v
		}
		for k, v := range e {
			if strVal, ok := v.(string); ok {
				evalContext := make(map[string]any)
				for ik, iv := range input {
					evalContext[ik] = iv
				}
				evalContext["content"] = taskOutput
				evaluated, err := expr.TraverseAndEvaluate(strVal, evalContext, ctx)
				if err != nil {
					output[k] = v
				} else {
					output[k] = evaluated
				}
			} else {
				output[k] = v
			}
		}
		return output, nil
	}

	return input, nil
}

// headerToMap converts http.Header to a simple map.
func headerToMap(h http.Header) map[string]string {
	result := make(map[string]string)
	for k, v := range h {
		if len(v) > 0 {
			result[k] = v[0]
		}
	}
	return result
}

// GetWorkflow returns the workflow definition.
func (r *Runner) GetWorkflow() *model.Workflow {
	return r.workflow
}

// RunnerBuilder provides a fluent interface for building a Runner.
type RunnerBuilder struct {
	config *RunnerConfig
}

// NewRunnerBuilder creates a new builder.
func NewRunnerBuilder() *RunnerBuilder {
	return &RunnerBuilder{
		config: &RunnerConfig{},
	}
}

// WithWorkflow sets the workflow definition.
func (b *RunnerBuilder) WithWorkflow(workflow *model.Workflow) *RunnerBuilder {
	b.config.Workflow = workflow
	return b
}

// WithTaskRegistry sets a custom task registry.
func (b *RunnerBuilder) WithTaskRegistry(registry *tasks.Registry) *RunnerBuilder {
	b.config.TaskRegistry = registry
	return b
}

// WithK8sClient sets the Kubernetes client.
func (b *RunnerBuilder) WithK8sClient(client k8s_client.K8sClient) *RunnerBuilder {
	b.config.K8sClient = client
	return b
}

// WithAPIClient sets the HyperFleet API client.
func (b *RunnerBuilder) WithAPIClient(client hyperfleet_api.Client) *RunnerBuilder {
	b.config.APIClient = client
	return b
}

// WithLogger sets the logger.
func (b *RunnerBuilder) WithLogger(log logger.Logger) *RunnerBuilder {
	b.config.Logger = log
	return b
}

// Build creates the Runner.
func (b *RunnerBuilder) Build() (*Runner, error) {
	return NewRunner(b.config)
}
