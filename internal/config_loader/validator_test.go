package config_loader

import (
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/criteria"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateConditionOperators(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid operators",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
  preconditions:
    - name: "checkStatus"
      conditions:
        - field: "status"
          operator: "equals"
          value: "Ready"
        - field: "provider"
          operator: "in"
          value: ["aws", "gcp"]
        - field: "vpcId"
          operator: "exists"
`,
			wantError: false,
		},
		{
			name: "invalid operator",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
  preconditions:
    - name: "checkStatus"
      conditions:
        - field: "status"
          operator: "invalidOp"
          value: "Ready"
`,
			wantError: true,
			errorMsg:  "invalid operator",
		},
		{
			name: "missing operator",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
  preconditions:
    - name: "checkStatus"
      conditions:
        - field: "status"
          value: "Ready"
`,
			wantError: true,
			errorMsg:  "operator is required",
		},
		{
			name: "missing value for equals operator",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
  preconditions:
    - name: "checkStatus"
      conditions:
        - field: "status"
          operator: "equals"
`,
			wantError: true,
			errorMsg:  "value is required for operator \"equals\"",
		},
		{
			name: "missing value for in operator",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
  preconditions:
    - name: "checkStatus"
      conditions:
        - field: "provider"
          operator: "in"
`,
			wantError: true,
			errorMsg:  "value is required for operator \"in\"",
		},
		{
			name: "non-list value for in operator",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
  preconditions:
    - name: "checkStatus"
      conditions:
        - field: "provider"
          operator: "in"
          value: "aws"
`,
			wantError: true,
			errorMsg:  "value must be a list for operator \"in\"",
		},
		{
			name: "non-list value for notIn operator",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
  preconditions:
    - name: "checkStatus"
      conditions:
        - field: "provider"
          operator: "notIn"
          value: "aws"
`,
			wantError: true,
			errorMsg:  "value must be a list for operator \"notIn\"",
		},
		{
			name: "exists operator without value is valid",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
  preconditions:
    - name: "checkStatus"
      conditions:
        - field: "vpcId"
          operator: "exists"
`,
			wantError: false,
		},
		{
			name: "missing value for greaterThan operator",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
  preconditions:
    - name: "checkCount"
      conditions:
        - field: "count"
          operator: "greaterThan"
`,
			wantError: true,
			errorMsg:  "value is required for operator \"greaterThan\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := Parse([]byte(tt.yaml))
			if tt.wantError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, config)
			}
		})
	}
}

func TestValidateTemplateVariables(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantError bool
		errorMsg  string
	}{
		{
			name: "defined variables",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
    - name: "apiUrl"
      source: "env.API_URL"
  preconditions:
    - name: "checkCluster"
      apiCall:
        method: "GET"
        url: "{{ .apiUrl }}/clusters/{{ .clusterId }}"
`,
			wantError: false,
		},
		{
			name: "undefined variable in URL",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
  preconditions:
    - name: "checkCluster"
      apiCall:
        method: "GET"
        url: "{{ .undefinedVar }}/clusters/{{ .clusterId }}"
`,
			wantError: true,
			errorMsg:  "undefined template variable \"undefinedVar\"",
		},
		{
			name: "undefined variable in resource manifest",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
  resources:
    - name: "testNs"
      manifest:
        apiVersion: v1
        kind: Namespace
        metadata:
          name: "ns-{{ .undefinedVar }}"
      discovery:
        namespace: "*"
        byName: "ns-{{ .clusterId }}"
`,
			wantError: true,
			errorMsg:  "undefined template variable \"undefinedVar\"",
		},
		{
			name: "nested variable access from stored response",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "apiUrl"
      source: "env.API_URL"
  preconditions:
    - name: "getCluster"
      apiCall:
        method: "GET"
        url: "{{ .apiUrl }}/clusters"
      capture:
        - name: "clusterName"
          field: "metadata.name"
  resources:
    - name: "testNs"
      manifest:
        apiVersion: v1
        kind: Namespace
        metadata:
          name: "ns-{{ .clusterName }}"
      discovery:
        namespace: "*"
        byName: "ns-{{ .clusterName }}"
`,
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := Parse([]byte(tt.yaml))
			if tt.wantError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, config)
			}
		})
	}
}

func TestValidateCELExpressions(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid CEL expression",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
  preconditions:
    - name: "checkPhase"
      expression: |
        clusterPhase == "Ready" || clusterPhase == "Provisioning"
`,
			wantError: false,
		},
		{
			name: "invalid CEL expression - syntax error",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
  preconditions:
    - name: "checkPhase"
      expression: |
        clusterPhase ==== "Ready"
`,
			wantError: true,
			errorMsg:  "CEL parse error",
		},
		{
			name: "valid CEL with has() function",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
  preconditions:
    - name: "checkField"
      expression: |
        has(cluster.status) && cluster.status.phase == "Ready"
`,
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := Parse([]byte(tt.yaml))
			if tt.wantError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, config)
			}
		})
	}
}

func TestValidateK8sManifests(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid K8s manifest",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  resources:
    - name: "testNs"
      manifest:
        apiVersion: v1
        kind: Namespace
        metadata:
          name: "test-namespace"
          labels:
            app: test
      discovery:
        namespace: "*"
        byName: "test-namespace"
`,
			wantError: false,
		},
		{
			name: "missing apiVersion in manifest",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  resources:
    - name: "testNs"
      manifest:
        kind: Namespace
        metadata:
          name: "test-namespace"
      discovery:
        namespace: "*"
        byName: "test-namespace"
`,
			wantError: true,
			errorMsg:  "missing required Kubernetes field \"apiVersion\"",
		},
		{
			name: "missing kind in manifest",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  resources:
    - name: "testNs"
      manifest:
        apiVersion: v1
        metadata:
          name: "test-namespace"
      discovery:
        namespace: "*"
        byName: "test-namespace"
`,
			wantError: true,
			errorMsg:  "missing required Kubernetes field \"kind\"",
		},
		{
			name: "missing metadata in manifest",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  resources:
    - name: "testNs"
      manifest:
        apiVersion: v1
        kind: Namespace
      discovery:
        namespace: "*"
        byName: "test-namespace"
`,
			wantError: true,
			errorMsg:  "missing required Kubernetes field \"metadata\"",
		},
		{
			name: "missing name in metadata",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  resources:
    - name: "testNs"
      manifest:
        apiVersion: v1
        kind: Namespace
        metadata:
          labels:
            app: test
      discovery:
        namespace: "*"
        byName: "test-namespace"
`,
			wantError: true,
			errorMsg:  "missing required field \"name\"",
		},
		{
			name: "valid manifest ref",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  resources:
    - name: "testDeployment"
      manifest:
        ref: "templates/deployment.yaml"
      discovery:
        namespace: "*"
        byName: "test-deployment"
`,
			wantError: false,
		},
		{
			name: "empty manifest ref",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  resources:
    - name: "testDeployment"
      manifest:
        ref: ""
      discovery:
        namespace: "*"
        byName: "test-deployment"
`,
			wantError: true,
			errorMsg:  "manifest ref cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := Parse([]byte(tt.yaml))
			if tt.wantError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, config)
			}
		})
	}
}

func TestValidateManifestItems(t *testing.T) {
	// Test that ManifestItems (from manifest.refs) are validated
	t.Run("valid ManifestItems are accepted", func(t *testing.T) {
		config := &AdapterConfig{
			APIVersion: "hyperfleet.redhat.com/v1alpha1",
			Kind:       "AdapterConfig",
			Metadata:   Metadata{Name: "test"},
			Spec: AdapterConfigSpec{
				Adapter:       AdapterInfo{Version: "1.0.0"},
				HyperfleetAPI: HyperfleetAPIConfig{Timeout: "5s"},
				Kubernetes:    KubernetesConfig{APIVersion: "v1"},
				Resources: []Resource{
					{
						Name: "multiManifest",
						ManifestItems: []map[string]interface{}{
							{
								"apiVersion": "v1",
								"kind":       "ConfigMap",
								"metadata":   map[string]interface{}{"name": "cm1"},
							},
							{
								"apiVersion": "v1",
								"kind":       "Secret",
								"metadata":   map[string]interface{}{"name": "secret1"},
							},
						},
						Discovery: &DiscoveryConfig{
							Namespace: "*",
							ByName:    "test",
						},
					},
				},
			},
		}

		err := Validate(config)
		assert.NoError(t, err)
	})

	t.Run("invalid ManifestItems are rejected", func(t *testing.T) {
		config := &AdapterConfig{
			APIVersion: "hyperfleet.redhat.com/v1alpha1",
			Kind:       "AdapterConfig",
			Metadata:   Metadata{Name: "test"},
			Spec: AdapterConfigSpec{
				Adapter:       AdapterInfo{Version: "1.0.0"},
				HyperfleetAPI: HyperfleetAPIConfig{Timeout: "5s"},
				Kubernetes:    KubernetesConfig{APIVersion: "v1"},
				Resources: []Resource{
					{
						Name: "multiManifest",
						ManifestItems: []map[string]interface{}{
							{
								"apiVersion": "v1",
								"kind":       "ConfigMap",
								"metadata":   map[string]interface{}{"name": "cm1"},
							},
							{
								// Missing apiVersion, kind, and metadata.name
								"data": map[string]interface{}{"key": "value"},
							},
						},
						Discovery: &DiscoveryConfig{
							Namespace: "*",
							ByName:    "test",
						},
					},
				},
			},
		}

		err := Validate(config)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "manifestItems[1]")
		assert.Contains(t, err.Error(), "missing required Kubernetes field")
	})
}

func TestValidOperators(t *testing.T) {
	// Verify all expected operators are defined in criteria package
	expectedOperators := []string{
		"equals", "notEquals", "in", "notIn",
		"contains", "greaterThan", "lessThan", "exists",
	}

	for _, op := range expectedOperators {
		assert.True(t, criteria.IsValidOperator(op), "operator %s should be valid", op)
	}
}

func TestValidationErrorsFormat(t *testing.T) {
	errors := &ValidationErrors{}
	errors.Add("path.to.field", "some error message")
	errors.Add("another.path", "another error")

	assert.True(t, errors.HasErrors())
	assert.Len(t, errors.Errors, 2)
	assert.Contains(t, errors.Error(), "validation failed with 2 error(s)")
	assert.Contains(t, errors.Error(), "path.to.field: some error message")
	assert.Contains(t, errors.Error(), "another.path: another error")
}

func TestValidate(t *testing.T) {
	// Test that Validate catches multiple errors
	yaml := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
  preconditions:
    - name: "check1"
      conditions:
        - field: "status"
          operator: "badOperator"
          value: "Ready"
    - name: "check2"
      expression: |
        invalid ))) syntax
  resources:
    - name: "testNs"
      manifest:
        kind: Namespace
        metadata:
          name: "test"
      discovery:
        namespace: "*"
        byName: "test"
`
	config, err := Parse([]byte(yaml))
	require.Error(t, err)
	require.Nil(t, config)

	// Should contain multiple validation errors
	assert.Contains(t, err.Error(), "validation failed")
}

func TestBuiltinVariables(t *testing.T) {
	// Test that builtin variables are recognized
	yaml := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  resources:
    - name: "testNs"
      manifest:
        apiVersion: v1
        kind: Namespace
        metadata:
          name: "ns-{{ .metadata.name }}"
          labels:
            adapter: "{{ .metadata.name }}"
      discovery:
        namespace: "*"
        byName: "ns-{{ .metadata.name }}"
`
	config, err := Parse([]byte(yaml))
	require.NoError(t, err)
	require.NotNil(t, config)
}

func TestPayloadValidate(t *testing.T) {
	tests := []struct {
		name      string
		payload   Payload
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid payload with Build only",
			payload: Payload{
				Name:  "test",
				Build: map[string]interface{}{"status": "ready"},
			},
			wantError: false,
		},
		{
			name: "valid payload with BuildRef only",
			payload: Payload{
				Name:     "test",
				BuildRef: "templates/payload.yaml",
			},
			wantError: false,
		},
		{
			name: "invalid - both Build and BuildRef set",
			payload: Payload{
				Name:     "test",
				Build:    map[string]interface{}{"status": "ready"},
				BuildRef: "templates/payload.yaml",
			},
			wantError: true,
			errorMsg:  "build and buildRef are mutually exclusive",
		},
		{
			name: "invalid - neither Build nor BuildRef set",
			payload: Payload{
				Name: "test",
			},
			wantError: true,
			errorMsg:  "either build or buildRef must be set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.payload.Validate()
			if tt.wantError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidatePayloads(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid payload with inline build",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  post:
    payloads:
      - name: "statusPayload"
        build:
          status: "ready"
          message: "completed"
`,
			wantError: false,
		},
		{
			name: "invalid - both build and buildRef specified",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  post:
    payloads:
      - name: "statusPayload"
        build:
          status: "ready"
        buildRef: "templates/payload.yaml"
`,
			wantError: true,
			errorMsg:  "build and buildRef are mutually exclusive",
		},
		{
			name: "invalid - neither build nor buildRef specified",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  post:
    payloads:
      - name: "statusPayload"
`,
			wantError: true,
			errorMsg:  "either build or buildRef must be set",
		},
		{
			name: "invalid - payload name missing",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  post:
    payloads:
      - build:
          status: "ready"
`,
			wantError: true,
			errorMsg:  "name is required",
		},
		{
			name: "error message includes payload context",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  post:
    payloads:
      - name: "myPayload"
`,
			wantError: true,
			errorMsg:  "myPayload",
		},
		{
			name: "multiple payloads - second one invalid",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    baseUrl: "https://test.example.com"
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  post:
    payloads:
      - name: "payload1"
        build:
          status: "ok"
      - name: "payload2"
        build:
          data: "test"
        buildRef: "templates/conflict.yaml"
`,
			wantError: true,
			errorMsg:  "payload2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := Parse([]byte(tt.yaml))
			if tt.wantError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, config)
			}
		})
	}
}

