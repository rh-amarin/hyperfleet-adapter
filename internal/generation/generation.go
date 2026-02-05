// Package generation provides utilities for generation-based resource tracking.
//
// This package handles generation annotation validation, comparison, and extraction
// for both k8s_client (Kubernetes resources) and maestro_client (ManifestWork).
package generation

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/constants"
	apperrors "github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	workv1 "open-cluster-management.io/api/work/v1"
)

// Operation represents the type of operation to perform on a resource
type Operation string

const (
	// OperationCreate indicates the resource should be created
	OperationCreate Operation = "create"
	// OperationUpdate indicates the resource should be updated
	OperationUpdate Operation = "update"
	// OperationRecreate indicates the resource should be deleted and recreated
	OperationRecreate Operation = "recreate"
	// OperationSkip indicates no operation is needed (generations match)
	OperationSkip Operation = "skip"
)

// ApplyDecision contains the decision about what operation to perform
// based on comparing generations between an existing resource and a new resource.
type ApplyDecision struct {
	// Operation is the recommended operation based on generation comparison
	Operation Operation
	// Reason explains why this operation was chosen
	Reason string
	// NewGeneration is the generation of the new resource
	NewGeneration int64
	// ExistingGeneration is the generation of the existing resource (0 if not found)
	ExistingGeneration int64
}

// CompareGenerations compares the generation of a new resource against an existing one
// and returns the recommended operation.
//
// Decision logic:
//   - If exists is false: Create (resource doesn't exist)
//   - If generations match: Skip (no changes needed)
//   - If generations differ: Update (apply changes)
//
// This function encapsulates the generation comparison logic used by both
// resource_executor (for k8s resources) and maestro_client (for ManifestWorks).
func CompareGenerations(newGen, existingGen int64, exists bool) ApplyDecision {
	if !exists {
		return ApplyDecision{
			Operation:          OperationCreate,
			Reason:             "resource not found",
			NewGeneration:      newGen,
			ExistingGeneration: 0,
		}
	}

	if existingGen == newGen {
		return ApplyDecision{
			Operation:          OperationSkip,
			Reason:             fmt.Sprintf("generation %d unchanged", existingGen),
			NewGeneration:      newGen,
			ExistingGeneration: existingGen,
		}
	}

	return ApplyDecision{
		Operation:          OperationUpdate,
		Reason:             fmt.Sprintf("generation changed %d->%d", existingGen, newGen),
		NewGeneration:      newGen,
		ExistingGeneration: existingGen,
	}
}

// GetGeneration extracts the generation annotation value from ObjectMeta.
// Returns 0 if the annotation is not found, empty, or cannot be parsed.
//
// This works with any Kubernetes resource that has ObjectMeta, including:
//   - Unstructured objects (via obj.GetAnnotations())
//   - ManifestWork objects (via work.ObjectMeta or work.Annotations)
//   - Any typed Kubernetes resource (via resource.ObjectMeta)
func GetGeneration(meta metav1.ObjectMeta) int64 {
	if meta.Annotations == nil {
		return 0
	}

	genStr, ok := meta.Annotations[constants.AnnotationGeneration]
	if !ok || genStr == "" {
		return 0
	}

	gen, err := strconv.ParseInt(genStr, 10, 64)
	if err != nil {
		return 0
	}

	return gen
}

// GetGenerationFromUnstructured is a convenience wrapper for getting generation from unstructured.Unstructured.
// Returns 0 if the resource is nil, has no annotations, or the annotation cannot be parsed.
func GetGenerationFromUnstructured(obj *unstructured.Unstructured) int64 {
	if obj == nil {
		return 0
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return 0
	}
	genStr, ok := annotations[constants.AnnotationGeneration]
	if !ok || genStr == "" {
		return 0
	}
	gen, err := strconv.ParseInt(genStr, 10, 64)
	if err != nil {
		return 0
	}
	return gen
}

// ValidateGeneration validates that the generation annotation exists and is valid on ObjectMeta.
// Returns error if:
//   - Annotation is missing
//   - Annotation value is empty
//   - Annotation value cannot be parsed as int64
//   - Annotation value is <= 0 (must be positive)
//
// This is used to validate that templates properly set the generation annotation.
func ValidateGeneration(meta metav1.ObjectMeta) error {
	if meta.Annotations == nil {
		return apperrors.Validation("missing %s annotation", constants.AnnotationGeneration).AsError()
	}

	genStr, ok := meta.Annotations[constants.AnnotationGeneration]
	if !ok {
		return apperrors.Validation("missing %s annotation", constants.AnnotationGeneration).AsError()
	}

	if genStr == "" {
		return apperrors.Validation("%s annotation is empty", constants.AnnotationGeneration).AsError()
	}

	gen, err := strconv.ParseInt(genStr, 10, 64)
	if err != nil {
		return apperrors.Validation("invalid %s annotation value %q: %v", constants.AnnotationGeneration, genStr, err).AsError()
	}

	if gen <= 0 {
		return apperrors.Validation("%s annotation must be > 0, got %d", constants.AnnotationGeneration, gen).AsError()
	}

	return nil
}

// ValidateManifestWorkGeneration validates that the generation annotation exists on both:
// 1. The ManifestWork metadata (required)
// 2. All manifests within the ManifestWork workload (required)
//
// Returns error if any generation annotation is missing or invalid.
// This ensures templates properly set generation annotations throughout the ManifestWork.
func ValidateManifestWorkGeneration(work *workv1.ManifestWork) error {
	if work == nil {
		return apperrors.Validation("work cannot be nil").AsError()
	}

	// Validate ManifestWork-level generation (required)
	if err := ValidateGeneration(work.ObjectMeta); err != nil {
		return apperrors.Validation("ManifestWork %q: %v", work.Name, err).AsError()
	}

	// Validate each manifest has generation annotation (required)
	for i, m := range work.Spec.Workload.Manifests {
		obj := &unstructured.Unstructured{}
		if err := obj.UnmarshalJSON(m.Raw); err != nil {
			return apperrors.Validation("ManifestWork %q manifest[%d]: failed to unmarshal: %v", work.Name, i, err).AsError()
		}

		// Validate generation annotation exists
		if err := ValidateGenerationFromUnstructured(obj); err != nil {
			kind := obj.GetKind()
			name := obj.GetName()
			return apperrors.Validation("ManifestWork %q manifest[%d] %s/%s: %v", work.Name, i, kind, name, err).AsError()
		}
	}

	return nil
}

// ValidateGenerationFromUnstructured validates that the generation annotation exists and is valid on an Unstructured object.
// Returns error if:
//   - Object is nil
//   - Annotation is missing
//   - Annotation value is empty
//   - Annotation value cannot be parsed as int64
//   - Annotation value is <= 0 (must be positive)
func ValidateGenerationFromUnstructured(obj *unstructured.Unstructured) error {
	if obj == nil {
		return apperrors.Validation("object cannot be nil").AsError()
	}

	annotations := obj.GetAnnotations()
	if annotations == nil {
		return apperrors.Validation("missing %s annotation", constants.AnnotationGeneration).AsError()
	}

	genStr, ok := annotations[constants.AnnotationGeneration]
	if !ok {
		return apperrors.Validation("missing %s annotation", constants.AnnotationGeneration).AsError()
	}

	if genStr == "" {
		return apperrors.Validation("%s annotation is empty", constants.AnnotationGeneration).AsError()
	}

	gen, err := strconv.ParseInt(genStr, 10, 64)
	if err != nil {
		return apperrors.Validation("invalid %s annotation value %q: %v", constants.AnnotationGeneration, genStr, err).AsError()
	}

	if gen <= 0 {
		return apperrors.Validation("%s annotation must be > 0, got %d", constants.AnnotationGeneration, gen).AsError()
	}

	return nil
}

// GetLatestGenerationFromList returns the resource with the highest generation annotation from a list.
// It sorts by generation annotation (descending) and uses metadata.name as a secondary sort key
// for deterministic behavior when generations are equal.
// Returns nil if the list is nil or empty.
//
// Useful for finding the most recent version of a resource when multiple versions exist.
func GetLatestGenerationFromList(list *unstructured.UnstructuredList) *unstructured.Unstructured {
	if list == nil || len(list.Items) == 0 {
		return nil
	}

	// Copy items to avoid modifying input
	items := make([]unstructured.Unstructured, len(list.Items))
	copy(items, list.Items)

	// Sort by generation annotation (descending) to return the one with the latest generation
	// Secondary sort by metadata.name for consistency when generations are equal
	sort.Slice(items, func(i, j int) bool {
		genI := GetGenerationFromUnstructured(&items[i])
		genJ := GetGenerationFromUnstructured(&items[j])
		if genI != genJ {
			return genI > genJ // Descending order - latest generation first
		}
		// Fall back to metadata.name for deterministic ordering when generations are equal
		return items[i].GetName() < items[j].GetName()
	})

	return &items[0]
}
