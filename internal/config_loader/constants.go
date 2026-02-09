package config_loader

// Field path constants for configuration structure.
// These constants define the known field names used in adapter configuration
// to avoid hardcoding strings throughout the codebase.

// Top-level field names
const (
	FieldSpec     = "spec"
	FieldMetadata = "metadata"
)

// Spec section field names
const (
	FieldAdapter       = "adapter"
	FieldHyperfleetAPI = "hyperfleetApi"
	FieldKubernetes    = "kubernetes"
	FieldParams        = "params"
	FieldPreconditions = "preconditions"
	FieldResources     = "resources"
	FieldPost          = "post"
)

// Adapter field names
const (
	FieldVersion = "version"
)

// Parameter field names
const (
	FieldName        = "name"
	FieldSource      = "source"
	FieldType        = "type"
	FieldDescription = "description"
	FieldRequired    = "required"
	FieldDefault     = "default"
)

// Payload field names (for post.payloads)
const (
	FieldPayloads = "payloads"
	FieldBuild    = "build"
	FieldBuildRef = "buildRef"
)

// Precondition field names
const (
	FieldAPICall    = "apiCall"
	FieldCapture    = "capture"
	FieldConditions = "conditions"
	FieldExpression = "expression"
)

// API call field names
const (
	FieldMethod  = "method"
	FieldURL     = "url"
	FieldTimeout = "timeout"
	FieldHeaders = "headers"
	FieldBody    = "body"
)

// Header field names
const (
	FieldHeaderValue = "value"
)

// Condition field names
const (
	FieldField    = "field"
	FieldOperator = "operator"
	FieldValue    = "value"  // Supports any type including lists for operators like "in", "notIn"
	FieldValues   = "values" // YAML alias for Value - both "value" and "values" are accepted in YAML
)

// Resource field names
const (
	FieldManifest         = "manifest"
	FieldManifests        = "manifests"
	FieldManifestRef      = "manifestRef"
	FieldRecreateOnChange = "recreateOnChange"
	FieldDiscovery        = "discovery"
	FieldTransport        = "transport"
)

// Transport field names
const (
	FieldClient        = "client"
	FieldMaestro       = "maestro"
	FieldTargetCluster = "targetCluster"
	FieldManifestWork  = "manifestWork"
)

// Manifest reference field names
const (
	FieldRef = "ref"
)

// Discovery field names
const (
	FieldNamespace   = "namespace"
	FieldByName      = "byName"
	FieldBySelectors = "bySelectors"
)

// Selector field names
const (
	FieldLabelSelector = "labelSelector"
)

// Post config field names
const (
	FieldPostActions = "postActions"
)

// Kubernetes manifest field names
const (
	FieldAPIVersion = "apiVersion"
	FieldKind       = "kind"
)
