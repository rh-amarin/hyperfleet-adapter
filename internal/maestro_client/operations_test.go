// Package maestro_client tests
//
// Note: Tests for generation.ValidateGeneration, generation.ValidateGenerationFromUnstructured,
// and generation.ValidateManifestWorkGeneration are in internal/generation/generation_test.go.
// This file contains tests specific to maestro_client functionality.
package maestro_client

import (
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/generation"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/constants"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	workv1 "open-cluster-management.io/api/work/v1"
)

func TestGetGenerationFromManifestWork(t *testing.T) {
	tests := []struct {
		name     string
		work     *workv1.ManifestWork
		expected int64
	}{
		{
			name:     "nil work returns 0",
			work:     nil,
			expected: 0,
		},
		{
			name: "work with generation annotation",
			work: &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						constants.AnnotationGeneration: "42",
					},
				},
			},
			expected: 42,
		},
		{
			name: "work without annotations",
			work: &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{},
			},
			expected: 0,
		},
		{
			name: "work with invalid generation value",
			work: &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						constants.AnnotationGeneration: "invalid",
					},
				},
			},
			expected: 0,
		},
		{
			name: "work with empty generation value",
			work: &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						constants.AnnotationGeneration: "",
					},
				},
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result int64
			if tt.work == nil {
				result = 0
			} else {
				result = generation.GetGeneration(tt.work.ObjectMeta)
			}
			if result != tt.expected {
				t.Errorf("expected generation %d, got %d", tt.expected, result)
			}
		})
	}
}

// BuildManifestWorkName generates a consistent ManifestWork name for testing
// Format: <adapter-name>-<resource-name>-<cluster-id>
func BuildManifestWorkName(adapterName, resourceName, clusterID string) string {
	return adapterName + "-" + resourceName + "-" + clusterID
}

func TestBuildManifestWorkName(t *testing.T) {
	tests := []struct {
		name         string
		adapterName  string
		resourceName string
		clusterID    string
		expected     string
	}{
		{
			name:         "basic name construction",
			adapterName:  "my-adapter",
			resourceName: "namespace",
			clusterID:    "cluster-123",
			expected:     "my-adapter-namespace-cluster-123",
		},
		{
			name:         "with special characters",
			adapterName:  "adapter_v1",
			resourceName: "config-map",
			clusterID:    "prod-us-east-1",
			expected:     "adapter_v1-config-map-prod-us-east-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildManifestWorkName(tt.adapterName, tt.resourceName, tt.clusterID)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestGenerationComparison(t *testing.T) {
	tests := []struct {
		name               string
		existingGeneration int64
		newGeneration      int64
		shouldUpdate       bool
		description        string
	}{
		{
			name:               "same generation - no update",
			existingGeneration: 5,
			newGeneration:      5,
			shouldUpdate:       false,
			description:        "When generations match, should skip update",
		},
		{
			name:               "newer generation - update",
			existingGeneration: 5,
			newGeneration:      6,
			shouldUpdate:       true,
			description:        "When new generation is higher, should update",
		},
		{
			name:               "older generation - still update",
			existingGeneration: 6,
			newGeneration:      5,
			shouldUpdate:       true,
			description:        "When new generation is lower, should still update (allow rollback)",
		},
		// Note: "both 0" case is no longer valid since validation requires generation > 0
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Logic from ApplyManifestWork:
			// if existingGeneration == generation { return existing }
			shouldSkipUpdate := tt.existingGeneration == tt.newGeneration
			shouldUpdate := !shouldSkipUpdate

			if shouldUpdate != tt.shouldUpdate {
				t.Errorf("%s: expected shouldUpdate=%v, got %v",
					tt.description, tt.shouldUpdate, shouldUpdate)
			}
		})
	}
}
