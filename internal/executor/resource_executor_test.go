package executor

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/maestro_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/constants"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestDeepCopyMap_BasicTypes(t *testing.T) {
	original := map[string]interface{}{
		"string": "hello",
		"int":    42,
		"float":  3.14,
		"bool":   true,
		"null":   nil,
	}

	copied := deepCopyMap(context.Background(), original, logger.NewTestLogger())

	// Verify values are copied correctly
	assert.Equal(t, "hello", copied["string"])
	assert.Equal(t, 42, copied["int"]) // copystructure preserves int (unlike JSON which converts to float64)
	assert.Equal(t, 3.14, copied["float"])
	assert.Equal(t, true, copied["bool"])
	assert.Nil(t, copied["null"])

	// Verify no warnings logged

	// Verify mutation doesn't affect original
	copied["string"] = "modified"
	assert.Equal(t, "hello", original["string"], "Original should not be modified")
}

func TestDeepCopyMap_NestedMaps(t *testing.T) {

	original := map[string]interface{}{
		"level1": map[string]interface{}{
			"level2": map[string]interface{}{
				"value": "deep",
			},
		},
	}

	copied := deepCopyMap(context.Background(), original, logger.NewTestLogger())

	// Verify deep copy works

	// Modify the copied nested map
	level1 := copied["level1"].(map[string]interface{})
	level2 := level1["level2"].(map[string]interface{})
	level2["value"] = "modified"

	// Verify original is NOT modified (deep copy worked)
	originalLevel1 := original["level1"].(map[string]interface{})
	originalLevel2 := originalLevel1["level2"].(map[string]interface{})
	assert.Equal(t, "deep", originalLevel2["value"], "Original nested value should not be modified")
}

func TestDeepCopyMap_Slices(t *testing.T) {

	original := map[string]interface{}{
		"items": []interface{}{"a", "b", "c"},
		"nested": []interface{}{
			map[string]interface{}{"key": "value"},
		},
	}

	copied := deepCopyMap(context.Background(), original, logger.NewTestLogger())

	// Modify copied slice
	copiedItems := copied["items"].([]interface{})
	copiedItems[0] = "modified"

	// Verify original is NOT modified
	originalItems := original["items"].([]interface{})
	assert.Equal(t, "a", originalItems[0], "Original slice should not be modified")
}

func TestDeepCopyMap_Channel(t *testing.T) {
	// copystructure handles channels properly (creates new channel)

	ch := make(chan int, 5)
	original := map[string]interface{}{
		"channel": ch,
		"normal":  "value",
	}

	copied := deepCopyMap(context.Background(), original, logger.NewTestLogger())

	// copystructure handles channels - no warning expected

	// Normal values are copied
	assert.Equal(t, "value", copied["normal"])

	// Verify channel exists in copied map
	copiedCh, ok := copied["channel"].(chan int)
	assert.True(t, ok, "Channel should be present in copied map")
	assert.NotNil(t, copiedCh, "Copied channel should not be nil")
}

func TestDeepCopyMap_Function(t *testing.T) {
	// copystructure handles functions (copies the function pointer)

	fn := func() string { return "hello" }
	original := map[string]interface{}{
		"func":   fn,
		"normal": "value",
	}

	copied := deepCopyMap(context.Background(), original, logger.NewTestLogger())

	// copystructure handles functions - no warning expected

	// Normal values are copied
	assert.Equal(t, "value", copied["normal"])

	// Function is preserved
	copiedFn := copied["func"].(func() string)
	assert.Equal(t, "hello", copiedFn(), "Copied function should work")
}

func TestDeepCopyMap_NestedWithChannel(t *testing.T) {
	// Test that nested maps are deep copied even when channels are present

	ch := make(chan int)
	nested := map[string]interface{}{"mutable": "original"}
	original := map[string]interface{}{
		"channel": ch,
		"nested":  nested,
	}

	copied := deepCopyMap(context.Background(), original, logger.NewTestLogger())

	// copystructure handles this properly - no warning expected

	// Modify the copied nested map
	copiedNested := copied["nested"].(map[string]interface{})
	copiedNested["mutable"] = "MUTATED"

	// Original should NOT be affected (deep copy works with copystructure)
	assert.Equal(t, "original", nested["mutable"],
		"Deep copy: original nested map should NOT be affected by mutation")
}

func TestDeepCopyMap_EmptyMap(t *testing.T) {

	original := map[string]interface{}{}
	copied := deepCopyMap(context.Background(), original, logger.NewTestLogger())

	assert.NotNil(t, copied)
	assert.Empty(t, copied)
}

func TestDeepCopyMap_DeepCopyVerification(t *testing.T) {
	// Verify deep copy works correctly
	original := map[string]interface{}{
		"string": "value",
		"nested": map[string]interface{}{
			"key": "nested_value",
		},
	}

	// Should not panic
	copied := deepCopyMap(context.Background(), original, logger.NewTestLogger())

	assert.Equal(t, "value", copied["string"])

	// Verify deep copy works
	copiedNested := copied["nested"].(map[string]interface{})
	copiedNested["key"] = "modified"

	originalNested := original["nested"].(map[string]interface{})
	assert.Equal(t, "nested_value", originalNested["key"], "Original should not be modified")
}

func TestDeepCopyMap_NilMap(t *testing.T) {

	copied := deepCopyMap(context.Background(), nil, logger.NewTestLogger())

	assert.Nil(t, copied)
}

func TestDeepCopyMap_KubernetesManifest(t *testing.T) {
	// Test with a realistic Kubernetes manifest structure

	original := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]interface{}{
			"name":      "test-config",
			"namespace": "default",
			"labels": map[string]interface{}{
				"app": "test",
			},
		},
		"data": map[string]interface{}{
			"key1": "value1",
			"key2": "value2",
		},
	}

	copied := deepCopyMap(context.Background(), original, logger.NewTestLogger())

	// Modify copied manifest
	copiedMetadata := copied["metadata"].(map[string]interface{})
	copiedLabels := copiedMetadata["labels"].(map[string]interface{})
	copiedLabels["app"] = "modified"

	// Verify original is NOT modified
	originalMetadata := original["metadata"].(map[string]interface{})
	originalLabels := originalMetadata["labels"].(map[string]interface{})
	assert.Equal(t, "test", originalLabels["app"], "Original manifest should not be modified")
}

// TestDeepCopyMap_Context ensures the function is used correctly in context
func TestDeepCopyMap_RealWorldContext(t *testing.T) {
	// This simulates how deepCopyMap is used in executeResource
	manifest := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]interface{}{
			"name": "{{ .namespace }}",
		},
	}

	// Deep copy before template rendering
	copied := deepCopyMap(context.Background(), manifest, logger.NewTestLogger())

	// Simulate template rendering modifying the copy
	copiedMetadata := copied["metadata"].(map[string]interface{})
	copiedMetadata["name"] = "rendered-namespace"

	// Original template should remain unchanged for next iteration
	originalMetadata := manifest["metadata"].(map[string]interface{})
	assert.Equal(t, "{{ .namespace }}", originalMetadata["name"])
}

// =============================================================================
// Maestro Transport Tests
// =============================================================================

// createTestNamespaceManifest creates a Namespace manifest for testing
func createTestNamespaceManifest(name string, generation int64) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]interface{}{
			"name": name,
			"annotations": map[string]interface{}{
				constants.AnnotationGeneration: fmt.Sprintf("%d", generation),
			},
		},
	}
}

// createTestConfigMapManifest creates a ConfigMap manifest for testing
func createTestConfigMapManifest(name, namespace string, generation int64) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
			"annotations": map[string]interface{}{
				constants.AnnotationGeneration: fmt.Sprintf("%d", generation),
			},
		},
		"data": map[string]interface{}{
			"key": "value",
		},
	}
}

func TestBuildManifestsMaestro(t *testing.T) {
	t.Run("builds multiple manifests with template rendering", func(t *testing.T) {
		re := &ResourceExecutor{
			log: logger.NewTestLogger(),
		}

		resource := config_loader.Resource{
			Name: "testResource",
			Manifests: []config_loader.NamedManifest{
				{
					Name: "ns",
					Manifest: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Namespace",
						"metadata": map[string]interface{}{
							"name": "{{ .namespace }}",
							"annotations": map[string]interface{}{
								constants.AnnotationGeneration: "1",
							},
						},
					},
				},
				{
					Name: "cm",
					Manifest: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
						"metadata": map[string]interface{}{
							"name":      "{{ .configName }}",
							"namespace": "{{ .namespace }}",
							"annotations": map[string]interface{}{
								constants.AnnotationGeneration: "1",
							},
						},
						"data": map[string]interface{}{
							"clusterId": "{{ .clusterId }}",
						},
					},
				},
			},
		}

		execCtx := &ExecutionContext{
			Params: map[string]interface{}{
				"namespace":  "test-ns",
				"configName": "my-config",
				"clusterId":  "cluster-123",
			},
		}

		manifests, err := re.buildManifestsMaestro(context.Background(), resource, execCtx)
		require.NoError(t, err)
		require.Len(t, manifests, 2)

		// Verify first manifest (Namespace)
		assert.Equal(t, "Namespace", manifests[0].GetKind())
		assert.Equal(t, "test-ns", manifests[0].GetName())

		// Verify second manifest (ConfigMap)
		assert.Equal(t, "ConfigMap", manifests[1].GetKind())
		assert.Equal(t, "my-config", manifests[1].GetName())
		assert.Equal(t, "test-ns", manifests[1].GetNamespace())

		// Verify data was rendered
		data, found := manifests[1].UnstructuredContent()["data"].(map[string]interface{})
		require.True(t, found, "ConfigMap should have data")
		assert.Equal(t, "cluster-123", data["clusterId"])
	})

	t.Run("returns error when manifest has no content", func(t *testing.T) {
		re := &ResourceExecutor{
			log: logger.NewTestLogger(),
		}

		resource := config_loader.Resource{
			Name: "testResource",
			Manifests: []config_loader.NamedManifest{
				{
					Name:     "empty",
					Manifest: nil, // No content
				},
			},
		}

		execCtx := &ExecutionContext{
			Params: map[string]interface{}{},
		}

		manifests, err := re.buildManifestsMaestro(context.Background(), resource, execCtx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "has no content")
		assert.Nil(t, manifests)
	})

	t.Run("handles manifestRefContent over manifest", func(t *testing.T) {
		re := &ResourceExecutor{
			log: logger.NewTestLogger(),
		}

		resource := config_loader.Resource{
			Name: "testResource",
			Manifests: []config_loader.NamedManifest{
				{
					Name: "fromRef",
					Manifest: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Secret",
					},
					// ManifestRefContent takes precedence
					ManifestRefContent: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
						"metadata": map[string]interface{}{
							"name": "from-ref",
							"annotations": map[string]interface{}{
								constants.AnnotationGeneration: "1",
							},
						},
					},
				},
			},
		}

		execCtx := &ExecutionContext{
			Params: map[string]interface{}{},
		}

		manifests, err := re.buildManifestsMaestro(context.Background(), resource, execCtx)
		require.NoError(t, err)
		require.Len(t, manifests, 1)

		// Should use ManifestRefContent (ConfigMap), not Manifest (Secret)
		assert.Equal(t, "ConfigMap", manifests[0].GetKind())
		assert.Equal(t, "from-ref", manifests[0].GetName())
	})

	t.Run("validates each manifest", func(t *testing.T) {
		re := &ResourceExecutor{
			log: logger.NewTestLogger(),
		}

		resource := config_loader.Resource{
			Name: "testResource",
			Manifests: []config_loader.NamedManifest{
				{
					Name: "valid",
					Manifest: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Namespace",
						"metadata": map[string]interface{}{
							"name": "valid-ns",
							"annotations": map[string]interface{}{
								constants.AnnotationGeneration: "1",
							},
						},
					},
				},
				{
					Name: "invalid",
					Manifest: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
						// Missing metadata.name - invalid!
						"metadata": map[string]interface{}{
							"annotations": map[string]interface{}{
								constants.AnnotationGeneration: "1",
							},
						},
					},
				},
			},
		}

		execCtx := &ExecutionContext{
			Params: map[string]interface{}{},
		}

		manifests, err := re.buildManifestsMaestro(context.Background(), resource, execCtx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid")
		assert.Contains(t, err.Error(), "missing metadata.name")
		assert.Nil(t, manifests)
	})
}

func TestBuildManifestWork(t *testing.T) {
	t.Run("bundles multiple manifests into single ManifestWork", func(t *testing.T) {
		re := &ResourceExecutor{
			log: logger.NewTestLogger(),
		}

		resource := config_loader.Resource{
			Name: "testResource",
			Transport: &config_loader.TransportConfig{
				Client: config_loader.TransportClientMaestro,
				Maestro: &config_loader.MaestroTransportConfig{
					TargetCluster: "test-cluster",
				},
			},
		}

		// Create test manifests
		nsManifest := createTestNamespaceManifest("test-ns", 1)
		cmManifest := createTestConfigMapManifest("test-cm", "test-ns", 1)

		// Convert to unstructured
		manifests, err := re.buildManifestsMaestro(context.Background(), config_loader.Resource{
			Name: "test",
			Manifests: []config_loader.NamedManifest{
				{Name: "ns", Manifest: nsManifest},
				{Name: "cm", Manifest: cmManifest},
			},
		}, &ExecutionContext{Params: map[string]interface{}{}})
		require.NoError(t, err)
		require.Len(t, manifests, 2)

		execCtx := &ExecutionContext{
			Params: map[string]interface{}{},
		}

		work, err := re.buildManifestWork(context.Background(), resource, manifests, execCtx)
		require.NoError(t, err)
		require.NotNil(t, work)

		// Verify ManifestWork contains both manifests
		assert.Len(t, work.Spec.Workload.Manifests, 2)
	})

	t.Run("uses configured manifestWork name", func(t *testing.T) {
		re := &ResourceExecutor{
			log: logger.NewTestLogger(),
		}

		resource := config_loader.Resource{
			Name: "testResource",
			Transport: &config_loader.TransportConfig{
				Client: config_loader.TransportClientMaestro,
				Maestro: &config_loader.MaestroTransportConfig{
					TargetCluster: "test-cluster",
					ManifestWork: &config_loader.ManifestWorkConfig{
						Name: "custom-work-{{ .clusterId }}",
					},
				},
			},
		}

		manifests, err := re.buildManifestsMaestro(context.Background(), config_loader.Resource{
			Name: "test",
			Manifests: []config_loader.NamedManifest{
				{Name: "ns", Manifest: createTestNamespaceManifest("test-ns", 1)},
			},
		}, &ExecutionContext{Params: map[string]interface{}{}})
		require.NoError(t, err)

		execCtx := &ExecutionContext{
			Params: map[string]interface{}{
				"clusterId": "cluster-abc",
			},
		}

		work, err := re.buildManifestWork(context.Background(), resource, manifests, execCtx)
		require.NoError(t, err)

		// Verify custom name was rendered
		assert.Equal(t, "custom-work-cluster-abc", work.GetName())
	})

	t.Run("generates name from resource and first manifest", func(t *testing.T) {
		re := &ResourceExecutor{
			log: logger.NewTestLogger(),
		}

		resource := config_loader.Resource{
			Name: "clusterResources",
			Transport: &config_loader.TransportConfig{
				Client: config_loader.TransportClientMaestro,
				Maestro: &config_loader.MaestroTransportConfig{
					TargetCluster: "test-cluster",
					// No ManifestWork.Name configured
				},
			},
		}

		manifests, err := re.buildManifestsMaestro(context.Background(), config_loader.Resource{
			Name: "test",
			Manifests: []config_loader.NamedManifest{
				{Name: "ns", Manifest: createTestNamespaceManifest("my-namespace", 1)},
			},
		}, &ExecutionContext{Params: map[string]interface{}{}})
		require.NoError(t, err)

		execCtx := &ExecutionContext{
			Params: map[string]interface{}{},
		}

		work, err := re.buildManifestWork(context.Background(), resource, manifests, execCtx)
		require.NoError(t, err)

		// Name should be generated from resource name and first manifest name
		assert.Equal(t, "clusterResources-my-namespace", work.GetName())
	})

	t.Run("copies generation annotation from first manifest", func(t *testing.T) {
		re := &ResourceExecutor{
			log: logger.NewTestLogger(),
		}

		resource := config_loader.Resource{
			Name: "testResource",
			Transport: &config_loader.TransportConfig{
				Client: config_loader.TransportClientMaestro,
				Maestro: &config_loader.MaestroTransportConfig{
					TargetCluster: "test-cluster",
				},
			},
		}

		manifests, err := re.buildManifestsMaestro(context.Background(), config_loader.Resource{
			Name: "test",
			Manifests: []config_loader.NamedManifest{
				{Name: "ns", Manifest: createTestNamespaceManifest("test-ns", 42)},
			},
		}, &ExecutionContext{Params: map[string]interface{}{}})
		require.NoError(t, err)

		execCtx := &ExecutionContext{
			Params: map[string]interface{}{},
		}

		work, err := re.buildManifestWork(context.Background(), resource, manifests, execCtx)
		require.NoError(t, err)

		// Verify generation annotation was copied to ManifestWork
		annotations := work.GetAnnotations()
		require.NotNil(t, annotations)
		assert.Equal(t, "42", annotations[constants.AnnotationGeneration])
	})
}

func TestApplyResourceMaestro(t *testing.T) {
	t.Run("applies ManifestWork via maestro client", func(t *testing.T) {
		mockClient := maestro_client.NewMockMaestroClient()

		re := &ResourceExecutor{
			maestroClient: mockClient,
			log:           logger.NewTestLogger(),
		}

		resource := config_loader.Resource{
			Name: "testResource",
			Transport: &config_loader.TransportConfig{
				Client: config_loader.TransportClientMaestro,
				Maestro: &config_loader.MaestroTransportConfig{
					TargetCluster: "{{ .targetCluster }}",
				},
			},
			Manifests: []config_loader.NamedManifest{
				{Name: "ns", Manifest: createTestNamespaceManifest("test-ns", 1)},
				{Name: "cm", Manifest: createTestConfigMapManifest("test-cm", "test-ns", 1)},
			},
		}

		manifests, err := re.buildManifestsMaestro(context.Background(), resource, &ExecutionContext{Params: map[string]interface{}{}})
		require.NoError(t, err)

		execCtx := &ExecutionContext{
			Params: map[string]interface{}{
				"targetCluster": "my-cluster-123",
			},
			Resources: make(map[string]*unstructured.Unstructured),
			Adapter:   AdapterMetadata{},
		}

		result, err := re.applyResourceMaestro(context.Background(), resource, manifests, execCtx)
		require.NoError(t, err)

		assert.Equal(t, StatusSuccess, result.Status)
		assert.Equal(t, "testResource", result.Name)

		// Verify maestro client was called
		appliedWorks := mockClient.GetAppliedWorks()
		require.Len(t, appliedWorks, 1)
		assert.Len(t, appliedWorks[0].Spec.Workload.Manifests, 2)

		// Verify correct consumer was used
		consumers := mockClient.GetApplyConsumers()
		require.Len(t, consumers, 1)
		assert.Equal(t, "my-cluster-123", consumers[0])
	})

	t.Run("stores manifests in execution context by compound name", func(t *testing.T) {
		mockClient := maestro_client.NewMockMaestroClient()

		re := &ResourceExecutor{
			maestroClient: mockClient,
			log:           logger.NewTestLogger(),
		}

		resource := config_loader.Resource{
			Name: "clusterSetup",
			Transport: &config_loader.TransportConfig{
				Client: config_loader.TransportClientMaestro,
				Maestro: &config_loader.MaestroTransportConfig{
					TargetCluster: "test-cluster",
				},
			},
			Manifests: []config_loader.NamedManifest{
				{Name: "namespace", Manifest: createTestNamespaceManifest("ns1", 1)},
				{Name: "config", Manifest: createTestConfigMapManifest("cm1", "ns1", 1)},
			},
		}

		manifests, err := re.buildManifestsMaestro(context.Background(), resource, &ExecutionContext{Params: map[string]interface{}{}})
		require.NoError(t, err)

		execCtx := &ExecutionContext{
			Params:    map[string]interface{}{},
			Resources: make(map[string]*unstructured.Unstructured),
			Adapter:   AdapterMetadata{},
		}

		_, err = re.applyResourceMaestro(context.Background(), resource, manifests, execCtx)
		require.NoError(t, err)

		// Verify manifests stored by compound name (resource.manifestName)
		assert.NotNil(t, execCtx.Resources["clusterSetup.namespace"], "First manifest should be stored as clusterSetup.namespace")
		assert.NotNil(t, execCtx.Resources["clusterSetup.config"], "Second manifest should be stored as clusterSetup.config")

		// First manifest should also be stored under resource name for convenience
		assert.NotNil(t, execCtx.Resources["clusterSetup"], "First manifest should also be stored as clusterSetup")
		assert.Equal(t, execCtx.Resources["clusterSetup.namespace"], execCtx.Resources["clusterSetup"])
	})

	t.Run("returns error when maestro client not configured", func(t *testing.T) {
		re := &ResourceExecutor{
			maestroClient: nil, // Not configured
			log:           logger.NewTestLogger(),
		}

		resource := config_loader.Resource{
			Name: "testResource",
			Transport: &config_loader.TransportConfig{
				Client: config_loader.TransportClientMaestro,
				Maestro: &config_loader.MaestroTransportConfig{
					TargetCluster: "test-cluster",
				},
			},
			Manifests: []config_loader.NamedManifest{
				{Name: "ns", Manifest: createTestNamespaceManifest("test-ns", 1)},
			},
		}

		manifests, err := re.buildManifestsMaestro(context.Background(), resource, &ExecutionContext{Params: map[string]interface{}{}})
		require.NoError(t, err)

		execCtx := &ExecutionContext{
			Params:    map[string]interface{}{},
			Resources: make(map[string]*unstructured.Unstructured),
			Adapter:   AdapterMetadata{},
		}

		result, err := re.applyResourceMaestro(context.Background(), resource, manifests, execCtx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maestro client not configured")
		assert.Equal(t, StatusFailed, result.Status)
	})

	t.Run("returns error when maestro config missing", func(t *testing.T) {
		mockClient := maestro_client.NewMockMaestroClient()

		re := &ResourceExecutor{
			maestroClient: mockClient,
			log:           logger.NewTestLogger(),
		}

		resource := config_loader.Resource{
			Name: "testResource",
			Transport: &config_loader.TransportConfig{
				Client:  config_loader.TransportClientMaestro,
				Maestro: nil, // Missing config
			},
			Manifests: []config_loader.NamedManifest{
				{Name: "ns", Manifest: createTestNamespaceManifest("test-ns", 1)},
			},
		}

		manifests, err := re.buildManifestsMaestro(context.Background(), resource, &ExecutionContext{Params: map[string]interface{}{}})
		require.NoError(t, err)

		execCtx := &ExecutionContext{
			Params:    map[string]interface{}{},
			Resources: make(map[string]*unstructured.Unstructured),
			Adapter:   AdapterMetadata{},
		}

		result, err := re.applyResourceMaestro(context.Background(), resource, manifests, execCtx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maestro transport configuration missing")
		assert.Equal(t, StatusFailed, result.Status)
	})

	t.Run("returns error when maestro client returns error", func(t *testing.T) {
		mockClient := maestro_client.NewMockMaestroClient()
		mockClient.ApplyManifestWorkError = errors.New("connection refused")

		re := &ResourceExecutor{
			maestroClient: mockClient,
			log:           logger.NewTestLogger(),
		}

		resource := config_loader.Resource{
			Name: "testResource",
			Transport: &config_loader.TransportConfig{
				Client: config_loader.TransportClientMaestro,
				Maestro: &config_loader.MaestroTransportConfig{
					TargetCluster: "test-cluster",
				},
			},
			Manifests: []config_loader.NamedManifest{
				{Name: "ns", Manifest: createTestNamespaceManifest("test-ns", 1)},
			},
		}

		manifests, err := re.buildManifestsMaestro(context.Background(), resource, &ExecutionContext{Params: map[string]interface{}{}})
		require.NoError(t, err)

		execCtx := &ExecutionContext{
			Params:    map[string]interface{}{},
			Resources: make(map[string]*unstructured.Unstructured),
			Adapter:   AdapterMetadata{},
		}

		result, err := re.applyResourceMaestro(context.Background(), resource, manifests, execCtx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to apply ManifestWork")
		assert.Equal(t, StatusFailed, result.Status)

		// Verify error was set in execution context
		assert.NotNil(t, execCtx.Adapter.ExecutionError)
		assert.Equal(t, "resources", execCtx.Adapter.ExecutionError.Phase)
		assert.Equal(t, "testResource", execCtx.Adapter.ExecutionError.Step)
	})
}
