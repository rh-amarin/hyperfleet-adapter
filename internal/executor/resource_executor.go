package executor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mitchellh/copystructure"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/generation"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/maestro_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/constants"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	workv1 "open-cluster-management.io/api/work/v1"
	"sigs.k8s.io/yaml"
)

// ResourceExecutor creates and updates Kubernetes resources
type ResourceExecutor struct {
	k8sClient     k8s_client.K8sClient
	maestroClient maestro_client.ManifestWorkClient
	log           logger.Logger
}

// newResourceExecutor creates a new resource executor
// NOTE: Caller (NewExecutor) is responsible for config validation
func newResourceExecutor(config *ExecutorConfig) *ResourceExecutor {
	return &ResourceExecutor{
		k8sClient:     config.K8sClient,
		maestroClient: config.MaestroClient,
		log:           config.Logger,
	}
}

// ExecuteAll creates/updates all resources in sequence
// Returns results for each resource and updates the execution context
func (re *ResourceExecutor) ExecuteAll(ctx context.Context, resources []config_loader.Resource, execCtx *ExecutionContext) ([]ResourceResult, error) {
	if execCtx.Resources == nil {
		execCtx.Resources = make(map[string]*unstructured.Unstructured)
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

// executeResource creates or updates a single Kubernetes resource
func (re *ResourceExecutor) executeResource(ctx context.Context, resource config_loader.Resource, execCtx *ExecutionContext) (ResourceResult, error) {
	result := ResourceResult{
		Name:   resource.Name,
		Status: StatusSuccess,
	}

	// Route based on transport type
	clientType := resource.Transport.GetClientType()

	switch clientType {
	case config_loader.TransportClientKubernetes:
		// Build single manifest for Kubernetes transport
		re.log.Debugf(ctx, "Building manifest from config for Kubernetes transport")
		manifest, err := re.buildManifestK8s(ctx, resource, execCtx)
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

		// Add K8s resource context fields for logging
		ctx = logger.WithK8sKind(ctx, result.Kind)
		ctx = logger.WithK8sName(ctx, result.ResourceName)
		ctx = logger.WithK8sNamespace(ctx, result.Namespace)

		re.log.Debugf(ctx, "Resource[%s] manifest built: namespace=%s", resource.Name, manifest.GetNamespace())
		return re.applyResourceK8s(ctx, resource, manifest, execCtx)

	case config_loader.TransportClientMaestro:
		// Build multiple manifests for Maestro transport
		re.log.Debugf(ctx, "Building manifests from config for Maestro transport")
		manifests, err := re.buildManifestsMaestro(ctx, resource, execCtx)
		if err != nil {
			result.Status = StatusFailed
			result.Error = err
			return result, NewExecutorError(PhaseResources, resource.Name, "failed to build manifests", err)
		}

		re.log.Debugf(ctx, "Resource[%s] built %d manifests for ManifestWork", resource.Name, len(manifests))
		return re.applyResourceMaestro(ctx, resource, manifests, execCtx)

	default:
		result.Status = StatusFailed
		result.Error = fmt.Errorf("unsupported transport client: %s", clientType)
		return result, NewExecutorError(PhaseResources, resource.Name, "unsupported transport client", result.Error)
	}
}

// applyResourceK8s handles resource discovery, generation comparison, and execution of operations via Kubernetes API.
// It discovers existing resources (via Discovery config or by name), compares generations,
// and performs the appropriate operation (create, update, recreate, or skip).
func (re *ResourceExecutor) applyResourceK8s(ctx context.Context, resource config_loader.Resource, manifest *unstructured.Unstructured, execCtx *ExecutionContext) (ResourceResult, error) {
	result := ResourceResult{
		Name:         resource.Name,
		Kind:         manifest.GetKind(),
		Namespace:    manifest.GetNamespace(),
		ResourceName: manifest.GetName(),
		Status:       StatusSuccess,
	}

	if re.k8sClient == nil {
		result.Status = StatusFailed
		result.Error = fmt.Errorf("kubernetes client not configured")
		return result, NewExecutorError(PhaseResources, resource.Name, "kubernetes client not configured", result.Error)
	}

	gvk := manifest.GroupVersionKind()

	// Discover existing resource
	var existingResource *unstructured.Unstructured
	var err error
	if resource.Discovery != nil {
		// Use Discovery config to find existing resource (e.g., by label selector)
		re.log.Debugf(ctx, "Discovering existing resource using discovery config...")
		existingResource, err = re.discoverExistingResource(ctx, gvk, resource.Discovery, execCtx)
	} else {
		// No Discovery config - lookup by name from manifest
		re.log.Debugf(ctx, "Looking up existing resource by name...")
		existingResource, err = re.k8sClient.GetResource(ctx, gvk, manifest.GetNamespace(), manifest.GetName())
	}

	// Fail fast on any error except NotFound (which means resource doesn't exist yet)
	if err != nil && !apierrors.IsNotFound(err) {
		result.Status = StatusFailed
		result.Error = err
		return result, NewExecutorError(PhaseResources, resource.Name, "failed to find existing resource", err)
	}

	if existingResource != nil {
		re.log.Debugf(ctx, "Existing resource found: %s/%s", existingResource.GetNamespace(), existingResource.GetName())
	} else {
		re.log.Debugf(ctx, "No existing resource found, will create")
	}

	// Extract manifest generation once for use in comparison and logging
	manifestGen := generation.GetGenerationFromUnstructured(manifest)

	// Add observed_generation to context early so it appears in all subsequent logs
	ctx = logger.WithObservedGeneration(ctx, manifestGen)

	// Get existing generation (0 if not found)
	var existingGen int64
	if existingResource != nil {
		existingGen = generation.GetGenerationFromUnstructured(existingResource)
	}

	// Compare generations to determine operation
	decision := generation.CompareGenerations(manifestGen, existingGen, existingResource != nil)

	// Handle recreateOnChange override
	result.Operation = decision.Operation
	result.OperationReason = decision.Reason
	if decision.Operation == generation.OperationUpdate && resource.RecreateOnChange {
		result.Operation = generation.OperationRecreate
		result.OperationReason = fmt.Sprintf("%s, recreateOnChange=true", decision.Reason)
	}

	// Log the operation decision
	re.log.Infof(ctx, "Resource[%s] is processing: operation=%s reason=%s",
		resource.Name, strings.ToUpper(string(result.Operation)), result.OperationReason)

	// Execute the operation
	switch result.Operation {
	case generation.OperationCreate:
		result.Resource, err = re.createResource(ctx, manifest)
	case generation.OperationUpdate:
		result.Resource, err = re.updateResource(ctx, existingResource, manifest)
	case generation.OperationRecreate:
		result.Resource, err = re.recreateResource(ctx, existingResource, manifest)
	case generation.OperationSkip:
		result.Resource = existingResource
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
		errCtx := logger.WithK8sResult(ctx, "FAILED")
		errCtx = logger.WithErrorField(errCtx, err)
		re.log.Errorf(errCtx, "Resource[%s] processed: operation=%s reason=%s",
			resource.Name, result.Operation, result.OperationReason)
		// Log the full manifest for debugging
		if manifestYAML, marshalErr := yaml.Marshal(manifest.Object); marshalErr == nil {
			re.log.Debugf(errCtx, "Resource[%s] failed manifest:\n%s", resource.Name, string(manifestYAML))
		}
		return result, NewExecutorError(PhaseResources, resource.Name,
			fmt.Sprintf("failed to %s resource", result.Operation), err)
	}
	successCtx := logger.WithK8sResult(ctx, "SUCCESS")
	re.log.Infof(successCtx, "Resource[%s] processed: operation=%s reason=%s",
		resource.Name, result.Operation, result.OperationReason)

	// Store resource in execution context
	if result.Resource != nil {
		execCtx.Resources[resource.Name] = result.Resource
		re.log.Debugf(ctx, "Resource stored in context as '%s'", resource.Name)
	}

	return result, nil
}

// applyResourceMaestro handles resource delivery via Maestro ManifestWork.
// It builds a ManifestWork containing all manifests and applies it to the target cluster.
func (re *ResourceExecutor) applyResourceMaestro(ctx context.Context, resource config_loader.Resource, manifests []*unstructured.Unstructured, execCtx *ExecutionContext) (ResourceResult, error) {
	result := ResourceResult{
		Name:   resource.Name,
		Status: StatusSuccess,
	}

	// Set result info from first manifest if available
	if len(manifests) > 0 {
		firstManifest := manifests[0]
		result.Kind = firstManifest.GetKind()
		result.Namespace = firstManifest.GetNamespace()
		result.ResourceName = firstManifest.GetName()
	}

	// Validate maestro client is configured
	if re.maestroClient == nil {
		result.Status = StatusFailed
		result.Error = fmt.Errorf("maestro client not configured")
		return result, NewExecutorError(PhaseResources, resource.Name, "maestro client not configured", result.Error)
	}

	// Validate maestro transport config is present
	if resource.Transport == nil || resource.Transport.Maestro == nil {
		result.Status = StatusFailed
		result.Error = fmt.Errorf("maestro transport configuration missing")
		return result, NewExecutorError(PhaseResources, resource.Name, "maestro transport configuration missing", result.Error)
	}

	maestroConfig := resource.Transport.Maestro

	// Render targetCluster template
	targetCluster, err := renderTemplate(maestroConfig.TargetCluster, execCtx.Params)
	if err != nil {
		result.Status = StatusFailed
		result.Error = fmt.Errorf("failed to render targetCluster template: %w", err)
		return result, NewExecutorError(PhaseResources, resource.Name, "failed to render targetCluster template", err)
	}

	re.log.Debugf(ctx, "Resource[%s] using Maestro transport to cluster=%s with %d manifests", resource.Name, targetCluster, len(manifests))

	// Build ManifestWork from manifests
	work, err := re.buildManifestWork(ctx, resource, manifests, execCtx)
	if err != nil {
		result.Status = StatusFailed
		result.Error = err
		return result, NewExecutorError(PhaseResources, resource.Name, "failed to build ManifestWork", err)
	}

	re.log.Infof(ctx, "Resource[%s] applying ManifestWork via Maestro: name=%s targetCluster=%s manifestCount=%d",
		resource.Name, work.Name, targetCluster, len(manifests))

	// Apply ManifestWork using maestro client
	appliedWork, err := re.maestroClient.ApplyManifestWork(ctx, targetCluster, work)
	if err != nil {
		result.Status = StatusFailed
		result.Error = err
		// Set ExecutionError for Maestro operation failure
		execCtx.Adapter.ExecutionError = &ExecutionError{
			Phase:   string(PhaseResources),
			Step:    resource.Name,
			Message: err.Error(),
		}
		errCtx := logger.WithK8sResult(ctx, "FAILED")
		errCtx = logger.WithErrorField(errCtx, err)
		re.log.Errorf(errCtx, "Resource[%s] ManifestWork apply failed: name=%s targetCluster=%s",
			resource.Name, work.Name, targetCluster)
		// Log the manifests for debugging
		for i, manifest := range manifests {
			if manifestYAML, marshalErr := yaml.Marshal(manifest.Object); marshalErr == nil {
				re.log.Debugf(errCtx, "Resource[%s] failed manifest[%d]:\n%s", resource.Name, i, string(manifestYAML))
			}
		}
		// Also log the ManifestWork for debugging
		if workYAML, marshalErr := yaml.Marshal(work); marshalErr == nil {
			re.log.Debugf(errCtx, "Resource[%s] failed ManifestWork:\n%s", resource.Name, string(workYAML))
		}
		return result, NewExecutorError(PhaseResources, resource.Name, "failed to apply ManifestWork", err)
	}

	// Set operation info
	result.Operation = generation.OperationCreate // ManifestWork apply is always an upsert
	result.OperationReason = fmt.Sprintf("ManifestWork applied to cluster %s with %d manifests", targetCluster, len(manifests))

	successCtx := logger.WithK8sResult(ctx, "SUCCESS")
	re.log.Infof(successCtx, "Resource[%s] ManifestWork applied: name=%s targetCluster=%s resourceVersion=%s manifestCount=%d",
		resource.Name, appliedWork.Name, targetCluster, appliedWork.ResourceVersion, len(manifests))

	// Store each manifest by compound name (resource.manifestName)
	for i, manifest := range manifests {
		if i < len(resource.Manifests) {
			manifestName := resource.Manifests[i].Name
			key := resource.Name + "." + manifestName
			execCtx.Resources[key] = manifest
			re.log.Debugf(ctx, "Resource stored in context as '%s'", key)
		}
	}
	// Store first manifest under resource name for convenience
	if len(manifests) > 0 {
		execCtx.Resources[resource.Name] = manifests[0]
		re.log.Debugf(ctx, "First manifest also stored in context as '%s'", resource.Name)
	}

	return result, nil
}

// buildManifestWork creates a ManifestWork containing the given manifests
func (re *ResourceExecutor) buildManifestWork(ctx context.Context, resource config_loader.Resource, manifests []*unstructured.Unstructured, execCtx *ExecutionContext) (*workv1.ManifestWork, error) {
	maestroConfig := resource.Transport.Maestro

	re.log.Debugf(ctx, "Building ManifestWork for resource[%s] with %d manifests: manifestWork=%v", resource.Name, len(manifests), maestroConfig.ManifestWork)

	// Determine ManifestWork name
	workName := ""
	if maestroConfig.ManifestWork != nil && maestroConfig.ManifestWork.Name != "" {
		// Use configured name (with template rendering)
		var err error
		workName, err = renderTemplate(maestroConfig.ManifestWork.Name, execCtx.Params)
		if err != nil {
			return nil, fmt.Errorf("failed to render manifestWork.name template: %w", err)
		}
		re.log.Debugf(ctx, "Using configured ManifestWork name: %s", workName)
	} else if len(manifests) > 0 {
		// Generate name from resource name and first manifest name
		workName = fmt.Sprintf("%s-%s", resource.Name, manifests[0].GetName())
		re.log.Debugf(ctx, "Generated ManifestWork name: %s", workName)
	} else {
		workName = resource.Name
		re.log.Debugf(ctx, "Using resource name as ManifestWork name: %s", workName)
	}

	// Build manifests array for ManifestWork
	manifestEntries := make([]workv1.Manifest, len(manifests))
	for i, manifest := range manifests {
		manifestBytes, err := manifest.MarshalJSON()
		if err != nil {
			return nil, fmt.Errorf("failed to marshal manifest[%d]: %w", i, err)
		}
		manifestEntries[i] = workv1.Manifest{
			RawExtension: runtime.RawExtension{Raw: manifestBytes},
		}
	}

	work := &workv1.ManifestWork{
		Spec: workv1.ManifestWorkSpec{
			Workload: workv1.ManifestsTemplate{
				Manifests: manifestEntries,
			},
		},
	}
	work.SetName(workName)

	// Copy the generation annotation from the first manifest to the ManifestWork
	// This is required by the maestro client for generation-based idempotency
	if len(manifests) > 0 {
		manifestAnnotations := manifests[0].GetAnnotations()
		if manifestAnnotations != nil {
			if gen, ok := manifestAnnotations[constants.AnnotationGeneration]; ok {
				work.SetAnnotations(map[string]string{
					constants.AnnotationGeneration: gen,
				})
				re.log.Debugf(ctx, "Set ManifestWork generation annotation: %s", gen)
			}
		}
	}

	// Apply any additional settings from manifestWork.refContent if present
	if maestroConfig.ManifestWork != nil && maestroConfig.ManifestWork.RefContent != nil {
		re.log.Debugf(ctx, "Applying ManifestWork settings from RefContent: %v", maestroConfig.ManifestWork.RefContent)
		if err := re.applyManifestWorkSettings(ctx, work, maestroConfig.ManifestWork.RefContent, execCtx.Params); err != nil {
			return nil, fmt.Errorf("failed to apply manifestWork settings: %w", err)
		}
	} else if maestroConfig.ManifestWork != nil {
		re.log.Debugf(ctx, "ManifestWork config present but RefContent is nil (Ref=%s)", maestroConfig.ManifestWork.Ref)
	}

	return work, nil
}

// applyManifestWorkSettings applies settings from the manifestWork ref file to the ManifestWork.
// The ref file can contain metadata (labels, annotations) and spec fields.
// Template variables in string values are rendered using the provided params.
func (re *ResourceExecutor) applyManifestWorkSettings(ctx context.Context, work *workv1.ManifestWork, settings map[string]interface{}, params map[string]interface{}) error {
	re.log.Debugf(ctx, "Applying ManifestWork settings: keys=%v", getMapKeys(settings))

	// Apply metadata if present
	if metadata, ok := settings["metadata"].(map[string]interface{}); ok {
		re.log.Debugf(ctx, "Found metadata in settings: %v", metadata)

		// Apply labels from metadata
		if labels, ok := metadata["labels"].(map[string]interface{}); ok {
			labelMap := make(map[string]string)
			for k, v := range labels {
				if str, ok := v.(string); ok {
					rendered, err := renderTemplate(str, params)
					if err != nil {
						return fmt.Errorf("failed to render label value for key %s: %w", k, err)
					}
					labelMap[k] = rendered
				}
			}
			work.SetLabels(labelMap)
			re.log.Debugf(ctx, "Applied labels: %v", labelMap)
		}

		// Apply annotations from metadata
		if annotations, ok := metadata["annotations"].(map[string]interface{}); ok {
			annotationMap := make(map[string]string)
			for k, v := range annotations {
				if str, ok := v.(string); ok {
					rendered, err := renderTemplate(str, params)
					if err != nil {
						return fmt.Errorf("failed to render annotation value for key %s: %w", k, err)
					}
					annotationMap[k] = rendered
				}
			}
			work.SetAnnotations(annotationMap)
			re.log.Debugf(ctx, "Applied annotations: %v", annotationMap)
		}
	}

	// Also check for labels/annotations at root level (backwards compatibility)
	if labels, ok := settings["labels"].(map[string]interface{}); ok {
		labelMap := make(map[string]string)
		for k, v := range labels {
			if str, ok := v.(string); ok {
				rendered, err := renderTemplate(str, params)
				if err != nil {
					return fmt.Errorf("failed to render label value for key %s: %w", k, err)
				}
				labelMap[k] = rendered
			}
		}
		// Merge with existing labels
		existing := work.GetLabels()
		if existing == nil {
			existing = make(map[string]string)
		}
		for k, v := range labelMap {
			existing[k] = v
		}
		work.SetLabels(existing)
	}

	if annotations, ok := settings["annotations"].(map[string]interface{}); ok {
		annotationMap := make(map[string]string)
		for k, v := range annotations {
			if str, ok := v.(string); ok {
				rendered, err := renderTemplate(str, params)
				if err != nil {
					return fmt.Errorf("failed to render annotation value for key %s: %w", k, err)
				}
				annotationMap[k] = rendered
			}
		}
		// Merge with existing annotations
		existing := work.GetAnnotations()
		if existing == nil {
			existing = make(map[string]string)
		}
		for k, v := range annotationMap {
			existing[k] = v
		}
		work.SetAnnotations(existing)
	}

	// Apply spec fields if present
	if spec, ok := settings["spec"].(map[string]interface{}); ok {
		re.log.Debugf(ctx, "Found spec in settings: keys=%v", getMapKeys(spec))

		// Apply deleteOption if present
		if deleteOption, ok := spec["deleteOption"].(map[string]interface{}); ok {
			if propagationPolicy, ok := deleteOption["propagationPolicy"].(string); ok {
				work.Spec.DeleteOption = &workv1.DeleteOption{
					PropagationPolicy: workv1.DeletePropagationPolicyType(propagationPolicy),
				}
				re.log.Debugf(ctx, "Applied deleteOption.propagationPolicy: %s", propagationPolicy)
			}
		}

		// Note: manifestConfigs and other complex spec fields would need
		// proper type conversion. For now, we handle the most common cases.
		// Additional spec fields can be added as needed.
	}

	return nil
}

// getMapKeys returns the keys of a map for debugging
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// buildManifestK8s builds an unstructured manifest from the resource configuration for Kubernetes transport
func (re *ResourceExecutor) buildManifestK8s(ctx context.Context, resource config_loader.Resource, execCtx *ExecutionContext) (*unstructured.Unstructured, error) {
	var manifestData map[string]interface{}

	// Get manifest (inline or loaded from ref)
	if resource.Manifest != nil {
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
	manifestData = deepCopyMap(ctx, manifestData, re.log)

	// Render all template strings in the manifest
	renderedData, err := renderManifestTemplates(manifestData, execCtx.Params)
	if err != nil {
		return nil, fmt.Errorf("failed to render manifest templates: %w", err)
	}

	// Convert to unstructured
	obj := &unstructured.Unstructured{Object: renderedData}

	// Validate manifest
	if err := validateManifest(obj); err != nil {
		return nil, err
	}

	return obj, nil
}

// buildManifestsMaestro builds unstructured manifests from the resource.Manifests array for Maestro transport
func (re *ResourceExecutor) buildManifestsMaestro(ctx context.Context, resource config_loader.Resource, execCtx *ExecutionContext) ([]*unstructured.Unstructured, error) {
	results := make([]*unstructured.Unstructured, 0, len(resource.Manifests))

	for i, nm := range resource.Manifests {
		content := nm.GetManifestContent()
		if content == nil {
			return nil, fmt.Errorf("manifest[%d] (%s) has no content", i, nm.Name)
		}

		var manifestData map[string]interface{}
		switch m := content.(type) {
		case map[string]interface{}:
			manifestData = m
		case map[interface{}]interface{}:
			manifestData = convertToStringKeyMap(m)
		default:
			return nil, fmt.Errorf("manifest[%d] (%s): unsupported manifest type: %T", i, nm.Name, content)
		}

		// Deep copy to avoid modifying the original
		manifestData = deepCopyMap(ctx, manifestData, re.log)

		// Render all template strings in the manifest
		renderedData, err := renderManifestTemplates(manifestData, execCtx.Params)
		if err != nil {
			return nil, fmt.Errorf("manifest[%d] (%s): failed to render templates: %w", i, nm.Name, err)
		}

		// Convert to unstructured
		obj := &unstructured.Unstructured{Object: renderedData}

		// Validate manifest
		if err := validateManifest(obj); err != nil {
			return nil, fmt.Errorf("manifest[%d] (%s): %w", i, nm.Name, err)
		}

		results = append(results, obj)
	}

	return results, nil
}

// validateManifest validates a Kubernetes manifest has all required fields and annotations
func validateManifest(obj *unstructured.Unstructured) error {
	// Validate required Kubernetes fields
	if obj.GetAPIVersion() == "" {
		return fmt.Errorf("manifest missing apiVersion")
	}
	if obj.GetKind() == "" {
		return fmt.Errorf("manifest missing kind")
	}
	if obj.GetName() == "" {
		return fmt.Errorf("manifest missing metadata.name")
	}

	// Validate required generation annotation
	if generation.GetGenerationFromUnstructured(obj) == 0 {
		return fmt.Errorf("manifest missing required annotation %q", constants.AnnotationGeneration)
	}

	return nil
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

		return generation.GetLatestGenerationFromList(list), nil
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
func (re *ResourceExecutor) recreateResource(ctx context.Context, existing, manifest *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if re.k8sClient == nil {
		return nil, fmt.Errorf("kubernetes client not configured")
	}

	gvk := existing.GroupVersionKind()
	namespace := existing.GetNamespace()
	name := existing.GetName()

	// Delete the existing resource
	re.log.Debugf(ctx, "Deleting resource for recreation")
	if err := re.k8sClient.DeleteResource(ctx, gvk, namespace, name); err != nil {
		return nil, fmt.Errorf("failed to delete resource for recreation: %w", err)
	}

	// Wait for the resource to be fully deleted
	re.log.Debugf(ctx, "Waiting for resource deletion to complete")
	if err := re.waitForDeletion(ctx, gvk, namespace, name); err != nil {
		return nil, fmt.Errorf("failed waiting for resource deletion: %w", err)
	}

	// Create the new resource
	re.log.Debugf(ctx, "Creating new resource after deletion confirmed")
	return re.k8sClient.CreateResource(ctx, manifest)
}

// waitForDeletion polls until the resource is confirmed deleted or context times out
// Returns nil when the resource is confirmed gone (NotFound), or an error otherwise
func (re *ResourceExecutor) waitForDeletion(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) error {
	const pollInterval = 100 * time.Millisecond

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			re.log.Warnf(ctx, "Context cancelled/timed out while waiting for deletion")
			return fmt.Errorf("context cancelled while waiting for resource deletion: %w", ctx.Err())
		case <-ticker.C:
			_, err := re.k8sClient.GetResource(ctx, gvk, namespace, name)
			if err != nil {
				// NotFound means the resource is deleted - this is success
				if apierrors.IsNotFound(err) {
					re.log.Debugf(ctx, "Resource deletion confirmed")
					return nil
				}
				// Any other error is unexpected
				errCtx := logger.WithErrorField(ctx, err)
				re.log.Errorf(errCtx, "Error checking resource deletion status")
				return fmt.Errorf("error checking deletion status: %w", err)
			}
			// Resource still exists, continue polling
			re.log.Debugf(ctx, "Resource still exists, waiting for deletion...")
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
