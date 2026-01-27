package converter

import (
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/stretchr/testify/assert"
)

func TestConditionToJQ_Equals(t *testing.T) {
	tests := []struct {
		name     string
		field    string
		operator string
		value    interface{}
		expected string
	}{
		{
			name:     "equals string",
			field:    "status",
			operator: "equals",
			value:    "ready",
			expected: `(.status == "ready")`,
		},
		{
			name:     "equals with dot prefix",
			field:    ".status",
			operator: "equals",
			value:    "ready",
			expected: `(.status == "ready")`,
		},
		{
			name:     "equals number",
			field:    "count",
			operator: "equals",
			value:    10,
			expected: `(.count == 10)`,
		},
		{
			name:     "equals bool",
			field:    "enabled",
			operator: "equals",
			value:    true,
			expected: `(.enabled == true)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConditionToJQ(tt.field, tt.operator, tt.value)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConditionToJQ_NotEquals(t *testing.T) {
	result := ConditionToJQ("status", "notEquals", "failed")
	assert.Equal(t, `(.status != "failed")`, result)
}

func TestConditionToJQ_GreaterThan(t *testing.T) {
	result := ConditionToJQ("count", "greaterThan", 5)
	assert.Equal(t, `(.count > 5)`, result)
}

func TestConditionToJQ_LessThan(t *testing.T) {
	result := ConditionToJQ("count", "lessThan", 10)
	assert.Equal(t, `(.count < 10)`, result)
}

func TestConditionToJQ_Contains(t *testing.T) {
	result := ConditionToJQ("name", "contains", "cluster")
	assert.Equal(t, `(.name | contains("cluster"))`, result)
}

func TestConditionToJQ_Exists(t *testing.T) {
	result := ConditionToJQ("metadata.name", "exists", nil)
	assert.Equal(t, `(.metadata.name != null)`, result)
}

func TestConditionToJQ_NotExists(t *testing.T) {
	result := ConditionToJQ("metadata.name", "notExists", nil)
	assert.Equal(t, `(.metadata.name == null)`, result)
}

func TestConditionToJQ_In(t *testing.T) {
	result := ConditionToJQ("status", "in", []string{"ready", "running", "active"})
	expected := `((.status == "ready") or (.status == "running") or (.status == "active"))`
	assert.Equal(t, expected, result)
}

func TestConditionsToJQ(t *testing.T) {
	conditions := []config_loader.Condition{
		{Field: "status", Operator: "equals", Value: "ready"},
		{Field: "count", Operator: "greaterThan", Value: 0},
	}

	result := ConditionsToJQ(conditions)
	expected := `(.status == "ready") and (.count > 0)`
	assert.Equal(t, expected, result)
}

func TestConditionsToJQ_Empty(t *testing.T) {
	result := ConditionsToJQ(nil)
	assert.Equal(t, "true", result)
}

func TestCaptureToJQ(t *testing.T) {
	captures := []config_loader.CaptureField{
		{
			Name: "clusterPhase",
			FieldExpressionDef: config_loader.FieldExpressionDef{
				Field: "status.phase",
			},
		},
		{
			Name: "clusterName",
			FieldExpressionDef: config_loader.FieldExpressionDef{
				Field: "metadata.name",
			},
		},
	}

	result := CaptureToJQ(captures)
	assert.Contains(t, result, "clusterPhase: .content.status.phase")
	assert.Contains(t, result, "clusterName: .content.metadata.name")
}

func TestBuildPreconditionExportExpr(t *testing.T) {
	captures := []config_loader.CaptureField{
		{
			Name: "phase",
			FieldExpressionDef: config_loader.FieldExpressionDef{
				Field: "status.phase",
			},
		},
	}
	conditions := []config_loader.Condition{
		{Field: "status.phase", Operator: "equals", Value: "Ready"},
	}

	result := BuildPreconditionExportExpr("check-cluster", captures, conditions)

	// Should contain the precondition name
	assert.Contains(t, result, "check-cluster: .content")
	// Should contain the captured field
	assert.Contains(t, result, "phase: .content.status.phase")
	// Should contain the _ok flag
	assert.Contains(t, result, "check_cluster_ok:")
}

func TestBuildAllMatchedExpr(t *testing.T) {
	precondNames := []string{"check-cluster", "check-resources"}
	result := BuildAllMatchedExpr(precondNames)
	assert.Contains(t, result, ".check_cluster_ok")
	assert.Contains(t, result, ".check_resources_ok")
	assert.Contains(t, result, " and ")
}

func TestBuildNotMetReasonExpr(t *testing.T) {
	precondNames := []string{"check-cluster", "check-resources"}
	result := BuildNotMetReasonExpr(precondNames)
	assert.Contains(t, result, "check_cluster_ok")
	assert.Contains(t, result, "check-cluster failed")
	assert.Contains(t, result, "check-resources failed")
}

func TestConvertGoTemplateToJQ(t *testing.T) {
	tests := []struct {
		name     string
		template string
		expected string
	}{
		{
			name:     "simple field",
			template: "{{ .clusterId }}",
			expected: "${ .params.clusterId }",
		},
		{
			name:     "field in URL",
			template: "https://api.example.com/clusters/{{ .clusterId }}/status",
			expected: "https://api.example.com/clusters/${ .params.clusterId }/status",
		},
		{
			name:     "multiple fields",
			template: "{{ .namespace }}/{{ .name }}",
			expected: "${ .params.namespace }/${ .params.name }",
		},
		{
			name:     "no template",
			template: "https://api.example.com/static",
			expected: "https://api.example.com/static",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertGoTemplateToJQ(tt.template)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestToSnakeCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"check-cluster", "check_cluster"},
		{"checkCluster", "check_cluster"},
		{"CheckCluster", "check_cluster"},
		{"check_cluster", "check_cluster"},
		{"simple", "simple"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := toSnakeCase(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
