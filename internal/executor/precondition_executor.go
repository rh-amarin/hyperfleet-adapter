package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/criteria"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
)

// PreconditionExecutor evaluates preconditions
type PreconditionExecutor struct {
	apiClient hyperfleet_api.Client
	log       logger.Logger
}

// newPreconditionExecutor creates a new precondition executor
// NOTE: Caller (NewExecutor) is responsible for config validation
func newPreconditionExecutor(config *ExecutorConfig) *PreconditionExecutor {
	return &PreconditionExecutor{
		apiClient: config.APIClient,
		log:       config.Logger,
	}
}

// ExecuteAll executes all preconditions in sequence
// Returns a high-level outcome with match status and individual results
func (pe *PreconditionExecutor) ExecuteAll(ctx context.Context, preconditions []config_loader.Precondition, execCtx *ExecutionContext) *PreconditionsOutcome {
	results := make([]PreconditionResult, 0, len(preconditions))

	for i, precond := range preconditions {
		pe.log.Infof(ctx, "  [Precondition %d/%d] Evaluating: %s", i+1, len(preconditions), precond.Name)
		result, err := pe.executePrecondition(ctx, precond, execCtx)
		results = append(results, result)

		if err != nil {
			// Execution error (API call failed, parse error, etc.)
			pe.log.Errorf(ctx, "  [Precondition %d/%d] %s: EXECUTION ERROR - %v", i+1, len(preconditions), precond.Name, err)
			return &PreconditionsOutcome{
				AllMatched: false,
				Results:    results,
				Error:      err,
			}
		}

		if !result.Matched {
			// Business outcome: precondition not satisfied
			pe.log.Infof(ctx, "  [Precondition %d/%d] %s: NOT MET", i+1, len(preconditions), precond.Name)
			return &PreconditionsOutcome{
				AllMatched:   false,
				Results:      results,
				Error:        nil,
				NotMetReason: fmt.Sprintf("precondition '%s' not met: %s", precond.Name, formatConditionDetails(result)),
			}
		}

		pe.log.Infof(ctx, "  [Precondition %d/%d] %s: MET ✓", i+1, len(preconditions), precond.Name)
	}

	// All preconditions matched
	return &PreconditionsOutcome{
		AllMatched: true,
		Results:    results,
		Error:      nil,
	}
}

// executePrecondition executes a single precondition
func (pe *PreconditionExecutor) executePrecondition(ctx context.Context, precond config_loader.Precondition, execCtx *ExecutionContext) (PreconditionResult, error) {
	result := PreconditionResult{
		Name:           precond.Name,
		Status:         StatusSuccess,
		CapturedFields: make(map[string]interface{}),
	}

	// Step 1: Execute log action if configured
	if precond.Log != nil {
		ExecuteLogAction(ctx, precond.Log, execCtx, pe.log)
	}

	// Step 2: Make API call if configured
	if precond.APICall != nil {
		pe.log.Infof(ctx, "    Making API call: %s %s", precond.APICall.Method, precond.APICall.URL)
		apiResult, err := pe.executeAPICall(ctx, precond.APICall, execCtx)
		if err != nil {
			result.Status = StatusFailed
			result.Error = err

			// Set ExecutionError for API call failure
			execCtx.Adapter.ExecutionError = &ExecutionError{
				Phase:   string(PhasePreconditions),
				Step:    precond.Name,
				Message: err.Error(),
			}

			return result, NewExecutorError(PhasePreconditions, precond.Name, "API call failed", err)
		}
		result.APICallMade = true
		result.APIResponse = apiResult

		// Parse response as JSON
		var responseData map[string]interface{}
		if err := json.Unmarshal(apiResult, &responseData); err != nil {
			result.Status = StatusFailed
			result.Error = fmt.Errorf("failed to parse API response as JSON: %w", err)

			// Set ExecutionError for parse failure
			execCtx.Adapter.ExecutionError = &ExecutionError{
				Phase:   string(PhasePreconditions),
				Step:    precond.Name,
				Message: err.Error(),
			}

			return result, NewExecutorError(PhasePreconditions, precond.Name, "failed to parse API response", err)
		}

		// Capture fields from response
		if len(precond.Capture) > 0 {
			pe.log.Infof(ctx, "    Capturing %d fields from API response", len(precond.Capture))
			for _, capture := range precond.Capture {
				value, err := captureFieldFromData(responseData, capture.Field)
				if err != nil {
					pe.log.Warnf(ctx, "    Failed to capture field '%s' as '%s': %v", capture.Field, capture.Name, err)
					continue
				}
				result.CapturedFields[capture.Name] = value
				execCtx.Params[capture.Name] = value
				pe.log.Debugf(ctx, "    captured[%s] = %v (from %s)", capture.Name, value, capture.Field)
			}
		}
		pe.log.Infof(ctx, "    API call successful, response captured")
	}

	// Step 3: Evaluate conditions
	// Create evaluation context with all CEL variables (params, adapter, resources)
	// Note: resources will be empty during preconditions since they haven't been created yet
	evalCtx := criteria.NewEvaluationContext()
	evalCtx.SetVariablesFromMap(execCtx.GetCELVariables())

	evaluator, err := criteria.NewEvaluator(ctx, evalCtx, pe.log)
	if err != nil {
		result.Status = StatusFailed
		result.Error = err
		return result, NewExecutorError(PhasePreconditions, precond.Name, "failed to create evaluator", err)
	}

	// Evaluate using structured conditions or CEL expression
	if len(precond.Conditions) > 0 {
		pe.log.Infof(ctx, "    Evaluating %d structured conditions", len(precond.Conditions))
		condDefs := ToConditionDefs(precond.Conditions)

		condResult, err := evaluator.EvaluateConditionsWithResult(condDefs)
		if err != nil {
			result.Status = StatusFailed
			result.Error = err
			return result, NewExecutorError(PhasePreconditions, precond.Name, "condition evaluation failed", err)
		}

		result.Matched = condResult.Matched
		result.ConditionResults = condResult.Results

		// Log individual condition results
		for _, cr := range condResult.Results {
			if cr.Matched {
				pe.log.Debugf(ctx, "    condition: %s %s %v = %v ✓", cr.Field, cr.Operator, cr.ExpectedValue, cr.FieldValue)
			} else {
				pe.log.Infof(ctx, "    condition FAILED: %s %s %v (actual: %v)", cr.Field, cr.Operator, cr.ExpectedValue, cr.FieldValue)
			}
		}

		// Record evaluation in execution context - reuse criteria.EvaluationResult directly
		fieldResults := make(map[string]criteria.EvaluationResult, len(condResult.Results))
		for _, cr := range condResult.Results {
			fieldResults[cr.Field] = cr
		}
		execCtx.AddConditionsEvaluation(PhasePreconditions, precond.Name, condResult.Matched, fieldResults)
	} else if precond.Expression != "" {
		// Evaluate CEL expression
		pe.log.Infof(ctx, "    Evaluating CEL expression")
		pe.log.Debugf(ctx, "    expression: %s", strings.TrimSpace(precond.Expression))
		celResult, err := evaluator.EvaluateCEL(strings.TrimSpace(precond.Expression))
		if err != nil {
			result.Status = StatusFailed
			result.Error = err
			return result, NewExecutorError(PhasePreconditions, precond.Name, "CEL expression evaluation failed", err)
		}

		result.Matched = celResult.Matched
		result.CELResult = celResult
		pe.log.Infof(ctx, "    CEL result: matched=%v value=%v", celResult.Matched, celResult.Value)

		// Record CEL evaluation in execution context
		execCtx.AddCELEvaluation(PhasePreconditions, precond.Name, precond.Expression, celResult.Matched)
	} else {
		// No conditions specified - consider it matched
		pe.log.Infof(ctx, "    No conditions specified, auto-matched")
		result.Matched = true
	}

	return result, nil
}

// executeAPICall executes an API call and returns the response body for field capture
func (pe *PreconditionExecutor) executeAPICall(ctx context.Context, apiCall *config_loader.APICall, execCtx *ExecutionContext) ([]byte, error) {
	resp, url, err := ExecuteAPICall(ctx, apiCall, execCtx, pe.apiClient, pe.log)

	// Validate response - returns APIError with full metadata if validation fails
	if validationErr := ValidateAPIResponse(resp, err, apiCall.Method, url); validationErr != nil {
		return nil, validationErr
	}

	return resp.Body, nil
}

// captureFieldFromData captures a field from API response data using dot notation
func captureFieldFromData(data map[string]interface{}, path string) (interface{}, error) {
	parts := strings.Split(path, ".")
	var current interface{} = data

	for i, part := range parts {
		switch v := current.(type) {
		case map[string]interface{}:
			val, ok := v[part]
			if !ok {
				return nil, fmt.Errorf("field '%s' not found at path '%s'", part, strings.Join(parts[:i+1], "."))
			}
			current = val
		case map[interface{}]interface{}:
			val, ok := v[part]
			if !ok {
				return nil, fmt.Errorf("field '%s' not found at path '%s'", part, strings.Join(parts[:i+1], "."))
			}
			current = val
		default:
			return nil, fmt.Errorf("cannot access field '%s': parent is not a map (got %T)", part, current)
		}
	}

	return current, nil
}

// formatConditionDetails formats condition evaluation details for error messages
func formatConditionDetails(result PreconditionResult) string {
	var details []string

	if result.CELResult != nil && result.CELResult.HasError() {
		details = append(details, fmt.Sprintf("CEL error: %s", result.CELResult.ErrorReason))
	}

	for _, condResult := range result.ConditionResults {
		if !condResult.Matched {
			details = append(details, fmt.Sprintf("%s %s %v (actual: %v)",
				condResult.Field, condResult.Operator, condResult.ExpectedValue, condResult.FieldValue))
		}
	}

	if len(details) == 0 {
		return "no specific details available"
	}

	return strings.Join(details, "; ")
}
