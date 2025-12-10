package k8s_client

import (
	"context"

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
	DiscoverResult       *unstructured.UnstructuredList
	DiscoverError        error
	ExtractSecretResult  string
	ExtractSecretError   error
	ExtractConfigResult  string
	ExtractConfigError   error
}

// NewMockK8sClient creates a new mock K8s client for testing
func NewMockK8sClient() *MockK8sClient {
	return &MockK8sClient{
		Resources: make(map[string]*unstructured.Unstructured),
	}
}

// GetResource implements K8sClient.GetResource
// Returns a NotFound error when the resource doesn't exist, matching real K8s client behavior.
func (m *MockK8sClient) GetResource(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error) {
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

// DiscoverResources implements K8sClient.DiscoverResources
func (m *MockK8sClient) DiscoverResources(ctx context.Context, gvk schema.GroupVersionKind, discovery Discovery) (*unstructured.UnstructuredList, error) {
	if m.DiscoverError != nil {
		return nil, m.DiscoverError
	}
	if m.DiscoverResult != nil {
		return m.DiscoverResult, nil
	}
	return &unstructured.UnstructuredList{}, nil
}

// ExtractFromSecret implements K8sClient.ExtractFromSecret
func (m *MockK8sClient) ExtractFromSecret(ctx context.Context, path string) (string, error) {
	if m.ExtractSecretError != nil {
		return "", m.ExtractSecretError
	}
	return m.ExtractSecretResult, nil
}

// ExtractFromConfigMap implements K8sClient.ExtractFromConfigMap
func (m *MockK8sClient) ExtractFromConfigMap(ctx context.Context, path string) (string, error) {
	if m.ExtractConfigError != nil {
		return "", m.ExtractConfigError
	}
	return m.ExtractConfigResult, nil
}

// Ensure MockK8sClient implements K8sClient
var _ K8sClient = (*MockK8sClient)(nil)

