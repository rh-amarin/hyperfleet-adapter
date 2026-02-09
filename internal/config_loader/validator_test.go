package config_loader

import (
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/criteria"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// baseTaskConfig returns a minimal valid AdapterTaskConfig for testing.
// Tests can modify the returned config to set up specific scenarios.
func baseTaskConfig() *AdapterTaskConfig {
	return &AdapterTaskConfig{
		APIVersion: "hyperfleet.redhat.com/v1alpha1",
		Kind:       "AdapterTaskConfig",
		Metadata:   Metadata{Name: "test-adapter"},
		Spec:       AdapterTaskSpec{},
	}
}

// newTaskValidator is a helper that creates a TaskConfigValidator with semantic validation
func newTaskValidator(cfg *AdapterTaskConfig) *TaskConfigValidator {
	return NewTaskConfigValidator(cfg, "")
}

func TestValidateConditionOperators(t *testing.T) {
	// Helper to create task config with a single condition
	withCondition := func(cond Condition) *AdapterTaskConfig {
		cfg := baseTaskConfig()
		cfg.Spec.Preconditions = []Precondition{{
			ActionBase: ActionBase{Name: "checkStatus"},
			Conditions: []Condition{cond},
		}}
		return cfg
	}

	t.Run("valid operators", func(t *testing.T) {
		cfg := baseTaskConfig()
		cfg.Spec.Preconditions = []Precondition{{
			ActionBase: ActionBase{Name: "checkStatus"},
			Conditions: []Condition{
				{Field: "status", Operator: "equals", Value: "Ready"},
				{Field: "provider", Operator: "in", Value: []interface{}{"aws", "gcp"}},
				{Field: "vpcId", Operator: "exists"},
			},
		}}
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		require.NoError(t, v.ValidateSemantic())
	})

	t.Run("invalid operator", func(t *testing.T) {
		cfg := withCondition(Condition{Field: "status", Operator: "invalidOp", Value: "Ready"})
		err := newTaskValidator(cfg).ValidateStructure()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid operator")
	})

	t.Run("missing operator", func(t *testing.T) {
		cfg := withCondition(Condition{Field: "status", Value: "Ready"})
		err := newTaskValidator(cfg).ValidateStructure()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "operator")
	})

	t.Run("missing value for equals operator", func(t *testing.T) {
		cfg := withCondition(Condition{Field: "status", Operator: "equals"})
		v := newTaskValidator(cfg)
		_ = v.ValidateStructure()
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "value is required for operator \"equals\"")
	})

	t.Run("missing value for in operator", func(t *testing.T) {
		cfg := withCondition(Condition{Field: "provider", Operator: "in"})
		v := newTaskValidator(cfg)
		_ = v.ValidateStructure()
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "value is required for operator \"in\"")
	})

	t.Run("non-list value for in operator", func(t *testing.T) {
		cfg := withCondition(Condition{Field: "provider", Operator: "in", Value: "aws"})
		v := newTaskValidator(cfg)
		_ = v.ValidateStructure()
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "value must be a list for operator \"in\"")
	})

	t.Run("non-list value for notIn operator", func(t *testing.T) {
		cfg := withCondition(Condition{Field: "provider", Operator: "notIn", Value: "aws"})
		v := newTaskValidator(cfg)
		_ = v.ValidateStructure()
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "value must be a list for operator \"notIn\"")
	})

	t.Run("exists operator without value is valid", func(t *testing.T) {
		cfg := withCondition(Condition{Field: "vpcId", Operator: "exists"})
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		require.NoError(t, v.ValidateSemantic())
	})

	t.Run("exists operator with value should fail", func(t *testing.T) {
		cfg := withCondition(Condition{Field: "vpcId", Operator: "exists", Value: "any-value"})
		v := newTaskValidator(cfg)
		_ = v.ValidateStructure()
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "value/values should not be set for operator \"exists\"")
	})

	t.Run("exists operator with list value should fail", func(t *testing.T) {
		cfg := withCondition(Condition{Field: "vpcId", Operator: "exists", Value: []interface{}{"a", "b"}})
		v := newTaskValidator(cfg)
		_ = v.ValidateStructure()
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "value/values should not be set for operator \"exists\"")
	})

	t.Run("missing value for greaterThan operator", func(t *testing.T) {
		cfg := withCondition(Condition{Field: "count", Operator: "greaterThan"})
		v := newTaskValidator(cfg)
		_ = v.ValidateStructure()
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "value is required for operator \"greaterThan\"")
	})
}

func TestValidateTemplateVariables(t *testing.T) {
	t.Run("defined variables", func(t *testing.T) {
		cfg := baseTaskConfig()
		cfg.Spec.Params = []Parameter{
			{Name: "clusterId", Source: "event.id"},
			{Name: "apiUrl", Source: "env.API_URL"},
		}
		cfg.Spec.Preconditions = []Precondition{{
			ActionBase: ActionBase{
				Name:    "checkCluster",
				APICall: &APICall{Method: "GET", URL: "{{ .apiUrl }}/clusters/{{ .clusterId }}"},
			},
		}}
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		require.NoError(t, v.ValidateSemantic())
	})

	t.Run("undefined variable in URL", func(t *testing.T) {
		cfg := baseTaskConfig()
		cfg.Spec.Params = []Parameter{{Name: "clusterId", Source: "event.id"}}
		cfg.Spec.Preconditions = []Precondition{{
			ActionBase: ActionBase{
				Name:    "checkCluster",
				APICall: &APICall{Method: "GET", URL: "{{ .undefinedVar }}/clusters/{{ .clusterId }}"},
			},
		}}
		v := newTaskValidator(cfg)
		_ = v.ValidateStructure()
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "undefined template variable \"undefinedVar\"")
	})

	t.Run("undefined variable in resource manifest", func(t *testing.T) {
		cfg := baseTaskConfig()
		cfg.Spec.Params = []Parameter{{Name: "clusterId", Source: "event.id"}}
		cfg.Spec.Resources = []Resource{{
			Name: "testNs",
			Manifest: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Namespace",
				"metadata":   map[string]interface{}{"name": "ns-{{ .undefinedVar }}"},
			},
			Discovery: &DiscoveryConfig{Namespace: "*", ByName: "ns-{{ .clusterId }}"},
		}}
		v := newTaskValidator(cfg)
		_ = v.ValidateStructure()
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "undefined template variable \"undefinedVar\"")
	})

	t.Run("captured variable is available for resources", func(t *testing.T) {
		cfg := baseTaskConfig()
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
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		require.NoError(t, v.ValidateSemantic())
	})
}

func TestValidateCELExpressions(t *testing.T) {
	// Helper to create config with a CEL expression precondition
	withExpression := func(expr string) *AdapterTaskConfig {
		cfg := baseTaskConfig()
		cfg.Spec.Preconditions = []Precondition{{ActionBase: ActionBase{Name: "check"}, Expression: expr}}
		return cfg
	}

	t.Run("valid CEL expression", func(t *testing.T) {
		cfg := withExpression(`clusterPhase == "Ready" || clusterPhase == "Provisioning"`)
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		require.NoError(t, v.ValidateSemantic())
	})

	t.Run("invalid CEL expression - syntax error", func(t *testing.T) {
		cfg := withExpression(`clusterPhase ==== "Ready"`)
		v := newTaskValidator(cfg)
		_ = v.ValidateStructure()
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "CEL parse error")
	})

	t.Run("valid CEL with has() function", func(t *testing.T) {
		cfg := withExpression(`has(cluster.status) && cluster.status.phase == "Ready"`)
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		require.NoError(t, v.ValidateSemantic())
	})
}

func TestValidateK8sManifests(t *testing.T) {
	// Helper to create config with a resource manifest
	withResource := func(manifest map[string]interface{}) *AdapterTaskConfig {
		cfg := baseTaskConfig()
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
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		require.NoError(t, v.ValidateSemantic())
	})

	t.Run("missing apiVersion in manifest", func(t *testing.T) {
		cfg := withResource(map[string]interface{}{
			"kind":     "Namespace",
			"metadata": map[string]interface{}{"name": "test"},
		})
		v := newTaskValidator(cfg)
		_ = v.ValidateStructure()
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required Kubernetes field \"apiVersion\"")
	})

	t.Run("missing kind in manifest", func(t *testing.T) {
		cfg := withResource(map[string]interface{}{
			"apiVersion": "v1",
			"metadata":   map[string]interface{}{"name": "test"},
		})
		v := newTaskValidator(cfg)
		_ = v.ValidateStructure()
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required Kubernetes field \"kind\"")
	})

	t.Run("missing metadata in manifest", func(t *testing.T) {
		cfg := withResource(map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
		})
		v := newTaskValidator(cfg)
		_ = v.ValidateStructure()
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required Kubernetes field \"metadata\"")
	})

	t.Run("missing name in metadata", func(t *testing.T) {
		cfg := withResource(map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata":   map[string]interface{}{"labels": map[string]interface{}{"app": "test"}},
		})
		v := newTaskValidator(cfg)
		_ = v.ValidateStructure()
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required field \"name\"")
	})

	t.Run("valid manifest ref", func(t *testing.T) {
		cfg := withResource(map[string]interface{}{"ref": "templates/deployment.yaml"})
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		require.NoError(t, v.ValidateSemantic())
	})

	t.Run("empty manifest ref", func(t *testing.T) {
		cfg := withResource(map[string]interface{}{"ref": ""})
		v := newTaskValidator(cfg)
		_ = v.ValidateStructure()
		err := v.ValidateSemantic()
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

func TestValidateSemantic(t *testing.T) {
	// Test that ValidateSemantic catches multiple errors
	cfg := baseTaskConfig()
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

	v := newTaskValidator(cfg)
	_ = v.ValidateStructure()
	err := v.ValidateSemantic()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validation failed")
}

func TestBuiltinVariables(t *testing.T) {
	// Test that builtin variables (like metadata.name) are recognized
	cfg := baseTaskConfig()
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
	v := newTaskValidator(cfg)
	require.NoError(t, v.ValidateStructure())
	require.NoError(t, v.ValidateSemantic())
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

func TestValidateCaptureFields(t *testing.T) {
	// Helper to create config with capture fields
	withCapture := func(captures []CaptureField) *AdapterTaskConfig {
		cfg := baseTaskConfig()
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
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		require.NoError(t, v.ValidateSemantic())
	})

	t.Run("valid capture with expression only", func(t *testing.T) {
		cfg := withCapture([]CaptureField{{Name: "activeCount", FieldExpressionDef: FieldExpressionDef{Expression: "1 + 1"}}})
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		require.NoError(t, v.ValidateSemantic())
	})

	t.Run("invalid - both field and expression set", func(t *testing.T) {
		cfg := withCapture([]CaptureField{{Name: "conflicting", FieldExpressionDef: FieldExpressionDef{Field: "metadata.name", Expression: "1 + 1"}}})
		err := newTaskValidator(cfg).ValidateStructure()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mutually exclusive")
	})

	t.Run("invalid - neither field nor expression set", func(t *testing.T) {
		cfg := withCapture([]CaptureField{{Name: "empty"}})
		err := newTaskValidator(cfg).ValidateStructure()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must have either")
	})

	t.Run("invalid - capture name missing", func(t *testing.T) {
		cfg := withCapture([]CaptureField{{FieldExpressionDef: FieldExpressionDef{Field: "metadata.name"}}})
		err := newTaskValidator(cfg).ValidateStructure()
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

func TestTransportConfigGetClientType(t *testing.T) {
	t.Run("nil transport returns kubernetes", func(t *testing.T) {
		var tc *TransportConfig
		assert.Equal(t, TransportClientKubernetes, tc.GetClientType())
	})

	t.Run("empty client returns kubernetes", func(t *testing.T) {
		tc := &TransportConfig{}
		assert.Equal(t, TransportClientKubernetes, tc.GetClientType())
	})

	t.Run("kubernetes client returns kubernetes", func(t *testing.T) {
		tc := &TransportConfig{Client: TransportClientKubernetes}
		assert.Equal(t, TransportClientKubernetes, tc.GetClientType())
	})

	t.Run("maestro client returns maestro", func(t *testing.T) {
		tc := &TransportConfig{Client: TransportClientMaestro}
		assert.Equal(t, TransportClientMaestro, tc.GetClientType())
	})
}

func TestValidateTransportConfig(t *testing.T) {
	validManifest := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]interface{}{
			"name":      "test-cm",
			"namespace": "default",
		},
	}

	// Helper to create resource with transport config for Kubernetes transport
	withK8sTransport := func(transport *TransportConfig) *AdapterTaskConfig {
		cfg := baseTaskConfig()
		cfg.Spec.Params = []Parameter{
			{Name: "targetCluster", Source: "event.targetCluster"},
		}
		cfg.Spec.Resources = []Resource{{
			Name:      "testResource",
			Transport: transport,
			Manifest:  validManifest,
			Discovery: &DiscoveryConfig{
				Namespace: "default",
				ByName:    "test-cm",
			},
		}}
		return cfg
	}

	// Helper to create resource with transport config for Maestro transport
	withMaestroTransport := func(transport *TransportConfig) *AdapterTaskConfig {
		cfg := baseTaskConfig()
		cfg.Spec.Params = []Parameter{
			{Name: "targetCluster", Source: "event.targetCluster"},
		}
		cfg.Spec.Resources = []Resource{{
			Name:      "testResource",
			Transport: transport,
			Manifests: []NamedManifest{
				{Name: "configmap", Manifest: validManifest},
			},
			Discovery: &DiscoveryConfig{
				Namespace: "default",
				ByName:    "test-cm",
			},
		}}
		return cfg
	}

	t.Run("valid kubernetes transport", func(t *testing.T) {
		cfg := withK8sTransport(&TransportConfig{Client: TransportClientKubernetes})
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		require.NoError(t, v.ValidateSemantic())
	})

	t.Run("valid nil transport defaults to kubernetes", func(t *testing.T) {
		cfg := withK8sTransport(nil)
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		require.NoError(t, v.ValidateSemantic())
	})

	t.Run("valid maestro transport", func(t *testing.T) {
		cfg := withMaestroTransport(&TransportConfig{
			Client: TransportClientMaestro,
			Maestro: &MaestroTransportConfig{
				TargetCluster: "{{ .targetCluster }}",
			},
		})
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		require.NoError(t, v.ValidateSemantic())
	})

	t.Run("valid maestro transport with manifestWork name", func(t *testing.T) {
		cfg := withMaestroTransport(&TransportConfig{
			Client: TransportClientMaestro,
			Maestro: &MaestroTransportConfig{
				TargetCluster: "{{ .targetCluster }}",
				ManifestWork: &ManifestWorkConfig{
					Name: "work-{{ .targetCluster }}",
				},
			},
		})
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		require.NoError(t, v.ValidateSemantic())
	})

	t.Run("invalid maestro transport missing maestro config", func(t *testing.T) {
		cfg := withMaestroTransport(&TransportConfig{
			Client: TransportClientMaestro,
		})
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		// Semantic validation catches missing maestro config
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maestro configuration is required")
	})

	t.Run("invalid maestro transport missing targetCluster", func(t *testing.T) {
		cfg := withMaestroTransport(&TransportConfig{
			Client:  TransportClientMaestro,
			Maestro: &MaestroTransportConfig{},
		})
		v := newTaskValidator(cfg)
		// Struct validation catches this via required tag on TargetCluster
		err := v.ValidateStructure()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "targetCluster")
	})

	t.Run("invalid maestro transport undefined template variable", func(t *testing.T) {
		cfg := withMaestroTransport(&TransportConfig{
			Client: TransportClientMaestro,
			Maestro: &MaestroTransportConfig{
				TargetCluster: "{{ .undefinedVar }}",
			},
		})
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "undefined template variable")
	})
}

func TestValidateManifestFields(t *testing.T) {
	validManifest := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata":   map[string]interface{}{"name": "test-namespace"},
	}

	t.Run("kubernetes transport requires manifest field", func(t *testing.T) {
		cfg := baseTaskConfig()
		cfg.Spec.Params = []Parameter{
			{Name: "targetCluster", Source: "event.targetCluster"},
		}
		cfg.Spec.Resources = []Resource{{
			Name:      "testResource",
			Transport: &TransportConfig{Client: TransportClientKubernetes},
			// Missing Manifest field
			Discovery: &DiscoveryConfig{Namespace: "*", ByName: "test"},
		}}
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "kubernetes transport requires 'manifest' field")
	})

	t.Run("kubernetes transport does not support manifests array", func(t *testing.T) {
		cfg := baseTaskConfig()
		cfg.Spec.Resources = []Resource{{
			Name:      "testResource",
			Transport: &TransportConfig{Client: TransportClientKubernetes},
			Manifest:  validManifest,
			Manifests: []NamedManifest{
				{Name: "ns", Manifest: validManifest},
			},
			Discovery: &DiscoveryConfig{Namespace: "*", ByName: "test"},
		}}
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "kubernetes transport does not support 'manifests' array")
	})

	t.Run("maestro transport requires manifests array", func(t *testing.T) {
		cfg := baseTaskConfig()
		cfg.Spec.Params = []Parameter{
			{Name: "targetCluster", Source: "event.targetCluster"},
		}
		cfg.Spec.Resources = []Resource{{
			Name: "testResource",
			Transport: &TransportConfig{
				Client:  TransportClientMaestro,
				Maestro: &MaestroTransportConfig{TargetCluster: "{{ .targetCluster }}"},
			},
			// Missing Manifests array
			Discovery: &DiscoveryConfig{Namespace: "*", ByName: "test"},
		}}
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maestro transport requires 'manifests' array")
	})

	t.Run("maestro transport does not support manifest field", func(t *testing.T) {
		cfg := baseTaskConfig()
		cfg.Spec.Params = []Parameter{
			{Name: "targetCluster", Source: "event.targetCluster"},
		}
		cfg.Spec.Resources = []Resource{{
			Name: "testResource",
			Transport: &TransportConfig{
				Client:  TransportClientMaestro,
				Maestro: &MaestroTransportConfig{TargetCluster: "{{ .targetCluster }}"},
			},
			Manifest: validManifest, // Should not be used with maestro
			Manifests: []NamedManifest{
				{Name: "ns", Manifest: validManifest},
			},
			Discovery: &DiscoveryConfig{Namespace: "*", ByName: "test"},
		}}
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maestro transport uses 'manifests' array, not 'manifest'")
	})

	t.Run("valid maestro transport with manifests array", func(t *testing.T) {
		cfg := baseTaskConfig()
		cfg.Spec.Params = []Parameter{
			{Name: "targetCluster", Source: "event.targetCluster"},
		}
		cfg.Spec.Resources = []Resource{{
			Name: "testResource",
			Transport: &TransportConfig{
				Client:  TransportClientMaestro,
				Maestro: &MaestroTransportConfig{TargetCluster: "{{ .targetCluster }}"},
			},
			Manifests: []NamedManifest{
				{Name: "namespace", Manifest: validManifest},
				{Name: "configmap", Manifest: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata":   map[string]interface{}{"name": "test-cm", "namespace": "default"},
				}},
			},
			Discovery: &DiscoveryConfig{Namespace: "*", ByName: "test"},
		}}
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		require.NoError(t, v.ValidateSemantic())
	})

	t.Run("named manifest requires manifest or manifestRef", func(t *testing.T) {
		cfg := baseTaskConfig()
		cfg.Spec.Params = []Parameter{
			{Name: "targetCluster", Source: "event.targetCluster"},
		}
		cfg.Spec.Resources = []Resource{{
			Name: "testResource",
			Transport: &TransportConfig{
				Client:  TransportClientMaestro,
				Maestro: &MaestroTransportConfig{TargetCluster: "{{ .targetCluster }}"},
			},
			Manifests: []NamedManifest{
				{Name: "empty"}, // Missing both manifest and manifestRef
			},
			Discovery: &DiscoveryConfig{Namespace: "*", ByName: "test"},
		}}
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "named manifest requires either 'manifest' or 'manifestRef'")
	})

	t.Run("named manifest cannot have both manifest and manifestRef", func(t *testing.T) {
		cfg := baseTaskConfig()
		cfg.Spec.Params = []Parameter{
			{Name: "targetCluster", Source: "event.targetCluster"},
		}
		cfg.Spec.Resources = []Resource{{
			Name: "testResource",
			Transport: &TransportConfig{
				Client:  TransportClientMaestro,
				Maestro: &MaestroTransportConfig{TargetCluster: "{{ .targetCluster }}"},
			},
			Manifests: []NamedManifest{
				{Name: "conflicting", Manifest: validManifest, ManifestRef: "templates/manifest.yaml"},
			},
			Discovery: &DiscoveryConfig{Namespace: "*", ByName: "test"},
		}}
		v := newTaskValidator(cfg)
		require.NoError(t, v.ValidateStructure())
		err := v.ValidateSemantic()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "'manifest' and 'manifestRef' are mutually exclusive")
	})
}

func TestNamedManifestAccessors(t *testing.T) {
	t.Run("GetManifestContent returns manifest when set", func(t *testing.T) {
		manifest := map[string]interface{}{"apiVersion": "v1", "kind": "Namespace"}
		nm := &NamedManifest{Name: "test", Manifest: manifest}
		content := nm.GetManifestContent()
		assert.Equal(t, manifest, content)
	})

	t.Run("GetManifestContent returns refContent when set", func(t *testing.T) {
		manifest := map[string]interface{}{"apiVersion": "v1", "kind": "Namespace"}
		refContent := map[string]interface{}{"apiVersion": "v1", "kind": "ConfigMap"}
		nm := &NamedManifest{Name: "test", Manifest: manifest, ManifestRefContent: refContent}
		content := nm.GetManifestContent()
		assert.Equal(t, refContent, content, "should prefer refContent over manifest")
	})

	t.Run("GetManifestContent returns nil for nil receiver", func(t *testing.T) {
		var nm *NamedManifest
		assert.Nil(t, nm.GetManifestContent())
	})

	t.Run("HasManifestRef returns true when manifestRef is set", func(t *testing.T) {
		nm := &NamedManifest{Name: "test", ManifestRef: "templates/manifest.yaml"}
		assert.True(t, nm.HasManifestRef())
	})

	t.Run("HasManifestRef returns false when manifestRef is empty", func(t *testing.T) {
		nm := &NamedManifest{Name: "test", Manifest: map[string]interface{}{}}
		assert.False(t, nm.HasManifestRef())
	})

	t.Run("HasManifestRef returns false for nil receiver", func(t *testing.T) {
		var nm *NamedManifest
		assert.False(t, nm.HasManifestRef())
	})
}
