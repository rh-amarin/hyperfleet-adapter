package executor

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/criteria"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
)

// PostActionExecutor executes post-processing actions
type PostActionExecutor struct {
	apiClient hyperfleet_api.Client
}

// NewPostActionExecutor creates a new post-action executor
func NewPostActionExecutor(apiClient hyperfleet_api.Client) *PostActionExecutor {
	return &PostActionExecutor{
		apiClient: apiClient,
	}
}

// ExecuteAll executes all post-processing actions
// First builds payloads from post.payloads, then executes post.postActions
func (pae *PostActionExecutor) ExecuteAll(ctx context.Context, postConfig *config_loader.PostConfig, execCtx *ExecutionContext, log logger.Logger) ([]PostActionResult, error) {
	if postConfig == nil {
		return []PostActionResult{}, nil
	}

	// Step 1: Build post payloads (like clusterStatusPayload)
	if len(postConfig.Payloads) > 0 {
		log.Infof("  Building %d post payloads", len(postConfig.Payloads))
		if err := buildPostPayloads(postConfig.Payloads, execCtx, log); err != nil {
			log.Error(fmt.Sprintf("  Failed to build post payloads: %v", err))
			execCtx.Adapter.ExecutionError = &ExecutionError{
				Phase:   string(PhasePostActions),
				Step:    "build_payloads",
				Message: err.Error(),
			}
			return []PostActionResult{}, NewExecutorError(PhasePostActions, "build_payloads", "failed to build post payloads", err)
		}
		for _, payload := range postConfig.Payloads {
			log.V(1).Infof("    payload[%s] built successfully", payload.Name)
		}
	}

	// Step 2: Execute post actions (sequential - stop on first failure)
	results := make([]PostActionResult, 0, len(postConfig.PostActions))
	for i, action := range postConfig.PostActions {
		log.Infof("  [PostAction %d/%d] Executing: %s", i+1, len(postConfig.PostActions), action.Name)
		result, err := pae.executePostAction(ctx, action, execCtx, log)
		results = append(results, result)

		if err != nil {
			log.Error(fmt.Sprintf("  [PostAction %d/%d] %s: FAILED - %v", i+1, len(postConfig.PostActions), action.Name, err))
			
			// Set ExecutionError for failed post action
			execCtx.Adapter.ExecutionError = &ExecutionError{
				Phase:   string(PhasePostActions),
				Step:    action.Name,
				Message: err.Error(),
			}
			
			// Stop execution - don't run remaining post actions
			return results, err
		}
		log.Infof("  [PostAction %d/%d] %s: SUCCESS ✓", i+1, len(postConfig.PostActions), action.Name)
	}

	return results, nil
}

// buildPostPayloads builds all post payloads and stores them in execCtx.Params
// Payloads are complex structures built from CEL expressions and templates
func buildPostPayloads(payloads []config_loader.Payload, execCtx *ExecutionContext, log logger.Logger) error {
	// Create evaluation context with all CEL variables (params, adapter, resources)
	evalCtx := criteria.NewEvaluationContext()
	evalCtx.SetVariablesFromMap(execCtx.GetCELVariables())

	evaluator := criteria.NewEvaluator(evalCtx, log)

	for _, payload := range payloads {
		// Determine build source (inline Build or BuildRef)
		var buildDef any
		if payload.Build != nil {
			buildDef = payload.Build
		} else if payload.BuildRefContent != nil {
			buildDef = payload.BuildRefContent
		} else {
			return fmt.Errorf("payload '%s' has neither Build nor BuildRefContent", payload.Name)
		}

		// Build the payload
		builtPayload, err := buildPayload(buildDef, evaluator, execCtx.Params, log)
		if err != nil {
			return fmt.Errorf("failed to build payload '%s': %w", payload.Name, err)
		}

		// Convert to JSON for template rendering (templates will render maps as "map[...]" otherwise)
		jsonBytes, err := json.Marshal(builtPayload)
		if err != nil {
			return fmt.Errorf("failed to marshal payload '%s' to JSON: %w", payload.Name, err)
		}

		// Store as JSON string in params for use in post action templates
		execCtx.Params[payload.Name] = string(jsonBytes)
	}

	return nil
}

// buildPayload builds a payload from a build definition
// The build definition can contain expressions that need to be evaluated
func buildPayload(build any, evaluator *criteria.Evaluator, params map[string]any, log logger.Logger) (any, error) {
	switch v := build.(type) {
	case map[string]any:
		return buildMapPayload(v, evaluator, params, log)
	case map[any]any:
		converted := convertToStringKeyMap(v)
		return buildMapPayload(converted, evaluator, params, log)
	default:
		return build, nil
	}
}

// buildMapPayload builds a map payload, evaluating expressions as needed
func buildMapPayload(m map[string]any, evaluator *criteria.Evaluator, params map[string]any, log logger.Logger) (map[string]any, error) {
	result := make(map[string]any)

	for k, v := range m {
		// Render the key
		renderedKey, err := renderTemplate(k, params)
		if err != nil {
			return nil, fmt.Errorf("failed to render key '%s': %w", k, err)
		}

		// Process the value
		processedValue, err := processValue(v, evaluator, params, log)
		if err != nil {
			return nil, fmt.Errorf("failed to process value for key '%s': %w", k, err)
		}

		result[renderedKey] = processedValue
	}

	return result, nil
}

// processValue processes a value, evaluating expressions as needed
func processValue(v any, evaluator *criteria.Evaluator, params map[string]any, log logger.Logger) (any, error) {
	switch val := v.(type) {
	case map[string]any:
		// Check if this is an expression definition
		if result, ok := evaluator.EvaluateExpressionDef(val); ok {
			return result, nil
		}
		
		// Check if this is a simple value definition
		if value, ok := criteria.GetValueDef(val); ok {
			// Render template if it's a string
			if strVal, ok := value.(string); ok {
				return renderTemplate(strVal, params)
			}
			return value, nil
		}

		// Recursively process nested maps
		return buildMapPayload(val, evaluator, params, log)

	case map[any]any:
		converted := convertToStringKeyMap(val)
		return processValue(converted, evaluator, params, log)

	case []any:
		result := make([]any, len(val))
		for i, item := range val {
			processed, err := processValue(item, evaluator, params, log)
			if err != nil {
				return nil, err
			}
			result[i] = processed
		}
		return result, nil

	case string:
		return renderTemplate(val, params)

	default:
		return v, nil
	}
}

// executePostAction executes a single post-action
func (pae *PostActionExecutor) executePostAction(ctx context.Context, action config_loader.PostAction, execCtx *ExecutionContext, log logger.Logger) (PostActionResult, error) {
	result := PostActionResult{
		Name:   action.Name,
		Status: StatusSuccess,
	}

	// Execute log action if configured
	if action.Log != nil {
		ExecuteLogAction(action.Log, execCtx, log)
	}

	// Execute API call if configured
	if action.APICall != nil {
		log.Infof("    Making API call: %s %s", action.APICall.Method, action.APICall.URL)
		if err := pae.executeAPICall(ctx, action.APICall, execCtx, &result, log); err != nil {
			return result, err
		}
		log.Infof("    API call successful: HTTP %d", result.HTTPStatus)
	}

	return result, nil
}

// executeAPICall executes an API call and populates the result with response details
func (pae *PostActionExecutor) executeAPICall(ctx context.Context, apiCall *config_loader.APICall, execCtx *ExecutionContext, result *PostActionResult, log logger.Logger) error {
	resp, url, err := ExecuteAPICall(ctx, apiCall, execCtx, pae.apiClient, log)
	result.APICallMade = true

	// Capture response details if available (even if err != nil)
	if resp != nil {
		result.APIResponse = resp.Body
		result.HTTPStatus = resp.StatusCode
	}

	// Validate response - returns APIError with full metadata if validation fails
	if validationErr := ValidateAPIResponse(resp, err, apiCall.Method, url); validationErr != nil {
		result.Status = StatusFailed
		result.Error = validationErr
		
		// Determine error context
		errorContext := "API call failed"
		if err == nil && resp != nil && !resp.IsSuccess() {
			errorContext = "API call returned non-success status"
		}
		
		return NewExecutorError(PhasePostActions, result.Name, errorContext, validationErr)
	}

	return nil
}
