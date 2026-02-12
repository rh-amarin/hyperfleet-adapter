package config_loader

import (
	"fmt"
	"strings"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	"gopkg.in/yaml.v3"
)

// Config is the unified configuration passed throughout the application.
// Created by merging AdapterConfig (deployment) and AdapterTaskConfig (task).
type Config struct {
	APIVersion string     `yaml:"apiVersion"`
	Kind       string     `yaml:"kind"`
	Metadata   Metadata   `yaml:"metadata"`
	Spec       ConfigSpec `yaml:"spec"`
}

// ConfigSpec contains the merged specification from both deployment and task configs
type ConfigSpec struct {
	// From AdapterConfig (deployment)
	Adapter     AdapterInfo   `yaml:"adapter"`
	Clients     ClientsConfig `yaml:"clients"`
	DebugConfig bool          `yaml:"debugConfig,omitempty"`

	// From AdapterTaskConfig (business logic)
	Params        []Parameter    `yaml:"params,omitempty"`
	Preconditions []Precondition `yaml:"preconditions,omitempty"`
	Resources     []Resource     `yaml:"resources,omitempty"`
	Post          *PostConfig    `yaml:"post,omitempty"`
}

// GetParams returns the parameters from the config spec
func (c *Config) GetParams() []Parameter {
	if c == nil {
		return nil
	}
	return c.Spec.Params
}

// GetMetadata returns the metadata from the config
func (c *Config) GetMetadata() Metadata {
	if c == nil {
		return Metadata{}
	}
	return c.Metadata
}

// Merge combines AdapterConfig (deployment) and AdapterTaskConfig (task) into a unified Config.
// The metadata is taken from the task config since it contains the adapter task name.
// The adapter info and clients come from the deployment config.
// The params, preconditions, resources, and post-processing come from the task config.
func Merge(adapterCfg *AdapterConfig, taskCfg *AdapterTaskConfig) *Config {
	if adapterCfg == nil || taskCfg == nil {
		return nil
	}

	return &Config{
		APIVersion: adapterCfg.APIVersion,
		Kind:       ExpectedKindConfig,
		Metadata:   taskCfg.Metadata, // Use task metadata for adapter name
		Spec: ConfigSpec{
			// From deployment config
			Adapter:     adapterCfg.Spec.Adapter,
			Clients:     adapterCfg.Spec.Clients,
			DebugConfig: adapterCfg.Spec.DebugConfig,
			// From task config
			Params:        taskCfg.Spec.Params,
			Preconditions: taskCfg.Spec.Preconditions,
			Resources:     taskCfg.Spec.Resources,
			Post:          taskCfg.Spec.Post,
		},
	}
}

// FieldExpressionDef represents a common pattern for value extraction.
// Used when a value should be computed via field extraction (JSONPath) or CEL expression.
// Only one of Field or Expression should be set.
type FieldExpressionDef struct {
	// Field uses JSONPath/dot notation to extract value (mutually exclusive with Expression)
	Field string `yaml:"field,omitempty" validate:"required_without=Expression,excluded_with=Expression"`
	// Expression uses CEL expression to evaluate (mutually exclusive with Field)
	Expression string `yaml:"expression,omitempty" validate:"required_without=Field,excluded_with=Field"`
}

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
	FieldExpressionDef `yaml:",inline"`
	Default            any `yaml:"default"` // Default value if extraction fails or returns nil
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

// Metadata contains the adapter metadata
type Metadata struct {
	Name   string            `yaml:"name" mapstructure:"name" validate:"required"`
	Labels map[string]string `yaml:"labels,omitempty" mapstructure:"labels"`
}

// AdapterInfo contains basic adapter information
type AdapterInfo struct {
	Version string `yaml:"version" mapstructure:"version" validate:"required"`
}

// HyperfleetAPIConfig is the HyperFleet API client configuration.
// Alias to hyperfleet_api.ClientConfig to ensure shared schema.
type HyperfleetAPIConfig = hyperfleet_api.ClientConfig

// BrokerConfig contains broker consumer configuration
type BrokerConfig struct {
	SubscriptionID string `yaml:"subscriptionId,omitempty" mapstructure:"subscriptionId"`
	Topic          string `yaml:"topic,omitempty" mapstructure:"topic"`
}

// KubernetesConfig contains Kubernetes configuration
type KubernetesConfig struct {
	APIVersion string `yaml:"apiVersion" mapstructure:"apiVersion"`
	// KubeConfigPath is the path to a kubeconfig file. Empty means in-cluster auth.
	KubeConfigPath string `yaml:"kubeConfigPath,omitempty" mapstructure:"kubeConfigPath"`
	// QPS is the client-side rate limit. Zero uses defaults.
	QPS float32 `yaml:"qps,omitempty" mapstructure:"qps"`
	// Burst is the client-side burst rate. Zero uses defaults.
	Burst int `yaml:"burst,omitempty" mapstructure:"burst"`
}

// Parameter represents a parameter extraction configuration.
// Parameters are extracted from external sources (event data, env vars) using Source.
type Parameter struct {
	Name        string      `yaml:"name" validate:"required"`
	Source      string      `yaml:"source,omitempty" validate:"required"`
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
	Name string `yaml:"name" validate:"required"`
	// Build contains a structure that will be evaluated and converted to JSON at runtime.
	// The structure is kept as raw interface{} to allow flexible schema definitions.
	// Mutually exclusive with BuildRef.
	Build interface{} `yaml:"build,omitempty" validate:"required_without=BuildRef,excluded_with=BuildRef"`
	// BuildRef references an external YAML file containing the build definition.
	// Mutually exclusive with Build.
	BuildRef string `yaml:"buildRef,omitempty" validate:"required_without=Build,excluded_with=Build"`
	// BuildRefContent holds the loaded content from BuildRef file (populated by loader)
	BuildRefContent map[string]interface{} `yaml:"-"`
}

// Validate checks that exactly one of Build or BuildRef is set.
func (p *Payload) Validate() error {
	hasBuild := p.Build != nil
	hasBuildRef := p.BuildRef != ""

	if !hasBuild && !hasBuildRef {
		return fmt.Errorf("either 'build' or 'buildRef' must be set")
	}
	if hasBuild && hasBuildRef {
		return fmt.Errorf("'build' and 'buildRef' are mutually exclusive")
	}
	return nil
}

// ActionBase contains common fields for action-like configurations.
// Used by Precondition and PostAction to reduce duplication.
type ActionBase struct {
	Name    string     `yaml:"name" validate:"required"`
	APICall *APICall   `yaml:"apiCall,omitempty" validate:"omitempty"`
	Log     *LogAction `yaml:"log,omitempty"`
}

// Precondition represents a precondition check.
// Must have at least one of: APICall (from ActionBase), Expression, or Conditions.
type Precondition struct {
	ActionBase `yaml:",inline"`
	Capture    []CaptureField `yaml:"capture,omitempty" validate:"dive"`
	Conditions []Condition    `yaml:"conditions,omitempty" validate:"dive,required_without_all=ActionBase.APICall Expression"`
	Expression string         `yaml:"expression,omitempty" validate:"required_without_all=ActionBase.APICall Conditions"`
}

// APICall represents an API call configuration
type APICall struct {
	Method        string   `yaml:"method" validate:"required,oneof=GET POST PUT PATCH DELETE"`
	URL           string   `yaml:"url" validate:"required"`
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
	Name               string `yaml:"name" validate:"required"`
	FieldExpressionDef `yaml:",inline"`
}

// Condition represents a structured condition
type Condition struct {
	Field    string      `yaml:"field"`
	Operator string      `yaml:"operator" validate:"required,validoperator"`
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

// TransportConfig specifies which transport client to use for a resource
type TransportConfig struct {
	// Client is the transport client type: "kubernetes" or "maestro"
	Client string `yaml:"client" validate:"required,oneof=kubernetes maestro"`
	// Maestro contains maestro-specific transport settings (required when Client is "maestro")
	Maestro *MaestroTransportConfig `yaml:"maestro,omitempty"`
}

// MaestroTransportConfig contains maestro-specific transport settings
type MaestroTransportConfig struct {
	// TargetCluster is the name of the target cluster (consumer) for ManifestWork delivery
	TargetCluster string `yaml:"targetCluster" validate:"required"`
}

// Resource represents a resource configuration.
// The manifest field holds either a K8s resource (for kubernetes transport)
// or a ManifestWork (for maestro transport). The transport client determines
// how to parse and apply it.
type Resource struct {
	Name             string           `yaml:"name" validate:"required,resourcename"`
	Transport        *TransportConfig `yaml:"transport,omitempty"`
	Manifest         interface{}      `yaml:"manifest,omitempty"`
	RecreateOnChange bool             `yaml:"recreateOnChange,omitempty"`
	Discovery        *DiscoveryConfig `yaml:"discovery,omitempty" validate:"required"`
	// NestedDiscoveries defines how to discover individual sub-resources within the applied manifest.
	// For example, discovering resources inside a ManifestWork's workload.
	NestedDiscoveries []NestedDiscovery `yaml:"nestedDiscoveries,omitempty" validate:"dive"`
}

// NestedDiscovery defines a named discovery for a sub-resource within the parent manifest.
type NestedDiscovery struct {
	Name      string           `yaml:"name" validate:"required,resourcename"`
	Discovery *DiscoveryConfig `yaml:"discovery" validate:"required"`
}

// DiscoveryConfig represents resource discovery configuration
type DiscoveryConfig struct {
	Namespace   string          `yaml:"namespace,omitempty"`
	ByName      string          `yaml:"byName,omitempty" validate:"required_without=BySelectors"`
	BySelectors *SelectorConfig `yaml:"bySelectors,omitempty" validate:"required_without=ByName,omitempty"`
}

// SelectorConfig represents label selector configuration
type SelectorConfig struct {
	LabelSelector map[string]string `yaml:"labelSelector,omitempty" validate:"required,min=1"`
}

// PostConfig represents post-processing configuration
type PostConfig struct {
	Payloads    []Payload    `yaml:"payloads,omitempty" validate:"dive"`
	PostActions []PostAction `yaml:"postActions,omitempty" validate:"dive"`
}

// PostAction represents a post-processing action
type PostAction struct {
	ActionBase `yaml:",inline"`
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

// -----------------------------------------------------------------------------
// Validation Errors
// -----------------------------------------------------------------------------

// ValidationError represents a validation error with context
type ValidationError struct {
	Path    string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}

// ValidationErrors holds multiple validation errors
type ValidationErrors struct {
	Errors []ValidationError
}

func (ve *ValidationErrors) Error() string {
	if len(ve.Errors) == 0 {
		return "no validation errors"
	}
	var msgs []string
	for _, e := range ve.Errors {
		msgs = append(msgs, e.Error())
	}
	return fmt.Sprintf("validation failed with %d error(s):\n  - %s", len(ve.Errors), strings.Join(msgs, "\n  - "))
}

func (ve *ValidationErrors) Add(path, message string) {
	ve.Errors = append(ve.Errors, ValidationError{Path: path, Message: message})
}

// Extend appends all errors from another ValidationErrors
func (ve *ValidationErrors) Extend(other *ValidationErrors) {
	if other != nil {
		ve.Errors = append(ve.Errors, other.Errors...)
	}
}

// First returns the first error message, or empty string if no errors
func (ve *ValidationErrors) First() string {
	if len(ve.Errors) == 0 {
		return ""
	}
	return ve.Errors[0].Message
}

// Count returns the number of errors
func (ve *ValidationErrors) Count() int {
	return len(ve.Errors)
}

func (ve *ValidationErrors) HasErrors() bool {
	return len(ve.Errors) > 0
}

// AdapterConfig represents the deployment-level configuration.
// Contains infrastructure settings that can be overridden via environment variables
// and CLI flags using Viper.
type AdapterConfig struct {
	APIVersion string            `yaml:"apiVersion" mapstructure:"apiVersion" validate:"required"`
	Kind       string            `yaml:"kind" mapstructure:"kind" validate:"required,eq=AdapterConfig"`
	Metadata   Metadata          `yaml:"metadata" mapstructure:"metadata"`
	Spec       AdapterConfigSpec `yaml:"spec" mapstructure:"spec"`
}

// AdapterConfigSpec contains the deployment specification
type AdapterConfigSpec struct {
	Adapter     AdapterInfo   `yaml:"adapter" mapstructure:"adapter"`
	Clients     ClientsConfig `yaml:"clients" mapstructure:"clients"`
	DebugConfig bool          `yaml:"debugConfig,omitempty" mapstructure:"debugConfig"`
}

// ClientsConfig contains configuration for all external clients
type ClientsConfig struct {
	Maestro       *MaestroClientConfig `yaml:"maestro,omitempty" mapstructure:"maestro"`
	HyperfleetAPI HyperfleetAPIConfig  `yaml:"hyperfleetApi" mapstructure:"hyperfleetApi"`
	Broker        BrokerConfig         `yaml:"broker,omitempty" mapstructure:"broker"`
	Kubernetes    KubernetesConfig     `yaml:"kubernetes" mapstructure:"kubernetes"`
}

// MaestroClientConfig contains Maestro client configuration
type MaestroClientConfig struct {
	GRPCServerAddress string            `yaml:"grpcServerAddress" mapstructure:"grpcServerAddress"`
	HTTPServerAddress string            `yaml:"httpServerAddress" mapstructure:"httpServerAddress"`
	SourceID          string            `yaml:"sourceId" mapstructure:"sourceId"`
	ClientID          string            `yaml:"clientId" mapstructure:"clientId"`
	Auth              MaestroAuthConfig `yaml:"auth" mapstructure:"auth"`
	Timeout           string            `yaml:"timeout" mapstructure:"timeout"`
	RetryAttempts     int               `yaml:"retryAttempts" mapstructure:"retryAttempts"`
	Keepalive         *KeepaliveConfig  `yaml:"keepalive,omitempty" mapstructure:"keepalive"`
	Insecure          bool              `yaml:"insecure,omitempty" mapstructure:"insecure"`
}

// MaestroAuthConfig contains authentication configuration for Maestro
type MaestroAuthConfig struct {
	Type      string     `yaml:"type" mapstructure:"type"` // "tls" or "none"
	TLSConfig *TLSConfig `yaml:"tlsConfig,omitempty" mapstructure:"tlsConfig"`
}

// TLSConfig contains TLS certificate configuration
type TLSConfig struct {
	CAFile   string `yaml:"caFile" mapstructure:"caFile"`
	CertFile string `yaml:"certFile" mapstructure:"certFile"`
	KeyFile  string `yaml:"keyFile" mapstructure:"keyFile"`
}

// KeepaliveConfig contains gRPC keepalive configuration
type KeepaliveConfig struct {
	Time    string `yaml:"time" mapstructure:"time"`
	Timeout string `yaml:"timeout" mapstructure:"timeout"`
}

// AdapterTaskConfig represents the business logic configuration.
// Contains params, preconditions, resources, and post-processing actions.
// This config is loaded from YAML without environment variable overrides.
type AdapterTaskConfig struct {
	APIVersion string          `yaml:"apiVersion" validate:"required"`
	Kind       string          `yaml:"kind" validate:"required,eq=AdapterTaskConfig"`
	Metadata   Metadata        `yaml:"metadata"`
	Spec       AdapterTaskSpec `yaml:"spec"`
}

// AdapterTaskSpec contains the task specification
type AdapterTaskSpec struct {
	Params        []Parameter    `yaml:"params,omitempty" validate:"dive"`
	Preconditions []Precondition `yaml:"preconditions,omitempty" validate:"dive"`
	Resources     []Resource     `yaml:"resources,omitempty" validate:"unique=Name,dive"`
	Post          *PostConfig    `yaml:"post,omitempty" validate:"omitempty"`
}
