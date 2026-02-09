package executor_integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/executor"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/generation"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/maestro_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Maestro Integration Test Helpers
// =============================================================================

// maestroTestAPIServer creates a mock HyperFleet API server for Maestro integration tests
type maestroTestAPIServer struct {
	server          *httptest.Server
	mu              sync.Mutex
	requests        []maestroTestRequest
	clusterResponse map[string]interface{}
	statusResponses []map[string]interface{}
}

type maestroTestRequest struct {
	Method string
	Path   string
	Body   string
}

func newMaestroTestAPIServer(t *testing.T) *maestroTestAPIServer {
	mock := &maestroTestAPIServer{
		requests: make([]maestroTestRequest, 0),
		clusterResponse: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name": "test-cluster",
			},
			"spec": map[string]interface{}{
				"region":     "us-west-2",
				"provider":   "gcp",
				"node_count": 5,
			},
			"status": map[string]interface{}{
				"conditions": []map[string]interface{}{
					{
						"type":   "Ready",
						"status": "True",
					},
				},
			},
		},
		statusResponses: make([]map[string]interface{}, 0),
	}

	mock.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mock.mu.Lock()
		defer mock.mu.Unlock()

		var bodyStr string
		if r.Body != nil {
			buf := make([]byte, 1024*1024)
			n, _ := r.Body.Read(buf)
			bodyStr = string(buf[:n])
		}

		mock.requests = append(mock.requests, maestroTestRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Body:   bodyStr,
		})

		t.Logf("Mock API: %s %s", r.Method, r.URL.Path)

		switch {
		case r.Method == http.MethodPost && r.URL.Path != "":
			var statusBody map[string]interface{}
			if err := json.Unmarshal([]byte(bodyStr), &statusBody); err == nil {
				mock.statusResponses = append(mock.statusResponses, statusBody)
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
			return
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(mock.clusterResponse)
			return
		}

		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
	}))

	return mock
}

func (m *maestroTestAPIServer) Close() {
	m.server.Close()
}

func (m *maestroTestAPIServer) URL() string {
	return m.server.URL
}

func (m *maestroTestAPIServer) GetStatusResponses() []map[string]interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]map[string]interface{}{}, m.statusResponses...)
}

// createMaestroTestEvent creates a CloudEvent for Maestro integration testing
func createMaestroTestEvent(clusterId string) *event.Event {
	evt := event.New()
	// Use the clusterId as the CloudEvent ID so that "event.id" parameter extraction works
	evt.SetID(clusterId)
	evt.SetType("com.redhat.hyperfleet.cluster.provision")
	evt.SetSource("maestro-integration-test")
	evt.SetTime(time.Now())

	eventData := map[string]interface{}{
		"id":            clusterId,
		"resource_type": "cluster",
		"generation":    "gen-001",
		"href":          "/api/v1/clusters/" + clusterId,
	}
	eventDataBytes, _ := json.Marshal(eventData)
	_ = evt.SetData(event.ApplicationJSON, eventDataBytes)

	return &evt
}

// createMaestroTestConfig creates a unified Config with Maestro resources
func createMaestroTestConfig(apiBaseURL, targetCluster string) *config_loader.Config {
	return &config_loader.Config{
		APIVersion: config_loader.APIVersionV1Alpha1,
		Kind:       config_loader.ExpectedKindConfig,
		Metadata: config_loader.Metadata{
			Name: "maestro-test-adapter",
		},
		Spec: config_loader.ConfigSpec{
			Adapter: config_loader.AdapterInfo{
				Version: "1.0.0",
			},
			Clients: config_loader.ClientsConfig{
				HyperfleetAPI: config_loader.HyperfleetAPIConfig{
					Timeout:       10 * time.Second,
					RetryAttempts: 1,
					RetryBackoff:  hyperfleet_api.BackoffConstant,
				},
			},
			Params: []config_loader.Parameter{
				{
					Name:     "hyperfleetApiBaseUrl",
					Source:   "env.HYPERFLEET_API_BASE_URL",
					Required: true,
				},
				{
					Name:     "hyperfleetApiVersion",
					Source:   "env.HYPERFLEET_API_VERSION",
					Default:  "v1",
					Required: false,
				},
				{
					Name:     "clusterId",
					Source:   "event.id",
					Required: true,
				},
				{
					Name:    "targetCluster",
					Default: targetCluster,
				},
			},
			Preconditions: []config_loader.Precondition{
				{
					ActionBase: config_loader.ActionBase{
						Name: "clusterStatus",
						APICall: &config_loader.APICall{
							Method:  "GET",
							URL:     "{{ .hyperfleetApiBaseUrl }}/api/{{ .hyperfleetApiVersion }}/clusters/{{ .clusterId }}",
							Timeout: "5s",
						},
					},
					Capture: []config_loader.CaptureField{
						{Name: "clusterName", FieldExpressionDef: config_loader.FieldExpressionDef{Field: "metadata.name"}},
						{Name: "region", FieldExpressionDef: config_loader.FieldExpressionDef{Field: "spec.region"}},
						{Name: "cloudProvider", FieldExpressionDef: config_loader.FieldExpressionDef{Field: "spec.provider"}},
					},
					Conditions: []config_loader.Condition{
						{Field: "metadata.name", Operator: "exists"},
					},
				},
			},
			// Maestro Resources with multiple manifests
			Resources: []config_loader.Resource{
				{
					Name: "clusterResources",
					Transport: &config_loader.TransportConfig{
						Client: config_loader.TransportClientMaestro,
						Maestro: &config_loader.MaestroTransportConfig{
							TargetCluster: "{{ .targetCluster }}",
							ManifestWork: &config_loader.ManifestWorkConfig{
								Name: "cluster-{{ .clusterId }}",
							},
						},
					},
					Manifests: []config_loader.NamedManifest{
						{
							Name: "namespace",
							Manifest: map[string]interface{}{
								"apiVersion": "v1",
								"kind":       "Namespace",
								"metadata": map[string]interface{}{
									"name": "cluster-{{ .clusterId }}",
									"labels": map[string]interface{}{
										"hyperfleet.io/cluster-id": "{{ .clusterId }}",
										"hyperfleet.io/managed-by": "{{ .metadata.name }}",
									},
									"annotations": map[string]interface{}{
										constants.AnnotationGeneration: "1",
									},
								},
							},
						},
						{
							Name: "configMap",
							Manifest: map[string]interface{}{
								"apiVersion": "v1",
								"kind":       "ConfigMap",
								"metadata": map[string]interface{}{
									"name":      "cluster-config",
									"namespace": "cluster-{{ .clusterId }}",
									"labels": map[string]interface{}{
										"hyperfleet.io/cluster-id": "{{ .clusterId }}",
									},
									"annotations": map[string]interface{}{
										constants.AnnotationGeneration: "1",
									},
								},
								"data": map[string]interface{}{
									"cluster-id":   "{{ .clusterId }}",
									"cluster-name": "{{ .clusterName }}",
									"region":       "{{ .region }}",
									"provider":     "{{ .cloudProvider }}",
								},
							},
						},
					},
					Discovery: &config_loader.DiscoveryConfig{
						ByName: "cluster-{{ .clusterId }}",
					},
				},
			},
		},
	}
}

// =============================================================================
// Maestro Integration Tests (using MockMaestroClient)
// =============================================================================

// TestExecutor_Maestro_CreateMultipleManifests tests creating a ManifestWork with multiple manifests
func TestExecutor_Maestro_CreateMultipleManifests(t *testing.T) {
	// Setup mock API server
	mockAPI := newMaestroTestAPIServer(t)
	defer mockAPI.Close()

	// Setup mock Maestro client
	mockMaestro := maestro_client.NewMockMaestroClient()

	// Set environment variables
	t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	// Create config with Maestro resources
	targetCluster := "test-target-cluster"
	config := createMaestroTestConfig(mockAPI.URL(), targetCluster)

	apiClient, err := hyperfleet_api.NewClient(testLog(),
		hyperfleet_api.WithTimeout(10*time.Second),
		hyperfleet_api.WithRetryAttempts(1),
	)
	require.NoError(t, err)

	// Create executor with mock K8s client and mock Maestro client
	mockK8s := k8s_client.NewMockK8sClient()
	exec, err := executor.NewBuilder().
		WithConfig(config).
		WithAPIClient(apiClient).
		WithK8sClient(mockK8s).
		WithMaestroClient(mockMaestro).
		WithLogger(testLog()).
		Build()
	require.NoError(t, err)

	// Create test event
	clusterId := fmt.Sprintf("maestro-cluster-%d", time.Now().UnixNano())
	evt := createMaestroTestEvent(clusterId)

	// Execute
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result := exec.Execute(ctx, evt)

	// Verify execution succeeded
	require.Equal(t, executor.StatusSuccess, result.Status, "Expected success status, got %s: errors=%v", result.Status, result.Errors)

	// Verify resource results
	require.Len(t, result.ResourceResults, 1, "Expected 1 resource result for clusterResources")

	rr := result.ResourceResults[0]
	assert.Equal(t, "clusterResources", rr.Name)
	assert.Equal(t, executor.StatusSuccess, rr.Status)
	assert.Equal(t, generation.OperationCreate, rr.Operation)
	t.Logf("Resource %s: operation=%s", rr.Name, rr.Operation)

	// Verify ManifestWork was created with multiple manifests
	appliedWorks := mockMaestro.GetAppliedWorks()
	require.Len(t, appliedWorks, 1, "Expected 1 ManifestWork to be applied")

	work := appliedWorks[0]
	assert.Equal(t, fmt.Sprintf("cluster-%s", clusterId), work.GetName())
	assert.Len(t, work.Spec.Workload.Manifests, 2, "ManifestWork should contain 2 manifests")

	// Verify correct target cluster was used
	consumers := mockMaestro.GetApplyConsumers()
	require.Len(t, consumers, 1)
	assert.Equal(t, targetCluster, consumers[0])

	t.Logf("ManifestWork applied: name=%s targetCluster=%s manifestCount=%d",
		work.GetName(), consumers[0], len(work.Spec.Workload.Manifests))
}

// TestExecutor_Maestro_TemplateRendering tests that template variables are rendered in all manifests
func TestExecutor_Maestro_TemplateRendering(t *testing.T) {
	mockAPI := newMaestroTestAPIServer(t)
	defer mockAPI.Close()

	mockMaestro := maestro_client.NewMockMaestroClient()

	t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	config := createMaestroTestConfig(mockAPI.URL(), "template-test-cluster")

	apiClient, err := hyperfleet_api.NewClient(testLog())
	require.NoError(t, err)

	mockK8s := k8s_client.NewMockK8sClient()
	exec, err := executor.NewBuilder().
		WithConfig(config).
		WithAPIClient(apiClient).
		WithK8sClient(mockK8s).
		WithMaestroClient(mockMaestro).
		WithLogger(testLog()).
		Build()
	require.NoError(t, err)

	clusterId := fmt.Sprintf("template-cluster-%d", time.Now().UnixNano())
	evt := createMaestroTestEvent(clusterId)

	result := exec.Execute(context.Background(), evt)
	require.Equal(t, executor.StatusSuccess, result.Status)

	// Verify templates were rendered in manifests
	appliedWorks := mockMaestro.GetAppliedWorks()
	require.Len(t, appliedWorks, 1)

	work := appliedWorks[0]

	// Check the ManifestWork name was rendered
	expectedName := fmt.Sprintf("cluster-%s", clusterId)
	assert.Equal(t, expectedName, work.GetName(), "ManifestWork name should be rendered from template")

	// Verify manifests contain rendered values (by checking raw manifest data)
	require.Len(t, work.Spec.Workload.Manifests, 2)

	// Parse first manifest (Namespace)
	var nsManifest map[string]interface{}
	err = json.Unmarshal(work.Spec.Workload.Manifests[0].Raw, &nsManifest)
	require.NoError(t, err)

	nsMetadata := nsManifest["metadata"].(map[string]interface{})
	assert.Equal(t, fmt.Sprintf("cluster-%s", clusterId), nsMetadata["name"],
		"Namespace name should be rendered")

	nsLabels := nsMetadata["labels"].(map[string]interface{})
	assert.Equal(t, clusterId, nsLabels["hyperfleet.io/cluster-id"],
		"Namespace label should contain rendered cluster ID")

	// Parse second manifest (ConfigMap)
	var cmManifest map[string]interface{}
	err = json.Unmarshal(work.Spec.Workload.Manifests[1].Raw, &cmManifest)
	require.NoError(t, err)

	cmMetadata := cmManifest["metadata"].(map[string]interface{})
	assert.Equal(t, fmt.Sprintf("cluster-%s", clusterId), cmMetadata["namespace"],
		"ConfigMap namespace should be rendered")

	cmData := cmManifest["data"].(map[string]interface{})
	assert.Equal(t, clusterId, cmData["cluster-id"],
		"ConfigMap data should contain rendered cluster ID")
	assert.Equal(t, "test-cluster", cmData["cluster-name"],
		"ConfigMap data should contain captured cluster name from precondition")
	assert.Equal(t, "us-west-2", cmData["region"],
		"ConfigMap data should contain captured region from precondition")

	t.Logf("Template rendering verified for ManifestWork: %s", work.GetName())
}

// TestExecutor_Maestro_ManifestWorkNaming tests ManifestWork naming behavior
func TestExecutor_Maestro_ManifestWorkNaming(t *testing.T) {
	t.Run("uses configured manifestWork name with templates", func(t *testing.T) {
		mockAPI := newMaestroTestAPIServer(t)
		defer mockAPI.Close()

		mockMaestro := maestro_client.NewMockMaestroClient()

		t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
		t.Setenv("HYPERFLEET_API_VERSION", "v1")

		config := createMaestroTestConfig(mockAPI.URL(), "naming-test-cluster")

		apiClient, err := hyperfleet_api.NewClient(testLog())
		require.NoError(t, err)

		mockK8s := k8s_client.NewMockK8sClient()
		exec, err := executor.NewBuilder().
			WithConfig(config).
			WithAPIClient(apiClient).
			WithK8sClient(mockK8s).
			WithMaestroClient(mockMaestro).
			WithLogger(testLog()).
			Build()
		require.NoError(t, err)

		clusterId := "naming-test-123"
		evt := createMaestroTestEvent(clusterId)

		result := exec.Execute(context.Background(), evt)
		require.Equal(t, executor.StatusSuccess, result.Status)

		appliedWorks := mockMaestro.GetAppliedWorks()
		require.Len(t, appliedWorks, 1)

		// Config specifies: manifestWork.name = "cluster-{{ .clusterId }}"
		assert.Equal(t, "cluster-naming-test-123", appliedWorks[0].GetName())
	})

	t.Run("generates name when manifestWork name not configured", func(t *testing.T) {
		mockAPI := newMaestroTestAPIServer(t)
		defer mockAPI.Close()

		mockMaestro := maestro_client.NewMockMaestroClient()

		t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())

		// Config without manifestWork.name configured
		config := createMaestroTestConfig(mockAPI.URL(), "auto-name-cluster")
		// Remove the manifestWork name
		config.Spec.Resources[0].Transport.Maestro.ManifestWork = nil

		apiClient, err := hyperfleet_api.NewClient(testLog())
		require.NoError(t, err)

		mockK8s := k8s_client.NewMockK8sClient()
		exec, err := executor.NewBuilder().
			WithConfig(config).
			WithAPIClient(apiClient).
			WithK8sClient(mockK8s).
			WithMaestroClient(mockMaestro).
			WithLogger(testLog()).
			Build()
		require.NoError(t, err)

		clusterId := "auto-name-456"
		evt := createMaestroTestEvent(clusterId)

		result := exec.Execute(context.Background(), evt)
		require.Equal(t, executor.StatusSuccess, result.Status)

		appliedWorks := mockMaestro.GetAppliedWorks()
		require.Len(t, appliedWorks, 1)

		// Name should be generated: {resourceName}-{firstManifestName}
		expectedName := fmt.Sprintf("clusterResources-cluster-%s", clusterId)
		assert.Equal(t, expectedName, appliedWorks[0].GetName(),
			"ManifestWork name should be auto-generated from resource name and first manifest name")
	})
}

// TestExecutor_Maestro_ErrorHandling tests error handling when Maestro client fails
func TestExecutor_Maestro_ErrorHandling(t *testing.T) {
	mockAPI := newMaestroTestAPIServer(t)
	defer mockAPI.Close()

	mockMaestro := maestro_client.NewMockMaestroClient()
	// Configure mock to return an error
	mockMaestro.ApplyManifestWorkError = fmt.Errorf("maestro server unavailable: connection refused")

	t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	config := createMaestroTestConfig(mockAPI.URL(), "error-test-cluster")

	apiClient, err := hyperfleet_api.NewClient(testLog())
	require.NoError(t, err)

	mockK8s := k8s_client.NewMockK8sClient()
	exec, err := executor.NewBuilder().
		WithConfig(config).
		WithAPIClient(apiClient).
		WithK8sClient(mockK8s).
		WithMaestroClient(mockMaestro).
		WithLogger(testLog()).
		Build()
	require.NoError(t, err)

	evt := createMaestroTestEvent("error-test-cluster")

	result := exec.Execute(context.Background(), evt)

	// Execution should fail
	assert.Equal(t, executor.StatusFailed, result.Status)

	// Resource result should show failure
	require.Len(t, result.ResourceResults, 1)
	rr := result.ResourceResults[0]
	assert.Equal(t, executor.StatusFailed, rr.Status)
	assert.NotNil(t, rr.Error)
	assert.Contains(t, rr.Error.Error(), "connection refused")

	// Error should be present in result
	require.NotEmpty(t, result.Errors)
	resourceError, ok := result.Errors[executor.PhaseResources]
	require.True(t, ok, "Should have error in resources phase")
	assert.Contains(t, resourceError.Error(), "failed to apply ManifestWork")

	t.Logf("Error handling verified: %v", rr.Error)
}

// TestExecutor_Maestro_ManifestsStoredInContext verifies manifests are stored in execution context
func TestExecutor_Maestro_ManifestsStoredInContext(t *testing.T) {
	mockAPI := newMaestroTestAPIServer(t)
	defer mockAPI.Close()

	mockMaestro := maestro_client.NewMockMaestroClient()

	t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	config := createMaestroTestConfig(mockAPI.URL(), "context-test-cluster")

	apiClient, err := hyperfleet_api.NewClient(testLog())
	require.NoError(t, err)

	mockK8s := k8s_client.NewMockK8sClient()
	exec, err := executor.NewBuilder().
		WithConfig(config).
		WithAPIClient(apiClient).
		WithK8sClient(mockK8s).
		WithMaestroClient(mockMaestro).
		WithLogger(testLog()).
		Build()
	require.NoError(t, err)

	clusterId := "context-test-789"
	evt := createMaestroTestEvent(clusterId)

	result := exec.Execute(context.Background(), evt)
	require.Equal(t, executor.StatusSuccess, result.Status)

	// Verify execution context contains stored manifests
	require.NotNil(t, result.ExecutionContext)
	resources := result.ExecutionContext.Resources

	// Manifests should be stored by compound name: {resourceName}.{manifestName}
	assert.NotNil(t, resources["clusterResources.namespace"],
		"Namespace manifest should be stored as clusterResources.namespace")
	assert.NotNil(t, resources["clusterResources.configMap"],
		"ConfigMap manifest should be stored as clusterResources.configMap")

	// First manifest should also be stored under resource name
	assert.NotNil(t, resources["clusterResources"],
		"First manifest should also be stored under resource name")

	// Verify stored manifest content
	nsManifest := resources["clusterResources.namespace"]
	assert.Equal(t, "Namespace", nsManifest.GetKind())
	assert.Equal(t, fmt.Sprintf("cluster-%s", clusterId), nsManifest.GetName())

	cmManifest := resources["clusterResources.configMap"]
	assert.Equal(t, "ConfigMap", cmManifest.GetKind())
	assert.Equal(t, "cluster-config", cmManifest.GetName())

	// Log the keys
	var keys []string
	for k := range resources {
		keys = append(keys, k)
	}
	t.Logf("Manifests stored in context: %v", keys)
}
