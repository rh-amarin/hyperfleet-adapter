package tasks

import (
	"context"
	"fmt"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
)

// K8sReadTaskRunner implements the hf:k8s-read task for reading secrets and configmaps.
// This task is used when native SWF set tasks cannot access K8s resources directly.
//
// Example usage in workflow:
//
//	call: hf:k8s-read
//	with:
//	  secrets:
//	    - name: apiToken
//	      ref: hyperfleet-system/api-credentials.token
//	  configmaps:
//	    - name: apiEndpoint
//	      ref: hyperfleet-system/config.endpoint
type K8sReadTaskRunner struct {
	k8sClient k8s_client.K8sClient
}

// NewK8sReadTaskRunner creates a new K8s read task runner.
func NewK8sReadTaskRunner(deps *Dependencies) (TaskRunner, error) {
	var k8sClient k8s_client.K8sClient
	if deps != nil && deps.K8sClient != nil {
		var ok bool
		k8sClient, ok = deps.K8sClient.(k8s_client.K8sClient)
		if !ok {
			return nil, fmt.Errorf("invalid K8sClient type")
		}
	}

	return &K8sReadTaskRunner{
		k8sClient: k8sClient,
	}, nil
}

func (r *K8sReadTaskRunner) Name() string {
	return TaskK8sRead
}

// Run reads secrets and configmaps from Kubernetes.
// Args should contain:
//   - secrets: array of {name, ref} where ref is "namespace/name.key"
//   - configmaps: array of {name, ref} where ref is "namespace/name.key"
//
// Returns extracted values as a map merged with the input.
func (r *K8sReadTaskRunner) Run(ctx context.Context, args map[string]any, input map[string]any) (map[string]any, error) {
	if r.k8sClient == nil {
		return nil, fmt.Errorf("kubernetes client not configured, cannot read K8s resources")
	}

	result := make(map[string]any)

	// Read secrets
	if secrets, ok := args["secrets"].([]any); ok {
		for _, s := range secrets {
			secretDef, ok := s.(map[string]any)
			if !ok {
				continue
			}

			name, _ := secretDef["name"].(string)
			ref, _ := secretDef["ref"].(string)

			if name == "" || ref == "" {
				continue
			}

			value, err := r.k8sClient.ExtractFromSecret(ctx, ref)
			if err != nil {
				// Check if this secret is required
				required, _ := secretDef["required"].(bool)
				if required {
					return nil, fmt.Errorf("failed to read required secret '%s' from '%s': %w", name, ref, err)
				}
				// Use default if provided
				if defaultVal, hasDefault := secretDef["default"]; hasDefault {
					result[name] = defaultVal
				}
				continue
			}

			result[name] = value
		}
	}

	// Read configmaps
	if configmaps, ok := args["configmaps"].([]any); ok {
		for _, c := range configmaps {
			cmDef, ok := c.(map[string]any)
			if !ok {
				continue
			}

			name, _ := cmDef["name"].(string)
			ref, _ := cmDef["ref"].(string)

			if name == "" || ref == "" {
				continue
			}

			value, err := r.k8sClient.ExtractFromConfigMap(ctx, ref)
			if err != nil {
				// Check if this configmap is required
				required, _ := cmDef["required"].(bool)
				if required {
					return nil, fmt.Errorf("failed to read required configmap '%s' from '%s': %w", name, ref, err)
				}
				// Use default if provided
				if defaultVal, hasDefault := cmDef["default"]; hasDefault {
					result[name] = defaultVal
				}
				continue
			}

			result[name] = value
		}
	}

	// Merge results into input
	output := make(map[string]any)
	for k, v := range input {
		output[k] = v
	}
	for k, v := range result {
		output[k] = v
	}

	return output, nil
}

func init() {
	// Register the k8s-read task runner in the default registry
	_ = RegisterDefault(TaskK8sRead, NewK8sReadTaskRunner)
}
