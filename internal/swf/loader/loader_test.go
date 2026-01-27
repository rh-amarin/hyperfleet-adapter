package loader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectFormat_SWF(t *testing.T) {
	swfYAML := `
document:
  dsl: "1.0.0"
  namespace: test
  name: my-workflow
  version: "1.0.0"
do:
  - setValues:
      set:
        hello: world
`
	format := DetectFormat([]byte(swfYAML))
	assert.Equal(t, FormatSWF, format)
}

func TestDetectFormat_AdapterConfig(t *testing.T) {
	adapterYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
  namespace: default
spec:
  adapter:
    version: "1.0.0"
`
	format := DetectFormat([]byte(adapterYAML))
	assert.Equal(t, FormatAdapterConfig, format)
}

func TestDetectFormat_Unknown(t *testing.T) {
	unknownYAML := `
some:
  random: yaml
  without: markers
`
	format := DetectFormat([]byte(unknownYAML))
	assert.Equal(t, FormatUnknown, format)
}

func TestDetectFormat_InvalidYAML(t *testing.T) {
	invalidYAML := `
not: valid: yaml: content
`
	format := DetectFormat([]byte(invalidYAML))
	assert.Equal(t, FormatUnknown, format)
}

func TestParse_SWF(t *testing.T) {
	swfYAML := `
document:
  dsl: "1.0.0"
  namespace: test
  name: my-workflow
  version: "1.0.0"
do:
  - setValues:
      set:
        hello: world
`
	result, err := Parse([]byte(swfYAML), "/tmp/test.yaml")
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, FormatSWF, result.Format)
	assert.NotNil(t, result.Workflow)
	assert.Nil(t, result.AdapterConfig)
	assert.Equal(t, "my-workflow", result.Workflow.Document.Name)
	assert.Equal(t, "1.0.0", result.Workflow.Document.DSL)
}

func TestParse_AdapterConfig(t *testing.T) {
	adapterYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
  namespace: default
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    timeout: "30s"
    retryAttempts: 3
    retryBackoff: exponential
  kubernetes:
    apiVersion: v1
  params:
    - name: clusterId
      source: event.id
`
	result, err := Parse([]byte(adapterYAML), "/tmp/test.yaml")
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, FormatAdapterConfig, result.Format)
	assert.NotNil(t, result.Workflow)
	assert.NotNil(t, result.AdapterConfig)
	assert.Equal(t, "test-adapter", result.Workflow.Document.Name)
	assert.Equal(t, "test-adapter", result.AdapterConfig.Metadata.Name)
}

func TestParse_Unknown(t *testing.T) {
	unknownYAML := `
some:
  random: yaml
`
	result, err := Parse([]byte(unknownYAML), "/tmp/test.yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unable to detect config format")
	assert.Nil(t, result)
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read config file")
}

func TestLoad_EmptyPathWithoutEnv(t *testing.T) {
	// Temporarily unset the env var
	original := os.Getenv("ADAPTER_CONFIG_PATH")
	os.Unsetenv("ADAPTER_CONFIG_PATH")
	defer func() {
		if original != "" {
			os.Setenv("ADAPTER_CONFIG_PATH", original)
		}
	}()

	_, err := Load("")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "config file path is required")
}

func TestLoad_SWFFile(t *testing.T) {
	// Create a temporary SWF file
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "workflow.yaml")

	swfYAML := `
document:
  dsl: "1.0.0"
  namespace: test
  name: loaded-workflow
  version: "1.0.0"
do:
  - setValues:
      set:
        key: value
`
	err := os.WriteFile(filePath, []byte(swfYAML), 0644)
	require.NoError(t, err)

	result, err := Load(filePath)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, FormatSWF, result.Format)
	assert.Equal(t, "loaded-workflow", result.Workflow.Document.Name)
	assert.Equal(t, filePath, result.FilePath)
}

func TestLoad_AdapterConfigFile(t *testing.T) {
	// Create a temporary AdapterConfig file
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "adapter.yaml")

	adapterYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: loaded-adapter
  namespace: default
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    timeout: "30s"
    retryAttempts: 3
    retryBackoff: exponential
  kubernetes:
    apiVersion: v1
`
	err := os.WriteFile(filePath, []byte(adapterYAML), 0644)
	require.NoError(t, err)

	result, err := Load(filePath)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, FormatAdapterConfig, result.Format)
	assert.Equal(t, "loaded-adapter", result.Workflow.Document.Name)
	assert.NotNil(t, result.AdapterConfig)
}

func TestLoadWorkflow(t *testing.T) {
	// Create a temporary SWF file
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "workflow.yaml")

	swfYAML := `
document:
  dsl: "1.0.0"
  namespace: test
  name: convenience-workflow
  version: "1.0.0"
do:
  - setValues:
      set:
        key: value
`
	err := os.WriteFile(filePath, []byte(swfYAML), 0644)
	require.NoError(t, err)

	workflow, err := LoadWorkflow(filePath)
	require.NoError(t, err)
	require.NotNil(t, workflow)
	assert.Equal(t, "convenience-workflow", workflow.Document.Name)
}

func TestLoadWorkflowFromBytes(t *testing.T) {
	swfYAML := `
document:
  dsl: "1.0.0"
  namespace: test
  name: bytes-workflow
  version: "1.0.0"
do:
  - setValues:
      set:
        key: value
`
	workflow, err := LoadWorkflowFromBytes([]byte(swfYAML))
	require.NoError(t, err)
	require.NotNil(t, workflow)
	assert.Equal(t, "bytes-workflow", workflow.Document.Name)
}

func TestIsNativeWorkflow(t *testing.T) {
	tempDir := t.TempDir()

	// Create SWF file
	swfPath := filepath.Join(tempDir, "workflow.yaml")
	swfYAML := `
document:
  dsl: "1.0.0"
  namespace: test
  name: test-workflow
  version: "1.0.0"
`
	err := os.WriteFile(swfPath, []byte(swfYAML), 0644)
	require.NoError(t, err)

	// Create AdapterConfig file
	adapterPath := filepath.Join(tempDir, "adapter.yaml")
	adapterYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
`
	err = os.WriteFile(adapterPath, []byte(adapterYAML), 0644)
	require.NoError(t, err)

	// Test SWF file
	isSWF, err := IsNativeWorkflow(swfPath)
	require.NoError(t, err)
	assert.True(t, isSWF)

	// Test AdapterConfig file
	isSWF, err = IsNativeWorkflow(adapterPath)
	require.NoError(t, err)
	assert.False(t, isSWF)
}

func TestIsAdapterConfig(t *testing.T) {
	tempDir := t.TempDir()

	// Create AdapterConfig file
	adapterPath := filepath.Join(tempDir, "adapter.yaml")
	adapterYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
`
	err := os.WriteFile(adapterPath, []byte(adapterYAML), 0644)
	require.NoError(t, err)

	// Create SWF file
	swfPath := filepath.Join(tempDir, "workflow.yaml")
	swfYAML := `
document:
  dsl: "1.0.0"
  namespace: test
  name: test-workflow
  version: "1.0.0"
`
	err = os.WriteFile(swfPath, []byte(swfYAML), 0644)
	require.NoError(t, err)

	// Test AdapterConfig file
	isAdapter, err := IsAdapterConfig(adapterPath)
	require.NoError(t, err)
	assert.True(t, isAdapter)

	// Test SWF file
	isAdapter, err = IsAdapterConfig(swfPath)
	require.NoError(t, err)
	assert.False(t, isAdapter)
}

func TestWorkflowFormatConstants(t *testing.T) {
	assert.Equal(t, WorkflowFormat("adapter-config"), FormatAdapterConfig)
	assert.Equal(t, WorkflowFormat("swf"), FormatSWF)
	assert.Equal(t, WorkflowFormat("unknown"), FormatUnknown)
}

func TestParse_WithOptions(t *testing.T) {
	adapterYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: options-test
  namespace: default
spec:
  adapter:
    version: "2.0.0"
  hyperfleetApi:
    timeout: "30s"
    retryAttempts: 3
    retryBackoff: exponential
  kubernetes:
    apiVersion: v1
`
	result, err := Parse([]byte(adapterYAML), "/tmp/test.yaml",
		WithAdapterVersion("2.0.0"),
		WithSkipSemanticValidation(),
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, FormatAdapterConfig, result.Format)
}
