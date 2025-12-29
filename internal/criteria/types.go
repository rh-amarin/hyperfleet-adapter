package criteria

import (
	"fmt"
	"reflect"
	"sync"
)

// Operator represents a comparison operator
type Operator string

const (
	// OperatorEquals checks if field equals value
	OperatorEquals Operator = "equals"
	// OperatorNotEquals checks if field does not equal value
	OperatorNotEquals Operator = "notEquals"
	// OperatorIn checks if field is in a list of values
	OperatorIn Operator = "in"
	// OperatorNotIn checks if field is not in a list of values
	OperatorNotIn Operator = "notIn"
	// OperatorContains checks if field contains value (for strings and arrays)
	OperatorContains Operator = "contains"
	// OperatorGreaterThan checks if field is greater than value
	OperatorGreaterThan Operator = "greaterThan"
	// OperatorLessThan checks if field is less than value
	OperatorLessThan Operator = "lessThan"
	// OperatorExists checks if field exists (is not nil/empty)
	OperatorExists Operator = "exists"
)

// SupportedOperators lists all supported operators.
var SupportedOperators = []Operator{
	OperatorEquals,
	OperatorNotEquals,
	OperatorIn,
	OperatorNotIn,
	OperatorContains,
	OperatorGreaterThan,
	OperatorLessThan,
	OperatorExists,
}

// IsValidOperator checks if the given operator string is valid
func IsValidOperator(op string) bool {
	for _, supported := range SupportedOperators {
		if string(supported) == op {
			return true
		}
	}
	return false
}

// OperatorStrings returns all operators as strings
func OperatorStrings() []string {
	result := make([]string, len(SupportedOperators))
	for i, op := range SupportedOperators {
		result[i] = string(op)
	}
	return result
}

// EvaluationContext holds the data available for criteria evaluation.
// It is safe for concurrent use by multiple goroutines.
type EvaluationContext struct {
	// data contains all variables available for evaluation
	data map[string]interface{}
	// version tracks modifications to detect when CEL evaluator needs recreation
	// This ensures the CEL environment stays in sync with the context data
	version int64
	// mu protects concurrent access to data and version
	mu sync.RWMutex
}

// NewEvaluationContext creates a new evaluation context
func NewEvaluationContext() *EvaluationContext {
	return &EvaluationContext{
		data:    make(map[string]interface{}),
		version: 0,
	}
}

// Version returns the current version of the context.
// The version increments with each modification (Set, SetVariablesFromMap, Merge).
func (c *EvaluationContext) Version() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.version
}

// Set sets a variable in the context.
// Only increments version if the value actually changes (optimization to avoid unnecessary CEL env recreation).
// This method is safe for concurrent use.
func (c *EvaluationContext) Set(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if value actually changed
	if existing, ok := c.data[key]; ok && reflect.DeepEqual(existing, value) {
		return // No change, no version increment
	}

	c.data[key] = value
	c.version++
}

// Get retrieves a variable from the context.
// This method is safe for concurrent use.
func (c *EvaluationContext) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, ok := c.data[key]
	return val, ok
}

// GetField retrieves a field using dot notation or JSONPath (e.g., "status.phase").
// This method is safe for concurrent use.
func (c *EvaluationContext) GetField(path string) (*FieldResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return ExtractField(c.data, path)
}

// Merge merges another context into this one.
// Only increments version if any value actually changes.
// This method is safe for concurrent use.
//
// To avoid deadlock when two goroutines call ctx1.Merge(ctx2) and ctx2.Merge(ctx1)
// simultaneously, we first snapshot the other context's data while holding only
// its read lock, then release it before acquiring our write lock.
func (c *EvaluationContext) Merge(other *EvaluationContext) {
	if other == nil {
		return
	}

	// Step 1: Snapshot other's data while holding only its read lock
	other.mu.RLock()
	otherSnapshot := make(map[string]interface{}, len(other.data))
	for k, v := range other.data {
		otherSnapshot[k] = v
	}
	other.mu.RUnlock()

	// Step 2: Now acquire our write lock and merge the snapshot
	// No deadlock possible since we don't hold other's lock anymore
	c.mu.Lock()
	defer c.mu.Unlock()

	changed := false
	for k, v := range otherSnapshot {
		if existing, ok := c.data[k]; !ok || !reflect.DeepEqual(existing, v) {
			c.data[k] = v
			changed = true
		}
	}

	if changed {
		c.version++
	}
}

// SetVariablesFromMap sets all key-value pairs from the provided map as evaluation variables.
// Only increments version if any value actually changes.
// This method is safe for concurrent use.
func (c *EvaluationContext) SetVariablesFromMap(data map[string]interface{}) {
	if data == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	changed := false
	for k, v := range data {
		if existing, ok := c.data[k]; !ok || !reflect.DeepEqual(existing, v) {
			c.data[k] = v
			changed = true
		}
	}

	if changed {
		c.version++
	}
}

// Data returns a copy of the internal data map.
// This is used by CEL evaluator for evaluation.
// Returns a shallow copy to prevent external modification.
func (c *EvaluationContext) Data() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()
	// Return a copy to prevent race conditions during CEL evaluation
	copy := make(map[string]interface{}, len(c.data))
	for k, v := range c.data {
		copy[k] = v
	}
	return copy
}

// EvaluationError represents an error during criteria evaluation
type EvaluationError struct {
	Field   string
	Message string
	Err     error
}

func (e *EvaluationError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("evaluation error for field '%s': %s: %v", e.Field, e.Message, e.Err)
	}
	return fmt.Sprintf("evaluation error for field '%s': %s", e.Field, e.Message)
}

func (e *EvaluationError) Unwrap() error {
	return e.Err
}
