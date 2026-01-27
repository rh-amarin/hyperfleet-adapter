package tasks

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	require.NotNil(t, r)
	assert.Empty(t, r.ListRegistered())
}

func TestRegistry_Register(t *testing.T) {
	r := NewRegistry()

	// Create a simple test factory
	factory := func(deps *Dependencies) (TaskRunner, error) {
		return &testRunner{name: "test"}, nil
	}

	// First registration should succeed
	err := r.Register("test:task", factory)
	require.NoError(t, err)

	// Second registration with same name should fail
	err = r.Register("test:task", factory)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")

	// Different name should succeed
	err = r.Register("test:task2", factory)
	require.NoError(t, err)

	// Verify both are registered
	registered := r.ListRegistered()
	assert.Len(t, registered, 2)
}

func TestRegistry_MustRegister(t *testing.T) {
	r := NewRegistry()

	factory := func(deps *Dependencies) (TaskRunner, error) {
		return &testRunner{name: "test"}, nil
	}

	// First registration should not panic
	assert.NotPanics(t, func() {
		r.MustRegister("test:task", factory)
	})

	// Second registration should panic
	assert.Panics(t, func() {
		r.MustRegister("test:task", factory)
	})
}

func TestRegistry_Get(t *testing.T) {
	r := NewRegistry()

	factory := func(deps *Dependencies) (TaskRunner, error) {
		return &testRunner{name: "test"}, nil
	}

	_ = r.Register("test:task", factory)

	// Existing task
	f, ok := r.Get("test:task")
	assert.True(t, ok)
	assert.NotNil(t, f)

	// Non-existing task
	f, ok = r.Get("nonexistent")
	assert.False(t, ok)
	assert.Nil(t, f)
}

func TestRegistry_Create(t *testing.T) {
	r := NewRegistry()

	factory := func(deps *Dependencies) (TaskRunner, error) {
		return &testRunner{name: "created"}, nil
	}

	_ = r.Register("test:task", factory)

	// Create existing task
	runner, err := r.Create("test:task", nil)
	require.NoError(t, err)
	require.NotNil(t, runner)
	assert.Equal(t, "created", runner.Name())

	// Create non-existing task
	_, err = r.Create("nonexistent", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no task runner registered")
}

func TestIsHyperFleetTask(t *testing.T) {
	tests := []struct {
		name     string
		taskName string
		expected bool
	}{
		{"hf:extract", "hf:extract", true},
		{"hf:k8s", "hf:k8s", true},
		{"hf:custom", "hf:custom", true},
		{"http", "http", false},
		{"openapi", "openapi", false},
		{"empty", "", false},
		{"hf without colon", "hfextract", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsHyperFleetTask(tt.taskName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTaskConstants(t *testing.T) {
	// Verify all task constants have the correct prefix
	tasks := []string{
		TaskExtract,
		TaskHTTP,
		TaskK8s,
		TaskCEL,
		TaskTemplate,
		TaskPrecondition,
		TaskPreconditions,
		TaskResources,
		TaskPost,
	}

	for _, task := range tasks {
		assert.True(t, IsHyperFleetTask(task), "task %s should be a HyperFleet task", task)
	}
}

func TestRegisterAllWithDeps(t *testing.T) {
	r := NewRegistry()
	deps := &Dependencies{}

	err := RegisterAllWithDeps(r, deps)
	require.NoError(t, err)

	// Verify all expected tasks are registered
	expectedTasks := []string{
		TaskExtract,
		TaskHTTP,
		TaskK8s,
		TaskCEL,
		TaskTemplate,
		TaskPrecondition,
		TaskPreconditions,
		TaskResources,
		TaskPost,
	}

	registered := r.ListRegistered()
	for _, task := range expectedTasks {
		assert.Contains(t, registered, task, "expected task %s to be registered", task)
	}
}

// testRunner is a simple TaskRunner implementation for testing
type testRunner struct {
	name string
}

func (r *testRunner) Name() string {
	return r.name
}

func (r *testRunner) Run(ctx context.Context, args map[string]any, input map[string]any) (map[string]any, error) {
	return input, nil
}
