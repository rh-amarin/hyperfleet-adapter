package tasks

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"text/template"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// TemplateTaskRunner implements the hf:template task for Go template rendering.
// It renders Go templates with access to workflow context data.
type TemplateTaskRunner struct{}

// NewTemplateTaskRunner creates a new template task runner.
func NewTemplateTaskRunner(deps *Dependencies) (TaskRunner, error) {
	return &TemplateTaskRunner{}, nil
}

func (r *TemplateTaskRunner) Name() string {
	return TaskTemplate
}

// Run executes the template rendering task.
// Args should contain:
//   - template: The Go template string to render
//   - data: Optional map of data to use (defaults to input)
//
// Returns a map with:
//   - result: The rendered template string
func (r *TemplateTaskRunner) Run(ctx context.Context, args map[string]any, input map[string]any) (map[string]any, error) {
	templateStr, ok := args["template"].(string)
	if !ok || templateStr == "" {
		return nil, fmt.Errorf("template is required for hf:template task")
	}

	// Get data - use args["data"] if provided, otherwise use input
	data := input
	if argData, ok := args["data"].(map[string]any); ok {
		data = argData
	}

	// Render the template
	result, err := RenderTemplate(templateStr, data)
	if err != nil {
		return nil, fmt.Errorf("template rendering failed: %w", err)
	}

	output := make(map[string]any)

	// Copy input for continuation
	for k, v := range input {
		output[k] = v
	}

	output["result"] = result

	return output, nil
}

// templateFuncs provides common functions for Go templates.
// This mirrors the functions available in the executor/utils.go.
var templateFuncs = template.FuncMap{
	// Time functions
	"now": time.Now,
	"date": func(layout string, t time.Time) string {
		return t.Format(layout)
	},
	"dateFormat": func(layout string, t time.Time) string {
		return t.Format(layout)
	},
	// String functions
	"lower": strings.ToLower,
	"upper": strings.ToUpper,
	"title": func(s string) string {
		return cases.Title(language.English).String(s)
	},
	"trim":      strings.TrimSpace,
	"replace":   strings.ReplaceAll,
	"contains":  strings.Contains,
	"hasPrefix": strings.HasPrefix,
	"hasSuffix": strings.HasSuffix,
	// Default value function
	"default": func(defaultVal, val any) any {
		if val == nil || val == "" {
			return defaultVal
		}
		return val
	},
	// Quote function
	"quote": func(s string) string {
		return fmt.Sprintf("%q", s)
	},
	// Type conversion functions
	"int": func(v any) int {
		switch val := v.(type) {
		case int:
			return val
		case int64:
			return int(val)
		case float64:
			return int(val)
		case string:
			i, _ := strconv.Atoi(val)
			return i
		default:
			return 0
		}
	},
	"int64": func(v any) int64 {
		switch val := v.(type) {
		case int:
			return int64(val)
		case int64:
			return val
		case float64:
			return int64(val)
		case string:
			i, _ := strconv.ParseInt(val, 10, 64)
			return i
		default:
			return 0
		}
	},
	"float64": func(v any) float64 {
		switch val := v.(type) {
		case int:
			return float64(val)
		case int64:
			return float64(val)
		case float64:
			return val
		case string:
			f, _ := strconv.ParseFloat(val, 64)
			return f
		default:
			return 0
		}
	},
	"string": func(v any) string {
		return fmt.Sprintf("%v", v)
	},
}

// RenderTemplate renders a Go template string with the given data.
// This is a shared utility function.
func RenderTemplate(templateStr string, data map[string]any) (string, error) {
	// If no template delimiters, return as-is
	if !strings.Contains(templateStr, "{{") {
		return templateStr, nil
	}

	tmpl, err := template.New("template").Funcs(templateFuncs).Option("missingkey=error").Parse(templateStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// RenderTemplateBytes renders a Go template string and returns bytes.
func RenderTemplateBytes(templateStr string, data map[string]any) ([]byte, error) {
	result, err := RenderTemplate(templateStr, data)
	if err != nil {
		return nil, err
	}
	return []byte(result), nil
}

func init() {
	// Register the template task runner in the default registry
	_ = RegisterDefault(TaskTemplate, NewTemplateTaskRunner)
}
