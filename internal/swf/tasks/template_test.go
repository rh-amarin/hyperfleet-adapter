package tasks

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTemplateTaskRunner(t *testing.T) {
	runner, err := NewTemplateTaskRunner(nil)
	require.NoError(t, err)
	require.NotNil(t, runner)
	assert.Equal(t, TaskTemplate, runner.Name())
}

func TestTemplateTaskRunner_Run(t *testing.T) {
	runner, _ := NewTemplateTaskRunner(nil)
	ctx := context.Background()

	tests := []struct {
		name        string
		args        map[string]any
		input       map[string]any
		expected    string
		expectError bool
	}{
		{
			name:        "missing template",
			args:        map[string]any{},
			input:       map[string]any{},
			expectError: true,
		},
		{
			name: "simple template without variables",
			args: map[string]any{
				"template": "Hello World",
			},
			input:    map[string]any{},
			expected: "Hello World",
		},
		{
			name: "template with variable substitution",
			args: map[string]any{
				"template": "Hello {{ .name }}!",
			},
			input: map[string]any{
				"name": "Alice",
			},
			expected: "Hello Alice!",
		},
		{
			name: "template with nested data",
			args: map[string]any{
				"template": "Cluster: {{ .cluster.id }}",
			},
			input: map[string]any{
				"cluster": map[string]any{
					"id": "cluster-123",
				},
			},
			expected: "Cluster: cluster-123",
		},
		{
			name: "template with custom data arg",
			args: map[string]any{
				"template": "Value: {{ .value }}",
				"data": map[string]any{
					"value": "custom",
				},
			},
			input: map[string]any{
				"value": "from-input",
			},
			expected: "Value: custom", // data arg takes precedence
		},
		{
			name: "template with lower function",
			args: map[string]any{
				"template": "{{ lower .name }}",
			},
			input: map[string]any{
				"name": "ALICE",
			},
			expected: "alice",
		},
		{
			name: "template with upper function",
			args: map[string]any{
				"template": "{{ upper .name }}",
			},
			input: map[string]any{
				"name": "alice",
			},
			expected: "ALICE",
		},
		{
			name: "template with default function - nil value",
			args: map[string]any{
				"template": "{{ default \"unknown\" .value }}",
			},
			input: map[string]any{
				"value": nil,
			},
			expected: "unknown",
		},
		{
			name: "template with default function - empty string",
			args: map[string]any{
				"template": "{{ default \"unknown\" .value }}",
			},
			input: map[string]any{
				"value": "",
			},
			expected: "unknown",
		},
		{
			name: "template with default function - has value",
			args: map[string]any{
				"template": "{{ default \"unknown\" .value }}",
			},
			input: map[string]any{
				"value": "actual",
			},
			expected: "actual",
		},
		{
			name: "no template delimiters returns as-is",
			args: map[string]any{
				"template": "plain text without delimiters",
			},
			input:    map[string]any{},
			expected: "plain text without delimiters",
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
			assert.Equal(t, tt.expected, output["result"])
		})
	}
}

func TestRenderTemplate(t *testing.T) {
	tests := []struct {
		name        string
		template    string
		data        map[string]any
		expected    string
		expectError bool
	}{
		{
			name:     "no delimiters",
			template: "plain text",
			data:     map[string]any{},
			expected: "plain text",
		},
		{
			name:     "simple substitution",
			template: "Hello {{ .name }}",
			data:     map[string]any{"name": "World"},
			expected: "Hello World",
		},
		{
			name:        "missing key with missingkey=error",
			template:    "Hello {{ .missing }}",
			data:        map[string]any{},
			expectError: true,
		},
		{
			name:     "trim function",
			template: "{{ trim .value }}",
			data:     map[string]any{"value": "  spaced  "},
			expected: "spaced",
		},
		{
			name:     "replace function",
			template: "{{ replace .value \"-\" \"_\" }}",
			data:     map[string]any{"value": "a-b-c"},
			expected: "a_b_c",
		},
		{
			name:     "contains function",
			template: "{{ if contains .value \"test\" }}yes{{ else }}no{{ end }}",
			data:     map[string]any{"value": "this is a test"},
			expected: "yes",
		},
		{
			name:     "int conversion",
			template: "{{ int .value }}",
			data:     map[string]any{"value": "42"},
			expected: "42",
		},
		{
			name:     "quote function",
			template: "{{ quote .value }}",
			data:     map[string]any{"value": "hello"},
			expected: `"hello"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := RenderTemplate(tt.template, tt.data)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTemplateTaskRunner_PreservesInput(t *testing.T) {
	runner, _ := NewTemplateTaskRunner(nil)
	ctx := context.Background()

	input := map[string]any{
		"existingKey": "existingValue",
		"nested": map[string]any{
			"key": "value",
		},
	}

	args := map[string]any{
		"template": "rendered",
	}

	output, err := runner.Run(ctx, args, input)
	require.NoError(t, err)

	// Original input should be preserved
	assert.Equal(t, "existingValue", output["existingKey"])
	assert.Equal(t, input["nested"], output["nested"])

	// Result should be added
	assert.Equal(t, "rendered", output["result"])
}
