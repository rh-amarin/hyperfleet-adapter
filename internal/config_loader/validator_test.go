package config_loader

import (
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/criteria"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// baseConfig returns a minimal valid AdapterConfig for testing.
// Tests can modify the returned config to set up specific scenarios.
func baseConfig() *AdapterConfig {
	return &AdapterConfig{
		APIVersion: "hyperfleet.redhat.com/v1alpha1",
		Kind:       "AdapterConfig",
		Metadata:   Metadata{Name: "test-adapter"},
		Spec: AdapterConfigSpec{
			Adapter:       AdapterInfo{Version: "1.0.0"},
			HyperfleetAPI: HyperfleetAPIConfig{BaseURL: "https://test.example.com", Timeout: "5s"},
			Kubernetes:    KubernetesConfig{APIVersion: "v1"},
		},
	}
}

func TestValidateConditionOperators(t *testing.T) {
	// Helper to create config with a single condition
	withCondition := func(cond Condition) *AdapterConfig {
		cfg := baseConfig()
		cfg.Spec.Preconditions = []Precondition{{
			ActionBase: ActionBase{Name: "checkStatus"},
			Conditions: []Condition{cond},
		}}
		return cfg
	}

	t.Run("valid operators", func(t *testing.T) {
		cfg := baseConfig()
		cfg.Spec.Preconditions = []Precondition{{
			ActionBase: ActionBase{Name: "checkStatus"},
			Conditions: []Condition{
				{Field: "status", Operator: "equals", Value: "Ready"},
				{Field: "provider", Operator: "in", Value: []interface{}{"aws", "gcp"}},
				{Field: "vpcId", Operator: "exists"},
			},
		}}
		assert.NoError(t, newValidator(cfg).Validate())
	})

	t.Run("invalid operator", func(t *testing.T) {
		cfg := withCondition(Condition{Field: "status", Operator: "invalidOp", Value: "Ready"})
		err := newValidator(cfg).ValidateStructure()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid operator")
	})

	t.Run("missing operator", func(t *testing.T) {
		cfg := withCondition(Condition{Field: "status", Value: "Ready"})
		err := newValidator(cfg).ValidateStructure()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "operator")
	})

	t.Run("missing value for equals operator", func(t *testing.T) {
		cfg := withCondition(Condition{Field: "status", Operator: "equals"})
		err := newValidator(cfg).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "value is required for operator \"equals\"")
	})

	t.Run("missing value for in operator", func(t *testing.T) {
		cfg := withCondition(Condition{Field: "provider", Operator: "in"})
		err := newValidator(cfg).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "value is required for operator \"in\"")
	})

	t.Run("non-list value for in operator", func(t *testing.T) {
		cfg := withCondition(Condition{Field: "provider", Operator: "in", Value: "aws"})
		err := newValidator(cfg).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "value must be a list for operator \"in\"")
	})

	t.Run("non-list value for notIn operator", func(t *testing.T) {
		cfg := withCondition(Condition{Field: "provider", Operator: "notIn", Value: "aws"})
		err := newValidator(cfg).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "value must be a list for operator \"notIn\"")
	})

	t.Run("exists operator without value is valid", func(t *testing.T) {
		cfg := withCondition(Condition{Field: "vpcId", Operator: "exists"})
		assert.NoError(t, newValidator(cfg).Validate())
	})

	t.Run("exists operator with value should fail", func(t *testing.T) {
		cfg := withCondition(Condition{Field: "vpcId", Operator: "exists", Value: "any-value"})
		err := newValidator(cfg).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "value/values should not be set for operator \"exists\"")
	})

	t.Run("exists operator with list value should fail", func(t *testing.T) {
		cfg := withCondition(Condition{Field: "vpcId", Operator: "exists", Value: []interface{}{"a", "b"}})
		err := newValidator(cfg).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "value/values should not be set for operator \"exists\"")
	})

	t.Run("missing value for greaterThan operator", func(t *testing.T) {
		cfg := withCondition(Condition{Field: "count", Operator: "greaterThan"})
		err := newValidator(cfg).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "value is required for operator \"greaterThan\"")
	})
}

func TestValidateTemplateVariables(t *testing.T) {
	t.Run("defined variables", func(t *testing.T) {
		cfg := baseConfig()
		cfg.Spec.Params = []Parameter{
			{Name: "clusterId", Source: "event.cluster_id"},
			{Name: "apiUrl", Source: "env.API_URL"},
		}
		cfg.Spec.Preconditions = []Precondition{{
			ActionBase: ActionBase{
				Name:    "checkCluster",
				APICall: &APICall{Method: "GET", URL: "{{ .apiUrl }}/clusters/{{ .clusterId }}"},
			},
		}}
		assert.NoError(t, newValidator(cfg).Validate())
	})

	t.Run("undefined variable in URL", func(t *testing.T) {
		cfg := baseConfig()
		cfg.Spec.Params = []Parameter{{Name: "clusterId", Source: "event.cluster_id"}}
		cfg.Spec.Preconditions = []Precondition{{
			ActionBase: ActionBase{
				Name:    "checkCluster",
				APICall: &APICall{Method: "GET", URL: "{{ .undefinedVar }}/clusters/{{ .clusterId }}"},
			},
		}}
		err := newValidator(cfg).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "undefined template variable \"undefinedVar\"")
	})

	t.Run("undefined variable in resource manifest", func(t *testing.T) {
		cfg := baseConfig()
		cfg.Spec.Params = []Parameter{{Name: "clusterId", Source: "event.cluster_id"}}
		cfg.Spec.Resources = []Resource{{
			Name: "testNs",
			Manifest: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Namespace",
				"metadata":   map[string]interface{}{"name": "ns-{{ .undefinedVar }}"},
			},
			Discovery: &DiscoveryConfig{Namespace: "*", ByName: "ns-{{ .clusterId }}"},
		}}
		err := newValidator(cfg).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "undefined template variable \"undefinedVar\"")
	})

	t.Run("captured variable is available for resources", func(t *testing.T) {
		cfg := baseConfig()
		cfg.Spec.Params = []Parameter{{Name: "apiUrl", Source: "env.API_URL"}}
		cfg.Spec.Preconditions = []Precondition{{
			ActionBase: ActionBase{
				Name:    "getCluster",
				APICall: &APICall{Method: "GET", URL: "{{ .apiUrl }}/clusters"},
			},
			Capture: []CaptureField{{Name: "clusterName", FieldExpressionDef: FieldExpressionDef{Field: "metadata.name"}}},
		}}
		cfg.Spec.Resources = []Resource{{
			Name: "testNs",
			Manifest: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Namespace",
				"metadata":   map[string]interface{}{"name": "ns-{{ .clusterName }}"},
			},
			Discovery: &DiscoveryConfig{Namespace: "*", ByName: "ns-{{ .clusterName }}"},
		}}
		assert.NoError(t, newValidator(cfg).Validate())
	})
}

func TestValidateCELExpressions(t *testing.T) {
	// Helper to create config with a CEL expression precondition
	withExpression := func(expr string) *AdapterConfig {
		cfg := baseConfig()
		cfg.Spec.Preconditions = []Precondition{{ActionBase: ActionBase{Name: "check"}, Expression: expr}}
		return cfg
	}

	t.Run("valid CEL expression", func(t *testing.T) {
		cfg := withExpression(`clusterPhase == "Ready" || clusterPhase == "Provisioning"`)
		assert.NoError(t, newValidator(cfg).Validate())
	})

	t.Run("invalid CEL expression - syntax error", func(t *testing.T) {
		cfg := withExpression(`clusterPhase ==== "Ready"`)
		err := newValidator(cfg).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "CEL parse error")
	})

	t.Run("valid CEL with has() function", func(t *testing.T) {
		cfg := withExpression(`has(cluster.status) && cluster.status.phase == "Ready"`)
		assert.NoError(t, newValidator(cfg).Validate())
	})
}

func TestValidateK8sManifests(t *testing.T) {
	// Helper to create config with a resource manifest
	withResource := func(manifest map[string]interface{}) *AdapterConfig {
		cfg := baseConfig()
		cfg.Spec.Resources = []Resource{{
			Name:      "testResource",
			Manifest:  manifest,
			Discovery: &DiscoveryConfig{Namespace: "*", ByName: "test"},
		}}
		return cfg
	}

	// Complete valid manifest
	validManifest := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata":   map[string]interface{}{"name": "test-namespace", "labels": map[string]interface{}{"app": "test"}},
	}

	t.Run("valid K8s manifest", func(t *testing.T) {
		cfg := withResource(validManifest)
		assert.NoError(t, newValidator(cfg).Validate())
	})

	t.Run("missing apiVersion in manifest", func(t *testing.T) {
		cfg := withResource(map[string]interface{}{
			"kind":     "Namespace",
			"metadata": map[string]interface{}{"name": "test"},
		})
		err := newValidator(cfg).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required Kubernetes field \"apiVersion\"")
	})

	t.Run("missing kind in manifest", func(t *testing.T) {
		cfg := withResource(map[string]interface{}{
			"apiVersion": "v1",
			"metadata":   map[string]interface{}{"name": "test"},
		})
		err := newValidator(cfg).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required Kubernetes field \"kind\"")
	})

	t.Run("missing metadata in manifest", func(t *testing.T) {
		cfg := withResource(map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
		})
		err := newValidator(cfg).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required Kubernetes field \"metadata\"")
	})

	t.Run("missing name in metadata", func(t *testing.T) {
		cfg := withResource(map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata":   map[string]interface{}{"labels": map[string]interface{}{"app": "test"}},
		})
		err := newValidator(cfg).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required field \"name\"")
	})

	t.Run("valid manifest ref", func(t *testing.T) {
		cfg := withResource(map[string]interface{}{"ref": "templates/deployment.yaml"})
		assert.NoError(t, newValidator(cfg).Validate())
	})

	t.Run("empty manifest ref", func(t *testing.T) {
		cfg := withResource(map[string]interface{}{"ref": ""})
		err := newValidator(cfg).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "manifest ref cannot be empty")
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
	cfg := baseConfig()
	cfg.Spec.Preconditions = []Precondition{
		{ActionBase: ActionBase{Name: "check1"}, Conditions: []Condition{{Field: "status", Operator: "badOperator", Value: "Ready"}}},
		{ActionBase: ActionBase{Name: "check2"}, Expression: "invalid ))) syntax"},
	}
	cfg.Spec.Resources = []Resource{{
		Name: "testNs",
		Manifest: map[string]interface{}{
			"kind":     "Namespace", // missing apiVersion
			"metadata": map[string]interface{}{"name": "test"},
		},
		Discovery: &DiscoveryConfig{Namespace: "*", ByName: "test"},
	}}

	err := newValidator(cfg).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validation failed")
}

func TestBuiltinVariables(t *testing.T) {
	// Test that builtin variables (like metadata.name) are recognized
	cfg := baseConfig()
	cfg.Spec.Resources = []Resource{{
		Name: "testNs",
		Manifest: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"name":   "ns-{{ .metadata.name }}",
				"labels": map[string]interface{}{"adapter": "{{ .metadata.name }}"},
			},
		},
		Discovery: &DiscoveryConfig{Namespace: "*", ByName: "ns-{{ .metadata.name }}"},
	}}
	assert.NoError(t, newValidator(cfg).Validate())
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
			errorMsg:  "mutually exclusive",
		},
		{
			name: "invalid - neither Build nor BuildRef set",
			payload: Payload{
				Name: "test",
			},
			wantError: true,
			errorMsg:  "must have either 'build' or 'buildRef' set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateStruct(&tt.payload)
			if tt.wantError {
				require.NotNil(t, errs)
				require.True(t, errs.HasErrors())
				assert.Contains(t, errs.Error(), tt.errorMsg)
			} else {
				if errs != nil {
					assert.False(t, errs.HasErrors(), "unexpected error: %v", errs)
				}
			}
		})
	}
}

func TestValidatePayloads(t *testing.T) {
	// Payload validation runs via SchemaValidator during Parse(), so we use Parse() here.
	// Helper builds minimal YAML with just the payload section varying.
	parseWithPayloads := func(payloadsYAML string) (*AdapterConfig, error) {
		yaml := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  post:
    payloads:
` + payloadsYAML
		return Parse([]byte(yaml))
	}

	t.Run("valid payload with inline build", func(t *testing.T) {
		_, err := parseWithPayloads(`      - name: "statusPayload"
        build:
          status: "ready"`)
		assert.NoError(t, err)
	})

	t.Run("invalid - both build and buildRef specified", func(t *testing.T) {
		_, err := parseWithPayloads(`      - name: "statusPayload"
        build:
          status: "ready"
        buildRef: "templates/payload.yaml"`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mutually exclusive")
	})

	t.Run("invalid - neither build nor buildRef specified", func(t *testing.T) {
		_, err := parseWithPayloads(`      - name: "statusPayload"`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must have either")
	})

	t.Run("invalid - payload name missing", func(t *testing.T) {
		_, err := parseWithPayloads(`      - build:
          status: "ready"`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "name is required")
	})

	t.Run("multiple payloads - second one invalid", func(t *testing.T) {
		_, err := parseWithPayloads(`      - name: "payload1"
        build:
          status: "ok"
      - name: "payload2"
        build:
          data: "test"
        buildRef: "templates/conflict.yaml"`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "payloads[1]")
	})
}

func TestValidateCaptureFields(t *testing.T) {
	// Helper to create config with capture fields
	withCapture := func(captures []CaptureField) *AdapterConfig {
		cfg := baseConfig()
		cfg.Spec.Preconditions = []Precondition{{
			ActionBase: ActionBase{
				Name:    "getStatus",
				APICall: &APICall{Method: "GET", URL: "http://example.com/api"},
			},
			Capture: captures,
		}}
		return cfg
	}

	t.Run("valid capture with field only", func(t *testing.T) {
		cfg := withCapture([]CaptureField{
			{Name: "clusterName", FieldExpressionDef: FieldExpressionDef{Field: "metadata.name"}},
			{Name: "clusterPhase", FieldExpressionDef: FieldExpressionDef{Field: "status.phase"}},
		})
		assert.NoError(t, newValidator(cfg).Validate())
	})

	t.Run("valid capture with expression only", func(t *testing.T) {
		cfg := withCapture([]CaptureField{{Name: "activeCount", FieldExpressionDef: FieldExpressionDef{Expression: "1 + 1"}}})
		assert.NoError(t, newValidator(cfg).Validate())
	})

	t.Run("invalid - both field and expression set", func(t *testing.T) {
		cfg := withCapture([]CaptureField{{Name: "conflicting", FieldExpressionDef: FieldExpressionDef{Field: "metadata.name", Expression: "1 + 1"}}})
		err := newValidator(cfg).ValidateStructure()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mutually exclusive")
	})

	t.Run("invalid - neither field nor expression set", func(t *testing.T) {
		cfg := withCapture([]CaptureField{{Name: "empty"}})
		err := newValidator(cfg).ValidateStructure()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must have either")
	})

	t.Run("invalid - capture name missing", func(t *testing.T) {
		cfg := withCapture([]CaptureField{{FieldExpressionDef: FieldExpressionDef{Field: "metadata.name"}}})
		err := newValidator(cfg).ValidateStructure()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "name is required")
	})
}

func TestYamlFieldName(t *testing.T) {
	// Ensure validator is initialized (populates fieldNameCache)
	getStructValidator()

	tests := []struct {
		goFieldName  string
		expectedYaml string
	}{
		{"ByName", "byName"},
		{"BySelectors", "bySelectors"},
		{"Field", "field"},
		{"Expression", "expression"},
		{"APIVersion", "apiVersion"},
		{"Name", "name"},
		{"Namespace", "namespace"},
		{"LabelSelector", "labelSelector"},
	}

	for _, tt := range tests {
		t.Run(tt.goFieldName, func(t *testing.T) {
			result := yamlFieldName(tt.goFieldName)
			assert.Equal(t, tt.expectedYaml, result)
		})
	}
}

func TestFieldNameCachePopulated(t *testing.T) {
	// Ensure validator is initialized
	getStructValidator()

	// Verify key fields are in the cache
	expectedFields := []string{
		"ByName", "BySelectors", "Field", "Expression",
		"Name", "Namespace", "APIVersion", "Kind",
	}

	for _, field := range expectedFields {
		t.Run(field, func(t *testing.T) {
			_, ok := fieldNameCache[field]
			assert.True(t, ok, "field %s should be in cache", field)
		})
	}
}
