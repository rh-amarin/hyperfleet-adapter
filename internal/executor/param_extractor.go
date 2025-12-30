package executor

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
)

// extractConfigParams extracts all configured parameters and populates execCtx.Params
// This is a pure function that directly modifies execCtx for simplicity
func extractConfigParams(config *config_loader.AdapterConfig, execCtx *ExecutionContext, k8sClient k8s_client.K8sClient) error {
	for _, param := range config.Spec.Params {
		value, err := extractParam(execCtx.Ctx, param, execCtx.EventData, k8sClient)
		if err != nil {
			if param.Required {
				return NewExecutorError(PhaseParamExtraction, param.Name,
					fmt.Sprintf("failed to extract required parameter '%s' from source '%s'", param.Name, param.Source), err)
			}
			// Use default for non-required params if extraction fails
			if param.Default != nil {
				execCtx.Params[param.Name] = param.Default
			}
			continue
		}

		// Apply default if value is nil or (for strings) empty
		isEmpty := value == nil
		if s, ok := value.(string); ok && s == "" {
			isEmpty = true
		}
		if isEmpty && param.Default != nil {
			value = param.Default
		}

		// Apply type conversion if specified
		if value != nil && param.Type != "" {
			converted, convErr := convertParamType(value, param.Type)
			if convErr != nil {
				if param.Required {
					return NewExecutorError(PhaseParamExtraction, param.Name,
						fmt.Sprintf("failed to convert parameter '%s' to type '%s'", param.Name, param.Type), convErr)
				}
				// Use default for non-required params if conversion fails
				if param.Default != nil {
					execCtx.Params[param.Name] = param.Default
				}
				continue
			}
			value = converted
		}

		if value != nil {
			execCtx.Params[param.Name] = value
		}
	}

	return nil
}

// extractParam extracts a single parameter based on its source
func extractParam(ctx context.Context, param config_loader.Parameter, eventData map[string]interface{}, k8sClient k8s_client.K8sClient) (interface{}, error) {
	source := param.Source

	// Handle different source types
	switch {
	case strings.HasPrefix(source, "env."):
		return extractFromEnv(source[4:])
	case strings.HasPrefix(source, "event."):
		return extractFromEvent(source[6:], eventData)
	case strings.HasPrefix(source, "secret."):
		return extractFromSecret(ctx, source[7:], k8sClient)
	case strings.HasPrefix(source, "configmap."):
		return extractFromConfigMap(ctx, source[10:], k8sClient)
	case source == "":
		// No source specified, return default or nil
		return param.Default, nil
	default:
		// Try to extract from event data directly
		return extractFromEvent(source, eventData)
	}
}

// extractFromEnv extracts a value from environment variables
func extractFromEnv(envVar string) (interface{}, error) {
	value, exists := os.LookupEnv(envVar)
	if !exists {
		return nil, fmt.Errorf("environment variable %s not set", envVar)
	}
	return value, nil
}

// extractFromEvent extracts a value from event data using dot notation
func extractFromEvent(path string, eventData map[string]interface{}) (interface{}, error) {
	parts := strings.Split(path, ".")
	var current interface{} = eventData

	for i, part := range parts {
		switch v := current.(type) {
		case map[string]interface{}:
			val, ok := v[part]
			if !ok {
				return nil, fmt.Errorf("field '%s' not found at path '%s'", part, strings.Join(parts[:i+1], "."))
			}
			current = val
		case map[interface{}]interface{}:
			val, ok := v[part]
			if !ok {
				return nil, fmt.Errorf("field '%s' not found at path '%s'", part, strings.Join(parts[:i+1], "."))
			}
			current = val
		default:
			return nil, fmt.Errorf("cannot access field '%s': parent is not a map (got %T)", part, current)
		}
	}

	return current, nil
}

// extractFromSecret extracts a value from a Kubernetes Secret
// Format: secret.<namespace>.<secret-name>.<key> (namespace is required)
func extractFromSecret(ctx context.Context, path string, k8sClient k8s_client.K8sClient) (interface{}, error) {
	if k8sClient == nil {
		return nil, fmt.Errorf("kubernetes client not configured, cannot extract from secret")
	}

	value, err := k8sClient.ExtractFromSecret(ctx, path)
	if err != nil {
		return nil, err
	}

	return value, nil
}

// extractFromConfigMap extracts a value from a Kubernetes ConfigMap
// Format: configmap.<namespace>.<configmap-name>.<key> (namespace is required)
func extractFromConfigMap(ctx context.Context, path string, k8sClient k8s_client.K8sClient) (interface{}, error) {
	if k8sClient == nil {
		return nil, fmt.Errorf("kubernetes client not configured, cannot extract from configmap")
	}

	value, err := k8sClient.ExtractFromConfigMap(ctx, path)
	if err != nil {
		return nil, err
	}

	return value, nil
}

// addMetadataParams adds adapter and event metadata to execCtx.Params
func addMetadataParams(config *config_loader.AdapterConfig, execCtx *ExecutionContext) {
	// Add metadata from adapter config
	execCtx.Params["metadata"] = map[string]interface{}{
		"name":      config.Metadata.Name,
		"namespace": config.Metadata.Namespace,
		"labels":    config.Metadata.Labels,
	}
}

// convertParamType converts a value to the specified type
// Supported types: string, int, int64, float, float64, bool
func convertParamType(value interface{}, targetType string) (interface{}, error) {
	// If value is already the target type, return as-is
	switch targetType {
	case "string":
		return convertToString(value)
	case "int", "int64":
		return convertToInt64(value)
	case "float", "float64":
		return convertToFloat64(value)
	case "bool":
		return convertToBool(value)
	default:
		return nil, fmt.Errorf("unsupported type: %s (supported: string, int, int64, float, float64, bool)", targetType)
	}
}

// convertToString converts a value to string
func convertToString(value interface{}) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case int, int8, int16, int32, int64:
		return fmt.Sprintf("%d", v), nil
	case uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", v), nil
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case bool:
		return strconv.FormatBool(v), nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

// convertToInt64 converts a value to int64
func convertToInt64(value interface{}) (int64, error) {
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case uint64:
		if v > math.MaxInt64 {
			return 0, fmt.Errorf("uint64 value %d overflows int64", v)
		}
		return int64(v), nil
	case int8:
		return int64(v), nil
	case int16:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case uint:
		if v > uint(math.MaxInt64) {
			return 0, fmt.Errorf("uint value %d overflows int64", v)
		}
		return int64(v), nil
	case uint8:
		return int64(v), nil
	case uint16:
		return int64(v), nil
	case uint32:
		return int64(v), nil
	case float32:
		return int64(v), nil
	case float64:
		return int64(v), nil
	case string:
		// Try parsing as int first
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i, nil
		}
		// Try parsing as float and convert
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return int64(f), nil
		}
		return 0, fmt.Errorf("cannot convert string '%s' to int", v)
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	default:
		return 0, fmt.Errorf("cannot convert %T to int", value)
	}
}

// convertToFloat64 converts a value to float64
func convertToFloat64(value interface{}) (float64, error) {
	switch v := value.(type) {
	case float32:
		return float64(v), nil
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int8:
		return float64(v), nil
	case int16:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case uint:
		return float64(v), nil
	case uint8:
		return float64(v), nil
	case uint16:
		return float64(v), nil
	case uint32:
		return float64(v), nil
	case uint64:
		return float64(v), nil
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0, fmt.Errorf("cannot convert string '%s' to float: %w", v, err)
		}
		return f, nil
	case bool:
		if v {
			return 1.0, nil
		}
		return 0.0, nil
	default:
		return 0, fmt.Errorf("cannot convert %T to float", value)
	}
}

// convertToBool converts a value to bool
func convertToBool(value interface{}) (bool, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		// Empty string is treated as false
		if v == "" {
			return false, nil
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			// Handle common truthy/falsy strings
			lower := strings.ToLower(v)
			switch lower {
			case "yes", "y", "on", "1":
				return true, nil
			case "no", "n", "off", "0":
				return false, nil
			}
			return false, fmt.Errorf("cannot convert string '%s' to bool", v)
		}
		return b, nil
	// NOTE: Each numeric type needs its own case arm. In Go type switches, combined
	// cases like "case int, int8, int16:" keep v as interface{}, so "v != 0" would
	// compare interface{}(int8(0)) with interface{}(int(0)) - different types that
	// are never equal, causing int8(0) to incorrectly return true.
	// With separate arms, v is bound to the concrete type, enabling correct comparison.
	case int:
		return v != 0, nil
	case int8:
		return v != 0, nil
	case int16:
		return v != 0, nil
	case int32:
		return v != 0, nil
	case int64:
		return v != 0, nil
	case uint:
		return v != 0, nil
	case uint8:
		return v != 0, nil
	case uint16:
		return v != 0, nil
	case uint32:
		return v != 0, nil
	case uint64:
		return v != 0, nil
	case float32:
		return v != 0, nil
	case float64:
		return v != 0, nil
	default:
		return false, fmt.Errorf("cannot convert %T to bool", value)
	}
}
