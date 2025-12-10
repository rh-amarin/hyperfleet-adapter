package executor

import (
	"context"
	"fmt"
	"os"
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

		// Apply default if value is nil or empty string
		if (value == nil || value == "") && param.Default != nil {
			value = param.Default
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

	// Add event metadata if available
	if execCtx.Event != nil {
		execCtx.Params["eventMetadata"] = map[string]interface{}{
			"id":     execCtx.Event.ID(),
			"type":   execCtx.Event.Type(),
			"source": execCtx.Event.Source(),
			"time":   execCtx.Event.Time().String(),
		}
	}
}

