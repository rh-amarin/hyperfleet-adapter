package tasks

import (
	"context"
	"fmt"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/criteria"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
)

// CELTaskRunner implements the hf:cel task for CEL expression evaluation.
// It evaluates CEL expressions against the workflow context.
type CELTaskRunner struct {
	log logger.Logger
}

// NewCELTaskRunner creates a new CEL task runner.
func NewCELTaskRunner(deps *Dependencies) (TaskRunner, error) {
	var log logger.Logger
	if deps != nil && deps.Logger != nil {
		var ok bool
		log, ok = deps.Logger.(logger.Logger)
		if !ok {
			log = &noopLogger{}
		}
	} else {
		log = &noopLogger{}
	}

	return &CELTaskRunner{log: log}, nil
}

func (r *CELTaskRunner) Name() string {
	return TaskCEL
}

// Run executes the CEL expression evaluation task.
// Args should contain:
//   - expression: The CEL expression to evaluate
//   - variables: Optional map of additional variables for evaluation
//
// Returns a map with:
//   - value: The evaluation result
//   - valueType: The CEL type of the result
//   - matched: Boolean indicating if the result is truthy
//   - error: Error message if evaluation failed
func (r *CELTaskRunner) Run(ctx context.Context, args map[string]any, input map[string]any) (map[string]any, error) {
	expression, ok := args["expression"].(string)
	if !ok || expression == "" {
		return nil, fmt.Errorf("expression is required for hf:cel task")
	}

	// Build evaluation context from input and additional variables
	evalCtx := criteria.NewEvaluationContext()

	// Add input data
	for k, v := range input {
		evalCtx.Set(k, v)
	}

	// Add explicit variables if provided
	if vars, ok := args["variables"].(map[string]any); ok {
		for k, v := range vars {
			evalCtx.Set(k, v)
		}
	}

	// Create evaluator using the public API
	evaluator, err := criteria.NewEvaluator(ctx, evalCtx, r.log)
	if err != nil {
		return nil, fmt.Errorf("failed to create evaluator: %w", err)
	}

	// Evaluate the expression
	result, err := evaluator.EvaluateCEL(expression)
	if err != nil {
		return nil, fmt.Errorf("CEL parse error: %w", err)
	}

	output := map[string]any{
		"value":      result.Value,
		"valueType":  result.ValueType,
		"matched":    result.Matched,
		"expression": result.Expression,
	}

	if result.Error != nil {
		output["error"] = result.Error.Error()
	}

	// Merge with input for workflow continuation
	for k, v := range input {
		if _, exists := output[k]; !exists {
			output[k] = v
		}
	}

	return output, nil
}

// CELConditionRunner implements the hf:cel task variant for condition evaluation.
// It evaluates structured conditions against the workflow context.
type CELConditionRunner struct {
	log logger.Logger
}

// NewCELConditionRunner creates a new condition runner.
func NewCELConditionRunner(deps *Dependencies) (TaskRunner, error) {
	var log logger.Logger
	if deps != nil && deps.Logger != nil {
		var ok bool
		log, ok = deps.Logger.(logger.Logger)
		if !ok {
			log = &noopLogger{}
		}
	} else {
		log = &noopLogger{}
	}

	return &CELConditionRunner{log: log}, nil
}

func (r *CELConditionRunner) Name() string {
	return "hf:condition"
}

// Run evaluates conditions against the workflow context.
// Args should contain:
//   - conditions: Array of condition objects with field, operator, value
//
// Returns a map with:
//   - matched: Boolean indicating if all conditions matched
//   - results: Array of individual condition results
func (r *CELConditionRunner) Run(ctx context.Context, args map[string]any, input map[string]any) (map[string]any, error) {
	conditionsRaw, ok := args["conditions"].([]any)
	if !ok || len(conditionsRaw) == 0 {
		// No conditions means matched
		return map[string]any{
			"matched": true,
			"results": []any{},
		}, nil
	}

	// Build evaluation context
	evalCtx := criteria.NewEvaluationContext()
	for k, v := range input {
		evalCtx.Set(k, v)
	}

	// Parse conditions
	var conditions []criteria.ConditionDef
	for _, c := range conditionsRaw {
		condMap, ok := c.(map[string]any)
		if !ok {
			continue
		}

		field, _ := condMap["field"].(string)
		operator, _ := condMap["operator"].(string)
		value := condMap["value"]

		conditions = append(conditions, criteria.ConditionDef{
			Field:    field,
			Operator: criteria.Operator(operator),
			Value:    value,
		})
	}

	// Create evaluator
	evaluator, err := criteria.NewEvaluator(ctx, evalCtx, r.log)
	if err != nil {
		return nil, fmt.Errorf("failed to create evaluator: %w", err)
	}

	// Evaluate conditions
	result, err := evaluator.EvaluateConditions(conditions)
	if err != nil {
		return nil, fmt.Errorf("condition evaluation failed: %w", err)
	}

	// Build results array
	results := make([]any, 0, len(result.Results))
	for _, r := range result.Results {
		results = append(results, map[string]any{
			"field":         r.Field,
			"operator":      string(r.Operator),
			"expectedValue": r.ExpectedValue,
			"fieldValue":    r.FieldValue,
			"matched":       r.Matched,
		})
	}

	output := map[string]any{
		"matched": result.Matched,
		"results": results,
	}

	// Merge with input
	for k, v := range input {
		if _, exists := output[k]; !exists {
			output[k] = v
		}
	}

	return output, nil
}

// noopLogger is a minimal logger for use when real logging is not available.
type noopLogger struct{}

func (l *noopLogger) Debug(ctx context.Context, msg string)                    {}
func (l *noopLogger) Debugf(ctx context.Context, format string, args ...any)   {}
func (l *noopLogger) Info(ctx context.Context, msg string)                     {}
func (l *noopLogger) Infof(ctx context.Context, format string, args ...any)    {}
func (l *noopLogger) Warn(ctx context.Context, msg string)                     {}
func (l *noopLogger) Warnf(ctx context.Context, format string, args ...any)    {}
func (l *noopLogger) Error(ctx context.Context, msg string)                    {}
func (l *noopLogger) Errorf(ctx context.Context, format string, args ...any)   {}
func (l *noopLogger) Fatal(ctx context.Context, msg string)                    {}
func (l *noopLogger) With(key string, value any) logger.Logger                 { return l }
func (l *noopLogger) WithFields(fields map[string]any) logger.Logger           { return l }
func (l *noopLogger) Without(key string) logger.Logger                         { return l }

func init() {
	// Register the CEL task runner in the default registry
	_ = RegisterDefault(TaskCEL, NewCELTaskRunner)
}
