package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// K8sTaskRunner implements the hf:k8s task for Kubernetes resource management.
// It handles create/update/recreate/skip operations based on discovery and generation tracking.
type K8sTaskRunner struct {
	k8sClient k8s_client.K8sClient
	log       logger.Logger
}

// NewK8sTaskRunner creates a new Kubernetes task runner.
func NewK8sTaskRunner(deps *Dependencies) (TaskRunner, error) {
	var k8sClient k8s_client.K8sClient
	var log logger.Logger

	if deps != nil {
		if deps.K8sClient != nil {
			var ok bool
			k8sClient, ok = deps.K8sClient.(k8s_client.K8sClient)
			if !ok {
				return nil, fmt.Errorf("invalid K8sClient type")
			}
		}
		if deps.Logger != nil {
			var ok bool
			log, ok = deps.Logger.(logger.Logger)
			if !ok {
				log = &noopLogger{}
			}
		} else {
			log = &noopLogger{}
		}
	} else {
		log = &noopLogger{}
	}

	return &K8sTaskRunner{
		k8sClient: k8sClient,
		log:       log,
	}, nil
}

func (r *K8sTaskRunner) Name() string {
	return TaskK8s
}

// Run executes a Kubernetes resource operation.
// Args should contain:
//   - name: Resource name (for tracking)
//   - manifest: The Kubernetes manifest (map with apiVersion, kind, metadata, spec)
//   - discovery: Optional discovery config (namespace, byName, bySelectors)
//   - recreateOnChange: Optional boolean to recreate instead of update
//
// Returns a map with:
//   - operation: The operation performed (create, update, recreate, skip)
//   - operationReason: Why this operation was chosen
//   - resource: The resulting resource object
//   - error: Error message if operation failed
func (r *K8sTaskRunner) Run(ctx context.Context, args map[string]any, input map[string]any) (map[string]any, error) {
	if r.k8sClient == nil {
		return nil, fmt.Errorf("Kubernetes client not configured")
	}

	name, _ := args["name"].(string)
	if name == "" {
		name = "unnamed"
	}

	output := make(map[string]any)
	for k, v := range input {
		output[k] = v
	}

	// Build manifest
	manifestData, ok := args["manifest"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("manifest is required for hf:k8s task")
	}

	// Render templates in manifest
	params, _ := input["params"].(map[string]any)
	if params == nil {
		params = make(map[string]any)
	}

	renderedManifest, err := renderManifestTemplates(manifestData, params)
	if err != nil {
		output["error"] = fmt.Sprintf("failed to render manifest: %v", err)
		return output, nil
	}

	manifest := &unstructured.Unstructured{Object: renderedManifest}

	// Validate manifest
	if manifest.GetAPIVersion() == "" {
		output["error"] = "manifest missing apiVersion"
		return output, nil
	}
	if manifest.GetKind() == "" {
		output["error"] = "manifest missing kind"
		return output, nil
	}
	if manifest.GetName() == "" {
		output["error"] = "manifest missing metadata.name"
		return output, nil
	}

	gvk := manifest.GroupVersionKind()
	recreateOnChange, _ := args["recreateOnChange"].(bool)

	r.log.Infof(ctx, "K8s[%s] processing: %s/%s %s", name, manifest.GetNamespace(), manifest.GetName(), gvk.Kind)

	// Discovery
	var existingResource *unstructured.Unstructured
	if discoveryConfig, ok := args["discovery"].(map[string]any); ok {
		existingResource, err = r.discoverResource(ctx, gvk, discoveryConfig, params)
		if err != nil && !apierrors.IsNotFound(err) {
			output["error"] = fmt.Sprintf("discovery failed: %v", err)
			return output, nil
		}
	}

	// Determine operation
	var operation string
	var operationReason string
	var resultResource *unstructured.Unstructured

	manifestGen := k8s_client.GetGenerationAnnotation(manifest)

	if existingResource != nil {
		existingGen := k8s_client.GetGenerationAnnotation(existingResource)

		if existingGen == manifestGen {
			operation = "skip"
			operationReason = fmt.Sprintf("generation %d unchanged", existingGen)
			resultResource = existingResource
		} else if recreateOnChange {
			operation = "recreate"
			operationReason = fmt.Sprintf("generation changed %d->%d, recreateOnChange=true", existingGen, manifestGen)
		} else {
			operation = "update"
			operationReason = fmt.Sprintf("generation changed %d->%d", existingGen, manifestGen)
		}
	} else {
		operation = "create"
		operationReason = "resource not found"
	}

	r.log.Infof(ctx, "K8s[%s] operation=%s reason=%s", name, operation, operationReason)

	// Execute operation
	switch operation {
	case "create":
		resultResource, err = r.k8sClient.CreateResource(ctx, manifest)
	case "update":
		manifest.SetResourceVersion(existingResource.GetResourceVersion())
		manifest.SetUID(existingResource.GetUID())
		resultResource, err = r.k8sClient.UpdateResource(ctx, manifest)
	case "recreate":
		resultResource, err = r.recreateResource(ctx, existingResource, manifest)
	case "skip":
		// Already set above
	}

	if err != nil {
		output["error"] = err.Error()
		output["operation"] = operation
		output["operationReason"] = operationReason
		return output, nil
	}

	output["operation"] = operation
	output["operationReason"] = operationReason
	output["success"] = true

	if resultResource != nil {
		output["resource"] = resultResource.Object
		output["resourceName"] = resultResource.GetName()
		output["resourceNamespace"] = resultResource.GetNamespace()
		output["resourceKind"] = gvk.Kind

		// Store in resources map
		if resources, ok := output["resources"].(map[string]any); ok {
			resources[name] = resultResource.Object
		} else {
			output["resources"] = map[string]any{
				name: resultResource.Object,
			}
		}
	}

	return output, nil
}

// discoverResource discovers an existing resource using the discovery config.
func (r *K8sTaskRunner) discoverResource(ctx context.Context, gvk schema.GroupVersionKind, discoveryConfig map[string]any, params map[string]any) (*unstructured.Unstructured, error) {
	namespace, _ := discoveryConfig["namespace"].(string)
	if namespace != "" {
		// Render namespace template
		rendered, err := RenderTemplate(namespace, params)
		if err == nil {
			namespace = rendered
		}
	}

	// Discovery by name
	if byName, ok := discoveryConfig["byName"].(string); ok && byName != "" {
		name, err := RenderTemplate(byName, params)
		if err != nil {
			return nil, fmt.Errorf("failed to render byName: %w", err)
		}
		return r.k8sClient.GetResource(ctx, gvk, namespace, name)
	}

	// Discovery by label selector
	if bySelectors, ok := discoveryConfig["bySelectors"].(map[string]any); ok {
		if labelSelector, ok := bySelectors["labelSelector"].(map[string]any); ok {
			// Render label values
			renderedLabels := make(map[string]string)
			for k, v := range labelSelector {
				key, _ := k, v.(string)
				val, _ := v.(string)
				renderedKey, _ := RenderTemplate(key, params)
				renderedVal, _ := RenderTemplate(val, params)
				renderedLabels[renderedKey] = renderedVal
			}

			selectorStr := k8s_client.BuildLabelSelector(renderedLabels)
			config := &k8s_client.DiscoveryConfig{
				Namespace:     namespace,
				LabelSelector: selectorStr,
			}

			list, err := r.k8sClient.DiscoverResources(ctx, gvk, config)
			if err != nil {
				return nil, err
			}

			if len(list.Items) == 0 {
				return nil, apierrors.NewNotFound(schema.GroupResource{Group: gvk.Group, Resource: gvk.Kind}, "")
			}

			return k8s_client.GetLatestGenerationResource(list), nil
		}
	}

	return nil, nil
}

// recreateResource deletes and recreates a resource.
func (r *K8sTaskRunner) recreateResource(ctx context.Context, existing, manifest *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	gvk := existing.GroupVersionKind()
	namespace := existing.GetNamespace()
	name := existing.GetName()

	// Delete
	if err := r.k8sClient.DeleteResource(ctx, gvk, namespace, name); err != nil {
		return nil, fmt.Errorf("failed to delete for recreation: %w", err)
	}

	// Wait for deletion
	if err := r.waitForDeletion(ctx, gvk, namespace, name); err != nil {
		return nil, fmt.Errorf("failed waiting for deletion: %w", err)
	}

	// Create
	return r.k8sClient.CreateResource(ctx, manifest)
}

// waitForDeletion polls until the resource is deleted.
func (r *K8sTaskRunner) waitForDeletion(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) error {
	const pollInterval = 100 * time.Millisecond

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled: %w", ctx.Err())
		case <-ticker.C:
			_, err := r.k8sClient.GetResource(ctx, gvk, namespace, name)
			if apierrors.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("error checking deletion: %w", err)
			}
		}
	}
}

// renderManifestTemplates recursively renders Go templates in a manifest.
func renderManifestTemplates(data map[string]any, params map[string]any) (map[string]any, error) {
	result := make(map[string]any)

	for k, v := range data {
		renderedKey, err := RenderTemplate(k, params)
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

func renderValue(v any, params map[string]any) (any, error) {
	switch val := v.(type) {
	case string:
		return RenderTemplate(val, params)
	case map[string]any:
		return renderManifestTemplates(val, params)
	case []any:
		result := make([]any, len(val))
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

// ResourcesTaskRunner implements the hf:resources task.
// It manages multiple Kubernetes resources in sequence.
type ResourcesTaskRunner struct {
	k8sClient k8s_client.K8sClient
	log       logger.Logger
}

// NewResourcesTaskRunner creates a new resources task runner.
func NewResourcesTaskRunner(deps *Dependencies) (TaskRunner, error) {
	var k8sClient k8s_client.K8sClient
	var log logger.Logger

	if deps != nil {
		if deps.K8sClient != nil {
			var ok bool
			k8sClient, ok = deps.K8sClient.(k8s_client.K8sClient)
			if !ok {
				return nil, fmt.Errorf("invalid K8sClient type")
			}
		}
		if deps.Logger != nil {
			var ok bool
			log, ok = deps.Logger.(logger.Logger)
			if !ok {
				log = &noopLogger{}
			}
		} else {
			log = &noopLogger{}
		}
	} else {
		log = &noopLogger{}
	}

	return &ResourcesTaskRunner{
		k8sClient: k8sClient,
		log:       log,
	}, nil
}

func (r *ResourcesTaskRunner) Name() string {
	return TaskResources
}

// Run manages multiple Kubernetes resources.
// Args should contain:
//   - config: Array of resource configurations
//
// Returns a map with:
//   - results: Array of individual resource results
//   - success: Boolean indicating if all operations succeeded
func (r *ResourcesTaskRunner) Run(ctx context.Context, args map[string]any, input map[string]any) (map[string]any, error) {
	output := make(map[string]any)
	for k, v := range input {
		output[k] = v
	}

	resourceConfigs, ok := args["config"].([]any)
	if !ok || len(resourceConfigs) == 0 {
		output["results"] = []any{}
		output["success"] = true
		return output, nil
	}

	results := make([]any, 0, len(resourceConfigs))
	allSuccess := true
	resources := make(map[string]any)

	// Create single resource runner
	singleRunner, err := NewK8sTaskRunner(&Dependencies{
		K8sClient: r.k8sClient,
		Logger:    r.log,
	})
	if err != nil {
		return nil, err
	}

	for _, resourceConfig := range resourceConfigs {
		resourceArgs, ok := resourceConfig.(map[string]any)
		if !ok {
			continue
		}

		result, err := singleRunner.Run(ctx, resourceArgs, input)
		if err != nil {
			allSuccess = false
			results = append(results, map[string]any{
				"name":  resourceArgs["name"],
				"error": err.Error(),
			})
			break
		}

		name, _ := resourceArgs["name"].(string)
		results = append(results, map[string]any{
			"name":            name,
			"operation":       result["operation"],
			"operationReason": result["operationReason"],
			"success":         result["success"],
		})

		// Collect resources
		if resource, ok := result["resource"].(map[string]any); ok && name != "" {
			resources[name] = resource
		}

		if success, ok := result["success"].(bool); !ok || !success {
			allSuccess = false
			break
		}
	}

	output["results"] = results
	output["success"] = allSuccess
	output["resources"] = resources

	return output, nil
}

func init() {
	_ = RegisterDefault(TaskK8s, NewK8sTaskRunner)
	_ = RegisterDefault(TaskResources, NewResourcesTaskRunner)
}
