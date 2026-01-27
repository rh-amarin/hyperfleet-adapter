package runner

import (
	"context"
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/swf/tasks"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	"github.com/serverlessworkflow/sdk-go/v3/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRunner(t *testing.T) {
	log := logger.NewTestLogger()

	tests := []struct {
		name        string
		config      *RunnerConfig
		expectError bool
		errContains string
	}{
		{
			name:        "nil config",
			config:      nil,
			expectError: true,
			errContains: "config is required",
		},
		{
			name: "missing workflow",
			config: &RunnerConfig{
				Logger: log,
			},
			expectError: true,
			errContains: "workflow is required",
		},
		{
			name: "missing logger",
			config: &RunnerConfig{
				Workflow: &model.Workflow{},
			},
			expectError: true,
			errContains: "logger is required",
		},
		{
			name: "valid config - minimal",
			config: &RunnerConfig{
				Workflow: &model.Workflow{},
				Logger:   log,
			},
			expectError: false,
		},
		{
			name: "valid config - with custom registry",
			config: &RunnerConfig{
				Workflow:     &model.Workflow{},
				Logger:       log,
				TaskRegistry: tasks.NewRegistry(),
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner, err := NewRunner(tt.config)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, runner)
		})
	}
}

func TestRunner_GetWorkflow(t *testing.T) {
	log := logger.NewTestLogger()
	workflow := &model.Workflow{
		Document: model.Document{
			Name: "test-workflow",
		},
	}

	runner, err := NewRunner(&RunnerConfig{
		Workflow: workflow,
		Logger:   log,
	})
	require.NoError(t, err)

	got := runner.GetWorkflow()
	assert.Equal(t, workflow, got)
	assert.Equal(t, "test-workflow", got.Document.Name)
}

func TestRunner_Run_EmptyWorkflow(t *testing.T) {
	log := logger.NewTestLogger()
	workflow := &model.Workflow{}

	runner, err := NewRunner(&RunnerConfig{
		Workflow: workflow,
		Logger:   log,
	})
	require.NoError(t, err)

	result, err := runner.Run(context.Background(), map[string]any{})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, StatusSuccess, result.Status)
}

func TestRunner_Run_WithEventData(t *testing.T) {
	log := logger.NewTestLogger()
	workflow := &model.Workflow{
		Do: &model.TaskList{},
	}

	runner, err := NewRunner(&RunnerConfig{
		Workflow: workflow,
		Logger:   log,
	})
	require.NoError(t, err)

	input := map[string]any{
		"event": map[string]any{
			"id":   "cluster-123",
			"type": "cluster.created",
		},
	}

	result, err := runner.Run(context.Background(), input)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, StatusSuccess, result.Status)
}

func TestRunner_Run_SetTask(t *testing.T) {
	log := logger.NewTestLogger()

	// Create a workflow with a Set task
	taskList := model.TaskList{
		{
			Key: "set_values",
			Task: &model.SetTask{
				Set: map[string]any{
					"clusterId": "test-123",
					"status":    "ready",
				},
			},
		},
	}

	workflow := &model.Workflow{
		Do: &taskList,
	}

	runner, err := NewRunner(&RunnerConfig{
		Workflow: workflow,
		Logger:   log,
	})
	require.NoError(t, err)

	result, err := runner.Run(context.Background(), map[string]any{})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, StatusSuccess, result.Status)

	// Check the params were set
	assert.Equal(t, "test-123", result.Params["clusterId"])
	assert.Equal(t, "ready", result.Params["status"])
}

func TestRunner_Run_DoTask(t *testing.T) {
	log := logger.NewTestLogger()

	// Create a workflow with a Do task containing nested tasks
	nestedTasks := model.TaskList{
		{
			Key: "inner_set",
			Task: &model.SetTask{
				Set: map[string]any{
					"nestedValue": "from-nested",
				},
			},
		},
	}

	taskList := model.TaskList{
		{
			Key: "outer_do",
			Task: &model.DoTask{
				Do: &nestedTasks,
			},
		},
	}

	workflow := &model.Workflow{
		Do: &taskList,
	}

	runner, err := NewRunner(&RunnerConfig{
		Workflow: workflow,
		Logger:   log,
	})
	require.NoError(t, err)

	result, err := runner.Run(context.Background(), map[string]any{})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, StatusSuccess, result.Status)
	assert.Equal(t, "from-nested", result.Params["nestedValue"])
}

func TestRunner_Run_CustomTask(t *testing.T) {
	log := logger.NewTestLogger()

	// Create a registry with a test task
	registry := tasks.NewRegistry()
	err := registry.Register("hf:test", func(deps *tasks.Dependencies) (tasks.TaskRunner, error) {
		return &testTaskRunner{}, nil
	})
	require.NoError(t, err)

	// Create a workflow with a custom HyperFleet task
	taskList := model.TaskList{
		{
			Key: "custom_task",
			Task: &model.CallFunction{
				Call: "hf:test",
				With: map[string]any{
					"arg1": "value1",
				},
			},
		},
	}

	workflow := &model.Workflow{
		Do: &taskList,
	}

	runner, err := NewRunner(&RunnerConfig{
		Workflow:     workflow,
		TaskRegistry: registry,
		Logger:       log,
	})
	require.NoError(t, err)

	result, err := runner.Run(context.Background(), map[string]any{})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, StatusSuccess, result.Status)
}

func TestRunner_Run_CustomTaskNotFound(t *testing.T) {
	log := logger.NewTestLogger()

	// Create a workflow with an unregistered custom task
	taskList := model.TaskList{
		{
			Key: "unknown_task",
			Task: &model.CallFunction{
				Call: "hf:unknown",
				With: map[string]any{},
			},
		},
	}

	workflow := &model.Workflow{
		Do: &taskList,
	}

	// Use empty registry
	registry := tasks.NewRegistry()

	runner, err := NewRunner(&RunnerConfig{
		Workflow:     workflow,
		TaskRegistry: registry,
		Logger:       log,
	})
	require.NoError(t, err)

	result, err := runner.Run(context.Background(), map[string]any{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no task runner registered")
	assert.Nil(t, result)
}

func TestRunnerBuilder(t *testing.T) {
	log := logger.NewTestLogger()
	workflow := &model.Workflow{}
	registry := tasks.NewRegistry()

	runner, err := NewRunnerBuilder().
		WithWorkflow(workflow).
		WithTaskRegistry(registry).
		WithLogger(log).
		Build()

	require.NoError(t, err)
	require.NotNil(t, runner)
	assert.Equal(t, workflow, runner.GetWorkflow())
}

func TestRunnerBuilder_MissingRequired(t *testing.T) {
	_, err := NewRunnerBuilder().Build()
	assert.Error(t, err)
}

// testTaskRunner is a simple task runner for testing
type testTaskRunner struct{}

func (r *testTaskRunner) Name() string {
	return "hf:test"
}

func (r *testTaskRunner) Run(ctx context.Context, args map[string]any, input map[string]any) (map[string]any, error) {
	output := make(map[string]any)
	for k, v := range input {
		output[k] = v
	}
	output["testRan"] = true
	return output, nil
}
