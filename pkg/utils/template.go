// Package utils provides general-purpose utility functions.
package utils

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/Masterminds/sprig/v3"
)

// TemplateFuncs provides helper functions for Go templates.
// These functions are available within {{ }} template expressions.
// This includes all Sprig functions plus custom adapter-specific functions.
var TemplateFuncs = buildTemplateFuncs()

// buildTemplateFuncs creates the full set of template functions
// by combining Sprig functions with custom adapter functions
func buildTemplateFuncs() template.FuncMap {
	// Start with all Sprig functions
	funcs := sprig.TxtFuncMap()

	// Add custom time functions that work with our date format
	funcs["now"] = time.Now
	funcs["date"] = func(layout string, t time.Time) string {
		return t.Format(layout)
	}
	funcs["dateFormat"] = func(layout string, t time.Time) string {
		return t.Format(layout)
	}

	// Add convenience wrapper for trimSpace (Sprig's trim requires cutset parameter)
	funcs["trimSpace"] = strings.TrimSpace

	// Add custom type conversion functions with fallback behavior
	funcs["toInt"] = func(v interface{}) int {
		switch val := v.(type) {
		case int:
			return val
		case int64:
			return int(val)
		case float64:
			return int(val)
		case string:
			i, _ := strconv.Atoi(val) //nolint:errcheck // returns 0 on error, which is acceptable
			return i
		default:
			return 0
		}
	}
	funcs["toInt64"] = func(v interface{}) int64 {
		switch val := v.(type) {
		case int:
			return int64(val)
		case int64:
			return val
		case float64:
			return int64(val)
		case string:
			i, _ := strconv.ParseInt(val, 10, 64) //nolint:errcheck // returns 0 on error, which is acceptable
			return i
		default:
			return 0
		}
	}
	funcs["toFloat"] = func(v interface{}) float64 {
		switch val := v.(type) {
		case int:
			return float64(val)
		case int64:
			return float64(val)
		case float64:
			return val
		case string:
			f, _ := strconv.ParseFloat(val, 64) //nolint:errcheck // returns 0 on error, which is acceptable
			return f
		default:
			return 0
		}
	}
	funcs["toFloat64"] = func(v interface{}) float64 {
		switch val := v.(type) {
		case int:
			return float64(val)
		case int64:
			return float64(val)
		case float64:
			return val
		case string:
			f, _ := strconv.ParseFloat(val, 64) //nolint:errcheck // returns 0 on error, which is acceptable
			return f
		default:
			return 0
		}
	}
	funcs["toString"] = func(v interface{}) string {
		return fmt.Sprintf("%v", v)
	}

	return funcs
}

// RenderTemplate renders a Go template string with the given data.
// If the string contains no template delimiters ({{ }}), it is returned as-is.
//
// Parameters:
//   - templateStr: The template string to render
//   - data: The data to use for template rendering
//
// Returns the rendered string or an error if rendering fails.
//
// Example:
//
//	rendered, err := RenderTemplate("Hello {{.name}}", map[string]interface{}{"name": "World"})
//	// rendered = "Hello World"
func RenderTemplate(templateStr string, data map[string]interface{}) (string, error) {
	// If no template delimiters, return as-is
	if !strings.Contains(templateStr, "{{") {
		return templateStr, nil
	}

	tmpl, err := template.New("template").Funcs(TemplateFuncs).Option("missingkey=error").Parse(templateStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// RenderTemplateBytes renders a Go template string and returns the result as bytes.
func RenderTemplateBytes(templateStr string, data map[string]interface{}) ([]byte, error) {
	result, err := RenderTemplate(templateStr, data)
	if err != nil {
		return nil, err
	}
	return []byte(result), nil
}
