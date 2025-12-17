package criteria

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	apperrors "github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/errors"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
)

// CELEvaluator evaluates CEL expressions against a context
type CELEvaluator struct {
	env       *cel.Env
	evalCtx   *EvaluationContext
	log       logger.Logger
	ctx     context.Context
}

// CELResult contains the result of evaluating a CEL expression.
// When using EvaluateSafe, errors are captured in Error/ErrorReason instead of being returned,
// allowing the caller to decide how to handle failures (e.g., treat as false, log, etc.).
type CELResult struct {
	// Value is the result of the CEL expression evaluation (nil if error)
	Value interface{}
	// Matched indicates if the result is boolean true (for conditions)
	// Always false when Error is set
	Matched bool
	// ValueType is the CEL type of Value (e.g., "bool", "string", "int", "map", "list")
	// Empty when evaluation failed
	ValueType string
	// Expression is the original expression that was evaluated
	Expression string
	// Error indicates if evaluation failed (nil if successful)
	Error error
	// ErrorReason provides a human-readable error description
	// Common values: "field not found", "null value access", "type mismatch"
	ErrorReason string
}

// HasError returns true if the evaluation resulted in an error
func (r *CELResult) HasError() bool {
	return r.Error != nil
}

// IsSuccess returns true if the evaluation succeeded without error
func (r *CELResult) IsSuccess() bool {
	return r.Error == nil
}

// newCELEvaluator creates a new CEL evaluator with the given context
// NOTE: Caller (NewEvaluator) is responsible for parameter validation
func newCELEvaluator(ctx context.Context, evalCtx *EvaluationContext, log logger.Logger) (*CELEvaluator, error) {
	// Build CEL environment with variables from context
	options := buildCELOptions(evalCtx)

	env, err := cel.NewEnv(options...)
	if err != nil {
		return nil, apperrors.NewCELEnvError("failed to initialize", err)
	}

	return &CELEvaluator{
		env:     env,
		evalCtx: evalCtx,
		log:     log,
		ctx:     ctx,
	}, nil
}

// buildCELOptions creates CEL environment options from the context
// Variables are dynamically registered based on what's in ctx.Data()
func buildCELOptions(ctx *EvaluationContext) []cel.EnvOption {
	options := make([]cel.EnvOption, 0)

	// Enable optional types for optional chaining syntax (e.g., a.?b.?c)
	options = append(options, cel.OptionalTypes())

	// Get a snapshot of the data for thread safety
	data := ctx.Data()
	for key, value := range data {
		celType := inferCELType(value)
		options = append(options, cel.Variable(key, celType))
	}

	return options
}

// inferCELType infers the CEL type from a Go value
func inferCELType(value interface{}) *cel.Type {
	if value == nil {
		return cel.DynType
	}

	switch value.(type) {
	case string:
		return cel.StringType
	case bool:
		return cel.BoolType
	case int, int8, int16, int32, int64:
		return cel.IntType
	case uint, uint8, uint16, uint32, uint64:
		return cel.UintType
	case float32, float64:
		return cel.DoubleType
	case []interface{}:
		return cel.ListType(cel.DynType)
	case map[string]interface{}:
		return cel.MapType(cel.StringType, cel.DynType)
	default:
		return cel.DynType
	}
}

// EvaluateSafe evaluates a CEL expression with safe handling for evaluation errors.
//
// Error handling strategy:
//   - Parse errors: returned as error (fail fast - indicates bug in expression)
//   - Program creation errors: returned as error (fail fast - indicates invalid expression)
//   - Evaluation errors: captured in CELResult.Error (safe - data might not exist yet)
//
// Use this when you expect that some fields might not exist or be null, and you want
// to handle those cases gracefully (e.g., treat as "not matched") rather than failing.
//
// Common evaluation error reasons captured in result:
//   - "field not found": when accessing a key that doesn't exist (e.g., data.missing.field)
//   - "null value access": when accessing a field on a null value
//   - "type mismatch": when operations are applied to incompatible types
func (e *CELEvaluator) EvaluateSafe(expression string) (*CELResult, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return &CELResult{
			Value:      true,
			Matched:    true,
			ValueType:  "bool",
			Expression: expression,
		}, nil
	}

	// Parse the expression - errors here indicate bugs in configuration
	ast, issues := e.env.Parse(expression)
	if issues != nil && issues.Err() != nil {
		return nil, apperrors.NewCELParseError(expression, issues.Err())
	}

	// Safety check: ensure AST is valid after parse
	if ast == nil {
		return nil, apperrors.NewCELParseError(expression, nil)
	}

	// Create the program directly from parsed AST
	// Skip type-check: we use DynType, so type errors are caught during evaluation
	prg, err := e.env.Program(ast)
	if err != nil {
		return nil, apperrors.NewCELProgramError(expression, err)
	}

	// Evaluate the expression - errors here are SAFE (data might not exist yet)
	// Get a snapshot of the data for thread-safe evaluation
	out, _, err := prg.Eval(e.evalCtx.Data())
	if err != nil {
		// Capture evaluation error in result - this is the "safe" part
		// These errors are expected when data fields don't exist yet
		e.log.Debugf(e.ctx, "CEL evaluation failed for %q: %v", expression, err)
		return &CELResult{
			Value:       nil,
			Matched:     false,
			Expression:  expression,
			Error:       apperrors.NewCELEvalError(expression, err),
			ErrorReason: err.Error(),
		}, nil // No error returned - evaluation errors are captured in result
	}

	// Convert result
	result := &CELResult{
		Value:      out.Value(),
		ValueType:  out.Type().TypeName(),
		Expression: expression,
	}

	// Check if result is boolean true
	// This is the most common use case for CEL expressions
	// has("result.value") will result the value to bool
	if boolVal, ok := out.Value().(bool); ok {
		result.Matched = boolVal
	} else {
		// Non-boolean results are considered "matched" if not nil/empty
		// This can used to dig values from the result
		// For example, if the result is a map, you can use result.value.key to get the value of the key
		result.Matched = !isEmptyValue(out)
	}

	return result, nil
}

// EvaluateAs evaluates a CEL expression and returns the result as the specified type.
// This is a type-safe generic function that handles all type assertions properly.
// Returns an error if:
//   - Parse/program error occurs (from EvaluateSafe)
//   - Evaluation error occurs (captured in result.Error)
//   - Type assertion fails (returns CELTypeMismatchError)
func EvaluateAs[T any](e *CELEvaluator, expression string) (T, error) {
	var zero T
	result, err := e.EvaluateSafe(expression)
	if err != nil {
		return zero, err
	}
	if result.Error != nil {
		return zero, result.Error
	}

	val, ok := result.Value.(T)
	if !ok {
		return zero, apperrors.NewCELTypeMismatchError(expression,
			fmt.Sprintf("%T", zero), fmt.Sprintf("%T", result.Value))
	}
	return val, nil
}

// EvaluateBool evaluates a CEL expression that should return a boolean.
func (e *CELEvaluator) EvaluateBool(expression string) (bool, error) {
	return EvaluateAs[bool](e, expression)
}

// EvaluateString evaluates a CEL expression that should return a string.
func (e *CELEvaluator) EvaluateString(expression string) (string, error) {
	return EvaluateAs[string](e, expression)
}

// EvaluateInt evaluates a CEL expression that should return an int64.
func (e *CELEvaluator) EvaluateInt(expression string) (int64, error) {
	return EvaluateAs[int64](e, expression)
}

// EvaluateUint evaluates a CEL expression that should return a uint64.
func (e *CELEvaluator) EvaluateUint(expression string) (uint64, error) {
	return EvaluateAs[uint64](e, expression)
}

// EvaluateFloat64 evaluates a CEL expression that should return a float64.
func (e *CELEvaluator) EvaluateFloat64(expression string) (float64, error) {
	return EvaluateAs[float64](e, expression)
}

// EvaluateArray evaluates a CEL expression that should return a slice.
func (e *CELEvaluator) EvaluateArray(expression string) ([]any, error) {
	return EvaluateAs[[]any](e, expression)
}

// EvaluateMap evaluates a CEL expression that should return a map.
func (e *CELEvaluator) EvaluateMap(expression string) (map[string]any, error) {
	return EvaluateAs[map[string]any](e, expression)
}

// isEmptyValue checks if a CEL value is empty/nil
func isEmptyValue(val ref.Val) bool {
	if val == nil {
		return true
	}

	switch v := val.(type) {
	case types.Null:
		return true
	case types.String:
		return string(v) == ""
	case types.Bool:
		return false  // Boolean values (true or false) are never empty
	default:
		// Check if it's a list or map
		if lister, ok := val.(interface{ Size() ref.Val }); ok {
			size := lister.Size()
			if intSize, ok := size.(types.Int); ok {
				return int64(intSize) == 0
			}
		}
		return false
	}
}

// ConditionToCEL converts a structured condition to a CEL expression.
// The generated expression does NOT include null-safety guards - if the field
// doesn't exist, CEL will return an error which is captured by EvaluateSafe().
// This allows the caller to decide how to handle missing fields at a higher level.
func ConditionToCEL(field, operator string, value interface{}) (string, error) {
	celValue, err := formatCELValue(value)
	if err != nil {
		return "", err
	}

	switch operator {
	case "equals":
		return fmt.Sprintf("%s == %s", field, celValue), nil
	case "notEquals":
		return fmt.Sprintf("%s != %s", field, celValue), nil
	case "in":
		return fmt.Sprintf("%s in %s", field, celValue), nil
	case "notIn":
		return fmt.Sprintf("!(%s in %s)", field, celValue), nil
	case "contains":
		return fmt.Sprintf("%s.contains(%s)", field, celValue), nil
	case "greaterThan":
		return fmt.Sprintf("%s > %s", field, celValue), nil
	case "lessThan":
		return fmt.Sprintf("%s < %s", field, celValue), nil
	case "exists":
		// For nested paths, use has() which checks if the field exists
		// Note: has(a.b.c) will error if a.b doesn't exist - caller handles via EvaluateSafe
		if strings.Contains(field, ".") {
			return fmt.Sprintf("has(%s)", field), nil
		}
		// For top-level variables, check not null and not empty string
		return fmt.Sprintf("(%s != null && %s != \"\")", field, field), nil
	default:
		return "", apperrors.NewCELUnsupportedOperatorError(operator)
	}
}

// formatCELValue formats a Go value as a CEL literal
func formatCELValue(value interface{}) (string, error) {
	if value == nil {
		return "null", nil
	}

	switch v := value.(type) {
	case string:
		return strconv.Quote(v), nil
	case bool:
		return fmt.Sprintf("%t", v), nil
	case int, int8, int16, int32, int64:
		return fmt.Sprintf("%d", v), nil
	case uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%du", v), nil
	case float32, float64:
		return fmt.Sprintf("%v", v), nil
	case []interface{}:
		items := make([]string, len(v))
		for i, item := range v {
			formatted, err := formatCELValue(item)
			if err != nil {
				return "", err
			}
			items[i] = formatted
		}
		return fmt.Sprintf("[%s]", strings.Join(items, ", ")), nil
	default:
		// Handle other slice/array types via reflection
		rv := reflect.ValueOf(value)
		switch rv.Kind() {
		case reflect.Slice, reflect.Array:
			items := make([]string, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				formatted, err := formatCELValue(rv.Index(i).Interface())
				if err != nil {
					return "", err
				}
				items[i] = formatted
			}
			return fmt.Sprintf("[%s]", strings.Join(items, ", ")), nil
		default:
			return "", apperrors.NewCELUnsupportedTypeError(fmt.Sprintf("%T", value))
		}
	}
}

// ConditionsToCEL converts multiple conditions to a single CEL expression (AND logic)
func ConditionsToCEL(conditions []ConditionDef) (string, error) {
	if len(conditions) == 0 {
		return "true", nil
	}

	expressions := make([]string, len(conditions))
	for i, cond := range conditions {
		expr, err := ConditionToCEL(cond.Field, string(cond.Operator), cond.Value)
		if err != nil {
			return "", apperrors.NewCELConditionConversionError(i, err)
		}
		expressions[i] = "(" + expr + ")"
	}

	return strings.Join(expressions, " && "), nil
}

