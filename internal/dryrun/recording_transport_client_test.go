package dryrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/manifest"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/transportclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// testDiscovery implements manifest.Discovery for use in tests.
type testDiscovery struct {
	namespace      string
	name           string
	labelSelector  string
	singleResource bool
}

func (d *testDiscovery) GetNamespace() string { return d.namespace }
func (d *testDiscovery) GetName() string      { return d.name }
func (d *testDiscovery) GetLabelSelector() string {
	return d.labelSelector
}
func (d *testDiscovery) IsSingleResource() bool { return d.singleResource }

// makeManifest builds a valid JSON manifest for testing.
func makeManifest(apiVersion, kind, namespace, name string) []byte {
	obj := map[string]interface{}{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
	}
	data, _ := json.Marshal(obj)
	return data
}

func TestApplyResource_CreateNew(t *testing.T) {
	ctx := context.Background()
	client := NewDryrunTransportClient()
	manifestBytes := makeManifest("v1", "ConfigMap", "default", "my-cm")

	result, err := client.ApplyResource(ctx, manifestBytes, nil, nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, manifest.OperationCreate, result.Operation)
	assert.Contains(t, result.Reason, "dry-run")

	// Verify record was appended.
	require.Len(t, client.Records, 1)
	assert.Equal(t, "apply", client.Records[0].Operation)
	assert.Equal(t, "my-cm", client.Records[0].Name)
	assert.Equal(t, "default", client.Records[0].Namespace)
	assert.Nil(t, client.Records[0].Error)

	// Verify resource is retrievable via Get.
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	obj, err := client.GetResource(ctx, gvk, "default", "my-cm", nil)
	require.NoError(t, err)
	assert.Equal(t, "my-cm", obj.GetName())
}

func TestApplyResource_UpdateExisting(t *testing.T) {
	ctx := context.Background()
	client := NewDryrunTransportClient()
	manifestBytes := makeManifest("v1", "ConfigMap", "default", "my-cm")

	// First apply: create.
	result1, err := client.ApplyResource(ctx, manifestBytes, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, manifest.OperationCreate, result1.Operation)

	// Second apply: update.
	result2, err := client.ApplyResource(ctx, manifestBytes, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, manifest.OperationUpdate, result2.Operation)

	require.Len(t, client.Records, 2)
}

func TestApplyResource_RecreateOnChange(t *testing.T) {
	ctx := context.Background()
	client := NewDryrunTransportClient()
	manifestBytes := makeManifest("v1", "ConfigMap", "default", "my-cm")

	// First apply to create the resource.
	_, err := client.ApplyResource(ctx, manifestBytes, nil, nil)
	require.NoError(t, err)

	// Second apply with RecreateOnChange.
	opts := &transportclient.ApplyOptions{RecreateOnChange: true}
	result, err := client.ApplyResource(ctx, manifestBytes, opts, nil)
	require.NoError(t, err)
	assert.Equal(t, manifest.OperationRecreate, result.Operation)
}

func TestApplyResource_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	client := NewDryrunTransportClient()

	result, err := client.ApplyResource(ctx, []byte("{invalid-json"), nil, nil)

	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse manifest")

	// Record should still be appended with the error.
	require.Len(t, client.Records, 1)
	assert.Equal(t, "apply", client.Records[0].Operation)
	assert.NotNil(t, client.Records[0].Error)
}

func TestApplyResource_NilOptions(t *testing.T) {
	ctx := context.Background()
	client := NewDryrunTransportClient()
	manifestBytes := makeManifest("v1", "ConfigMap", "default", "my-cm")

	// First apply to create.
	_, err := client.ApplyResource(ctx, manifestBytes, nil, nil)
	require.NoError(t, err)

	// Second apply with nil opts should not panic and should produce Update.
	result, err := client.ApplyResource(ctx, manifestBytes, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, manifest.OperationUpdate, result.Operation)
}

func TestApplyResource_WithDiscoveryOverride(t *testing.T) {
	ctx := context.Background()

	overrides := DiscoveryOverrides{
		"my-cm": {
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":            "my-cm",
				"namespace":       "default",
				"resourceVersion": "12345",
				"uid":             "fake-uid",
			},
			"data": map[string]interface{}{
				"overridden": "true",
			},
		},
	}
	client := NewDryrunTransportClientWithOverrides(overrides)
	manifestBytes := makeManifest("v1", "ConfigMap", "default", "my-cm")

	_, err := client.ApplyResource(ctx, manifestBytes, nil, nil)
	require.NoError(t, err)

	// Get should return the override, not the original manifest.
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	obj, err := client.GetResource(ctx, gvk, "default", "my-cm", nil)
	require.NoError(t, err)

	data, found, err := unstructuredNestedString(obj.Object, "data", "overridden")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "true", data)
}

// unstructuredNestedString is a small helper to navigate nested maps.
func unstructuredNestedString(obj map[string]interface{}, fields ...string) (string, bool, error) {
	current := obj
	for i, f := range fields {
		val, ok := current[f]
		if !ok {
			return "", false, nil
		}
		if i == len(fields)-1 {
			s, strOk := val.(string)
			return s, strOk, nil
		}
		m, ok := val.(map[string]interface{})
		if !ok {
			return "", false, fmt.Errorf("field %q is not a map", f)
		}
		current = m
	}
	return "", false, nil
}

func TestApplyResource_OverrideNoMatch(t *testing.T) {
	ctx := context.Background()

	overrides := DiscoveryOverrides{
		"other-resource": {
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "other-resource",
				"namespace": "default",
			},
		},
	}
	client := NewDryrunTransportClientWithOverrides(overrides)
	manifestBytes := makeManifest("v1", "ConfigMap", "default", "my-cm")

	_, err := client.ApplyResource(ctx, manifestBytes, nil, nil)
	require.NoError(t, err)

	// Get should return the original manifest since no override matched.
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	obj, err := client.GetResource(ctx, gvk, "default", "my-cm", nil)
	require.NoError(t, err)
	assert.Equal(t, "my-cm", obj.GetName())

	// The override's extra fields should not be present.
	_, found, _ := unstructuredNestedString(obj.Object, "data", "overridden")
	assert.False(t, found)
}

func TestGetResource_NotFound(t *testing.T) {
	ctx := context.Background()
	client := NewDryrunTransportClient()

	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	obj, err := client.GetResource(ctx, gvk, "default", "missing", nil)

	assert.Nil(t, obj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// Record should still be appended.
	require.Len(t, client.Records, 1)
	assert.Equal(t, "get", client.Records[0].Operation)
	assert.Equal(t, "missing", client.Records[0].Name)
}

func TestGetResource_ReturnsDeepCopy(t *testing.T) {
	ctx := context.Background()
	client := NewDryrunTransportClient()
	manifestBytes := makeManifest("v1", "ConfigMap", "default", "my-cm")

	_, err := client.ApplyResource(ctx, manifestBytes, nil, nil)
	require.NoError(t, err)

	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}

	// Get the resource and mutate it.
	obj, err := client.GetResource(ctx, gvk, "default", "my-cm", nil)
	require.NoError(t, err)
	obj.SetName("mutated-name")

	// Get again and verify the store was not affected.
	obj2, err := client.GetResource(ctx, gvk, "default", "my-cm", nil)
	require.NoError(t, err)
	assert.Equal(t, "my-cm", obj2.GetName())
}

func TestDiscoverResources_ByGVK(t *testing.T) {
	ctx := context.Background()
	client := NewDryrunTransportClient()

	// Apply two ConfigMaps and one Secret.
	_, err := client.ApplyResource(ctx, makeManifest("v1", "ConfigMap", "default", "cm-1"), nil, nil)
	require.NoError(t, err)
	_, err = client.ApplyResource(ctx, makeManifest("v1", "ConfigMap", "default", "cm-2"), nil, nil)
	require.NoError(t, err)
	_, err = client.ApplyResource(ctx, makeManifest("v1", "Secret", "default", "secret-1"), nil, nil)
	require.NoError(t, err)

	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	disc := &testDiscovery{namespace: "", name: ""}

	list, err := client.DiscoverResources(ctx, gvk, disc, nil)
	require.NoError(t, err)
	assert.Len(t, list.Items, 2)

	// All returned items should be ConfigMaps.
	for _, item := range list.Items {
		assert.Equal(t, "ConfigMap", item.GetKind())
	}
}

func TestDiscoverResources_FilterByNamespace(t *testing.T) {
	ctx := context.Background()
	client := NewDryrunTransportClient()

	_, err := client.ApplyResource(ctx, makeManifest("v1", "ConfigMap", "ns-a", "cm-1"), nil, nil)
	require.NoError(t, err)
	_, err = client.ApplyResource(ctx, makeManifest("v1", "ConfigMap", "ns-b", "cm-2"), nil, nil)
	require.NoError(t, err)

	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	disc := &testDiscovery{namespace: "ns-a"}

	list, err := client.DiscoverResources(ctx, gvk, disc, nil)
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	assert.Equal(t, "cm-1", list.Items[0].GetName())
}

func TestDiscoverResources_SingleResourceByName(t *testing.T) {
	ctx := context.Background()
	client := NewDryrunTransportClient()

	_, err := client.ApplyResource(ctx, makeManifest("v1", "ConfigMap", "default", "cm-1"), nil, nil)
	require.NoError(t, err)
	_, err = client.ApplyResource(ctx, makeManifest("v1", "ConfigMap", "default", "cm-2"), nil, nil)
	require.NoError(t, err)

	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	disc := &testDiscovery{
		namespace:      "default",
		name:           "cm-1",
		singleResource: true,
	}

	list, err := client.DiscoverResources(ctx, gvk, disc, nil)
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	assert.Equal(t, "cm-1", list.Items[0].GetName())
}

func TestDiscoverResources_EmptyStore(t *testing.T) {
	ctx := context.Background()
	client := NewDryrunTransportClient()

	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	disc := &testDiscovery{}

	list, err := client.DiscoverResources(ctx, gvk, disc, nil)
	require.NoError(t, err)
	assert.Empty(t, list.Items)
}

// maestroTarget is a non-nil transport context value that represents a Maestro call.
// The concrete type doesn't matter here — only nil vs non-nil is tested.
var maestroTarget transportclient.TransportContext = struct{ ConsumerName string }{"cluster-1"}

func TestDeleteResource_Kubernetes_RemovesFromStore(t *testing.T) {
	ctx := context.Background()
	client := NewDryrunTransportClient()
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}

	_, err := client.ApplyResource(ctx, makeManifest("v1", "ConfigMap", "default", "my-cm"), nil, nil)
	require.NoError(t, err)

	// K8s delete: nil target → synchronous, resource must be gone immediately.
	err = client.DeleteResource(ctx, gvk, "default", "my-cm", nil, nil)
	require.NoError(t, err)

	disc := &testDiscovery{namespace: "default", name: "my-cm", singleResource: true}
	list, err := client.DiscoverResources(ctx, gvk, disc, nil)
	require.NoError(t, err)
	assert.Empty(t, list.Items, "K8s resource must be removed from store immediately after delete")

	// Delete record must be appended (1 apply + 1 delete + 1 discover).
	deleteFound := false
	for _, r := range client.Records {
		if r.Operation == operationDelete {
			deleteFound = true
			assert.Equal(t, "my-cm", r.Name)
		}
	}
	assert.True(t, deleteFound)
}

func TestDeleteResource_Maestro_SetsDeleteTimestampAndKeepsInStore(t *testing.T) {
	ctx := context.Background()
	client := NewDryrunTransportClient()
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}

	_, err := client.ApplyResource(ctx, makeManifest("v1", "ConfigMap", "default", "my-cm"), nil, nil)
	require.NoError(t, err)

	// Maestro delete: non-nil target → async, resource must stay with deletionTimestamp.
	err = client.DeleteResource(ctx, gvk, "default", "my-cm", nil, maestroTarget)
	require.NoError(t, err)

	disc := &testDiscovery{namespace: "default", name: "my-cm", singleResource: true}
	list, err := client.DiscoverResources(ctx, gvk, disc, nil)
	require.NoError(t, err)
	require.Len(t, list.Items, 1, "Maestro resource must remain in store after delete (async cleanup)")

	ts := list.Items[0].GetDeletionTimestamp()
	assert.NotNil(t, ts, "deletionTimestamp must be set for Maestro async delete")
	assert.False(t, ts.IsZero())
}

func TestDeleteResource_NonExistentResource_NoOp(t *testing.T) {
	ctx := context.Background()
	client := NewDryrunTransportClient()
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}

	// Deleting a resource that was never applied must not panic.
	err := client.DeleteResource(ctx, gvk, "default", "ghost", nil, nil)
	require.NoError(t, err)

	require.Len(t, client.Records, 1)
	assert.Equal(t, operationDelete, client.Records[0].Operation)
}

func TestDeleteResource_WithOverrides_Maestro_SetsDeleteTimestamp(t *testing.T) {
	ctx := context.Background()
	overrides := DiscoveryOverrides{
		"my-cm": {
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "my-cm",
				"namespace": "default",
			},
		},
	}
	client := NewDryrunTransportClientWithOverrides(overrides)
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}

	// Maestro delete on a pre-loaded override resource.
	err := client.DeleteResource(ctx, gvk, "default", "my-cm", nil, maestroTarget)
	require.NoError(t, err)

	disc := &testDiscovery{namespace: "default", name: "my-cm", singleResource: true}
	list, err := client.DiscoverResources(ctx, gvk, disc, nil)
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	assert.NotNil(t, list.Items[0].GetDeletionTimestamp())
}

func TestConcurrentApplyAndGet(t *testing.T) {
	ctx := context.Background()
	client := NewDryrunTransportClient()
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}

	const n = 50
	var wg sync.WaitGroup

	// Concurrently apply n resources.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := fmt.Sprintf("cm-%d", idx)
			m := makeManifest("v1", "ConfigMap", "default", name)
			_, err := client.ApplyResource(ctx, m, nil, nil)
			assert.NoError(t, err)
		}(i)
	}
	wg.Wait()

	// Concurrently get those resources.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := fmt.Sprintf("cm-%d", idx)
			obj, err := client.GetResource(ctx, gvk, "default", name, nil)
			assert.NoError(t, err)
			assert.Equal(t, name, obj.GetName())
		}(i)
	}
	wg.Wait()

	// Verify all apply records exist (n applies + n gets).
	assert.Len(t, client.Records, 2*n)

	// Count apply records specifically.
	applyCount := 0
	for _, r := range client.Records {
		if r.Operation == "apply" {
			applyCount++
		}
	}
	assert.Equal(t, n, applyCount)

	// Verify no errors in any record.
	for _, r := range client.Records {
		if r.Operation == "apply" {
			assert.Nil(t, r.Error)
		}
	}

	// Also verify discover works after concurrent writes.
	disc := &testDiscovery{}
	list, err := client.DiscoverResources(ctx, gvk, disc, nil)
	require.NoError(t, err)
	assert.Len(t, list.Items, n)

	// Check no "not found" errors leaked into the results.
	for _, r := range client.Records {
		if r.Operation == "get" {
			assert.False(t, strings.Contains(fmt.Sprintf("%v", r.Error), "not found"),
				"unexpected not-found error for resource %s", r.Name)
		}
	}
}
