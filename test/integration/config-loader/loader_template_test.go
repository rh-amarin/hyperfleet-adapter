package config_loader_integration

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getProjectRoot traverses upwards from the directory of the current file,
// checking each parent for a .git directory, returning the first match.
// This approach reliably finds the project root, even if the project name is repeated in the path.
func getProjectRoot() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}

	dir := filepath.Dir(filename)
	for {
		gitDir := filepath.Join(dir, ".git")
		if info, err := os.Stat(gitDir); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached root
		}
		dir = parent
	}
	panic("could not find project root: no .git directory found upwards from path: " + filename)
}

// TestLoadTemplateConfig tests loading the actual adapter-config-template.yaml
// This is an integration test that validates the shipped template configuration.
func TestLoadTemplateConfig(t *testing.T) {
	// Set required environment variables for the template config
	t.Setenv("HYPERFLEET_API_BASE_URL", "http://test-api.example.com")

	projectRoot := getProjectRoot()
	configPath := filepath.Join(projectRoot, "configs/adapterconfig-template.yaml")

	config, err := config_loader.Load(configPath)
	require.NoError(t, err, "should be able to load template config")
	require.NotNil(t, config)

	// Verify basic structure
	assert.Equal(t, "hyperfleet.redhat.com/v1alpha1", config.APIVersion)
	assert.Equal(t, "AdapterConfig", config.Kind)
	assert.Equal(t, "example-adapter", config.Metadata.Name)
	assert.Equal(t, "hyperfleet-system", config.Metadata.Namespace)

	// Verify adapter info
	assert.Equal(t, "0.1.0", config.Spec.Adapter.Version)

	// Verify HyperFleet API config
	assert.Equal(t, "2s", config.Spec.HyperfleetAPI.Timeout)
	assert.Equal(t, 3, config.Spec.HyperfleetAPI.RetryAttempts)
	assert.Equal(t, "exponential", config.Spec.HyperfleetAPI.RetryBackoff)

	// Verify params exist
	assert.NotEmpty(t, config.Spec.Params)
	assert.GreaterOrEqual(t, len(config.Spec.Params), 3, "should have at least 3 parameters")

	// Check specific params (using accessor method)
	clusterIdParam := config.GetParamByName("clusterId")
	require.NotNil(t, clusterIdParam, "clusterId parameter should exist")
	assert.Equal(t, "event.id", clusterIdParam.Source)
	assert.True(t, clusterIdParam.Required)

	// Verify preconditions
	assert.NotEmpty(t, config.Spec.Preconditions)
	assert.GreaterOrEqual(t, len(config.Spec.Preconditions), 1, "should have at least 1 precondition")

	// Check first precondition
	firstPrecond := config.Spec.Preconditions[0]
	assert.Equal(t, "clusterStatus", firstPrecond.Name)
	assert.NotNil(t, firstPrecond.APICall)
	assert.Equal(t, "GET", firstPrecond.APICall.Method)
	assert.NotEmpty(t, firstPrecond.Capture)
	assert.NotEmpty(t, firstPrecond.Conditions)

	// Verify captured fields
	clusterNameCapture := findCaptureByName(firstPrecond.Capture, "clusterName")
	require.NotNil(t, clusterNameCapture)
	assert.Equal(t, "name", clusterNameCapture.Field)

	// Verify conditions in precondition
	assert.GreaterOrEqual(t, len(firstPrecond.Conditions), 1)
	firstCondition := firstPrecond.Conditions[0]
	assert.Equal(t, "readyConditionStatus", firstCondition.Field)
	assert.Equal(t, "equals", firstCondition.Operator)

	// Verify resources
	assert.NotEmpty(t, config.Spec.Resources)
	assert.GreaterOrEqual(t, len(config.Spec.Resources), 1, "should have at least 1 resource")

	// Check first resource
	firstResource := config.Spec.Resources[0]
	assert.Equal(t, "clusterNamespace", firstResource.Name)
	assert.NotNil(t, firstResource.Manifest)
	assert.NotNil(t, firstResource.Discovery)

	// Verify post configuration
	if config.Spec.Post != nil {
		assert.NotEmpty(t, config.Spec.Post.Payloads)
		assert.NotEmpty(t, config.Spec.Post.PostActions)

		// Check post action
		if len(config.Spec.Post.PostActions) > 0 {
			firstAction := config.Spec.Post.PostActions[0]
			assert.NotEmpty(t, firstAction.Name)
			if firstAction.APICall != nil {
				assert.NotEmpty(t, firstAction.APICall.Method)
				assert.NotEmpty(t, firstAction.APICall.URL)
			}
		}
	}
}

// TestLoadValidTestConfig tests loading the valid test config
func TestLoadValidTestConfig(t *testing.T) {
	// Set required environment variables for the test config
	t.Setenv("HYPERFLEET_API_BASE_URL", "http://test-api.example.com")

	projectRoot := getProjectRoot()
	configPath := filepath.Join(projectRoot, "test/testdata/adapter_config_valid.yaml")

	config, err := config_loader.Load(configPath)
	require.NoError(t, err)
	require.NotNil(t, config)

	assert.Equal(t, "hyperfleet.redhat.com/v1alpha1", config.APIVersion)
	assert.Equal(t, "AdapterConfig", config.Kind)
	assert.Equal(t, "example-adapter", config.Metadata.Name)

	// Verify resource exists
	configMapResource := findResourceByName(config.Spec.Resources, "clusterConfigMap")
	require.NotNil(t, configMapResource, "clusterConfigMap resource should exist")
}

// Helper function to find a capture field by name
func findCaptureByName(captures []config_loader.CaptureField, name string) *config_loader.CaptureField {
	for i := range captures {
		if captures[i].Name == name {
			return &captures[i]
		}
	}
	return nil
}

// Helper function to find a resource by name
func findResourceByName(resources []config_loader.Resource, name string) *config_loader.Resource {
	for i := range resources {
		if resources[i].Name == name {
			return &resources[i]
		}
	}
	return nil
}
