package tasks

import (
	"context"
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCELTaskRunner(t *testing.T) {
	runner, err := NewCELTaskRunner(nil)
	require.NoError(t, err)
	require.NotNil(t, runner)
	assert.Equal(t, TaskCEL, runner.Name())
}

func TestNewCELTaskRunner_WithLogger(t *testing.T) {
	deps := &Dependencies{
		Logger: logger.NewTestLogger(),
	}
	runner, err := NewCELTaskRunner(deps)
	require.NoError(t, err)
	require.NotNil(t, runner)
}

func TestCELTaskRunner_Run(t *testing.T) {
	runner, _ := NewCELTaskRunner(nil)
	ctx := context.Background()

	tests := []struct {
		name           string
		args           map[string]any
		input          map[string]any
		expectError    bool
		expectedMatch  bool
		expectedValue  any
		checkValueType bool
	}{
		{
			name:        "missing expression",
			args:        map[string]any{},
			input:       map[string]any{},
			expectError: true,
		},
		{
			name: "empty expression",
			args: map[string]any{
				"expression": "",
			},
			input:       map[string]any{},
			expectError: true,
		},
		{
			name: "simple true expression",
			args: map[string]any{
				"expression": "true",
			},
			input:         map[string]any{},
			expectedMatch: true,
			expectedValue: true,
		},
		{
			name: "simple false expression",
			args: map[string]any{
				"expression": "false",
			},
			input:         map[string]any{},
			expectedMatch: false,
			expectedValue: false,
		},
		{
			name: "comparison expression",
			args: map[string]any{
				"expression": "status == \"active\"",
			},
			input: map[string]any{
				"status": "active",
			},
			expectedMatch: true,
			expectedValue: true,
		},
		{
			name: "comparison expression - false",
			args: map[string]any{
				"expression": "status == \"active\"",
			},
			input: map[string]any{
				"status": "inactive",
			},
			expectedMatch: false,
			expectedValue: false,
		},
		{
			name: "arithmetic expression",
			args: map[string]any{
				"expression": "count + 10",
			},
			input: map[string]any{
				"count": int64(5),
			},
			expectedMatch: true, // Non-zero is truthy
			expectedValue: int64(15),
		},
		{
			name: "string expression",
			args: map[string]any{
				"expression": "name + \" World\"",
			},
			input: map[string]any{
				"name": "Hello",
			},
			expectedMatch: true, // Non-empty string is truthy
			expectedValue: "Hello World",
		},
		{
			name: "nested field access",
			args: map[string]any{
				"expression": "cluster.status == \"ready\"",
			},
			input: map[string]any{
				"cluster": map[string]any{
					"status": "ready",
				},
			},
			expectedMatch: true,
			expectedValue: true,
		},
		{
			name: "with additional variables",
			args: map[string]any{
				"expression": "a + b",
				"variables": map[string]any{
					"a": int64(10),
					"b": int64(20),
				},
			},
			input:         map[string]any{},
			expectedMatch: true,
			expectedValue: int64(30),
		},
		{
			name: "variables override input",
			args: map[string]any{
				"expression": "value",
				"variables": map[string]any{
					"value": "from-variables",
				},
			},
			input: map[string]any{
				"value": "from-input",
			},
			expectedMatch: true,
			expectedValue: "from-variables",
		},
		{
			name: "list contains check",
			args: map[string]any{
				"expression": "\"a\" in items",
			},
			input: map[string]any{
				"items": []any{"a", "b", "c"},
			},
			expectedMatch: true,
			expectedValue: true,
		},
		{
			name: "size function",
			args: map[string]any{
				"expression": "size(items) > 0",
			},
			input: map[string]any{
				"items": []any{"a", "b"},
			},
			expectedMatch: true,
			expectedValue: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := runner.Run(ctx, tt.args, tt.input)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, output)

			assert.Equal(t, tt.expectedMatch, output["matched"], "matched mismatch")
			assert.Equal(t, tt.expectedValue, output["value"], "value mismatch")
		})
	}
}

func TestCELTaskRunner_PreservesInput(t *testing.T) {
	runner, _ := NewCELTaskRunner(nil)
	ctx := context.Background()

	input := map[string]any{
		"existingKey": "existingValue",
		"count":       int64(42),
	}

	args := map[string]any{
		"expression": "count * 2",
	}

	output, err := runner.Run(ctx, args, input)
	require.NoError(t, err)

	// Original input should be preserved
	assert.Equal(t, "existingValue", output["existingKey"])
	assert.Equal(t, int64(42), output["count"])

	// CEL results should be added
	assert.NotNil(t, output["value"])
	assert.NotNil(t, output["matched"])
}

func TestNewCELConditionRunner(t *testing.T) {
	runner, err := NewCELConditionRunner(nil)
	require.NoError(t, err)
	require.NotNil(t, runner)
	assert.Equal(t, "hf:condition", runner.Name())
}

func TestCELConditionRunner_Run(t *testing.T) {
	runner, _ := NewCELConditionRunner(nil)
	ctx := context.Background()

	tests := []struct {
		name          string
		args          map[string]any
		input         map[string]any
		expectError   bool
		expectedMatch bool
	}{
		{
			name:          "no conditions - matches",
			args:          map[string]any{},
			input:         map[string]any{},
			expectedMatch: true,
		},
		{
			name: "empty conditions - matches",
			args: map[string]any{
				"conditions": []any{},
			},
			input:         map[string]any{},
			expectedMatch: true,
		},
		{
			name: "single equals condition - matches",
			args: map[string]any{
				"conditions": []any{
					map[string]any{
						"field":    "status",
						"operator": "equals",
						"value":    "active",
					},
				},
			},
			input: map[string]any{
				"status": "active",
			},
			expectedMatch: true,
		},
		{
			name: "single equals condition - no match",
			args: map[string]any{
				"conditions": []any{
					map[string]any{
						"field":    "status",
						"operator": "equals",
						"value":    "active",
					},
				},
			},
			input: map[string]any{
				"status": "inactive",
			},
			expectedMatch: false,
		},
		{
			name: "multiple conditions - all match",
			args: map[string]any{
				"conditions": []any{
					map[string]any{
						"field":    "status",
						"operator": "equals",
						"value":    "active",
					},
					map[string]any{
						"field":    "count",
						"operator": "greaterThan",
						"value":    int64(0),
					},
				},
			},
			input: map[string]any{
				"status": "active",
				"count":  int64(5),
			},
			expectedMatch: true,
		},
		{
			name: "multiple conditions - one fails",
			args: map[string]any{
				"conditions": []any{
					map[string]any{
						"field":    "status",
						"operator": "equals",
						"value":    "active",
					},
					map[string]any{
						"field":    "count",
						"operator": "greaterThan",
						"value":    int64(10),
					},
				},
			},
			input: map[string]any{
				"status": "active",
				"count":  int64(5),
			},
			expectedMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := runner.Run(ctx, tt.args, tt.input)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, output)

			assert.Equal(t, tt.expectedMatch, output["matched"])
		})
	}
}

func TestNoopLogger(t *testing.T) {
	// Test that noopLogger implements all required methods without panicking
	l := &noopLogger{}
	ctx := context.Background()

	assert.NotPanics(t, func() {
		l.Debug(ctx, "test")
		l.Debugf(ctx, "test %s", "arg")
		l.Info(ctx, "test")
		l.Infof(ctx, "test %s", "arg")
		l.Warn(ctx, "test")
		l.Warnf(ctx, "test %s", "arg")
		l.Error(ctx, "test")
		l.Errorf(ctx, "test %s", "arg")
		l.Fatal(ctx, "test")
		_ = l.With("key", "value")
		_ = l.WithFields(map[string]any{"key": "value"})
		_ = l.Without("key")
	})

	// With methods should return the same logger
	assert.Equal(t, l, l.With("key", "value"))
	assert.Equal(t, l, l.WithFields(map[string]any{}))
	assert.Equal(t, l, l.Without("key"))
}
