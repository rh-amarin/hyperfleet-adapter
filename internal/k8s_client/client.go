package k8s_client

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strconv"

	apperrors "github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/errors"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// EnvKubeConfig is the environment variable for kubeconfig path
const EnvKubeConfig = "KUBECONFIG"

// AnnotationGeneration is the annotation key for tracking resource generation
const AnnotationGeneration = "hyperfleet.io/generation"

// Client is the Kubernetes client for managing resources using controller-runtime
type Client struct {
	client client.Client
	log    logger.Logger
}

// ClientConfig holds configuration for creating a Kubernetes client
type ClientConfig struct {
	// KubeConfigPath is the path to kubeconfig file
	// If empty, checks KUBECONFIG env var, then falls back to in-cluster config
	KubeConfigPath string
	// QPS is the queries per second rate limiter
	QPS float32
	// Burst is the burst rate limiter
	Burst int
}

// NewClient creates a new Kubernetes client with automatic authentication detection
//
// Authentication Methods (in order of priority):
//  1. KubeConfigPath - If explicitly set in ClientConfig
//  2. KUBECONFIG env var - If KubeConfigPath is empty but env var is set
//  3. In-Cluster (ServiceAccount) - If neither above is set
//     - Uses ServiceAccount token mounted at /var/run/secrets/kubernetes.io/serviceaccount/token
//     - Uses CA certificate at /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
//     - Automatically configured when running in a Kubernetes pod
//     - Requires appropriate RBAC permissions for the ServiceAccount
//
// Example Usage:
//
//	// For production deployment in K8s cluster (uses ServiceAccount)
//	config := ClientConfig{QPS: 100.0, Burst: 200}
//	client, err := NewClient(ctx, config, log)
//
//	// For local development (uses KUBECONFIG env var or explicit path)
//	config := ClientConfig{KubeConfigPath: "/home/user/.kube/config"}
//	client, err := NewClient(ctx, config, log)
func NewClient(ctx context.Context, config ClientConfig, log logger.Logger) (*Client, error) {
	var restConfig *rest.Config
	var err error

	// Resolve kubeconfig path: explicit config > KUBECONFIG env var
	kubeConfigPath := config.KubeConfigPath
	if kubeConfigPath == "" {
		kubeConfigPath = os.Getenv(EnvKubeConfig)
	}

	if kubeConfigPath != "" {
		// Use kubeconfig file for local development or remote access
		restConfig, err = clientcmd.BuildConfigFromFlags("", kubeConfigPath)
		if err != nil {
			return nil, apperrors.KubernetesError("failed to load kubeconfig from %s: %v", kubeConfigPath, err)
		}
		log.Infof(ctx, "Using kubeconfig from: %s", kubeConfigPath)
	} else {
		// Use in-cluster config with ServiceAccount
		restConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, apperrors.KubernetesError("failed to create in-cluster config: %v", err)
		}
		log.Info(ctx, "Using in-cluster Kubernetes configuration (ServiceAccount)")
	}

	// Set rate limits
	if config.QPS == 0 {
		restConfig.QPS = 100.0
	} else {
		restConfig.QPS = config.QPS
	}
	if config.Burst == 0 {
		restConfig.Burst = 200
	} else {
		restConfig.Burst = config.Burst
	}

	// Create controller-runtime client
	// This provides automatic caching, better performance, and cleaner API
	k8sClient, err := client.New(restConfig, client.Options{})
	if err != nil {
		return nil, apperrors.KubernetesError("failed to create kubernetes client: %v", err)
	}

	return &Client{
		client: k8sClient,
		log:    log,
	}, nil
}

// NewClientFromConfig creates a client from an existing rest.Config
// This is useful for testing with envtest
func NewClientFromConfig(ctx context.Context, restConfig *rest.Config, log logger.Logger) (*Client, error) {
	k8sClient, err := client.New(restConfig, client.Options{})
	if err != nil {
		return nil, apperrors.KubernetesError("failed to create kubernetes client: %v", err)
	}

	return &Client{
		client: k8sClient,
		log:    log,
	}, nil
}

// CreateResource creates a Kubernetes resource from an unstructured object
func (c *Client) CreateResource(ctx context.Context, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	gvk := obj.GroupVersionKind()
	namespace := obj.GetNamespace()
	name := obj.GetName()

	c.log.Infof(ctx, "Creating resource: %s/%s (namespace: %s)", gvk.Kind, name, namespace)

	err := c.client.Create(ctx, obj)
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil, err
		}
		return nil, &apperrors.K8sOperationError{
			Operation: "create",
			Resource:  name,
			Kind:      gvk.Kind,
			Namespace: namespace,
			Message:   err.Error(),
			Err:       err,
		}
	}

	c.log.Infof(ctx, "Successfully created resource: %s/%s", gvk.Kind, name)
	return obj, nil
}

// GetResource retrieves a specific Kubernetes resource by GVK, namespace, and name
func (c *Client) GetResource(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error) {
	c.log.Infof(ctx, "Getting resource: %s/%s (namespace: %s)", gvk.Kind, name, namespace)

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)

	key := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}

	err := c.client.Get(ctx, key, obj)
	if err != nil {
		// Don't wrap NotFound errors so callers can check for them
		if apierrors.IsNotFound(err) {
			return nil, err
		}
		return nil, &apperrors.K8sOperationError{
			Operation: "get",
			Resource:  name,
			Kind:      gvk.Kind,
			Namespace: namespace,
			Message:   err.Error(),
			Err:       err,
		}
	}

	c.log.Infof(ctx, "Successfully retrieved resource: %s/%s", gvk.Kind, name)
	return obj, nil
}

// ListResources lists Kubernetes resources by GVK, namespace, and label selector.
//
// Parameters:
//   - gvk: GroupVersionKind of the resources to list
//   - namespace: namespace to list resources in (empty string for cluster-scoped or all namespaces)
//   - labelSelector: label selector string (e.g., "app=myapp,env=prod") - empty to skip
//
// For more flexible discovery (including by-name lookup), use DiscoverResources() instead.
func (c *Client) ListResources(ctx context.Context, gvk schema.GroupVersionKind, namespace string, labelSelector string) (*unstructured.UnstructuredList, error) {
	c.log.Infof(ctx, "Listing resources: %s (namespace: %s, labelSelector: %s)", gvk.Kind, namespace, labelSelector)

	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvk)

	opts := []client.ListOption{}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	if labelSelector != "" {
		selector, err := metav1.ParseToLabelSelector(labelSelector)
		if err != nil {
			return nil, apperrors.KubernetesError("invalid label selector %s: %v", labelSelector, err)
		}
		parsedLabelSelector, err := metav1.LabelSelectorAsSelector(selector)
		if err != nil {
			return nil, apperrors.KubernetesError("failed to convert label selector: %v", err)
		}
		opts = append(opts, client.MatchingLabelsSelector{Selector: parsedLabelSelector})
	}

	err := c.client.List(ctx, list, opts...)
	if err != nil {
		return nil, &apperrors.K8sOperationError{
			Operation: "list",
			Resource:  gvk.Kind,
			Kind:      gvk.Kind,
			Namespace: namespace,
			Message:   err.Error(),
			Err:       err,
		}
	}

	c.log.Infof(ctx, "Successfully listed resources: %s (found %d items)", gvk.Kind, len(list.Items))
	return list, nil
}

// UpdateResource updates an existing Kubernetes resource by replacing it entirely.
//
// This performs a full resource replacement - all fields in the provided object
// will replace the existing resource. Any fields not included will be reset to
// their default values. Requires the object to have a valid resourceVersion.
//
// Use UpdateResource when:
//   - You have the complete, current resource (e.g., from GetResource)
//   - You want to replace the entire resource
//   - You're making multiple changes across the object
//
// Use PatchResource instead when:
//   - You only want to modify specific fields
//   - You don't have the current resource
//   - You want to avoid conflicts with concurrent updates
//
// Example:
//
//	resource, _ := client.GetResource(ctx, gvk, "default", "my-cm")
//	resource.SetLabels(map[string]string{"app": "myapp"})
//	updated, err := client.UpdateResource(ctx, resource)
func (c *Client) UpdateResource(ctx context.Context, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	gvk := obj.GroupVersionKind()
	namespace := obj.GetNamespace()
	name := obj.GetName()

	c.log.Infof(ctx, "Updating resource: %s/%s (namespace: %s)", gvk.Kind, name, namespace)

	err := c.client.Update(ctx, obj)
	if err != nil {
		if apierrors.IsConflict(err) {
			return nil, err
		}
		return nil, &apperrors.K8sOperationError{
			Operation: "update",
			Resource:  name,
			Kind:      gvk.Kind,
			Namespace: namespace,
			Message:   err.Error(),
			Err:       err,
		}
	}

	c.log.Infof(ctx, "Successfully updated resource: %s/%s", gvk.Kind, name)
	return obj, nil
}

// DeleteResource deletes a Kubernetes resource
func (c *Client) DeleteResource(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) error {
	c.log.Infof(ctx, "Deleting resource: %s/%s (namespace: %s)", gvk.Kind, name, namespace)

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetNamespace(namespace)
	obj.SetName(name)

	err := c.client.Delete(ctx, obj)
	if err != nil {
		if apierrors.IsNotFound(err) {
			c.log.Infof(ctx, "Resource already deleted: %s/%s", gvk.Kind, name)
			return nil
		}
		return &apperrors.K8sOperationError{
			Operation: "delete",
			Resource:  name,
			Kind:      gvk.Kind,
			Namespace: namespace,
			Message:   err.Error(),
			Err:       err,
		}
	}

	c.log.Infof(ctx, "Successfully deleted resource: %s/%s", gvk.Kind, name)
	return nil
}

// PatchResource applies a patch to a Kubernetes resource
//
// This performs a JSON merge patch (RFC 7386), updating only the specified fields
// while preserving other fields. This is safer than UpdateResource for
// concurrent modifications.
//
// Patch Types:
//   - JSON Merge Patch: Merges the patch with existing resource
//   - Preserves fields not specified in the patch
//   - No need for resourceVersion (optimistic concurrency)
//
// Use PatchResource when:
//   - You only want to update specific fields
//   - You don't have the complete current resource
//   - You want to avoid conflicts from concurrent updates
//   - You're updating labels, annotations, or specific spec fields
//
// Use UpdateResource instead when:
//   - You have the complete resource and want to replace it entirely
//   - You're making complex multi-field changes
//
// Example:
//
//	patchData := []byte(`{"metadata":{"labels":{"new-label":"value"}}}`)
//	patched, err := client.PatchResource(ctx, gvk, "default", "my-cm", patchData)
func (c *Client) PatchResource(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string, patchData []byte) (*unstructured.Unstructured, error) {
	c.log.Infof(ctx, "Patching resource: %s/%s (namespace: %s)", gvk.Kind, name, namespace)

	// Parse patch data to validate JSON
	var patchObj map[string]interface{}
	if err := json.Unmarshal(patchData, &patchObj); err != nil {
		return nil, apperrors.KubernetesError("invalid patch data: %v", err)
	}

	// Create the resource reference
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetNamespace(namespace)
	obj.SetName(name)

	// Apply the patch using JSON merge patch type
	// This is equivalent to kubectl patch with --type=merge
	patch := client.RawPatch(types.MergePatchType, patchData)

	err := c.client.Patch(ctx, obj, patch)
	if err != nil {
		// Don't wrap NotFound errors so callers can check for them
		if apierrors.IsNotFound(err) {
			return nil, err
		}
		return nil, &apperrors.K8sOperationError{
			Operation: "patch",
			Resource:  name,
			Kind:      gvk.Kind,
			Namespace: namespace,
			Message:   err.Error(),
			Err:       err,
		}
	}

	c.log.Infof(ctx, "Successfully patched resource: %s/%s", gvk.Kind, name)

	// Get the updated resource to return
	return c.GetResource(ctx, gvk, namespace, name)
}

// GetGenerationAnnotation extracts the generation annotation value from a resource.
// Returns 0 if the resource is nil, has no annotations, or the annotation cannot be parsed.
// Used for resource management to determine if a resource has changed.
// In MVP we won't do validation for the mandatory annotations. Here return 0 if there is no generation annotation.
func GetGenerationAnnotation(obj *unstructured.Unstructured) int64 {
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
	gen, err := strconv.ParseInt(genStr, 10, 64)
	if err != nil {
		return 0
	}
	return gen
}

// GetLatestGenerationResource returns the resource with the highest generation annotation from a list.
// It sorts by generation annotation (descending) and uses metadata.name as a secondary sort key
// for deterministic behavior when generations are equal.
// Returns nil if the list is nil or empty.
func GetLatestGenerationResource(list *unstructured.UnstructuredList) *unstructured.Unstructured {
	if list == nil || len(list.Items) == 0 {
		return nil
	}

	// Sort by generation annotation (descending) to return the one with the latest generation
	// Secondary sort by metadata.name for consistency when generations are equal
	sort.Slice(list.Items, func(i, j int) bool {
		genI := GetGenerationAnnotation(&list.Items[i])
		genJ := GetGenerationAnnotation(&list.Items[j])
		if genI != genJ {
			return genI > genJ // Descending order - latest generation first
		}
		// Fall back to metadata.name for deterministic ordering when generations are equal
		return list.Items[i].GetName() < list.Items[j].GetName()
	})

	return &list.Items[0]
}
