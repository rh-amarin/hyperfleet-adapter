package converter

import (
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/serverlessworkflow/sdk-go/v3/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertAdapterConfig_Nil(t *testing.T) {
	workflow, err := ConvertAdapterConfig(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "config cannot be nil")
	assert.Nil(t, workflow)
}

func TestConvertAdapterConfig_Minimal(t *testing.T) {
	config := &config_loader.AdapterConfig{
		Kind:       "AdapterConfig",
		APIVersion: "v1alpha1",
		Metadata: config_loader.Metadata{
			Name:      "test-adapter",
			Namespace: "default",
		},
		Spec: config_loader.AdapterConfigSpec{
			Adapter: config_loader.AdapterInfo{
				Version: "1.0.0",
			},
		},
	}

	workflow, err := ConvertAdapterConfig(config)
	require.NoError(t, err)
	require.NotNil(t, workflow)

	assert.Equal(t, "1.0.0", workflow.Document.DSL)
	assert.Equal(t, "hyperfleet", workflow.Document.Namespace)
	assert.Equal(t, "test-adapter", workflow.Document.Name)
	assert.Equal(t, "1.0.0", workflow.Document.Version)
	assert.Equal(t, "Adapter: test-adapter", workflow.Document.Title)
}

func TestConvertAdapterConfig_WithParams(t *testing.T) {
	config := &config_loader.AdapterConfig{
		Metadata: config_loader.Metadata{
			Name: "params-adapter",
		},
		Spec: config_loader.AdapterConfigSpec{
			Params: []config_loader.Parameter{
				{
					Name:     "clusterId",
					Source:   "event.id",
					Type:     "string",
					Required: true,
				},
				{
					Name:    "count",
					Source:  "event.count",
					Type:    "int",
					Default: 10,
				},
			},
		},
	}

	workflow, err := ConvertAdapterConfig(config)
	require.NoError(t, err)
	require.NotNil(t, workflow)
	require.NotNil(t, workflow.Do)

	// Should have one task - extract_params (native SWF set task)
	assert.Len(t, *workflow.Do, 1)

	task := (*workflow.Do)[0]
	assert.Equal(t, "extract_params", task.Key)
}

func TestConvertAdapterConfig_WithPreconditions(t *testing.T) {
	config := &config_loader.AdapterConfig{
		Metadata: config_loader.Metadata{
			Name: "preconditions-adapter",
		},
		Spec: config_loader.AdapterConfigSpec{
			Preconditions: []config_loader.Precondition{
				{
					ActionBase: config_loader.ActionBase{
						Name: "check-cluster",
						APICall: &config_loader.APICall{
							Method:  "GET",
							URL:     "https://api.example.com/clusters/{{ .clusterId }}",
							Timeout: "10s",
						},
					},
					Conditions: []config_loader.Condition{
						{
							Field:    "status",
							Operator: "equals",
							Value:    "ready",
						},
					},
				},
			},
		},
	}

	workflow, err := ConvertAdapterConfig(config)
	require.NoError(t, err)
	require.NotNil(t, workflow)
	require.NotNil(t, workflow.Do)

	// Should have one task - phase_preconditions (now a DoTask with nested tasks)
	assert.Len(t, *workflow.Do, 1)

	task := (*workflow.Do)[0]
	assert.Equal(t, "phase_preconditions", task.Key)

	// Verify it's a DoTask with nested tasks
	doTask, ok := task.Task.(*model.DoTask)
	require.True(t, ok, "phase_preconditions should be a DoTask")
	require.NotNil(t, doTask.Do)

	// Should have 2 tasks: check_cluster (HTTP) and evaluate (set)
	assert.Len(t, *doTask.Do, 2)

	// First task should be the HTTP call for check-cluster
	checkClusterTask := (*doTask.Do)[0]
	assert.Equal(t, "check_cluster", checkClusterTask.Key)

	// Last task should be evaluate
	evaluateTask := (*doTask.Do)[1]
	assert.Equal(t, "evaluate", evaluateTask.Key)
}

func TestConvertAdapterConfig_WithResources(t *testing.T) {
	config := &config_loader.AdapterConfig{
		Metadata: config_loader.Metadata{
			Name: "resources-adapter",
		},
		Spec: config_loader.AdapterConfigSpec{
			Resources: []config_loader.Resource{
				{
					Name: "my-configmap",
					Manifest: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
						"metadata": map[string]interface{}{
							"name": "test-cm",
						},
					},
					Discovery: &config_loader.DiscoveryConfig{
						ByName: "test-cm",
					},
				},
			},
		},
	}

	workflow, err := ConvertAdapterConfig(config)
	require.NoError(t, err)
	require.NotNil(t, workflow)
	require.NotNil(t, workflow.Do)

	// Should have one task - phase_resources
	assert.Len(t, *workflow.Do, 1)

	task := (*workflow.Do)[0]
	assert.Equal(t, "phase_resources", task.Key)
}

func TestConvertAdapterConfig_WithPost(t *testing.T) {
	config := &config_loader.AdapterConfig{
		Metadata: config_loader.Metadata{
			Name: "post-adapter",
		},
		Spec: config_loader.AdapterConfigSpec{
			Post: &config_loader.PostConfig{
				PostActions: []config_loader.PostAction{
					{
						ActionBase: config_loader.ActionBase{
							Name: "notify",
							APICall: &config_loader.APICall{
								Method: "POST",
								URL:    "https://api.example.com/notify",
								Body:   `{"status": "completed"}`,
							},
						},
					},
				},
			},
		},
	}

	workflow, err := ConvertAdapterConfig(config)
	require.NoError(t, err)
	require.NotNil(t, workflow)
	require.NotNil(t, workflow.Do)

	// Should have one task - phase_post (now a DoTask with native HTTP call)
	assert.Len(t, *workflow.Do, 1)

	task := (*workflow.Do)[0]
	assert.Equal(t, "phase_post", task.Key)

	// Verify it's a DoTask with nested tasks
	doTask, ok := task.Task.(*model.DoTask)
	require.True(t, ok, "phase_post should be a DoTask")
	require.NotNil(t, doTask.Do)

	// Should have one task: notify (HTTP call)
	assert.Len(t, *doTask.Do, 1)
	assert.Equal(t, "notify", (*doTask.Do)[0].Key)
}

func TestConvertAdapterConfig_FullPipeline(t *testing.T) {
	config := &config_loader.AdapterConfig{
		Kind:       "AdapterConfig",
		APIVersion: "v1alpha1",
		Metadata: config_loader.Metadata{
			Name:      "full-pipeline-adapter",
			Namespace: "test-namespace",
			Labels: map[string]string{
				"app": "test",
			},
		},
		Spec: config_loader.AdapterConfigSpec{
			Adapter: config_loader.AdapterInfo{
				Version: "2.0.0",
			},
			Params: []config_loader.Parameter{
				{
					Name:   "clusterId",
					Source: "event.id",
				},
			},
			Preconditions: []config_loader.Precondition{
				{
					ActionBase: config_loader.ActionBase{
						Name: "check-exists",
					},
					Expression: "clusterId != \"\"",
				},
			},
			Resources: []config_loader.Resource{
				{
					Name: "configmap",
					Manifest: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
					},
					Discovery: &config_loader.DiscoveryConfig{
						ByName: "test-cm",
					},
				},
			},
			Post: &config_loader.PostConfig{
				PostActions: []config_loader.PostAction{
					{
						ActionBase: config_loader.ActionBase{
							Name: "report-status",
							APICall: &config_loader.APICall{
								Method: "POST",
								URL:    "https://api.example.com/status",
							},
						},
					},
				},
			},
		},
	}

	workflow, err := ConvertAdapterConfig(config)
	require.NoError(t, err)
	require.NotNil(t, workflow)
	require.NotNil(t, workflow.Do)

	// Should have 4 phases
	assert.Len(t, *workflow.Do, 4)

	// Verify phase order (params now uses native set task with "extract_params" key)
	assert.Equal(t, "extract_params", (*workflow.Do)[0].Key)
	assert.Equal(t, "phase_preconditions", (*workflow.Do)[1].Key)
	assert.Equal(t, "phase_resources", (*workflow.Do)[2].Key)
	assert.Equal(t, "phase_post", (*workflow.Do)[3].Key)

	// Check metadata preservation
	assert.Equal(t, "AdapterConfig", workflow.Document.Metadata["originalKind"])
	assert.Equal(t, "v1alpha1", workflow.Document.Metadata["originalAPIVersion"])
	assert.Equal(t, "test-namespace", workflow.Document.Metadata["namespace"])

	// Check labels are preserved as tags
	assert.Equal(t, map[string]string{"app": "test"}, workflow.Document.Tags)
}

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty",
			input:    "",
			expected: "unnamed-workflow",
		},
		{
			name:     "already valid",
			input:    "my-adapter",
			expected: "my-adapter",
		},
		{
			name:     "uppercase",
			input:    "MyAdapter",
			expected: "myadapter",
		},
		{
			name:     "underscores",
			input:    "my_adapter_name",
			expected: "my-adapter-name",
		},
		{
			name:     "spaces",
			input:    "my adapter",
			expected: "my-adapter",
		},
		{
			name:     "special characters",
			input:    "my@adapter!name",
			expected: "my-adapter-name",
		},
		{
			name:     "numbers",
			input:    "adapter123",
			expected: "adapter123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWorkflowFromConfig(t *testing.T) {
	config := &config_loader.AdapterConfig{
		Metadata: config_loader.Metadata{
			Name: "convenience-test",
		},
	}

	workflow, err := WorkflowFromConfig(config)
	require.NoError(t, err)
	require.NotNil(t, workflow)
	assert.Equal(t, "convenience-test", workflow.Document.Name)
}

func TestConvertAdapterConfig_PreconditionsWithCapture(t *testing.T) {
	config := &config_loader.AdapterConfig{
		Metadata: config_loader.Metadata{
			Name: "capture-test",
		},
		Spec: config_loader.AdapterConfigSpec{
			Preconditions: []config_loader.Precondition{
				{
					ActionBase: config_loader.ActionBase{
						Name: "fetch-data",
						APICall: &config_loader.APICall{
							Method: "GET",
							URL:    "https://api.example.com/data",
							Headers: []config_loader.Header{
								{Name: "Authorization", Value: "Bearer {{ .token }}"},
							},
						},
					},
					Capture: []config_loader.CaptureField{
						{
							Name: "dataId",
							FieldExpressionDef: config_loader.FieldExpressionDef{
								Field: "id",
							},
						},
						{
							Name: "status",
							FieldExpressionDef: config_loader.FieldExpressionDef{
								Expression: "data.status",
							},
						},
					},
				},
			},
		},
	}

	workflow, err := ConvertAdapterConfig(config)
	require.NoError(t, err)
	require.NotNil(t, workflow)
	require.NotNil(t, workflow.Do)

	assert.Len(t, *workflow.Do, 1)
}

func TestConvertAdapterConfig_ResourcesWithDiscovery(t *testing.T) {
	config := &config_loader.AdapterConfig{
		Metadata: config_loader.Metadata{
			Name: "discovery-test",
		},
		Spec: config_loader.AdapterConfigSpec{
			Resources: []config_loader.Resource{
				{
					Name:             "update-cm",
					RecreateOnChange: true,
					Manifest: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
					},
					Discovery: &config_loader.DiscoveryConfig{
						Namespace: "default",
						ByName:    "existing-cm",
					},
				},
			},
		},
	}

	workflow, err := ConvertAdapterConfig(config)
	require.NoError(t, err)
	require.NotNil(t, workflow)
	require.NotNil(t, workflow.Do)

	assert.Len(t, *workflow.Do, 1)
	assert.Equal(t, "phase_resources", (*workflow.Do)[0].Key)
}

func TestConvertAdapterConfig_ResourcesWithPreconditionsHasIfCondition(t *testing.T) {
	config := &config_loader.AdapterConfig{
		Metadata: config_loader.Metadata{
			Name: "if-condition-test",
		},
		Spec: config_loader.AdapterConfigSpec{
			Preconditions: []config_loader.Precondition{
				{
					ActionBase: config_loader.ActionBase{
						Name: "check",
					},
					Expression: "true",
				},
			},
			Resources: []config_loader.Resource{
				{
					Name: "configmap",
					Manifest: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
					},
					Discovery: &config_loader.DiscoveryConfig{
						ByName: "test-cm",
					},
				},
			},
		},
	}

	workflow, err := ConvertAdapterConfig(config)
	require.NoError(t, err)
	require.NotNil(t, workflow)
	require.NotNil(t, workflow.Do)

	// Should have 2 tasks
	assert.Len(t, *workflow.Do, 2)

	// The resources phase should have an `if` condition since there are preconditions
	resourcesTask := (*workflow.Do)[1]
	assert.Equal(t, "phase_resources", resourcesTask.Key)

	// Verify it's a CallFunction with an If condition
	callFunc, ok := resourcesTask.Task.(*model.CallFunction)
	require.True(t, ok, "phase_resources should be a CallFunction")
	require.NotNil(t, callFunc.If, "phase_resources should have an If condition")
	assert.Equal(t, "${ .allMatched == true }", callFunc.If.Value)
}

func TestConvertAdapterConfig_PostWithPayloads(t *testing.T) {
	config := &config_loader.AdapterConfig{
		Metadata: config_loader.Metadata{
			Name: "payloads-test",
		},
		Spec: config_loader.AdapterConfigSpec{
			Post: &config_loader.PostConfig{
				Payloads: []config_loader.Payload{
					{
						Name: "notification",
						Build: map[string]interface{}{
							"message": "Cluster created",
							"id":      "{{ .clusterId }}",
						},
					},
					{
						Name:     "status-update",
						BuildRef: "statusPayload",
					},
				},
				PostActions: []config_loader.PostAction{
					{
						ActionBase: config_loader.ActionBase{
							Name: "send-notification",
							APICall: &config_loader.APICall{
								Method:        "POST",
								URL:           "https://api.example.com/notify",
								Body:          `{{ .notification }}`,
								Timeout:       "30s",
								RetryAttempts: 3,
								RetryBackoff:  "exponential",
							},
						},
					},
				},
			},
		},
	}

	workflow, err := ConvertAdapterConfig(config)
	require.NoError(t, err)
	require.NotNil(t, workflow)
	require.NotNil(t, workflow.Do)

	assert.Len(t, *workflow.Do, 1)
	assert.Equal(t, "phase_post", (*workflow.Do)[0].Key)
}
