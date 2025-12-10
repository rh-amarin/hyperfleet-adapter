package executor

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/mitchellh/copystructure"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
	apperrors "github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/errors"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ResourceExecutor creates and updates Kubernetes resources
type ResourceExecutor struct {
	k8sClient k8s_client.K8sClient
}

// NewResourceExecutor creates a new resource executor
func NewResourceExecutor(k8sClient k8s_client.K8sClient) *ResourceExecutor {
	return &ResourceExecutor{
		k8sClient: k8sClient,
	}
}

// ExecuteAll creates/updates all resources in sequence
// Returns results for each resource and updates the execution context
func (re *ResourceExecutor) ExecuteAll(ctx context.Context, resources []config_loader.Resource, execCtx *ExecutionContext, log logger.Logger) ([]ResourceResult, error) {
	if execCtx.Resources == nil {
		execCtx.Resources = make(map[string]*unstructured.Unstructured)
	}
	results := make([]ResourceResult, 0, len(resources))

	for i, resource := range resources {
		log.Infof("  [Resource %d/%d] Processing: %s", i+1, len(resources), resource.Name)
		result, err := re.executeResource(ctx, resource, execCtx, log)
		results = append(results, result)

		if err != nil {
			log.Error(fmt.Sprintf("  [Resource %d/%d] %s: FAILED - %v", i+1, len(resources), resource.Name, err))
			return results, err
		}
		log.Infof("  [Resource %d/%d] %s: %s %s/%s (operation: %s) ✓", 
			i+1, len(resources), resource.Name, result.Kind, result.Namespace, result.ResourceName, result.Operation)
	}

	return results, nil
}

// executeResource creates or updates a single Kubernetes resource
func (re *ResourceExecutor) executeResource(ctx context.Context, resource config_loader.Resource, execCtx *ExecutionContext, log logger.Logger) (ResourceResult, error) {
	result := ResourceResult{
		Name:   resource.Name,
		Status: StatusSuccess,
	}

	// Step 1: Build the manifest
	log.V(1).Infof("    Building manifest from config")
	manifest, err := re.buildManifest(resource, execCtx, log)
	if err != nil {
		result.Status = StatusFailed
		result.Error = err
		return result, NewExecutorError(PhaseResources, resource.Name, "failed to build manifest", err)
	}

	// Extract resource info
	gvk := manifest.GroupVersionKind()
	result.Kind = gvk.Kind
	result.Namespace = manifest.GetNamespace()
	result.ResourceName = manifest.GetName()

	log.Infof("    Manifest: %s/%s %s (namespace: %s)",
		gvk.Group, gvk.Kind, manifest.GetName(), manifest.GetNamespace())

	// Step 2: Check for existing resource using discovery
	var existingResource *unstructured.Unstructured
	if resource.Discovery != nil {
		log.V(1).Infof("    Discovering existing resource...")
		existingResource, err = re.discoverExistingResource(ctx, gvk, resource.Discovery, execCtx)
		if err != nil && !apierrors.IsNotFound(err) {
			if apperrors.IsRetryableDiscoveryError(err) {
				// Transient/network error - log and continue, we'll try to create
				log.Warning(fmt.Sprintf("    Transient discovery error (continuing): %v", err))
			} else {
				// Fatal error (auth, permission, validation) - fail fast
				result.Status = StatusFailed
				result.Error = err
				return result, NewExecutorError(PhaseResources, resource.Name, "failed to discover existing resource", err)
			}
		}
		if existingResource != nil {
			log.Infof("    Existing resource found: %s/%s", existingResource.GetNamespace(), existingResource.GetName())
		} else {
			log.Infof("    No existing resource found, will create")
		}
	}

	// Step 3: Perform the appropriate operation
	if existingResource != nil {
		// Resource exists - update or recreate
		if resource.RecreateOnChange {
			result.Operation = OperationRecreate
			log.Infof("    Operation: RECREATE (recreateOnChange=true)")
			result.Resource, err = re.recreateResource(ctx, existingResource, manifest, log)
		} else {
			result.Operation = OperationUpdate
			log.Infof("    Operation: UPDATE")
			result.Resource, err = re.updateResource(ctx, existingResource, manifest)
		}
	} else {
		// Create new resource
		result.Operation = OperationCreate
		log.Infof("    Operation: CREATE")
		result.Resource, err = re.createResource(ctx, manifest)
	}

	if err != nil {
		result.Status = StatusFailed
		result.Error = err
		
		// Set ExecutionError for K8s operation failure
		execCtx.Adapter.ExecutionError = &ExecutionError{
			Phase:   string(PhaseResources),
			Step:    resource.Name,
			Message: err.Error(),
		}
		
		return result, NewExecutorError(PhaseResources, resource.Name,
			fmt.Sprintf("failed to %s resource", result.Operation), err)
	}

	// Store resource in execution context
	if result.Resource != nil {
		execCtx.Resources[resource.Name] = result.Resource
		log.V(1).Infof("    Resource stored in context as '%s'", resource.Name)
	}

	return result, nil
}

// buildManifest builds an unstructured manifest from the resource configuration
func (re *ResourceExecutor) buildManifest(resource config_loader.Resource, execCtx *ExecutionContext, log logger.Logger) (*unstructured.Unstructured, error) {
	var manifestData map[string]interface{}

	// Check if manifest is inline or from ManifestItems (loaded from ref)
	if len(resource.ManifestItems) > 0 {
		// Use first manifest item (loaded from ref file)
		manifestData = resource.ManifestItems[0]
	} else if resource.Manifest != nil {
		// Use inline manifest
		switch m := resource.Manifest.(type) {
		case map[string]interface{}:
			manifestData = m
		case map[interface{}]interface{}:
			manifestData = convertToStringKeyMap(m)
		default:
			return nil, fmt.Errorf("unsupported manifest type: %T", resource.Manifest)
		}
	} else {
		return nil, fmt.Errorf("no manifest specified for resource %s", resource.Name)
	}

	// Deep copy to avoid modifying the original
	manifestData = deepCopyMap(manifestData, log)

	// Render all template strings in the manifest
	renderedData, err := renderManifestTemplates(manifestData, execCtx.Params)
	if err != nil {
		return nil, fmt.Errorf("failed to render manifest templates: %w", err)
	}

	// Convert to unstructured
	obj := &unstructured.Unstructured{Object: renderedData}

	// Validate required fields
	if obj.GetAPIVersion() == "" {
		return nil, fmt.Errorf("manifest missing apiVersion")
	}
	if obj.GetKind() == "" {
		return nil, fmt.Errorf("manifest missing kind")
	}
	if obj.GetName() == "" {
		return nil, fmt.Errorf("manifest missing metadata.name")
	}

	return obj, nil
}

// discoverExistingResource discovers an existing resource using the discovery config
func (re *ResourceExecutor) discoverExistingResource(ctx context.Context, gvk schema.GroupVersionKind, discovery *config_loader.DiscoveryConfig, execCtx *ExecutionContext) (*unstructured.Unstructured, error) {
	if re.k8sClient == nil {
		return nil, fmt.Errorf("kubernetes client not configured")
	}

	// Render discovery namespace template
	// Empty namespace means all namespaces (normalized from "*" at config load time)
	namespace, err := renderTemplate(discovery.Namespace, execCtx.Params)
	if err != nil {
		return nil, fmt.Errorf("failed to render namespace template: %w", err)
	}

	// Check if discovering by name
	if discovery.ByName != "" {
		name, err := renderTemplate(discovery.ByName, execCtx.Params)
		if err != nil {
			return nil, fmt.Errorf("failed to render byName template: %w", err)
		}
		return re.k8sClient.GetResource(ctx, gvk, namespace, name)
	}

	// Discover by label selector
	if discovery.BySelectors != nil && len(discovery.BySelectors.LabelSelector) > 0 {
		// Render label selector templates
		renderedLabels := make(map[string]string)
		for k, v := range discovery.BySelectors.LabelSelector {
			renderedK, err := renderTemplate(k, execCtx.Params)
			if err != nil {
				return nil, fmt.Errorf("failed to render label key template: %w", err)
			}
			renderedV, err := renderTemplate(v, execCtx.Params)
			if err != nil {
				return nil, fmt.Errorf("failed to render label value template: %w", err)
			}
			renderedLabels[renderedK] = renderedV
		}

		labelSelector := k8s_client.BuildLabelSelector(renderedLabels)

		discoveryConfig := &k8s_client.DiscoveryConfig{
			Namespace:     namespace,
			LabelSelector: labelSelector,
		}

		list, err := re.k8sClient.DiscoverResources(ctx, gvk, discoveryConfig)
		if err != nil {
			return nil, err
		}

		if len(list.Items) == 0 {
			return nil, apierrors.NewNotFound(schema.GroupResource{Group: gvk.Group, Resource: gvk.Kind}, "")
		}

		// Sort by generation annotation (descending) to return the one with the latest generation
		// This ensures deterministic behavior when multiple resources match the label selector
		// Secondary sort by metadata.name for consistency when generations are equal
		sort.Slice(list.Items, func(i, j int) bool {
			genI := getGenerationAnnotationValue(&list.Items[i])
			genJ := getGenerationAnnotationValue(&list.Items[j])
			if genI != genJ {
				return genI > genJ // Descending order - latest generation first
			}
			// Fall back to metadata.name for deterministic ordering when generations are equal
			return list.Items[i].GetName() < list.Items[j].GetName()
		})

		return &list.Items[0], nil
	}

	return nil, fmt.Errorf("discovery config must specify byName or bySelectors")
}

// createResource creates a new Kubernetes resource
func (re *ResourceExecutor) createResource(ctx context.Context, manifest *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if re.k8sClient == nil {
		return nil, fmt.Errorf("kubernetes client not configured")
	}

	return re.k8sClient.CreateResource(ctx, manifest)
}

// updateResource updates an existing Kubernetes resource
func (re *ResourceExecutor) updateResource(ctx context.Context, existing, manifest *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if re.k8sClient == nil {
		return nil, fmt.Errorf("kubernetes client not configured")
	}

	// Preserve resourceVersion from existing for update
	manifest.SetResourceVersion(existing.GetResourceVersion())
	manifest.SetUID(existing.GetUID())

	return re.k8sClient.UpdateResource(ctx, manifest)
}

// recreateResource deletes and recreates a Kubernetes resource
// It waits for the resource to be fully deleted before creating the new one
// to avoid race conditions with Kubernetes asynchronous deletion
func (re *ResourceExecutor) recreateResource(ctx context.Context, existing, manifest *unstructured.Unstructured, log logger.Logger) (*unstructured.Unstructured, error) {
	if re.k8sClient == nil {
		return nil, fmt.Errorf("kubernetes client not configured")
	}

	gvk := existing.GroupVersionKind()
	namespace := existing.GetNamespace()
	name := existing.GetName()

	// Delete the existing resource
	log.Infof("Deleting resource for recreation: %s/%s", gvk.Kind, name)
	if err := re.k8sClient.DeleteResource(ctx, gvk, namespace, name); err != nil {
		return nil, fmt.Errorf("failed to delete resource for recreation: %w", err)
	}

	// Wait for the resource to be fully deleted
	log.Infof("Waiting for resource deletion to complete: %s/%s", gvk.Kind, name)
	if err := re.waitForDeletion(ctx, gvk, namespace, name, log); err != nil {
		return nil, fmt.Errorf("failed waiting for resource deletion: %w", err)
	}

	// Create the new resource
	log.Infof("Creating new resource after deletion confirmed: %s/%s", gvk.Kind, manifest.GetName())
	return re.k8sClient.CreateResource(ctx, manifest)
}

// waitForDeletion polls until the resource is confirmed deleted or context times out
// Returns nil when the resource is confirmed gone (NotFound), or an error otherwise
func (re *ResourceExecutor) waitForDeletion(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string, log logger.Logger) error {
	const pollInterval = 100 * time.Millisecond

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Warning(fmt.Sprintf("Context cancelled/timed out while waiting for deletion of %s/%s", gvk.Kind, name))
			return fmt.Errorf("context cancelled while waiting for resource deletion: %w", ctx.Err())
		case <-ticker.C:
			_, err := re.k8sClient.GetResource(ctx, gvk, namespace, name)
			if err != nil {
				// NotFound means the resource is deleted - this is success
				if apierrors.IsNotFound(err) {
					log.Infof("Resource deletion confirmed: %s/%s", gvk.Kind, name)
					return nil
				}
				// Any other error is unexpected
				log.Error(fmt.Sprintf("Error checking resource deletion status for %s/%s: %v", gvk.Kind, name, err))
				return fmt.Errorf("error checking deletion status: %w", err)
			}
			// Resource still exists, continue polling
			log.V(2).Infof("Resource %s/%s still exists, waiting for deletion...", gvk.Kind, name)
		}
	}
}

// convertToStringKeyMap converts map[interface{}]interface{} to map[string]interface{}
func convertToStringKeyMap(m map[interface{}]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range m {
		strKey := fmt.Sprintf("%v", k)
		switch val := v.(type) {
		case map[interface{}]interface{}:
			result[strKey] = convertToStringKeyMap(val)
		case []interface{}:
			result[strKey] = convertSlice(val)
		default:
			result[strKey] = v
		}
	}
	return result
}

// convertSlice converts slice elements recursively
func convertSlice(s []interface{}) []interface{} {
	result := make([]interface{}, len(s))
	for i, v := range s {
		switch val := v.(type) {
		case map[interface{}]interface{}:
			result[i] = convertToStringKeyMap(val)
		case []interface{}:
			result[i] = convertSlice(val)
		default:
			result[i] = v
		}
	}
	return result
}

// deepCopyMap creates a deep copy of a map using github.com/mitchellh/copystructure.
// This handles non-JSON-serializable types (channels, functions, time.Time, etc.)
// and preserves type information (e.g., int64 stays int64, not float64).
// If deep copy fails, it falls back to a shallow copy and logs a warning.
// WARNING: Shallow copy means nested maps/slices will share references with the original,
// which could lead to unexpected mutations.
func deepCopyMap(m map[string]interface{}, log logger.Logger) map[string]interface{} {
	if m == nil {
		return nil
	}

	copied, err := copystructure.Copy(m)
	if err != nil {
		// Fallback to shallow copy - LOG WARNING
		if log != nil {
			log.Warning(fmt.Sprintf("deepCopyMap: deep copy failed, falling back to shallow copy (mutations may affect original): %v", err))
		}
		result := make(map[string]interface{})
		for k, v := range m {
			result[k] = v
		}
		return result
	}

	result, ok := copied.(map[string]interface{})
	if !ok {
		// Should not happen, but handle gracefully
		if log != nil {
			log.Warning(fmt.Sprintf("deepCopyMap: unexpected type after copy (%T), falling back to shallow copy", copied))
		}
		result := make(map[string]interface{})
		for k, v := range m {
			result[k] = v
		}
		return result
	}

	return result
}

// renderManifestTemplates recursively renders all template strings in a manifest
func renderManifestTemplates(data map[string]interface{}, params map[string]interface{}) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	for k, v := range data {
		renderedKey, err := renderTemplate(k, params)
		if err != nil {
			return nil, fmt.Errorf("failed to render key '%s': %w", k, err)
		}

		renderedValue, err := renderValue(v, params)
		if err != nil {
			return nil, fmt.Errorf("failed to render value for key '%s': %w", k, err)
		}

		result[renderedKey] = renderedValue
	}

	return result, nil
}

// renderValue renders a value recursively
func renderValue(v interface{}, params map[string]interface{}) (interface{}, error) {
	switch val := v.(type) {
	case string:
		return renderTemplate(val, params)
	case map[string]interface{}:
		return renderManifestTemplates(val, params)
	case map[interface{}]interface{}:
		converted := convertToStringKeyMap(val)
		return renderManifestTemplates(converted, params)
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, item := range val {
			rendered, err := renderValue(item, params)
			if err != nil {
				return nil, err
			}
			result[i] = rendered
		}
		return result, nil
	default:
		return v, nil
	}
}

// getGenerationAnnotationValue extracts the generation annotation value from a resource
// Returns 0 if the resource is nil, has no annotations, or the annotation cannot be parsed
func getGenerationAnnotationValue(obj *unstructured.Unstructured) int64 {
	if obj == nil {
		return 0
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return 0
	}
	genStr, ok := annotations[AnnotationGeneration]
	if !ok || genStr == "" {
		return 0
	}
	// Try to parse as integer directly
	gen, err := strconv.ParseInt(genStr, 10, 64)
	if err != nil {
		// Generation value is not a valid integer, return 0
		return 0
	}
	return gen
}

// GetResourceAsMap converts an unstructured resource to a map for CEL evaluation
func GetResourceAsMap(resource *unstructured.Unstructured) map[string]interface{} {
	if resource == nil {
		return nil
	}
	return resource.Object
}

// BuildResourcesMap builds a map of all resources for CEL evaluation.
// Resource names are used directly as keys (snake_case and camelCase both work in CEL).
// Name validation (no hyphens, no duplicates) is done at config load time.
func BuildResourcesMap(resources map[string]*unstructured.Unstructured) map[string]interface{} {
	result := make(map[string]interface{})
	for name, resource := range resources {
		if resource != nil {
			result[name] = resource.Object
		}
	}
	return result
}

