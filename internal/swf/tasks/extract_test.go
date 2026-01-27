package tasks

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewExtractTaskRunner(t *testing.T) {
	runner, err := NewExtractTaskRunner(nil)
	require.NoError(t, err)
	require.NotNil(t, runner)
	assert.Equal(t, TaskExtract, runner.Name())
}

func TestExtractTaskRunner_Run(t *testing.T) {
	runner, _ := NewExtractTaskRunner(nil)
	ctx := context.Background()

	tests := []struct {
		name        string
		args        map[string]any
		input       map[string]any
		setup       func()
		cleanup     func()
		expectError bool
		checkParams func(t *testing.T, output map[string]any)
	}{
		{
			name:  "no sources",
			args:  map[string]any{},
			input: map[string]any{},
			checkParams: func(t *testing.T, output map[string]any) {
				params, ok := output["params"].(map[string]any)
				require.True(t, ok)
				assert.Empty(t, params)
			},
		},
		{
			name: "extract from event using dot notation",
			args: map[string]any{
				"sources": []any{
					map[string]any{
						"name":   "clusterId",
						"source": "event.id",
					},
				},
			},
			input: map[string]any{
				"event": map[string]any{
					"id": "cluster-123",
				},
			},
			checkParams: func(t *testing.T, output map[string]any) {
				params, ok := output["params"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "cluster-123", params["clusterId"])
			},
		},
		{
			name: "extract from nested event field",
			args: map[string]any{
				"sources": []any{
					map[string]any{
						"name":   "status",
						"source": "event.cluster.status",
					},
				},
			},
			input: map[string]any{
				"event": map[string]any{
					"cluster": map[string]any{
						"status": "ready",
					},
				},
			},
			checkParams: func(t *testing.T, output map[string]any) {
				params, ok := output["params"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "ready", params["status"])
			},
		},
		{
			name: "extract from env",
			args: map[string]any{
				"sources": []any{
					map[string]any{
						"name":   "testEnvVar",
						"source": "env.TEST_EXTRACT_VAR",
					},
				},
			},
			input: map[string]any{},
			setup: func() {
				os.Setenv("TEST_EXTRACT_VAR", "env-value")
			},
			cleanup: func() {
				os.Unsetenv("TEST_EXTRACT_VAR")
			},
			checkParams: func(t *testing.T, output map[string]any) {
				params, ok := output["params"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "env-value", params["testEnvVar"])
			},
		},
		{
			name: "required field missing - uses default",
			args: map[string]any{
				"sources": []any{
					map[string]any{
						"name":     "missing",
						"source":   "event.nonexistent",
						"required": false,
						"default":  "default-value",
					},
				},
			},
			input: map[string]any{
				"event": map[string]any{},
			},
			checkParams: func(t *testing.T, output map[string]any) {
				params, ok := output["params"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "default-value", params["missing"])
			},
		},
		{
			name: "multiple sources",
			args: map[string]any{
				"sources": []any{
					map[string]any{
						"name":   "id",
						"source": "event.id",
					},
					map[string]any{
						"name":   "kind",
						"source": "event.kind",
					},
				},
			},
			input: map[string]any{
				"event": map[string]any{
					"id":   "resource-123",
					"kind": "Cluster",
				},
			},
			checkParams: func(t *testing.T, output map[string]any) {
				params, ok := output["params"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "resource-123", params["id"])
				assert.Equal(t, "Cluster", params["kind"])
			},
		},
		{
			name: "direct field access without prefix",
			args: map[string]any{
				"sources": []any{
					map[string]any{
						"name":   "directId",
						"source": "id",
					},
				},
			},
			input: map[string]any{
				"event": map[string]any{
					"id": "direct-value",
				},
			},
			checkParams: func(t *testing.T, output map[string]any) {
				params, ok := output["params"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "direct-value", params["directId"])
			},
		},
		{
			name: "type conversion to int",
			args: map[string]any{
				"sources": []any{
					map[string]any{
						"name":   "count",
						"source": "event.count",
						"type":   "int",
					},
				},
			},
			input: map[string]any{
				"event": map[string]any{
					"count": "42",
				},
			},
			checkParams: func(t *testing.T, output map[string]any) {
				params, ok := output["params"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, int64(42), params["count"])
			},
		},
		{
			name: "type conversion to bool",
			args: map[string]any{
				"sources": []any{
					map[string]any{
						"name":   "enabled",
						"source": "event.enabled",
						"type":   "bool",
					},
				},
			},
			input: map[string]any{
				"event": map[string]any{
					"enabled": "true",
				},
			},
			checkParams: func(t *testing.T, output map[string]any) {
				params, ok := output["params"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, true, params["enabled"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup()
			}
			if tt.cleanup != nil {
				defer tt.cleanup()
			}

			output, err := runner.Run(ctx, tt.args, tt.input)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, output)

			if tt.checkParams != nil {
				tt.checkParams(t, output)
			}
		})
	}
}

func TestExtractTaskRunner_RequiredFieldMissing(t *testing.T) {
	runner, _ := NewExtractTaskRunner(nil)
	ctx := context.Background()

	args := map[string]any{
		"sources": []any{
			map[string]any{
				"name":     "required",
				"source":   "event.nonexistent",
				"required": true,
			},
		},
	}

	input := map[string]any{
		"event": map[string]any{},
	}

	_, err := runner.Run(ctx, args, input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to extract required parameter")
}

func TestExtractTaskRunner_PreservesInput(t *testing.T) {
	runner, _ := NewExtractTaskRunner(nil)
	ctx := context.Background()

	input := map[string]any{
		"existingKey": "existingValue",
		"event": map[string]any{
			"id": "test-id",
		},
	}

	args := map[string]any{
		"sources": []any{
			map[string]any{
				"name":   "eventId",
				"source": "event.id",
			},
		},
	}

	output, err := runner.Run(ctx, args, input)
	require.NoError(t, err)

	// Original input should be preserved
	assert.Equal(t, "existingValue", output["existingKey"])
	assert.Equal(t, input["event"], output["event"])

	// Params should contain extracted value
	params, ok := output["params"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "test-id", params["eventId"])
}

func TestExtractFromEvent(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		eventData   map[string]any
		expected    any
		expectError bool
	}{
		{
			name:      "simple field",
			path:      "id",
			eventData: map[string]any{"id": "123"},
			expected:  "123",
		},
		{
			name:      "nested field",
			path:      "cluster.status",
			eventData: map[string]any{"cluster": map[string]any{"status": "ready"}},
			expected:  "ready",
		},
		{
			name:      "deeply nested",
			path:      "a.b.c.d",
			eventData: map[string]any{"a": map[string]any{"b": map[string]any{"c": map[string]any{"d": "value"}}}},
			expected:  "value",
		},
		{
			name:        "missing field",
			path:        "nonexistent",
			eventData:   map[string]any{},
			expectError: true,
		},
		{
			name:        "missing nested field",
			path:        "a.missing",
			eventData:   map[string]any{"a": map[string]any{}},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := extractFromEvent(tt.path, tt.eventData)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertParamType(t *testing.T) {
	tests := []struct {
		name        string
		value       any
		targetType  string
		expected    any
		expectError bool
	}{
		// String conversions
		{"string to string", "hello", "string", "hello", false},
		{"int to string", 42, "string", "42", false},
		{"bool to string", true, "string", "true", false},

		// Int conversions
		{"string to int", "123", "int", int64(123), false},
		{"float to int", 3.14, "int", int64(3), false},
		{"invalid string to int", "abc", "int", nil, true},

		// Float conversions
		{"string to float", "3.14", "float", 3.14, false},
		{"int to float", 42, "float", float64(42), false},

		// Bool conversions
		{"string to bool true", "true", "bool", true, false},
		{"string to bool yes", "yes", "bool", true, false},
		{"string to bool false", "false", "bool", false, false},
		{"string to bool no", "no", "bool", false, false},
		{"int to bool", 1, "bool", true, false},
		{"zero to bool", 0, "bool", false, false},

		// Unsupported type
		{"unsupported type", "value", "unsupported", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertParamType(tt.value, tt.targetType)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
