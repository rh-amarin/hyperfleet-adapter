package criteria

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
)

// EvaluationResult contains the result of evaluating a condition
type EvaluationResult struct {
	// Matched indicates if the condition was satisfied
	Matched bool
	// FieldValue is the actual value of the field that was evaluated
	FieldValue interface{}
	// Field is the field path that was evaluated
	Field string
	// Operator is the operator used
	Operator Operator
	// ExpectedValue is the value the condition was compared against
	ExpectedValue interface{}
}

// ConditionsResult contains the result of evaluating multiple conditions
type ConditionsResult struct {
	// Matched indicates if all conditions were satisfied
	Matched bool
	// Results contains individual results for each condition
	Results []EvaluationResult
	// FailedCondition is the index of the first failed condition (-1 if all passed)
	FailedCondition int
	// ExtractedFields maps field paths to their values
	ExtractedFields map[string]interface{}
}

// Evaluator evaluates criteria against an evaluation context
type Evaluator struct {
	evalCtx *EvaluationContext
	log     logger.Logger
	ctx   context.Context

	// Lazily cached CEL evaluator for repeated CEL evaluations
	// Recreated when context version changes
	celEval        *CELEvaluator
	celEvalVersion int64 // Track which context version the CEL eval was created with
	mu             sync.Mutex
}

// NewEvaluator creates a new criteria evaluator.
// All parameters are required - ctx for logging correlation, evalCtx for CEL data.
// Returns an error if any required parameter is nil.
func NewEvaluator(ctx context.Context, evalCtx *EvaluationContext, log logger.Logger) (*Evaluator, error) {
	if ctx == nil {
		return nil, fmt.Errorf("ctx is required for Evaluator")
	}
	if evalCtx == nil {
		return nil, fmt.Errorf("evalCtx is required for Evaluator")
	}
	if log == nil {
		return nil, fmt.Errorf("log is required for Evaluator")
	}
	return &Evaluator{
		evalCtx: evalCtx,
		log:     log,
		ctx:     ctx,
	}, nil
}

// getCELEvaluator returns a cached CEL evaluator, creating it lazily on first use.
// If the context has been modified (version changed), the CEL evaluator is recreated
// to ensure the CEL environment stays in sync with the context data.
// This prevents "undeclared reference" errors when variables are added after first evaluation.
func (e *Evaluator) getCELEvaluator() (*CELEvaluator, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	currentVersion := e.evalCtx.Version()
	
	// Recreate CEL evaluator if context changed or not yet created
	if e.celEval == nil || e.celEvalVersion != currentVersion {
		celEval, err := newCELEvaluator(e.ctx, e.evalCtx, e.log)
		if err != nil {
			return nil, err
		}
		e.celEval = celEval
		e.celEvalVersion = currentVersion
	}
	
	return e.celEval, nil
}

// GetField extracts a field value from the context using dot notation
func (e *Evaluator) GetField(field string) (interface{}, error) {
	return e.evalCtx.GetNestedField(field)
}

// GetFieldOrDefault extracts a field value or returns a default if not found or null
func (e *Evaluator) GetFieldOrDefault(field string, defaultValue interface{}) interface{} {
	value, err := e.evalCtx.GetNestedField(field)
	if err != nil || value == nil {
		return defaultValue
	}
	return value
}

// GetFieldSafe extracts a field value, returning nil for any error (null-safe)
func (e *Evaluator) GetFieldSafe(field string) interface{} {
	value, _ := e.evalCtx.GetNestedField(field)
	return value
}

// HasField checks if a field exists and is not null
func (e *Evaluator) HasField(field string) bool {
	value, err := e.evalCtx.GetNestedField(field)
	return err == nil && value != nil
}

// EvaluateCondition evaluates a single condition (backward compatible)
func (e *Evaluator) EvaluateCondition(field string, operator Operator, value interface{}) (bool, error) {
	result, err := e.EvaluateConditionWithResult(field, operator, value)
	if err != nil {
		return false, err
	}
	return result.Matched, nil
}

// EvaluateConditionSafe evaluates a condition, returning false for null/missing fields (no error)
func (e *Evaluator) EvaluateConditionSafe(field string, operator Operator, value interface{}) bool {
	result, err := e.EvaluateConditionWithResult(field, operator, value)
	if err != nil || result == nil {
		return false
	}
	return result.Matched
}

// EvaluateConditionWithResult evaluates a single condition and returns detailed result
func (e *Evaluator) EvaluateConditionWithResult(field string, operator Operator, value interface{}) (*EvaluationResult, error) {
	// Get the field value from context
	fieldValue, err := e.evalCtx.GetNestedField(field)
	if err != nil {
		return nil, &EvaluationError{
			Field:   field,
			Message: "failed to retrieve field",
			Err:     err,
		}
	}

	result := &EvaluationResult{
		Field:         field,
		FieldValue:    fieldValue,
		Operator:      operator,
		ExpectedValue: value,
	}

	// Evaluate based on operator
	var matched bool
	if operator == OperatorExists {
		// Exists is special - only checks fieldValue, no expected value
		matched = evaluateExists(fieldValue)
	} else if evalFn, ok := operatorFuncs[operator]; ok {
		matched, err = evalFn(fieldValue, value)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, &EvaluationError{
			Field:   field,
			Message: fmt.Sprintf("unsupported operator: %s", operator),
		}
	}

	result.Matched = matched
	return result, nil
}

// EvaluateConditions evaluates multiple conditions (AND logic) - backward compatible
func (e *Evaluator) EvaluateConditions(conditions []ConditionDef) (bool, error) {
	result, err := e.EvaluateConditionsWithResult(conditions)
	if err != nil {
		return false, err
	}
	return result.Matched, nil
}

// EvaluateConditionsWithResult evaluates multiple conditions and returns detailed results
func (e *Evaluator) EvaluateConditionsWithResult(conditions []ConditionDef) (*ConditionsResult, error) {
	result := &ConditionsResult{
		Matched:         true,
		Results:         make([]EvaluationResult, 0, len(conditions)),
		FailedCondition: -1,
		ExtractedFields: make(map[string]interface{}),
	}

	for i, cond := range conditions {
		evalResult, err := e.EvaluateConditionWithResult(cond.Field, cond.Operator, cond.Value)
		if err != nil {
			return nil, err
		}

		result.Results = append(result.Results, *evalResult)
		result.ExtractedFields[cond.Field] = evalResult.FieldValue

		if !evalResult.Matched && result.Matched {
			result.Matched = false
			result.FailedCondition = i
		}
	}

	return result, nil
}

// ExtractFields extracts multiple field values from the context.
// Returns an error if any field is not found. Use ExtractFieldsSafe for null-safe extraction.
func (e *Evaluator) ExtractFields(fields []string) (map[string]interface{}, error) {
	extracted := make(map[string]interface{})
	for _, field := range fields {
		value, err := e.GetField(field)
		if err != nil {
			return nil, err
		}
		extracted[field] = value
	}
	return extracted, nil
}

// ExtractFieldsSafe extracts multiple field values, returning nil for missing fields (null-safe).
// This never returns an error - missing or inaccessible fields are set to nil.
func (e *Evaluator) ExtractFieldsSafe(fields []string) map[string]interface{} {
	extracted := make(map[string]interface{})
	for _, field := range fields {
		extracted[field] = e.GetFieldSafe(field)
	}
	return extracted
}

// ExtractFieldsOrDefault extracts multiple fields, using default for missing ones
func (e *Evaluator) ExtractFieldsOrDefault(fields map[string]interface{}) map[string]interface{} {
	extracted := make(map[string]interface{})
	for field, defaultValue := range fields {
		extracted[field] = e.GetFieldOrDefault(field, defaultValue)
	}
	return extracted
}

// withCELEvaluator gets the CEL evaluator and applies a function to it
func withCELEvaluator[T any](e *Evaluator, fn func(*CELEvaluator) (T, error)) (T, error) {
	var zero T
	celEval, err := e.getCELEvaluator()
	if err != nil {
		return zero, err
	}
	return fn(celEval)
}

// EvaluateCEL evaluates a CEL expression against the current context
func (e *Evaluator) EvaluateCEL(expression string) (*CELResult, error) {
	return withCELEvaluator(e, func(c *CELEvaluator) (*CELResult, error) {
		return c.EvaluateSafe(expression)
	})
}

// EvaluateCELBool evaluates a CEL expression that returns a boolean.
// Returns (false, error) on failure - ignore the error for safe default.
func (e *Evaluator) EvaluateCELBool(expression string) (bool, error) {
	return withCELEvaluator(e, func(c *CELEvaluator) (bool, error) {
		return c.EvaluateBool(expression)
	})
}

// EvaluateCELString evaluates a CEL expression that returns a string.
// Returns ("", error) on failure - ignore the error for safe default.
func (e *Evaluator) EvaluateCELString(expression string) (string, error) {
	return withCELEvaluator(e, func(c *CELEvaluator) (string, error) {
		return c.EvaluateString(expression)
	})
}

// Expression/Value definition keys
const (
	ExpressionKey = "expression"
	DefaultKey    = "default"
	ValueKey      = "value"
)

// EvaluateExpressionDef evaluates an expression definition map.
// The map should contain:
//   - "expression": CEL expression string (required)
//   - "default": default value on failure (optional)
//
// Behavior:
//   - No default → returns whatever type CEL expression returns (nil on failure)
//   - With default → returns default value on evaluation failure
func (e *Evaluator) EvaluateExpressionDef(def map[string]any) (any, bool) {
	expr, ok := def[ExpressionKey].(string)
	if !ok || expr == "" {
		return nil, false
	}

	exprStr := strings.TrimSpace(expr)
	defaultVal, hasDefault := def[DefaultKey]

	// Evaluate CEL expression
	result, err := e.EvaluateCEL(exprStr)
	if err != nil || result.HasError() || result.Value == nil {
		// On failure, return default if specified
		if hasDefault {
			return defaultVal, true
		}
		return nil, true
	}

	return result.Value, true
}

// GetValueDef extracts a value from a value definition map.
// The map should contain:
//   - "value": the value to return (required)
//
// Returns the value and true if found, nil and false otherwise.
func GetValueDef(def map[string]any) (any, bool) {
	value, ok := def[ValueKey]
	return value, ok
}

// EvaluateConditionAsCEL converts a condition to CEL and evaluates it
func (e *Evaluator) EvaluateConditionAsCEL(field string, operator Operator, value interface{}) (*CELResult, error) {
	celExpr, err := ConditionToCEL(field, string(operator), value)
	if err != nil {
		return nil, err
	}
	return e.EvaluateCEL(celExpr)
}

// EvaluateConditionsAsCEL converts conditions to CEL and evaluates them
func (e *Evaluator) EvaluateConditionsAsCEL(conditions []ConditionDef) (*CELResult, error) {
	celExpr, err := ConditionsToCEL(conditions)
	if err != nil {
		return nil, err
	}
	return e.EvaluateCEL(celExpr)
}

// GetCELExpression returns the CEL expression equivalent for a condition
func (e *Evaluator) GetCELExpression(field string, operator Operator, value interface{}) (string, error) {
	return ConditionToCEL(field, string(operator), value)
}

// GetCELExpressionForConditions returns the CEL expression for multiple conditions
func (e *Evaluator) GetCELExpressionForConditions(conditions []ConditionDef) (string, error) {
	return ConditionsToCEL(conditions)
}

// ConditionDef defines a condition to evaluate
type ConditionDef struct {
	Field    string
	Operator Operator
	Value    interface{}
}

// ConditionDefJSON is used for JSON/YAML unmarshaling with string operator
type ConditionDefJSON struct {
	Field    string      `json:"field" yaml:"field"`
	Operator string      `json:"operator" yaml:"operator"`
	Value    interface{} `json:"value" yaml:"value"`
}

// ToConditionDef converts ConditionDefJSON to ConditionDef with typed Operator
func (c ConditionDefJSON) ToConditionDef() ConditionDef {
	return ConditionDef{
		Field:    c.Field,
		Operator: Operator(c.Operator),
		Value:    c.Value,
	}
}

// evalFunc is a function type for operator evaluation
type evalFunc func(fieldValue, expected interface{}) (bool, error)

// operatorFuncs maps operators to their evaluation functions
var operatorFuncs = map[Operator]evalFunc{
	OperatorEquals:      evaluateEquals,
	OperatorNotEquals:   negate(evaluateEquals),
	OperatorIn:          evaluateIn,
	OperatorNotIn:       negate(evaluateIn),
	OperatorContains:    evaluateContains,
	OperatorGreaterThan: evaluateGreaterThan,
	OperatorLessThan:    evaluateLessThan,
}

// negate wraps an evalFunc to return the opposite result
func negate(fn evalFunc) evalFunc {
	return func(a, b interface{}) (bool, error) {
		result, err := fn(a, b)
		if err != nil {
			return false, err
		}
		return !result, nil
	}
}

// evaluateEquals checks if two values are equal
func evaluateEquals(fieldValue, expectedValue interface{}) (bool, error) {
	// Handle nil cases
	if fieldValue == nil && expectedValue == nil {
		return true, nil
	}
	if fieldValue == nil || expectedValue == nil {
		return false, nil
	}

	// Use reflect.DeepEqual for complex types
	return reflect.DeepEqual(fieldValue, expectedValue), nil
}

// evaluateIn checks if a value is in a list
func evaluateIn(fieldValue, expectedList interface{}) (bool, error) {
	// Guard against nil expectedList
	if expectedList == nil {
		return false, fmt.Errorf("expected list cannot be nil")
	}

	list := reflect.ValueOf(expectedList)

	// Guard against invalid reflect.Value (e.g., from nil interface)
	if !list.IsValid() {
		return false, fmt.Errorf("expected list is invalid")
	}

	// expectedList should be a slice or array
	if list.Kind() != reflect.Slice && list.Kind() != reflect.Array {
		return false, fmt.Errorf("expected list to be a slice or array, got %s", list.Kind())
	}

	// Check if fieldValue is in the list
	for i := 0; i < list.Len(); i++ {
		item := list.Index(i).Interface()
		if reflect.DeepEqual(fieldValue, item) {
			return true, nil
		}
	}

	return false, nil
}

// evaluateContains checks if a value contains another value
func evaluateContains(fieldValue, needle interface{}) (bool, error) {
	// Guard against nil fieldValue
	if fieldValue == nil {
		return false, fmt.Errorf("fieldValue cannot be nil")
	}

	// Guard against nil needle
	if needle == nil {
		return false, fmt.Errorf("needle cannot be nil")
	}

	// For strings
	if str, ok := fieldValue.(string); ok {
		needleStr, ok := needle.(string)
		if !ok {
			return false, fmt.Errorf("needle must be a string when field is a string")
		}
		return strings.Contains(str, needleStr), nil
	}

	value := reflect.ValueOf(fieldValue)

	// Guard against invalid reflect.Value
	if !value.IsValid() {
		return false, fmt.Errorf("fieldValue is invalid")
	}

	// For maps - check if needle is a key in the map
	if value.Kind() == reflect.Map {
		needleVal := reflect.ValueOf(needle)

		// Guard against invalid needle value
		if !needleVal.IsValid() {
			return false, fmt.Errorf("needle is invalid for map key lookup")
		}

		// Check if needle type is compatible with map key type
		if needleVal.Type().AssignableTo(value.Type().Key()) {
			result := value.MapIndex(needleVal)
			return result.IsValid(), nil
		}
		// Try string conversion for interface{} keyed maps (common in YAML/JSON)
		if value.Type().Key().Kind() == reflect.Interface {
			result := value.MapIndex(needleVal)
			return result.IsValid(), nil
		}
		return false, fmt.Errorf("needle type %T not compatible with map key type %v", needle, value.Type().Key())
	}

	// For slices/arrays
	if value.Kind() == reflect.Slice || value.Kind() == reflect.Array {
		for i := 0; i < value.Len(); i++ {
			itemVal := value.Index(i)
			// Guard against invalid item value before calling Interface()
			if !itemVal.IsValid() {
				continue
			}
			item := itemVal.Interface()
			if reflect.DeepEqual(item, needle) {
				return true, nil
			}
		}
		return false, nil
	}

	return false, fmt.Errorf("contains operator requires string, slice, array, or map field type")
}

// evaluateGreaterThan checks if a value is greater than another
func evaluateGreaterThan(fieldValue, threshold interface{}) (bool, error) {
	return compareNumbers(fieldValue, threshold, func(a, b float64) bool {
		return a > b
	})
}

// evaluateLessThan checks if a value is less than another
func evaluateLessThan(fieldValue, threshold interface{}) (bool, error) {
	return compareNumbers(fieldValue, threshold, func(a, b float64) bool {
		return a < b
	})
}

// evaluateExists checks if a value exists (is not nil or empty)
func evaluateExists(fieldValue interface{}) bool {
	if fieldValue == nil {
		return false
	}

	// Check for empty string
	if str, ok := fieldValue.(string); ok {
		return str != ""
	}

	// Check for zero values
	value := reflect.ValueOf(fieldValue)
	switch value.Kind() {
	case reflect.Slice, reflect.Map, reflect.Array:
		return value.Len() > 0
	case reflect.Ptr, reflect.Interface:
		return !value.IsNil()
	}

	return true
}

// compareNumbers compares two numeric values using the provided comparison function
func compareNumbers(a, b interface{}, compare func(float64, float64) bool) (bool, error) {
	aFloat, err := toFloat64(a)
	if err != nil {
		return false, fmt.Errorf("failed to convert field value to number: %w", err)
	}

	bFloat, err := toFloat64(b)
	if err != nil {
		return false, fmt.Errorf("failed to convert comparison value to number: %w", err)
	}

	return compare(aFloat, bFloat), nil
}

// toFloat64 converts various numeric types to float64
func toFloat64(value interface{}) (float64, error) {
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		return v.Float(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(v.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(v.Uint()), nil
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", value)
	}
}

// getNestedField retrieves a nested field from a map using dot notation
func getNestedField(data map[string]interface{}, path string) (interface{}, error) {
	return getFieldRecursive(data, path, strings.Split(path, "."), 0)
}

// getFieldRecursive recursively traverses the data structure to find a field
func getFieldRecursive(current interface{}, fullPath string, parts []string, depth int) (interface{}, error) {
	// Base case: no more parts to traverse
	if depth >= len(parts) {
		return current, nil
	}

	field := parts[depth]
	currentPath := strings.Join(parts[:depth+1], ".")

	// Handle nil/null
	if current == nil {
		return nil, &FieldNotFoundError{
			Path:    currentPath,
			Field:   field,
			Message: fmt.Sprintf("cannot access '%s': parent is null", currentPath),
		}
	}

	// Get the next value based on current type
	next, err := getFieldValue(current, field, currentPath)
	if err != nil {
		return nil, err
	}

	// Recurse to next level
	return getFieldRecursive(next, fullPath, parts, depth+1)
}

// getFieldValue extracts a single field from a value (map or struct)
func getFieldValue(current interface{}, field, path string) (interface{}, error) {
	switch v := current.(type) {
	case map[string]interface{}:
		val, ok := v[field]
		if !ok {
			return nil, &FieldNotFoundError{Path: path, Field: field,
				Message: fmt.Sprintf("field '%s' not found", path)}
		}
		return val, nil

	case map[interface{}]interface{}:
		val, ok := v[field]
		if !ok {
			return nil, &FieldNotFoundError{Path: path, Field: field,
				Message: fmt.Sprintf("field '%s' not found", path)}
		}
		return val, nil

	default:
		return getStructField(current, field, path)
	}
}

// getStructField extracts a field from a struct using reflection
func getStructField(current interface{}, field, path string) (interface{}, error) {
	rv := reflect.ValueOf(current)

	// Handle invalid or nil pointer
	if !rv.IsValid() || (rv.Kind() == reflect.Ptr && rv.IsNil()) {
		return nil, &FieldNotFoundError{Path: path, Field: field,
			Message: fmt.Sprintf("cannot access '%s': value is null", path)}
	}

	// Dereference pointer
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}

	// Must be a struct
	if rv.Kind() != reflect.Struct {
		return nil, &FieldNotFoundError{Path: path, Field: field,
			Message: fmt.Sprintf("cannot access '%s': not a map or struct (got %T)", path, current)}
	}

	// Try exact match first, then case-insensitive
	f := rv.FieldByName(field)
	if !f.IsValid() {
		f = rv.FieldByNameFunc(func(n string) bool {
			return strings.EqualFold(n, field)
		})
	}

	if !f.IsValid() {
		return nil, &FieldNotFoundError{Path: path, Field: field,
			Message: fmt.Sprintf("field '%s' not found in struct", path)}
	}

	if !f.CanInterface() {
		return nil, &FieldNotFoundError{Path: path, Field: field,
			Message: fmt.Sprintf("field '%s' is not accessible (unexported)", path)}
	}

	return f.Interface(), nil
}

// FieldNotFoundError represents a field not found during path traversal
type FieldNotFoundError struct {
	Path    string
	Field   string
	Message string
}

func (e *FieldNotFoundError) Error() string {
	return e.Message
}

// IsFieldNotFound checks if an error is or wraps a FieldNotFoundError.
// It uses errors.As to unwrap nested errors (e.g., FieldNotFoundError wrapped in EvaluationError).
func IsFieldNotFound(err error) bool {
	var target *FieldNotFoundError
	return errors.As(err, &target)
}

