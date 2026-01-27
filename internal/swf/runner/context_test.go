package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestNewWorkflowContext(t *testing.T) {
	ctx := context.Background()
	eventData := map[string]any{
		"id":   "test-123",
		"type": "cluster.created",
	}

	wfCtx := NewWorkflowContext(ctx, eventData)

	require.NotNil(t, wfCtx)
	assert.Equal(t, eventData, wfCtx.EventData)
	assert.Equal(t, PhaseParamExtraction, wfCtx.CurrentPhase)
	assert.Equal(t, string(StatusSuccess), wfCtx.Adapter.ExecutionStatus)
	assert.NotNil(t, wfCtx.Params)
	assert.NotNil(t, wfCtx.Resources)
	assert.NotNil(t, wfCtx.PreconditionResponses)
	assert.NotNil(t, wfCtx.Errors)
}

func TestWorkflowContext_SetParam(t *testing.T) {
	wfCtx := NewWorkflowContext(context.Background(), nil)

	wfCtx.SetParam("clusterId", "cluster-123")
	wfCtx.SetParam("count", 42)

	val, ok := wfCtx.GetParam("clusterId")
	assert.True(t, ok)
	assert.Equal(t, "cluster-123", val)

	val, ok = wfCtx.GetParam("count")
	assert.True(t, ok)
	assert.Equal(t, 42, val)

	_, ok = wfCtx.GetParam("nonexistent")
	assert.False(t, ok)
}

func TestWorkflowContext_SetResource(t *testing.T) {
	wfCtx := NewWorkflowContext(context.Background(), nil)

	resource := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name":      "test-cm",
				"namespace": "default",
			},
		},
	}

	wfCtx.SetResource("myConfigMap", resource)

	r, ok := wfCtx.GetResource("myConfigMap")
	assert.True(t, ok)
	require.NotNil(t, r)
	assert.Equal(t, "ConfigMap", r.GetKind())

	_, ok = wfCtx.GetResource("nonexistent")
	assert.False(t, ok)
}

func TestWorkflowContext_SetPreconditionResponse(t *testing.T) {
	wfCtx := NewWorkflowContext(context.Background(), nil)

	response := map[string]any{
		"status": "active",
		"data":   []any{"a", "b"},
	}

	wfCtx.SetPreconditionResponse("checkCluster", response)

	assert.Equal(t, response, wfCtx.PreconditionResponses["checkCluster"])
}

func TestWorkflowContext_SetError(t *testing.T) {
	wfCtx := NewWorkflowContext(context.Background(), nil)

	testErr := assert.AnError
	wfCtx.SetError(PhasePreconditions, "API_ERROR", "Failed to call API", testErr)

	assert.Equal(t, string(StatusFailed), wfCtx.Adapter.ExecutionStatus)
	assert.Equal(t, "API_ERROR", wfCtx.Adapter.ErrorReason)
	assert.Equal(t, "Failed to call API", wfCtx.Adapter.ErrorMessage)
	assert.Equal(t, testErr, wfCtx.Errors[PhasePreconditions])
}

func TestWorkflowContext_SetSkipped(t *testing.T) {
	wfCtx := NewWorkflowContext(context.Background(), nil)

	wfCtx.SetSkipped("PRECONDITION_NOT_MET", "Cluster not in ready state")

	assert.True(t, wfCtx.ResourcesSkipped)
	assert.Equal(t, "PRECONDITION_NOT_MET", wfCtx.SkipReason)
	assert.True(t, wfCtx.Adapter.ResourcesSkipped)
	assert.Equal(t, "Cluster not in ready state", wfCtx.Adapter.SkipReason)
}

func TestWorkflowContext_GetCELVariables(t *testing.T) {
	wfCtx := NewWorkflowContext(context.Background(), nil)

	// Set some params
	wfCtx.SetParam("clusterId", "cluster-123")
	wfCtx.SetParam("status", "ready")

	// Set a resource
	resource := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name": "test-cm",
			},
		},
	}
	wfCtx.SetResource("configMap", resource)

	// Set a precondition response
	wfCtx.SetPreconditionResponse("checkStatus", map[string]any{
		"result": "ok",
	})

	vars := wfCtx.GetCELVariables()

	// Check params are present
	assert.Equal(t, "cluster-123", vars["clusterId"])
	assert.Equal(t, "ready", vars["status"])

	// Check adapter metadata
	adapter, ok := vars["adapter"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, string(StatusSuccess), adapter["executionStatus"])

	// Check resources
	resources, ok := vars["resources"].(map[string]any)
	require.True(t, ok)
	assert.NotNil(t, resources["configMap"])

	// Check precondition response
	assert.Equal(t, map[string]any{"result": "ok"}, vars["checkStatus"])
}

func TestWorkflowContext_ToWorkflowInput(t *testing.T) {
	eventData := map[string]any{"id": "event-123"}
	wfCtx := NewWorkflowContext(context.Background(), eventData)
	wfCtx.SetParam("clusterId", "cluster-456")

	config := map[string]any{
		"metadata": map[string]any{"name": "test-adapter"},
	}

	input := wfCtx.ToWorkflowInput(config)

	assert.Equal(t, eventData, input["event"])
	assert.Equal(t, config, input["config"])
	assert.Equal(t, wfCtx.Params, input["params"])

	adapter, ok := input["adapter"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, string(StatusSuccess), adapter["executionStatus"])
}

func TestWorkflowResult_GetOutput(t *testing.T) {
	tests := []struct {
		name     string
		output   any
		expected map[string]any
	}{
		{
			name:     "nil output",
			output:   nil,
			expected: map[string]any{},
		},
		{
			name: "map output",
			output: map[string]any{
				"key": "value",
			},
			expected: map[string]any{
				"key": "value",
			},
		},
		{
			name:     "non-map output",
			output:   "string value",
			expected: map[string]any{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &WorkflowResult{Output: tt.output}
			got := result.GetOutput()
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestWorkflowResult_GetTaskOutput(t *testing.T) {
	tests := []struct {
		name     string
		result   *WorkflowResult
		taskName string
		wantOK   bool
	}{
		{
			name: "task output exists",
			result: &WorkflowResult{
				Output: map[string]any{
					"phase_params": map[string]any{
						"clusterId": "123",
					},
				},
			},
			taskName: "phase_params",
			wantOK:   true,
		},
		{
			name: "task output not found - returns full output",
			result: &WorkflowResult{
				Output: map[string]any{
					"clusterId": "123",
				},
			},
			taskName: "nonexistent",
			wantOK:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := tt.result.GetTaskOutput(tt.taskName)
			assert.Equal(t, tt.wantOK, ok)
		})
	}
}

func TestExecutionPhaseConstants(t *testing.T) {
	assert.Equal(t, ExecutionPhase("param_extraction"), PhaseParamExtraction)
	assert.Equal(t, ExecutionPhase("preconditions"), PhasePreconditions)
	assert.Equal(t, ExecutionPhase("resources"), PhaseResources)
	assert.Equal(t, ExecutionPhase("post_actions"), PhasePostActions)
}

func TestExecutionStatusConstants(t *testing.T) {
	assert.Equal(t, ExecutionStatus("success"), StatusSuccess)
	assert.Equal(t, ExecutionStatus("failed"), StatusFailed)
}
