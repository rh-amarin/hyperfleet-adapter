package criteria

import (
	"bytes"
	"fmt"
	"strings"

	"k8s.io/client-go/util/jsonpath"
)

type FieldResult struct {
	Value interface{}
	Error error
}

// ExtractField extracts a field from any data structure using JSONPath.
//
// Supports both simple dot notation and full JSONPath syntax:
//   - Simple: "metadata.name", "status.phase"
//   - JSONPath: "{.items[*].metadata.name}"
//   - JSONPath with filter: "{.items[?(@.adapter=='landing-zone-adapter')].data.namespace.status}"
//
// Simple paths are auto-converted to JSONPath (e.g., "metadata.name" → "{.metadata.name}")
func ExtractField(data interface{}, field string) (*FieldResult, error) {
	result := &FieldResult{}
	originalField := field
	field = strings.TrimSpace(field)
	if field == "" {
		return result, fmt.Errorf("empty field path")
	}

	// Convert simple dot notation to JSONPath format
	// e.g., "metadata.name" → "{.metadata.name}"
	jsonPath := field
	if !strings.HasPrefix(jsonPath, "{") {
		if !strings.HasPrefix(jsonPath, ".") {
			jsonPath = "." + jsonPath
		}
		jsonPath = "{" + jsonPath + "}"
	}

	// Create JSONPath parser
	// AllowMissingKeys(false) ensures we get errors for missing fields (backward compatible)
	jp := jsonpath.New("field-extractor").AllowMissingKeys(false)

	// Parse the JSONPath
	if err := jp.Parse(jsonPath); err != nil {
		return result, fmt.Errorf("invalid field path '%s': %w", originalField, err)
	}

	// Execute the query to check if the path is valid and exists
	var buf bytes.Buffer
	if err := jp.Execute(&buf, data); err != nil {
		result.Error = fmt.Errorf("failed to execute JSONPath: %w", err)
		return result, nil
	}

	// Check if the result is empty (field not found or empty value)
	if strings.TrimSpace(buf.String()) == "" {
		// result.Value remains nil
		return result, nil
	}

	// Use FindResults to get typed values (not just string representation)
	results, err := jp.FindResults(data)
	if err != nil {
		result.Error = fmt.Errorf("failed to find results for '%s': %w", originalField, err)
		// result.Value remains nil - don't mix string from Execute with error
		return result, nil
	}

	// Flatten results
	var values []interface{}
	for _, r := range results {
		for _, v := range r {
			if v.CanInterface() {
				values = append(values, v.Interface())
			}
		}
	}

	// Set result value based on count
	switch len(values) {
	case 0:
		// result.Value remains nil
	case 1:
		result.Value = values[0]
	default:
		result.Value = values
	}

	return result, nil
}
