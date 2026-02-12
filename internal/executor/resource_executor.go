package executor

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mitchellh/copystructure"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/maestro_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/manifest"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/transport_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ResourceExecutor creates and updates Kubernetes resources
type ResourceExecutor struct {
	client transport_client.TransportClient
	log    logger.Logger
}

// newResourceExecutor creates a new resource executor
// NOTE: Caller (NewExecutor) is responsible for config validation
func newResourceExecutor(config *ExecutorConfig) *ResourceExecutor {
	return &ResourceExecutor{
		client: config.TransportClient,
		log:    config.Logger,
	}
}

// ExecuteAll creates/updates all resources in sequence
// Returns results for each resource and updates the execution context
func (re *ResourceExecutor) ExecuteAll(ctx context.Context, resources []config_loader.Resource, execCtx *ExecutionContext) ([]ResourceResult, error) {
	if execCtx.Resources == nil {
		execCtx.Resources = make(map[string]interface{})
	}
	results := make([]ResourceResult, 0, len(resources))

	for _, resource := range resources {
		result, err := re.executeResource(ctx, resource, execCtx)
		results = append(results, result)

		if err != nil {
			return results, err
		}
	}

	return results, nil
}

// executeResource creates or updates a single resource via the transport client.
// For k8s transport: renders manifest template → marshals to JSON → calls ApplyResource(bytes)
// For maestro transport: renders manifestWork template → marshals to JSON → calls ApplyResource(bytes)
func (re *ResourceExecutor) executeResource(ctx context.Context, resource config_loader.Resource, execCtx *ExecutionContext) (ResourceResult, error) {
	result := ResourceResult{
		Name:   resource.Name,
		Status: StatusSuccess,
	}

	transportClient := re.client
	if transportClient == nil {
		result.Status = StatusFailed
		result.Error = fmt.Errorf("transport client not configured for %s", resource.GetTransportClient())
		return result, NewExecutorError(PhaseResources, resource.Name, "transport client not configured", result.Error)
	}

	// Step 1: Render the manifest/manifestWork to bytes
	re.log.Debugf(ctx, "Rendering manifest template for resource %s", resource.Name)
	renderedBytes, err := re.renderToBytes(ctx, resource, execCtx)
	if err != nil {
		result.Status = StatusFailed
		result.Error = err
		return result, NewExecutorError(PhaseResources, resource.Name, "failed to render manifest", err)
	}

	// Step 2: Prepare apply options
	var applyOpts *transport_client.ApplyOptions
	if resource.RecreateOnChange {
		applyOpts = &transport_client.ApplyOptions{RecreateOnChange: true}
	}

	// Step 3: Build transport context (nil for k8s, *maestro_client.TransportContext for maestro)
	var transportTarget transport_client.TransportContext
	if resource.IsMaestroTransport() && resource.Transport.Maestro != nil {
		targetCluster, tplErr := renderTemplate(resource.Transport.Maestro.TargetCluster, execCtx.Params)
		if tplErr != nil {
			result.Status = StatusFailed
			result.Error = tplErr
			return result, NewExecutorError(PhaseResources, resource.Name, "failed to render targetCluster template", tplErr)
		}
		transportTarget = &maestro_client.TransportContext{
			ConsumerName: targetCluster,
		}
	}

	// Step 4: Call transport client ApplyResource with rendered bytes
	applyResult, err := transportClient.ApplyResource(ctx, renderedBytes, applyOpts, transportTarget)
	if err != nil {
		result.Status = StatusFailed
		result.Error = err
		execCtx.Adapter.ExecutionError = &ExecutionError{
			Phase:   string(PhaseResources),
			Step:    resource.Name,
			Message: err.Error(),
		}
		errCtx := logger.WithK8sResult(ctx, "FAILED")
		errCtx = logger.WithErrorField(errCtx, err)
		re.log.Errorf(errCtx, "Resource[%s] processed: FAILED", resource.Name)
		return result, NewExecutorError(PhaseResources, resource.Name, "failed to apply resource", err)
	}

	// Step 5: Extract result
	result.Operation = applyResult.Operation
	result.OperationReason = applyResult.Reason

	successCtx := logger.WithK8sResult(ctx, "SUCCESS")
	re.log.Infof(successCtx, "Resource[%s] processed: operation=%s reason=%s",
		resource.Name, result.Operation, result.OperationReason)

	// Step 6: Post-apply discovery — find the applied resource and store in execCtx for CEL evaluation
	if resource.Discovery != nil {
		discovered, discoverErr := re.discoverResource(ctx, resource, execCtx, transportTarget)
		if discoverErr != nil {
			re.log.Warnf(ctx, "Resource[%s] discovery after apply failed: %v", resource.Name, discoverErr)
		} else if discovered != nil {
			// Step 7: Nested discoveries — find sub-resources within the discovered parent (e.g., ManifestWork)
			if len(resource.NestedDiscoveries) > 0 {
				nestedResults := re.discoverNestedResources(ctx, resource, execCtx, discovered)
				execCtx.Resources[resource.Name] = nestedResults
				re.log.Debugf(ctx, "Resource[%s] discovered with %d nested resources", resource.Name, len(nestedResults))
			} else {
				execCtx.Resources[resource.Name] = discovered
				re.log.Debugf(ctx, "Resource[%s] discovered and stored in context", resource.Name)
			}
		}
	}

	return result, nil
}

// renderToBytes renders the resource's manifest template to JSON bytes.
// The manifest holds either a K8s resource or a ManifestWork depending on transport type.
func (re *ResourceExecutor) renderToBytes(ctx context.Context, resource config_loader.Resource, execCtx *ExecutionContext) ([]byte, error) {
	if resource.Manifest == nil {
		return nil, fmt.Errorf("no manifest specified for resource %s", resource.Name)
	}

	manifestSource := resource.Manifest

	// Convert to map[string]interface{}
	var manifestData map[string]interface{}
	switch m := manifestSource.(type) {
	case map[string]interface{}:
		manifestData = m
	case map[interface{}]interface{}:
		manifestData = convertToStringKeyMap(m)
	default:
		return nil, fmt.Errorf("unsupported manifest type: %T", manifestSource)
	}

	// Deep copy to avoid modifying the original
	manifestData = deepCopyMap(ctx, manifestData, re.log)

	// Render all template strings in the manifest
	renderedData, err := renderManifestTemplates(manifestData, execCtx.Params)
	if err != nil {
		return nil, fmt.Errorf("failed to render manifest templates: %w", err)
	}

	// Marshal to JSON bytes
	data, err := json.Marshal(renderedData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal rendered manifest: %w", err)
	}

	return data, nil
}

// discoverResource discovers the applied resource using the discovery config.
// For k8s transport: discovers the K8s resource by name or label selector.
// For maestro transport: discovers the ManifestWork by name or label selector.
// The discovered resource is stored in execCtx.Resources for post-action CEL evaluation.
func (re *ResourceExecutor) discoverResource(ctx context.Context, resource config_loader.Resource, execCtx *ExecutionContext, transportTarget transport_client.TransportContext) (*unstructured.Unstructured, error) {
	discovery := resource.Discovery
	if discovery == nil {
		return nil, nil
	}

	// Render discovery namespace template
	namespace, err := renderTemplate(discovery.Namespace, execCtx.Params)
	if err != nil {
		return nil, fmt.Errorf("failed to render namespace template: %w", err)
	}

	// Discover by name
	if discovery.ByName != "" {
		name, err := renderTemplate(discovery.ByName, execCtx.Params)
		if err != nil {
			return nil, fmt.Errorf("failed to render byName template: %w", err)
		}

		// For maestro: use ManifestWork GVK
		// For k8s: parse the rendered manifest to get GVK
		gvk := re.resolveGVK(resource)

		return re.client.GetResource(ctx, gvk, namespace, name, transportTarget)
	}

	// Discover by label selector
	if discovery.BySelectors != nil && len(discovery.BySelectors.LabelSelector) > 0 {
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

		labelSelector := manifest.BuildLabelSelector(renderedLabels)
		discoveryConfig := &manifest.DiscoveryConfig{
			Namespace:     namespace,
			LabelSelector: labelSelector,
		}

		gvk := re.resolveGVK(resource)

		list, err := re.client.DiscoverResources(ctx, gvk, discoveryConfig, transportTarget)
		if err != nil {
			return nil, err
		}

		if len(list.Items) == 0 {
			return nil, apierrors.NewNotFound(schema.GroupResource{Group: gvk.Group, Resource: gvk.Kind}, "")
		}

		return manifest.GetLatestGenerationFromList(list), nil
	}

	return nil, fmt.Errorf("discovery config must specify byName or bySelectors")
}

// discoverNestedResources discovers sub-resources within a parent resource (e.g., manifests inside a ManifestWork).
// Each nestedDiscovery is matched against the parent's nested manifests using manifest.DiscoverNestedManifest.
func (re *ResourceExecutor) discoverNestedResources(
	ctx context.Context,
	resource config_loader.Resource,
	execCtx *ExecutionContext,
	parent *unstructured.Unstructured,
) map[string]*unstructured.Unstructured {
	nestedResults := make(map[string]*unstructured.Unstructured)

	for _, nd := range resource.NestedDiscoveries {
		if nd.Discovery == nil {
			continue
		}

		// Build discovery config with rendered templates
		discoveryConfig, err := re.buildNestedDiscoveryConfig(nd.Discovery, execCtx.Params)
		if err != nil {
			re.log.Warnf(ctx, "Resource[%s] nested discovery[%s] failed to build config: %v",
				resource.Name, nd.Name, err)
			continue
		}

		// Search within the parent resource
		list, err := manifest.DiscoverNestedManifest(parent, discoveryConfig)
		if err != nil {
			re.log.Warnf(ctx, "Resource[%s] nested discovery[%s] failed: %v",
				resource.Name, nd.Name, err)
			continue
		}

		if len(list.Items) == 0 {
			re.log.Debugf(ctx, "Resource[%s] nested discovery[%s] found no matches",
				resource.Name, nd.Name)
			continue
		}

		// Use the latest generation match
		best := manifest.GetLatestGenerationFromList(list)
		if best != nil {
			nestedResults[nd.Name] = best
			re.log.Debugf(ctx, "Resource[%s] nested discovery[%s] found: %s/%s",
				resource.Name, nd.Name, best.GetKind(), best.GetName())
		}
	}

	return nestedResults
}

// buildNestedDiscoveryConfig renders templates in a discovery config and returns a manifest.DiscoveryConfig.
func (re *ResourceExecutor) buildNestedDiscoveryConfig(
	discovery *config_loader.DiscoveryConfig,
	params map[string]interface{},
) (*manifest.DiscoveryConfig, error) {
	namespace, err := renderTemplate(discovery.Namespace, params)
	if err != nil {
		return nil, fmt.Errorf("failed to render namespace template: %w", err)
	}

	if discovery.ByName != "" {
		name, err := renderTemplate(discovery.ByName, params)
		if err != nil {
			return nil, fmt.Errorf("failed to render byName template: %w", err)
		}
		return &manifest.DiscoveryConfig{
			Namespace: namespace,
			ByName:    name,
		}, nil
	}

	if discovery.BySelectors != nil && len(discovery.BySelectors.LabelSelector) > 0 {
		renderedLabels := make(map[string]string)
		for k, v := range discovery.BySelectors.LabelSelector {
			renderedK, err := renderTemplate(k, params)
			if err != nil {
				return nil, fmt.Errorf("failed to render label key template: %w", err)
			}
			renderedV, err := renderTemplate(v, params)
			if err != nil {
				return nil, fmt.Errorf("failed to render label value template: %w", err)
			}
			renderedLabels[renderedK] = renderedV
		}
		return &manifest.DiscoveryConfig{
			Namespace:     namespace,
			LabelSelector: manifest.BuildLabelSelector(renderedLabels),
		}, nil
	}

	return nil, fmt.Errorf("discovery must specify byName or bySelectors")
}

// resolveGVK extracts the GVK from the resource's manifest.
// Works for both K8s resources and ManifestWorks since both have apiVersion and kind.
func (re *ResourceExecutor) resolveGVK(resource config_loader.Resource) schema.GroupVersionKind {
	manifestData, ok := resource.Manifest.(map[string]interface{})
	if !ok {
		return schema.GroupVersionKind{}
	}
	apiVersion, ok1 := manifestData["apiVersion"].(string)
	kind, ok2 := manifestData["kind"].(string)
	if !ok1 || !ok2 {
		return schema.GroupVersionKind{}
	}
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return schema.GroupVersionKind{}
	}
	return gv.WithKind(kind)
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
func deepCopyMap(ctx context.Context, m map[string]interface{}, log logger.Logger) map[string]interface{} {
	if m == nil {
		return nil
	}

	copied, err := copystructure.Copy(m)
	if err != nil {
		// Fallback to shallow copy if deep copy fails
		log.Warnf(ctx, "Failed to deep copy map: %v. Falling back to shallow copy.", err)
		result := make(map[string]interface{})
		for k, v := range m {
			result[k] = v
		}
		return result
	}

	result, ok := copied.(map[string]interface{})
	if !ok {
		// Should not happen, but handle gracefully
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
