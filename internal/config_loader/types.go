package config_loader

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ValueDef represents a dynamic value definition in payload builds.
// Used when a payload field should be computed via field extraction (JSONPath) or CEL expression.
// Only one of Field or Expression should be set.
//
// Example YAML with field (JSONPath):
//
//	status:
//	  field: "response.status"
//	  default: "unknown"
//
// Example YAML with expression (CEL):
//
//	status:
//	  expression: "adapter.?errorMessage.orValue(\"\")"
//	  default: "success"
type ValueDef struct {
	Field      string `yaml:"field"`      // JSONPath/dot notation to extract value
	Expression string `yaml:"expression"` // CEL expression to evaluate
	Default    any    `yaml:"default"`    // Default value if extraction fails or returns nil
}

// ParseValueDef attempts to parse a value as a ValueDef.
// Returns the parsed ValueDef and true if the value contains either field or expression.
// Returns nil and false if the value is not a value definition.
func ParseValueDef(v any) (*ValueDef, bool) {
	// Must be a map to be a value definition
	if _, ok := v.(map[string]any); !ok {
		return nil, false
	}

	// Marshal to YAML then unmarshal to ValueDef
	data, err := yaml.Marshal(v)
	if err != nil {
		return nil, false
	}

	var valueDef ValueDef
	if err := yaml.Unmarshal(data, &valueDef); err != nil {
		return nil, false
	}

	// Must have at least one of field or expression
	if valueDef.Field == "" && valueDef.Expression == "" {
		return nil, false
	}

	return &valueDef, true
}

// AdapterConfig represents the complete adapter configuration structure
type AdapterConfig struct {
	APIVersion string            `yaml:"apiVersion"`
	Kind       string            `yaml:"kind"`
	Metadata   Metadata          `yaml:"metadata"`
	Spec       AdapterConfigSpec `yaml:"spec"`
}

// Metadata contains the adapter metadata
type Metadata struct {
	Name      string            `yaml:"name"`
	Namespace string            `yaml:"namespace"`
	Labels    map[string]string `yaml:"labels,omitempty"`
}

// AdapterConfigSpec contains the adapter specification
type AdapterConfigSpec struct {
	Adapter       AdapterInfo         `yaml:"adapter"`
	HyperfleetAPI HyperfleetAPIConfig `yaml:"hyperfleetApi"`
	Kubernetes    KubernetesConfig    `yaml:"kubernetes"`
	Params        []Parameter         `yaml:"params,omitempty"`
	Preconditions []Precondition      `yaml:"preconditions,omitempty"`
	Resources     []Resource          `yaml:"resources,omitempty"`
	Post          *PostConfig         `yaml:"post,omitempty"`
}

// AdapterInfo contains basic adapter information
type AdapterInfo struct {
	Version string `yaml:"version"`
}

// HyperfleetAPIConfig contains HyperFleet API configuration
type HyperfleetAPIConfig struct {
	BaseURL       string `yaml:"baseUrl,omitempty"`
	Timeout       string `yaml:"timeout"`
	RetryAttempts int    `yaml:"retryAttempts"`
	RetryBackoff  string `yaml:"retryBackoff"`
}

// KubernetesConfig contains Kubernetes configuration
type KubernetesConfig struct {
	APIVersion string `yaml:"apiVersion"`
}

// Parameter represents a parameter extraction configuration.
// Parameters are extracted from external sources (event data, env vars) using Source.
type Parameter struct {
	Name        string      `yaml:"name"`
	Source      string      `yaml:"source,omitempty"`
	Type        string      `yaml:"type,omitempty"`
	Description string      `yaml:"description,omitempty"`
	Required    bool        `yaml:"required,omitempty"`
	Default     interface{} `yaml:"default,omitempty"`
}

// Payload represents a dynamically built payload for post-processing.
// Payloads are computed internally using expressions and build definitions.
//
// IMPORTANT: Build and BuildRef are mutually exclusive - exactly one must be set.
// Setting both or neither will result in a validation error.
// - Use Build for inline payload definitions directly in the config
// - Use BuildRef to reference an external YAML file containing the build definition
type Payload struct {
	Name string `yaml:"name"`
	// Build contains a structure that will be evaluated and converted to JSON at runtime.
	// The structure is kept as raw interface{} to allow flexible schema definitions.
	// Mutually exclusive with BuildRef.
	Build interface{} `yaml:"build,omitempty"`
	// BuildRef references an external YAML file containing the build definition.
	// Mutually exclusive with Build.
	BuildRef string `yaml:"buildRef,omitempty"`
	// BuildRefContent holds the loaded content from BuildRef file (populated by loader)
	BuildRefContent map[string]interface{} `yaml:"-"`
}

// Validate checks that the Payload configuration is valid.
// Returns an error if:
// - Both Build and BuildRef are set (mutually exclusive)
// - Neither Build nor BuildRef is set (one is required)
func (p *Payload) Validate() error {
	hasBuild := p.Build != nil
	hasBuildRef := p.BuildRef != ""

	if hasBuild && hasBuildRef {
		return fmt.Errorf("build and buildRef are mutually exclusive; set only one")
	}
	if !hasBuild && !hasBuildRef {
		return fmt.Errorf("either build or buildRef must be set")
	}
	return nil
}

// Precondition represents a precondition check
type Precondition struct {
	Name       string         `yaml:"name"`
	APICall    *APICall       `yaml:"apiCall,omitempty"`
	Capture    []CaptureField `yaml:"capture,omitempty"`
	Conditions []Condition    `yaml:"conditions,omitempty"`
	Expression string         `yaml:"expression,omitempty"`
	Log        *LogAction     `yaml:"log,omitempty"`
}

// APICall represents an API call configuration
type APICall struct {
	Method        string   `yaml:"method"`
	URL           string   `yaml:"url"`
	Timeout       string   `yaml:"timeout,omitempty"`
	RetryAttempts int      `yaml:"retryAttempts,omitempty"`
	RetryBackoff  string   `yaml:"retryBackoff,omitempty"`
	Headers       []Header `yaml:"headers,omitempty"`
	Body          string   `yaml:"body,omitempty"`
}

// Header represents an HTTP header
type Header struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// CaptureField represents a field capture configuration from API response.
//
// Supports two modes (mutually exclusive):
//   - Field: JSONPath expression for simple field extraction (e.g., "{.items[0].name}")
//   - Expression: CEL expression for complex transformations (e.g., "response.items.filter(i, i.adapter == 'x')")
type CaptureField struct {
	Name       string `yaml:"name"`
	Field      string `yaml:"field,omitempty"`
	Expression string `yaml:"expression,omitempty"`
}

// Condition represents a structured condition
type Condition struct {
	Field    string      `yaml:"field"`
	Operator string      `yaml:"operator"`
	Value    interface{} `yaml:"-"` // Populated by UnmarshalYAML from "value" or "values"
}

// conditionRaw is used for custom unmarshaling to support both "value" and "values" keys
type conditionRaw struct {
	Field    string      `yaml:"field"`
	Operator string      `yaml:"operator"`
	Value    interface{} `yaml:"value"`
	Values   interface{} `yaml:"values"` // Alias for Value
}

// UnmarshalYAML implements custom unmarshaling to support both "value" and "values" keys
func (c *Condition) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw conditionRaw
	if err := unmarshal(&raw); err != nil {
		return err
	}

	c.Field = raw.Field
	c.Operator = raw.Operator

	// Fail if both "value" and "values" are specified
	if raw.Value != nil && raw.Values != nil {
		return fmt.Errorf("condition has both 'value' and 'values' keys; use only one")
	}

	// Use whichever key is provided
	if raw.Values != nil {
		c.Value = raw.Values
	} else {
		c.Value = raw.Value
	}

	return nil
}

// Resource represents a Kubernetes resource configuration
type Resource struct {
	Name             string           `yaml:"name"`
	Manifest         interface{}      `yaml:"manifest,omitempty"`
	RecreateOnChange bool             `yaml:"recreateOnChange,omitempty"`
	Discovery        *DiscoveryConfig `yaml:"discovery,omitempty"`
	// ManifestItems holds loaded content when manifest.ref is an array (populated by loader)
	ManifestItems []map[string]interface{} `yaml:"-"`
}

// DiscoveryConfig represents resource discovery configuration
type DiscoveryConfig struct {
	Namespace   string          `yaml:"namespace,omitempty"`
	ByName      string          `yaml:"byName,omitempty"`
	BySelectors *SelectorConfig `yaml:"bySelectors,omitempty"`
}

// SelectorConfig represents label selector configuration
type SelectorConfig struct {
	LabelSelector map[string]string `yaml:"labelSelector,omitempty"`
}

// PostConfig represents post-processing configuration
type PostConfig struct {
	Payloads    []Payload    `yaml:"payloads,omitempty"`
	PostActions []PostAction `yaml:"postActions,omitempty"`
}

// PostAction represents a post-processing action
type PostAction struct {
	Name    string     `yaml:"name"`
	APICall *APICall   `yaml:"apiCall,omitempty"`
	Log     *LogAction `yaml:"log,omitempty"`
}

// LogAction represents a logging action that can be configured in the adapter config
type LogAction struct {
	Message string `yaml:"message"`
	Level   string `yaml:"level,omitempty"` // debug, info, warning, error (default: info)
}

// ManifestRef represents a manifest reference
type ManifestRef struct {
	Ref string `yaml:"ref"`
}
