package tasks

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
)

// ExtractTaskRunner implements the hf:extract task for parameter extraction.
// It extracts parameters from event data, environment variables, secrets, and configmaps.
type ExtractTaskRunner struct {
	k8sClient k8s_client.K8sClient
}

// NewExtractTaskRunner creates a new extract task runner.
func NewExtractTaskRunner(deps *Dependencies) (TaskRunner, error) {
	var k8sClient k8s_client.K8sClient
	if deps != nil && deps.K8sClient != nil {
		var ok bool
		k8sClient, ok = deps.K8sClient.(k8s_client.K8sClient)
		if !ok {
			return nil, fmt.Errorf("invalid K8sClient type")
		}
	}

	return &ExtractTaskRunner{
		k8sClient: k8sClient,
	}, nil
}

func (r *ExtractTaskRunner) Name() string {
	return TaskExtract
}

// Run executes the parameter extraction task.
// Args should contain a "sources" array with parameter definitions.
// Returns extracted parameters as a map.
func (r *ExtractTaskRunner) Run(ctx context.Context, args map[string]any, input map[string]any) (map[string]any, error) {
	// Get event data from input
	eventData, _ := input["event"].(map[string]any)
	if eventData == nil {
		eventData = make(map[string]any)
	}

	// Get parameter sources from args
	sources, ok := args["sources"].([]any)
	if !ok {
		// Try getting from config.spec.params path
		if config, ok := args["config"].(map[string]any); ok {
			if spec, ok := config["spec"].(map[string]any); ok {
				sources, _ = spec["params"].([]any)
			}
		}
	}

	params := make(map[string]any)

	for _, src := range sources {
		paramDef, ok := src.(map[string]any)
		if !ok {
			continue
		}

		name, _ := paramDef["name"].(string)
		source, _ := paramDef["source"].(string)
		paramType, _ := paramDef["type"].(string)
		required, _ := paramDef["required"].(bool)
		defaultVal := paramDef["default"]

		if name == "" {
			continue
		}

		// Extract the parameter value
		value, err := r.extractParam(ctx, source, eventData)
		if err != nil {
			if required {
				return nil, fmt.Errorf("failed to extract required parameter '%s' from source '%s': %w", name, source, err)
			}
			// Use default for non-required params if extraction fails
			if defaultVal != nil {
				params[name] = defaultVal
			}
			continue
		}

		// Apply default if value is nil or empty string
		isEmpty := value == nil
		if s, ok := value.(string); ok && s == "" {
			isEmpty = true
		}
		if isEmpty && defaultVal != nil {
			value = defaultVal
		}

		// Apply type conversion if specified
		if value != nil && paramType != "" {
			converted, convErr := convertParamType(value, paramType)
			if convErr != nil {
				if required {
					return nil, fmt.Errorf("failed to convert parameter '%s' to type '%s': %w", name, paramType, convErr)
				}
				// Use default for non-required params if conversion fails
				if defaultVal != nil {
					params[name] = defaultVal
				}
				continue
			}
			value = converted
		}

		if value != nil {
			params[name] = value
		}
	}

	// Add metadata if available in args
	if metadata, ok := args["metadata"].(map[string]any); ok {
		params["metadata"] = metadata
	}

	// Return merged output with extracted params
	output := make(map[string]any)
	for k, v := range input {
		output[k] = v
	}
	output["params"] = params

	return output, nil
}

// extractParam extracts a single parameter based on its source.
func (r *ExtractTaskRunner) extractParam(ctx context.Context, source string, eventData map[string]any) (any, error) {
	switch {
	case strings.HasPrefix(source, "env."):
		return extractFromEnv(source[4:])
	case strings.HasPrefix(source, "event."):
		return extractFromEvent(source[6:], eventData)
	case strings.HasPrefix(source, "secret."):
		return r.extractFromSecret(ctx, source[7:])
	case strings.HasPrefix(source, "configmap."):
		return r.extractFromConfigMap(ctx, source[10:])
	case source == "":
		return nil, nil
	default:
		// Try to extract from event data directly
		return extractFromEvent(source, eventData)
	}
}

func (r *ExtractTaskRunner) extractFromSecret(ctx context.Context, path string) (any, error) {
	if r.k8sClient == nil {
		return nil, fmt.Errorf("kubernetes client not configured, cannot extract from secret")
	}
	return r.k8sClient.ExtractFromSecret(ctx, path)
}

func (r *ExtractTaskRunner) extractFromConfigMap(ctx context.Context, path string) (any, error) {
	if r.k8sClient == nil {
		return nil, fmt.Errorf("kubernetes client not configured, cannot extract from configmap")
	}
	return r.k8sClient.ExtractFromConfigMap(ctx, path)
}

// extractFromEnv extracts a value from environment variables.
func extractFromEnv(envVar string) (any, error) {
	value, exists := os.LookupEnv(envVar)
	if !exists {
		return nil, fmt.Errorf("environment variable %s not set", envVar)
	}
	return value, nil
}

// extractFromEvent extracts a value from event data using dot notation.
func extractFromEvent(path string, eventData map[string]any) (any, error) {
	parts := strings.Split(path, ".")
	var current any = eventData

	for i, part := range parts {
		switch v := current.(type) {
		case map[string]any:
			val, ok := v[part]
			if !ok {
				return nil, fmt.Errorf("field '%s' not found at path '%s'", part, strings.Join(parts[:i+1], "."))
			}
			current = val
		case map[any]any:
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

// convertParamType converts a value to the specified type.
func convertParamType(value any, targetType string) (any, error) {
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
		return nil, fmt.Errorf("unsupported type: %s", targetType)
	}
}

func convertToString(value any) (string, error) {
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

func convertToInt64(value any) (int64, error) {
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case float64:
		return int64(v), nil
	case string:
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i, nil
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return int64(f), nil
		}
		return 0, fmt.Errorf("cannot convert string '%s' to int", v)
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	case uint64:
		if v > math.MaxInt64 {
			return 0, fmt.Errorf("uint64 value %d overflows int64", v)
		}
		return int64(v), nil
	default:
		return 0, fmt.Errorf("cannot convert %T to int", value)
	}
}

func convertToFloat64(value any) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
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

func convertToBool(value any) (bool, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		if v == "" {
			return false, nil
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
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
	case int:
		return v != 0, nil
	case int64:
		return v != 0, nil
	case float64:
		return v != 0, nil
	default:
		return false, fmt.Errorf("cannot convert %T to bool", value)
	}
}

func init() {
	// Register the extract task runner in the default registry
	_ = RegisterDefault(TaskExtract, NewExtractTaskRunner)
}
