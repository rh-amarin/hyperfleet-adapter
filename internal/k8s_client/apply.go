package k8s_client

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/manifest"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/transport_client"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"
)

// Type aliases for backward compatibility.
type (
	ApplyOptions = transport_client.ApplyOptions
	ApplyResult  = transport_client.ApplyResult
)

// ApplyResource implements transport_client.TransportClient.
// It accepts rendered JSON/YAML bytes, parses them into an unstructured K8s resource,
// discovers the existing resource by name, and applies with generation comparison.
func (c *Client) ApplyResource(
	ctx context.Context,
	manifestBytes []byte,
	opts *transport_client.ApplyOptions,
	_ transport_client.TransportContext,
) (*transport_client.ApplyResult, error) {
	if len(manifestBytes) == 0 {
		return nil, fmt.Errorf("manifest bytes cannot be empty")
	}

	// Parse bytes into unstructured
	obj, err := parseToUnstructured(manifestBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	// Discover existing resource by name
	gvk := obj.GroupVersionKind()
	existing, err := c.GetResource(ctx, gvk, obj.GetNamespace(), obj.GetName(), nil)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get existing resource %s/%s: %w", gvk.Kind, obj.GetName(), err)
	}

	// Apply with generation comparison
	return c.ApplyManifest(ctx, obj, existing, opts)
}

// ApplyManifest creates or updates a Kubernetes resource based on generation comparison.
// This is the K8s-specific method that operates on parsed unstructured resources.
//
// If the resource doesn't exist, it creates it.
// If it exists and the generation differs, it updates (or recreates if RecreateOnChange=true).
// If it exists and the generation matches, it skips the update (idempotent).
//
// The manifest must have the hyperfleet.io/generation annotation set.
func (c *Client) ApplyManifest(
	ctx context.Context,
	newManifest *unstructured.Unstructured,
	existing *unstructured.Unstructured,
	opts *ApplyOptions,
) (*ApplyResult, error) {
	if newManifest == nil {
		return nil, fmt.Errorf("new manifest cannot be nil")
	}

	if opts == nil {
		opts = &ApplyOptions{}
	}

	// Get generation from new manifest
	newGen := manifest.GetGenerationFromUnstructured(newManifest)

	// Get existing generation (0 if not found)
	var existingGen int64
	if existing != nil {
		existingGen = manifest.GetGenerationFromUnstructured(existing)
	}

	// Compare generations to determine operation
	decision := manifest.CompareGenerations(newGen, existingGen, existing != nil)

	result := &ApplyResult{
		Operation: decision.Operation,
		Reason:    decision.Reason,
	}

	// Handle recreateOnChange override
	if decision.Operation == manifest.OperationUpdate && opts.RecreateOnChange {
		result.Operation = manifest.OperationRecreate
		result.Reason = fmt.Sprintf("%s, recreateOnChange=true", decision.Reason)
	}

	gvk := newManifest.GroupVersionKind()
	name := newManifest.GetName()

	c.log.Debugf(ctx, "ApplyManifest %s/%s: operation=%s reason=%s",
		gvk.Kind, name, result.Operation, result.Reason)

	// Execute the operation
	var applyErr error
	switch result.Operation {
	case manifest.OperationCreate:
		_, applyErr = c.CreateResource(ctx, newManifest)

	case manifest.OperationUpdate:
		// Preserve resourceVersion and UID from existing for update
		newManifest.SetResourceVersion(existing.GetResourceVersion())
		newManifest.SetUID(existing.GetUID())
		_, applyErr = c.UpdateResource(ctx, newManifest)

	case manifest.OperationRecreate:
		_, applyErr = c.recreateResource(ctx, existing, newManifest)

	case manifest.OperationSkip:
		// Nothing to do
	}

	if applyErr != nil {
		return nil, fmt.Errorf("failed to %s resource %s/%s: %w",
			result.Operation, gvk.Kind, name, applyErr)
	}

	return result, nil
}

// recreateResource deletes and recreates a Kubernetes resource.
// It waits for the resource to be fully deleted before creating the new one
// to avoid race conditions with Kubernetes asynchronous deletion.
func (c *Client) recreateResource(
	ctx context.Context,
	existing *unstructured.Unstructured,
	newManifest *unstructured.Unstructured,
) (*unstructured.Unstructured, error) {
	gvk := existing.GroupVersionKind()
	namespace := existing.GetNamespace()
	name := existing.GetName()

	// Delete the existing resource
	c.log.Debugf(ctx, "Deleting resource for recreation: %s/%s", gvk.Kind, name)
	if err := c.DeleteResource(ctx, gvk, namespace, name); err != nil {
		return nil, fmt.Errorf("failed to delete resource for recreation: %w", err)
	}

	// Wait for the resource to be fully deleted
	c.log.Debugf(ctx, "Waiting for resource deletion to complete: %s/%s", gvk.Kind, name)
	if err := c.waitForDeletion(ctx, gvk, namespace, name); err != nil {
		return nil, fmt.Errorf("failed waiting for resource deletion: %w", err)
	}

	// Create the new resource
	c.log.Debugf(ctx, "Creating new resource after deletion confirmed: %s/%s", gvk.Kind, name)
	return c.CreateResource(ctx, newManifest)
}

// waitForDeletion polls until the resource is confirmed deleted or context times out.
// Returns nil when the resource is confirmed gone (NotFound), or an error otherwise.
func (c *Client) waitForDeletion(
	ctx context.Context,
	gvk schema.GroupVersionKind,
	namespace, name string,
) error {
	const pollInterval = 100 * time.Millisecond

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.log.Warnf(ctx, "Context cancelled/timed out while waiting for deletion of %s/%s", gvk.Kind, name)
			return fmt.Errorf("context cancelled while waiting for resource deletion: %w", ctx.Err())
		case <-ticker.C:
			_, err := c.GetResource(ctx, gvk, namespace, name, nil)
			if err != nil {
				// NotFound means the resource is deleted - this is success
				if apierrors.IsNotFound(err) {
					c.log.Debugf(ctx, "Resource deletion confirmed: %s/%s", gvk.Kind, name)
					return nil
				}
				// Any other error is unexpected
				c.log.Errorf(ctx, "Error checking deletion status for %s/%s: %v", gvk.Kind, name, err)
				return fmt.Errorf("error checking deletion status: %w", err)
			}
			// Resource still exists, continue polling
			c.log.Debugf(ctx, "Resource %s/%s still exists, waiting for deletion...", gvk.Kind, name)
		}
	}
}

// parseToUnstructured parses JSON or YAML bytes into an unstructured Kubernetes resource.
func parseToUnstructured(data []byte) (*unstructured.Unstructured, error) {
	obj := &unstructured.Unstructured{}

	// Try JSON first
	if err := json.Unmarshal(data, &obj.Object); err == nil && obj.Object != nil {
		return obj, nil
	}

	// Fall back to YAML → JSON → unstructured
	jsonData, err := yaml.YAMLToJSON(data)
	if err != nil {
		return nil, fmt.Errorf("failed to convert YAML to JSON: %w", err)
	}

	if err := json.Unmarshal(jsonData, &obj.Object); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	return obj, nil
}
