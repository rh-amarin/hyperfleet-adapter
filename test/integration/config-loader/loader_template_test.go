package config_loader_integration

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const projectName = "hyperfleet-adapter"

// getProjectRoot returns the absolute path to the project root by finding
// the LAST occurrence of the exact project directory name in the current file's path.
// This handles cases where the workspace path also contains the project name
// (e.g., /workspace/hyperfleet-adapter/pr-branch/hyperfleet-adapter/...)
func getProjectRoot() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}

	// Match exact directory name by looking for /projectName/ or /projectName at end
	sep := string(filepath.Separator)
	searchPattern := sep + projectName + sep

	// Find the last occurrence of the exact directory name
	idx := strings.LastIndex(filename, searchPattern)
	if idx == -1 {
		panic("could not find project root: " + projectName + " not found as directory in path: " + filename)
	}

	// Return path up to and including the project directory
	return filename[:idx+len(searchPattern)-1]
}

// TestLoadTemplateConfig tests loading the actual adapter-config-template.yaml
// This is an integration test that validates the shipped template configuration.
func TestLoadTemplateConfig(t *testing.T) {
	projectRoot := getProjectRoot()
	configPath := filepath.Join(projectRoot, "configs/adapter-config-template.yaml")

	config, err := config_loader.Load(configPath)
	require.NoError(t, err, "should be able to load template config")
	require.NotNil(t, config)

	// Verify basic structure
	assert.Equal(t, "hyperfleet.redhat.com/v1alpha1", config.APIVersion)
	assert.Equal(t, "AdapterConfig", config.Kind)
	assert.Equal(t, "example-adapter", config.Metadata.Name)
	assert.Equal(t, "hyperfleet-system", config.Metadata.Namespace)

	// Verify adapter info
	assert.Equal(t, "0.0.1", config.Spec.Adapter.Version)

	// Verify HyperFleet API config
	assert.Equal(t, "2s", config.Spec.HyperfleetAPI.Timeout)
	assert.Equal(t, 3, config.Spec.HyperfleetAPI.RetryAttempts)
	assert.Equal(t, "exponential", config.Spec.HyperfleetAPI.RetryBackoff)

	// Verify params exist
	assert.NotEmpty(t, config.Spec.Params)
	assert.GreaterOrEqual(t, len(config.Spec.Params), 5, "should have at least 5 parameters")

	// Check specific params (using accessor method)
	clusterIdParam := config.GetParamByName("clusterId")
	require.NotNil(t, clusterIdParam, "clusterId parameter should exist")
	assert.Equal(t, "event.cluster_id", clusterIdParam.Source)
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
	assert.Equal(t, "metadata.name", clusterNameCapture.Field)

	// Verify conditions in precondition
	assert.GreaterOrEqual(t, len(firstPrecond.Conditions), 1)
	firstCondition := firstPrecond.Conditions[0]
	assert.Equal(t, "clusterPhase", firstCondition.Field)
	assert.Equal(t, "in", firstCondition.Operator)

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

