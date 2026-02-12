package k8s_client

import (
	"context"
	"encoding/json"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/manifest"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/transport_client"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// MockK8sClient implements K8sClient for testing.
// It stores resources in memory and allows configuring mock responses.
type MockK8sClient struct {
	// Resources stores created/updated resources by "namespace/name" key
	Resources map[string]*unstructured.Unstructured

	// Mock responses - set these to control behavior
	GetResourceResult    *unstructured.Unstructured
	GetResourceError     error
	CreateResourceResult *unstructured.Unstructured
	CreateResourceError  error
	UpdateResourceResult *unstructured.Unstructured
	UpdateResourceError  error
	DeleteResourceError  error
	ApplyManifestResult  *ApplyResult
	ApplyManifestError   error
	ApplyResourceResult  *ApplyResult
	ApplyResourceError   error
	DiscoverResult       *unstructured.UnstructuredList
	DiscoverError        error
}

// NewMockK8sClient creates a new mock K8s client for testing
func NewMockK8sClient() *MockK8sClient {
	return &MockK8sClient{
		Resources: make(map[string]*unstructured.Unstructured),
	}
}

// GetResource implements K8sClient.GetResource
// Returns a NotFound error when the resource doesn't exist, matching real K8s client behavior.
func (m *MockK8sClient) GetResource(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string, _ transport_client.TransportContext) (*unstructured.Unstructured, error) {
	// Check explicit error override first
	if m.GetResourceError != nil {
		return nil, m.GetResourceError
	}
	// Check explicit result override
	if m.GetResourceResult != nil {
		return m.GetResourceResult, nil
	}
	// Check stored resources
	key := namespace + "/" + name
	if res, ok := m.Resources[key]; ok {
		return res, nil
	}
	// Resource not found - return proper K8s NotFound error (matches real client behavior)
	gr := schema.GroupResource{Group: gvk.Group, Resource: gvk.Kind + "s"}
	return nil, apierrors.NewNotFound(gr, name)
}

// CreateResource implements K8sClient.CreateResource
func (m *MockK8sClient) CreateResource(ctx context.Context, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if m.CreateResourceError != nil {
		return nil, m.CreateResourceError
	}
	if m.CreateResourceResult != nil {
		return m.CreateResourceResult, nil
	}
	// Store the resource
	key := obj.GetNamespace() + "/" + obj.GetName()
	m.Resources[key] = obj.DeepCopy()
	return obj, nil
}

// UpdateResource implements K8sClient.UpdateResource
func (m *MockK8sClient) UpdateResource(ctx context.Context, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if m.UpdateResourceError != nil {
		return nil, m.UpdateResourceError
	}
	if m.UpdateResourceResult != nil {
		return m.UpdateResourceResult, nil
	}
	// Store the resource
	key := obj.GetNamespace() + "/" + obj.GetName()
	m.Resources[key] = obj.DeepCopy()
	return obj, nil
}

// DeleteResource implements K8sClient.DeleteResource
func (m *MockK8sClient) DeleteResource(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) error {
	if m.DeleteResourceError != nil {
		return m.DeleteResourceError
	}
	// Remove from stored resources
	key := namespace + "/" + name
	delete(m.Resources, key)
	return nil
}

// ApplyManifest implements K8sClient.ApplyManifest
func (m *MockK8sClient) ApplyManifest(ctx context.Context, newManifest *unstructured.Unstructured, existing *unstructured.Unstructured, opts *ApplyOptions) (*ApplyResult, error) {
	if m.ApplyManifestError != nil {
		return nil, m.ApplyManifestError
	}
	if m.ApplyManifestResult != nil {
		return m.ApplyManifestResult, nil
	}
	// Default behavior: store the resource and return create result
	key := newManifest.GetNamespace() + "/" + newManifest.GetName()
	m.Resources[key] = newManifest
	return &ApplyResult{
		Operation: manifest.OperationCreate,
		Reason:    "mock apply",
	}, nil
}

// ApplyResource implements transport_client.TransportClient.ApplyResource
func (m *MockK8sClient) ApplyResource(ctx context.Context, manifestBytes []byte, opts *transport_client.ApplyOptions, _ transport_client.TransportContext) (*transport_client.ApplyResult, error) {
	if m.ApplyResourceError != nil {
		return nil, m.ApplyResourceError
	}
	if m.ApplyResourceResult != nil {
		return m.ApplyResourceResult, nil
	}
	// Default behavior: parse and store
	obj := &unstructured.Unstructured{}
	if err := json.Unmarshal(manifestBytes, &obj.Object); err != nil {
		return nil, err
	}
	key := obj.GetNamespace() + "/" + obj.GetName()
	m.Resources[key] = obj
	return &transport_client.ApplyResult{
		Operation: manifest.OperationCreate,
		Reason:    "mock apply",
	}, nil
}

// DiscoverResources implements K8sClient.DiscoverResources
func (m *MockK8sClient) DiscoverResources(ctx context.Context, gvk schema.GroupVersionKind, discovery manifest.Discovery, _ transport_client.TransportContext) (*unstructured.UnstructuredList, error) {
	if m.DiscoverError != nil {
		return nil, m.DiscoverError
	}
	if m.DiscoverResult != nil {
		return m.DiscoverResult, nil
	}
	return &unstructured.UnstructuredList{}, nil
}

// Ensure MockK8sClient implements K8sClient
var _ K8sClient = (*MockK8sClient)(nil)
