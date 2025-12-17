package criteria

import (
	"context"
	"testing"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_newCELEvaluator(t *testing.T) {
	ctx := NewEvaluationContext()
	ctx.Set("status", "Ready")
	ctx.Set("replicas", 3)

	evaluator, err := newCELEvaluator(context.Background(), ctx, logger.NewTestLogger())
	require.NoError(t, err)
	require.NotNil(t, evaluator)
}

func TestCELEvaluatorEvaluate(t *testing.T) {
	ctx := NewEvaluationContext()
	ctx.Set("status", "Ready")
	ctx.Set("replicas", 3)
	ctx.Set("provider", "aws")
	ctx.Set("enabled", true)

	evaluator, err := newCELEvaluator(context.Background(), ctx, logger.NewTestLogger())
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		wantMatch  bool
		wantValue  interface{}
		wantErr    bool
	}{
		{
			name:       "string equality true",
			expression: `status == "Ready"`,
			wantMatch:  true,
			wantValue:  true,
		},
		{
			name:       "string equality false",
			expression: `status == "Failed"`,
			wantMatch:  false,
			wantValue:  false,
		},
		{
			name:       "numeric comparison greater",
			expression: `replicas > 2`,
			wantMatch:  true,
			wantValue:  true,
		},
		{
			name:       "numeric comparison less",
			expression: `replicas < 5`,
			wantMatch:  true,
			wantValue:  true,
		},
		{
			name:       "boolean variable",
			expression: `enabled`,
			wantMatch:  true,
			wantValue:  true,
		},
		{
			name:       "compound and",
			expression: `status == "Ready" && replicas > 0`,
			wantMatch:  true,
			wantValue:  true,
		},
		{
			name:       "compound or",
			expression: `status == "Failed" || replicas > 0`,
			wantMatch:  true,
			wantValue:  true,
		},
		{
			name:       "string in list",
			expression: `provider in ["aws", "gcp", "azure"]`,
			wantMatch:  true,
			wantValue:  true,
		},
		{
			name:       "empty expression",
			expression: ``,
			wantMatch:  true,
			wantValue:  true,
		},
		{
			name:       "invalid syntax",
			expression: `status ===== "Ready"`,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := evaluator.EvaluateSafe(tt.expression)
			if tt.wantErr {
				// Parse errors are returned as error, eval errors in result
				if err != nil {
					assert.Error(t, err)
					return
				}
				// Evaluation error captured in result
				assert.True(t, result.HasError())
				return
			}
			require.NoError(t, err)
			assert.False(t, result.HasError())
			assert.Equal(t, tt.wantMatch, result.Matched)
			assert.Equal(t, tt.wantValue, result.Value)
			assert.Equal(t, tt.expression, result.Expression)
		})
	}
}

func TestCELEvaluatorWithNestedData(t *testing.T) {
	ctx := NewEvaluationContext()
	ctx.Set("cluster", map[string]interface{}{
		"status": map[string]interface{}{
			"phase": "Ready",
		},
		"spec": map[string]interface{}{
			"replicas": 3,
		},
	})

	evaluator, err := newCELEvaluator(context.Background(), ctx, logger.NewTestLogger())
	require.NoError(t, err)

	// Test nested field access
	result, err := evaluator.EvaluateSafe(`cluster.status.phase == "Ready"`)
	require.NoError(t, err)
	assert.False(t, result.HasError())
	assert.True(t, result.Matched)

	// Test nested numeric comparison
	result, err = evaluator.EvaluateSafe(`cluster.spec.replicas > 1`)
	require.NoError(t, err)
	assert.False(t, result.HasError())
	assert.True(t, result.Matched)
}

func TestCELEvaluatorEvaluateSafe(t *testing.T) {
	ctx := NewEvaluationContext()
	ctx.Set("cluster", map[string]interface{}{
		"status": map[string]interface{}{
			"phase": "Ready",
		},
	})
	ctx.Set("nullValue", nil)

	evaluator, err := newCELEvaluator(context.Background(), ctx, logger.NewTestLogger())
	require.NoError(t, err)

	t.Run("successful evaluation", func(t *testing.T) {
		result, err := evaluator.EvaluateSafe(`cluster.status.phase == "Ready"`)
		require.NoError(t, err, "EvaluateSafe should not return error for valid expression")
		assert.True(t, result.IsSuccess())
		assert.False(t, result.HasError())
		assert.True(t, result.Matched)
		assert.Nil(t, result.Error)
		assert.Empty(t, result.ErrorReason)
	})

	t.Run("missing field returns error in result (safe)", func(t *testing.T) {
		// Evaluation errors (missing fields) are captured in result, NOT returned as error
		result, err := evaluator.EvaluateSafe(`cluster.nonexistent.field == "test"`)
		require.NoError(t, err, "EvaluateSafe should not return error for evaluation errors")
		assert.True(t, result.HasError())
		assert.False(t, result.IsSuccess())
		assert.False(t, result.Matched)
		assert.NotNil(t, result.Error)
		assert.Contains(t, result.ErrorReason, "no such key")
	})

	t.Run("access field on null returns error in result (safe)", func(t *testing.T) {
		result, err := evaluator.EvaluateSafe(`nullValue.field == "test"`)
		require.NoError(t, err, "EvaluateSafe should not return error for null access")
		assert.True(t, result.HasError())
		assert.False(t, result.Matched)
		assert.NotNil(t, result.Error)
	})

	t.Run("has() on missing intermediate key returns error in result", func(t *testing.T) {
		// Without preprocessing, has(cluster.nonexistent.field) errors
		// because cluster.nonexistent doesn't exist
		result, err := evaluator.EvaluateSafe(`has(cluster.nonexistent.field)`)
		require.NoError(t, err)
		assert.True(t, result.HasError())
		assert.False(t, result.Matched)
		assert.Contains(t, result.ErrorReason, "no such key")
	})

	t.Run("has() on existing intermediate key returns false for missing leaf", func(t *testing.T) {
		// has(cluster.status.missing) - cluster.status exists, but missing doesn't
		result, err := evaluator.EvaluateSafe(`has(cluster.status.missing)`)
		require.NoError(t, err)
		assert.True(t, result.IsSuccess())
		assert.False(t, result.Matched) // false because field doesn't exist
		assert.Nil(t, result.Error)
	})

	t.Run("empty expression returns true", func(t *testing.T) {
		result, err := evaluator.EvaluateSafe("")
		require.NoError(t, err)
		assert.True(t, result.IsSuccess())
		assert.True(t, result.Matched)
	})

	t.Run("error result can be used for conditional logic", func(t *testing.T) {
		result, err := evaluator.EvaluateSafe(`cluster.missing.path == "value"`)
		require.NoError(t, err, "Evaluation errors should be captured, not returned")

		// You can use the result for conditional logic
		var finalValue interface{}
		var reason string

		if result.HasError() {
			finalValue = nil
			reason = result.ErrorReason
		} else {
			finalValue = result.Value
			reason = ""
		}

		assert.Nil(t, finalValue)
		assert.NotEmpty(t, reason)
	})

	t.Run("parse error returns actual error (not safe)", func(t *testing.T) {
		// Parse errors should be returned as actual errors - they indicate bugs
		result, err := evaluator.EvaluateSafe(`invalid syntax ===`)
		assert.Error(t, err, "Parse errors should be returned as errors")
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "parse error")
	})
}

func TestCELEvaluatorEvaluateBool(t *testing.T) {
	ctx := NewEvaluationContext()
	ctx.Set("status", "Ready")

	evaluator, err := newCELEvaluator(context.Background(), ctx, logger.NewTestLogger())
	require.NoError(t, err)

	// True result
	matched, err := evaluator.EvaluateBool(`status == "Ready"`)
	require.NoError(t, err)
	assert.True(t, matched)

	// False result
	matched, err = evaluator.EvaluateBool(`status == "Failed"`)
	require.NoError(t, err)
	assert.False(t, matched)
}

func TestCELEvaluatorEvaluateString(t *testing.T) {
	ctx := NewEvaluationContext()
	ctx.Set("status", "Ready")
	ctx.Set("name", "test-cluster")

	evaluator, err := newCELEvaluator(context.Background(), ctx, logger.NewTestLogger())
	require.NoError(t, err)

	// String result
	result, err := evaluator.EvaluateString(`name`)
	require.NoError(t, err)
	assert.Equal(t, "test-cluster", result)

	// String concatenation
	result, err = evaluator.EvaluateString(`name + "-suffix"`)
	require.NoError(t, err)
	assert.Equal(t, "test-cluster-suffix", result)
}

func TestConditionToCEL(t *testing.T) {
	tests := []struct {
		name     string
		field    string
		operator string
		value    interface{}
		want     string
		wantErr  bool
	}{
		{
			name:     "equals string",
			field:    "status",
			operator: "equals",
			value:    "Ready",
			want:     `status == "Ready"`,
		},
		{
			name:     "notEquals string",
			field:    "status",
			operator: "notEquals",
			value:    "Failed",
			want:     `status != "Failed"`,
		},
		{
			name:     "greaterThan number",
			field:    "replicas",
			operator: "greaterThan",
			value:    2,
			want:     `replicas > 2`,
		},
		{
			name:     "lessThan number",
			field:    "count",
			operator: "lessThan",
			value:    10,
			want:     `count < 10`,
		},
		{
			name:     "in list",
			field:    "provider",
			operator: "in",
			value:    []string{"aws", "gcp"},
			want:     `provider in ["aws", "gcp"]`,
		},
		{
			name:     "notIn list",
			field:    "provider",
			operator: "notIn",
			value:    []string{"azure"},
			want:     `!(provider in ["azure"])`,
		},
		{
			name:     "contains",
			field:    "name",
			operator: "contains",
			value:    "test",
			want:     `name.contains("test")`,
		},
		{
			name:     "exists simple nested",
			field:    "metadata.name",
			operator: "exists",
			value:    nil,
			want:     `has(metadata.name)`,
		},
		{
			name:     "exists deeply nested",
			field:    "cluster.status.phase",
			operator: "exists",
			value:    nil,
			want:     `has(cluster.status.phase)`,
		},
		{
			name:     "exists very deeply nested",
			field:    "a.b.c.d",
			operator: "exists",
			value:    nil,
			want:     `has(a.b.c.d)`,
		},
		{
			name:     "exists top level",
			field:    "name",
			operator: "exists",
			value:    nil,
			want:     `(name != null && name != "")`,
		},
		{
			name:     "invalid operator",
			field:    "status",
			operator: "invalid",
			value:    "Ready",
			wantErr:  true,
		},
		// Nested field tests - direct expressions without null-safe wrapping
		// (errors are handled by EvaluateSafe at a higher level)
		{
			name:     "equals nested field",
			field:    "cluster.status.phase",
			operator: "equals",
			value:    "Ready",
			want:     `cluster.status.phase == "Ready"`,
		},
		{
			name:     "in nested field",
			field:    "metadata.labels.env",
			operator: "in",
			value:    []string{"prod", "staging"},
			want:     `metadata.labels.env in ["prod", "staging"]`,
		},
		{
			name:     "greaterThan nested field",
			field:    "status.replicas",
			operator: "greaterThan",
			value:    0,
			want:     `status.replicas > 0`,
		},
		{
			name:     "contains nested field",
			field:    "spec.containers.name",
			operator: "contains",
			value:    "app",
			want:     `spec.containers.name.contains("app")`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ConditionToCEL(tt.field, tt.operator, tt.value)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestConditionsToCEL(t *testing.T) {
	conditions := []ConditionDef{
		{Field: "status", Operator: "equals", Value: "Ready"},
		{Field: "replicas", Operator: "greaterThan", Value: 0},
		{Field: "provider", Operator: "in", Value: []string{"aws", "gcp"}},
	}

	result, err := ConditionsToCEL(conditions)
	require.NoError(t, err)
	expected := `(status == "Ready") && (replicas > 0) && (provider in ["aws", "gcp"])`
	assert.Equal(t, expected, result)

	// Empty conditions
	result, err = ConditionsToCEL([]ConditionDef{})
	require.NoError(t, err)
	assert.Equal(t, "true", result)
}

func TestEvaluatorCELIntegration(t *testing.T) {
	ctx := NewEvaluationContext()
	ctx.Set("status", "Ready")
	ctx.Set("replicas", 3)
	ctx.Set("provider", "aws")

	evaluator, err := NewEvaluator(context.Background(), ctx, logger.NewTestLogger())
	require.NoError(t, err)
	// Test EvaluateCEL
	result, err := evaluator.EvaluateCEL(`status == "Ready" && replicas > 1`)
	require.NoError(t, err)
	assert.True(t, result.Matched)

	// Test EvaluateCELBool
	matched, err := evaluator.EvaluateCELBool(`provider == "aws"`)
	require.NoError(t, err)
	assert.True(t, matched)

	// Test EvaluateConditionAsCEL
	result, err = evaluator.EvaluateConditionAsCEL("status", OperatorEquals, "Ready")
	require.NoError(t, err)
	assert.True(t, result.Matched)

	// Test EvaluateConditionsAsCEL
	conditions := []ConditionDef{
		{Field: "status", Operator: "equals", Value: "Ready"},
		{Field: "replicas", Operator: "greaterThan", Value: 1},
	}
	result, err = evaluator.EvaluateConditionsAsCEL(conditions)
	require.NoError(t, err)
	assert.True(t, result.Matched)
}

func TestGetCELExpression(t *testing.T) {
	ctx := NewEvaluationContext()
	evaluator, err := NewEvaluator(context.Background(), ctx, logger.NewTestLogger())
	require.NoError(t, err)
	// Single condition
	expr, err := evaluator.GetCELExpression("status", OperatorEquals, "Ready")
	require.NoError(t, err)
	assert.Equal(t, `status == "Ready"`, expr)

	// Multiple conditions
	conditions := []ConditionDef{
		{Field: "status", Operator: "equals", Value: "Ready"},
		{Field: "replicas", Operator: "greaterThan", Value: 0},
	}
	expr, err = evaluator.GetCELExpressionForConditions(conditions)
	require.NoError(t, err)
	assert.Equal(t, `(status == "Ready") && (replicas > 0)`, expr)
}

func TestFormatCELValue(t *testing.T) {
	tests := []struct {
		name    string
		value   interface{}
		want    string
		wantErr bool
	}{
		{"nil", nil, "null", false},
		{"string", "hello", `"hello"`, false},
		{"string with quotes", `say "hi"`, `"say \"hi\""`, false},
		{"bool true", true, "true", false},
		{"bool false", false, "false", false},
		{"int", 42, "42", false},
		{"float", 3.14, "3.14", false},
		{"string slice", []string{"a", "b"}, `["a", "b"]`, false},
		{"custom int type", time.Second, "", true}, // time.Duration is now unsupported (type alias not handled)
		{"unsupported channel", make(chan int), "", true},
		{"unsupported map", map[string]string{"a": "b"}, "", true},
		{"unsupported func", func() {}, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := formatCELValue(tt.value)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, result)
		})
	}
}

// TestEvaluateSafeErrorHandling tests how EvaluateSafe handles various error scenarios
// and how callers can use the result to make decisions at a higher level
func TestEvaluateSafeErrorHandling(t *testing.T) {
	ctx := NewEvaluationContext()
	ctx.Set("data", map[string]interface{}{
		"level1": map[string]interface{}{
			"level2": map[string]interface{}{
				"value": "found",
			},
		},
	})

	evaluator, err := newCELEvaluator(context.Background(), ctx, logger.NewTestLogger())
	require.NoError(t, err)

	tests := []struct {
		name        string
		expression  string
		wantSuccess bool
		wantMatched bool
		wantReason  string // substring to match in ErrorReason
	}{
		{
			name:        "existing nested field",
			expression:  `data.level1.level2.value == "found"`,
			wantSuccess: true,
			wantMatched: true,
		},
		{
			name:        "missing leaf field",
			expression:  `data.level1.level2.missing == "test"`,
			wantSuccess: false,
			wantReason:  "no such key",
		},
		{
			name:        "missing intermediate field",
			expression:  `data.level1.nonexistent.value == "test"`,
			wantSuccess: false,
			wantReason:  "no such key",
		},
		{
			name:        "has() on existing path",
			expression:  `has(data.level1.level2.value)`,
			wantSuccess: true,
			wantMatched: true,
		},
		{
			name:        "has() on missing leaf",
			expression:  `has(data.level1.level2.missing)`,
			wantSuccess: true,
			wantMatched: false,
		},
		{
			name:        "has() on missing intermediate",
			expression:  `has(data.level1.nonexistent.value)`,
			wantSuccess: false,
			wantReason:  "no such key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := evaluator.EvaluateSafe(tt.expression)
			require.NoError(t, err, "EvaluateSafe should not return parse/program errors for valid expressions")

			if tt.wantSuccess {
				assert.True(t, result.IsSuccess(), "expected success but got error: %v", result.Error)
				assert.Equal(t, tt.wantMatched, result.Matched)
			} else {
				assert.True(t, result.HasError(), "expected error but got success")
				assert.Contains(t, result.ErrorReason, tt.wantReason)
			}
		})
	}
}

