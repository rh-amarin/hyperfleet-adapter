package config_loader

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"

	"github.com/google/cel-go/cel"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/criteria"
)

// templateVarRegex matches Go template variables like {{ .varName }} or {{ .nested.var }}
var templateVarRegex = regexp.MustCompile(`\{\{\s*\.([a-zA-Z_][a-zA-Z0-9_\.]*)\s*(?:\|[^}]*)?\}\}`)

// -----------------------------------------------------------------------------
// Validators
// -----------------------------------------------------------------------------

// AdapterConfigValidator validates AdapterConfig (deployment configuration)
type AdapterConfigValidator struct {
	config  *AdapterConfig
	baseDir string
	errors  *ValidationErrors
}

// NewAdapterConfigValidator creates a validator for AdapterConfig
func NewAdapterConfigValidator(config *AdapterConfig, baseDir string) *AdapterConfigValidator {
	return &AdapterConfigValidator{
		config:  config,
		baseDir: baseDir,
		errors:  &ValidationErrors{},
	}
}

// ValidateStructure validates the structural requirements of AdapterConfig
func (v *AdapterConfigValidator) ValidateStructure() error {
	if v.config == nil {
		return fmt.Errorf("adapter config is nil")
	}

	// Phase 1: Struct tag validation
	if errs := ValidateStruct(v.config); errs != nil && errs.HasErrors() {
		return fmt.Errorf("%s", errs.First())
	}

	// Phase 2: API version validation
	if !IsSupportedAPIVersion(v.config.APIVersion) {
		return fmt.Errorf("unsupported apiVersion %q (supported: %s)",
			v.config.APIVersion, strings.Join(SupportedAPIVersions, ", "))
	}

	return nil
}

// TaskConfigValidator validates AdapterTaskConfig (task configuration)
type TaskConfigValidator struct {
	config      *AdapterTaskConfig
	baseDir     string
	errors      *ValidationErrors
	definedVars map[string]bool
	celEnv      *cel.Env
}

// NewTaskConfigValidator creates a validator for AdapterTaskConfig
func NewTaskConfigValidator(config *AdapterTaskConfig, baseDir string) *TaskConfigValidator {
	return &TaskConfigValidator{
		config:  config,
		baseDir: baseDir,
		errors:  &ValidationErrors{},
	}
}

// ValidateStructure validates the structural requirements of AdapterTaskConfig
func (v *TaskConfigValidator) ValidateStructure() error {
	if v.config == nil {
		return fmt.Errorf("task config is nil")
	}

	// Phase 1: Struct tag validation
	if errs := ValidateStruct(v.config); errs != nil && errs.HasErrors() {
		return fmt.Errorf("%s", errs.First())
	}

	// Phase 2: API version validation
	if !IsSupportedAPIVersion(v.config.APIVersion) {
		return fmt.Errorf("unsupported apiVersion %q (supported: %s)",
			v.config.APIVersion, strings.Join(SupportedAPIVersions, ", "))
	}

	return nil
}

// ValidateFileReferences validates that all file references in the task config exist
func (v *TaskConfigValidator) ValidateFileReferences() error {
	if v.baseDir == "" {
		return nil
	}

	var errors []string

	// Validate buildRef in spec.post.payloads
	if v.config.Spec.Post != nil {
		for i, payload := range v.config.Spec.Post.Payloads {
			if payload.BuildRef != "" {
				path := fmt.Sprintf("%s.%s.%s[%d].%s", FieldSpec, FieldPost, FieldPayloads, i, FieldBuildRef)
				if err := v.validateFileExists(payload.BuildRef, path); err != nil {
					errors = append(errors, err.Error())
				}
			}
		}
	}

	// Validate manifest.ref in spec.resources
	for i, resource := range v.config.Spec.Resources {
		ref := resource.GetManifestRef()
		if ref != "" {
			path := fmt.Sprintf("%s.%s[%d].%s.%s", FieldSpec, FieldResources, i, FieldManifest, FieldRef)
			if err := v.validateFileExists(ref, path); err != nil {
				errors = append(errors, err.Error())
			}
		}

		// Validate manifestRef in manifests array (Maestro transport)
		for j, nm := range resource.Manifests {
			if nm.ManifestRef != "" {
				path := fmt.Sprintf("%s.%s[%d].%s[%d].%s", FieldSpec, FieldResources, i, FieldManifests, j, FieldManifestRef)
				if err := v.validateFileExists(nm.ManifestRef, path); err != nil {
					errors = append(errors, err.Error())
				}
			}
		}

		// Validate transport.maestro.manifestWork.ref in spec.resources
		if resource.Transport != nil && resource.Transport.Maestro != nil && resource.Transport.Maestro.ManifestWork != nil {
			if resource.Transport.Maestro.ManifestWork.Ref != "" {
				path := fmt.Sprintf("%s.%s[%d].%s.%s.%s.%s", FieldSpec, FieldResources, i, FieldTransport, FieldMaestro, FieldManifestWork, FieldRef)
				if err := v.validateFileExists(resource.Transport.Maestro.ManifestWork.Ref, path); err != nil {
					errors = append(errors, err.Error())
				}
			}
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("file reference errors:\n  - %s", strings.Join(errors, "\n  - "))
	}
	return nil
}

func (v *TaskConfigValidator) validateFileExists(refPath, configPath string) error {
	if refPath == "" {
		return fmt.Errorf("%s: file reference is empty", configPath)
	}

	fullPath, err := resolvePath(v.baseDir, refPath)
	if err != nil {
		return fmt.Errorf("%s: %w", configPath, err)
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s: referenced file %q does not exist (resolved to %q)", configPath, refPath, fullPath)
		}
		return fmt.Errorf("%s: error checking file %q: %w", configPath, refPath, err)
	}

	if info.IsDir() {
		return fmt.Errorf("%s: referenced path %q is a directory, not a file", configPath, refPath)
	}

	return nil
}

// ValidateSemantic performs semantic validation on the task config
func (v *TaskConfigValidator) ValidateSemantic() error {
	if v.config == nil {
		return fmt.Errorf("config is nil")
	}

	// Initialize validation context
	v.collectDefinedVariables()
	if err := v.initCELEnv(); err != nil {
		v.errors.Add("cel", fmt.Sprintf("failed to create CEL environment: %v", err))
	}

	// Run all semantic validators
	v.validateConditionValues()
	v.validateCaptureFieldExpressions()
	v.validateTemplateVariables()
	v.validateCELExpressions()
	v.validateManifestFields()
	v.validateK8sManifests()
	v.validateTransportConfig()

	if v.errors.HasErrors() {
		return v.errors
	}
	return nil
}

func (v *TaskConfigValidator) collectDefinedVariables() {
	v.definedVars = v.config.GetDefinedVariables()
}

// GetDefinedVariables returns all variables defined in the task config
func (c *AdapterTaskConfig) GetDefinedVariables() map[string]bool {
	vars := make(map[string]bool)

	if c == nil {
		return vars
	}

	// Built-in variables
	for _, b := range BuiltinVariables() {
		vars[b] = true
	}

	// Parameters from spec.params
	for _, p := range c.Spec.Params {
		if p.Name != "" {
			vars[p.Name] = true
		}
	}

	// Variables from precondition captures
	for _, precond := range c.Spec.Preconditions {
		for _, capture := range precond.Capture {
			if capture.Name != "" {
				vars[capture.Name] = true
			}
		}
	}

	// Post payloads
	if c.Spec.Post != nil {
		for _, p := range c.Spec.Post.Payloads {
			if p.Name != "" {
				vars[p.Name] = true
			}
		}
	}

	// Resource aliases
	for _, r := range c.Spec.Resources {
		if r.Name != "" {
			vars[FieldResources+"."+r.Name] = true
		}
	}

	return vars
}

func (v *TaskConfigValidator) initCELEnv() error {
	options := make([]cel.EnvOption, 0, len(v.definedVars)+2)
	options = append(options, cel.OptionalTypes())

	addedRoots := make(map[string]bool)

	for varName := range v.definedVars {
		root := varName
		if idx := strings.Index(varName, "."); idx > 0 {
			root = varName[:idx]
		}

		if addedRoots[root] {
			continue
		}
		addedRoots[root] = true

		options = append(options, cel.Variable(root, cel.DynType))
	}

	if !addedRoots[FieldResources] {
		options = append(options, cel.Variable(FieldResources, cel.MapType(cel.StringType, cel.DynType)))
	}

	if !addedRoots[FieldAdapter] {
		options = append(options, cel.Variable(FieldAdapter, cel.MapType(cel.StringType, cel.DynType)))
	}

	env, err := cel.NewEnv(options...)
	if err != nil {
		return err
	}
	v.celEnv = env
	return nil
}

func (v *TaskConfigValidator) validateConditionValues() {
	for i, precond := range v.config.Spec.Preconditions {
		for j, cond := range precond.Conditions {
			path := fmt.Sprintf("%s.%s[%d].%s[%d]", FieldSpec, FieldPreconditions, i, FieldConditions, j)
			v.validateConditionValue(cond.Operator, cond.Value, path)
		}
	}
}

func (v *TaskConfigValidator) validateConditionValue(operator string, value interface{}, path string) {
	op := criteria.Operator(operator)

	if op == criteria.OperatorExists {
		if value != nil {
			v.errors.Add(path, fmt.Sprintf("value/values should not be set for operator \"%s\"", operator))
		}
		return
	}

	if value == nil {
		v.errors.Add(path, fmt.Sprintf("value is required for operator %q", operator))
		return
	}

	if op == criteria.OperatorIn || op == criteria.OperatorNotIn {
		if !isSliceOrArray(value) {
			v.errors.Add(path, fmt.Sprintf("value must be a list for operator %q", operator))
		}
	}
}

func (v *TaskConfigValidator) validateCaptureFieldExpressions() {
	for i, precond := range v.config.Spec.Preconditions {
		for j, capture := range precond.Capture {
			if capture.Expression != "" && v.celEnv != nil {
				path := fmt.Sprintf("%s.%s[%d].%s[%d].%s", FieldSpec, FieldPreconditions, i, FieldCapture, j, FieldExpression)
				v.validateCELExpression(capture.Expression, path)
			}
		}
	}
}

func (v *TaskConfigValidator) validateTemplateVariables() {
	// Validate precondition API call URLs and bodies
	for i, precond := range v.config.Spec.Preconditions {
		if precond.APICall != nil {
			basePath := fmt.Sprintf("%s.%s[%d].%s", FieldSpec, FieldPreconditions, i, FieldAPICall)
			v.validateTemplateString(precond.APICall.URL, basePath+"."+FieldURL)
			v.validateTemplateString(precond.APICall.Body, basePath+"."+FieldBody)
			for j, header := range precond.APICall.Headers {
				v.validateTemplateString(header.Value,
					fmt.Sprintf("%s.%s[%d].%s", basePath, FieldHeaders, j, FieldHeaderValue))
			}
		}
	}

	// Validate resource manifests
	for i, resource := range v.config.Spec.Resources {
		resourcePath := fmt.Sprintf("%s.%s[%d]", FieldSpec, FieldResources, i)

		// Validate single manifest (Kubernetes transport)
		if manifest, ok := resource.Manifest.(map[string]interface{}); ok {
			v.validateTemplateMap(manifest, resourcePath+"."+FieldManifest)
		}

		// Validate manifests array (Maestro transport)
		for j, nm := range resource.Manifests {
			content := nm.GetManifestContent()
			if manifest, ok := content.(map[string]interface{}); ok {
				manifestPath := fmt.Sprintf("%s.%s[%d].%s", resourcePath, FieldManifests, j, FieldManifest)
				v.validateTemplateMap(manifest, manifestPath)
			}
		}

		if resource.Discovery != nil {
			discoveryPath := resourcePath + "." + FieldDiscovery
			v.validateTemplateString(resource.Discovery.Namespace, discoveryPath+"."+FieldNamespace)
			v.validateTemplateString(resource.Discovery.ByName, discoveryPath+"."+FieldByName)
			if resource.Discovery.BySelectors != nil {
				for k, val := range resource.Discovery.BySelectors.LabelSelector {
					v.validateTemplateString(val,
						fmt.Sprintf("%s.%s.%s[%s]", discoveryPath, FieldBySelectors, FieldLabelSelector, k))
				}
			}
		}
	}

	// Validate post action API calls
	if v.config.Spec.Post != nil {
		for i, action := range v.config.Spec.Post.PostActions {
			if action.APICall != nil {
				basePath := fmt.Sprintf("%s.%s.%s[%d].%s", FieldSpec, FieldPost, FieldPostActions, i, FieldAPICall)
				v.validateTemplateString(action.APICall.URL, basePath+"."+FieldURL)
				v.validateTemplateString(action.APICall.Body, basePath+"."+FieldBody)
				for j, header := range action.APICall.Headers {
					v.validateTemplateString(header.Value,
						fmt.Sprintf("%s.%s[%d].%s", basePath, FieldHeaders, j, FieldHeaderValue))
				}
			}
		}

		// Validate post payload build value templates
		for i, payload := range v.config.Spec.Post.Payloads {
			if payload.Build != nil {
				if buildMap, ok := payload.Build.(map[string]interface{}); ok {
					v.validateTemplateMap(buildMap, fmt.Sprintf("%s.%s.%s[%d].%s", FieldSpec, FieldPost, FieldPayloads, i, FieldBuild))
				}
			}
		}
	}
}

func (v *TaskConfigValidator) validateTemplateString(s string, path string) {
	if s == "" {
		return
	}

	matches := templateVarRegex.FindAllStringSubmatch(s, -1)
	for _, match := range matches {
		if len(match) > 1 {
			varName := match[1]
			if !v.isVariableDefined(varName) {
				v.errors.Add(path, fmt.Sprintf("undefined template variable %q", varName))
			}
		}
	}
}

func (v *TaskConfigValidator) isVariableDefined(varName string) bool {
	if v.definedVars[varName] {
		return true
	}

	parts := strings.Split(varName, ".")
	if len(parts) > 0 {
		root := parts[0]

		if v.definedVars[root] {
			return true
		}

		if root == FieldResources && len(parts) > 1 {
			alias := root + "." + parts[1]
			if v.definedVars[alias] {
				return true
			}
		}
	}

	return false
}

func (v *TaskConfigValidator) validateTemplateMap(m map[string]interface{}, path string) {
	for key, value := range m {
		currentPath := fmt.Sprintf("%s.%s", path, key)
		switch val := value.(type) {
		case string:
			v.validateTemplateString(val, currentPath)
		case map[string]interface{}:
			v.validateTemplateMap(val, currentPath)
		case []interface{}:
			for i, item := range val {
				itemPath := fmt.Sprintf("%s[%d]", currentPath, i)
				if str, ok := item.(string); ok {
					v.validateTemplateString(str, itemPath)
				} else if m, ok := item.(map[string]interface{}); ok {
					v.validateTemplateMap(m, itemPath)
				}
			}
		}
	}
}

func (v *TaskConfigValidator) validateCELExpressions() {
	if v.celEnv == nil {
		return
	}

	for i, precond := range v.config.Spec.Preconditions {
		if precond.Expression != "" {
			path := fmt.Sprintf("%s.%s[%d].%s", FieldSpec, FieldPreconditions, i, FieldExpression)
			v.validateCELExpression(precond.Expression, path)
		}
	}

	if v.config.Spec.Post != nil {
		for i, payload := range v.config.Spec.Post.Payloads {
			if payload.Build != nil {
				if buildMap, ok := payload.Build.(map[string]interface{}); ok {
					v.validateBuildExpressions(buildMap, fmt.Sprintf("%s.%s.%s[%d].%s", FieldSpec, FieldPost, FieldPayloads, i, FieldBuild))
				}
			}
		}
	}
}

func (v *TaskConfigValidator) validateCELExpression(expr string, path string) {
	if expr == "" {
		return
	}

	expr = strings.TrimSpace(expr)

	_, issues := v.celEnv.Parse(expr)
	if issues != nil && issues.Err() != nil {
		v.errors.Add(path, fmt.Sprintf("CEL parse error: %v", issues.Err()))
	}
}

func (v *TaskConfigValidator) validateBuildExpressions(m map[string]interface{}, path string) {
	for key, value := range m {
		currentPath := fmt.Sprintf("%s.%s", path, key)
		switch val := value.(type) {
		case string:
			if key == FieldExpression {
				v.validateCELExpression(val, currentPath)
			}
		case map[string]interface{}:
			v.validateBuildExpressions(val, currentPath)
		case []interface{}:
			for i, item := range val {
				itemPath := fmt.Sprintf("%s[%d]", currentPath, i)
				if m, ok := item.(map[string]interface{}); ok {
					v.validateBuildExpressions(m, itemPath)
				}
			}
		}
	}
}

// validateManifestFields validates that manifest/manifests fields are used correctly per transport type
func (v *TaskConfigValidator) validateManifestFields() {
	for i, resource := range v.config.Spec.Resources {
		path := fmt.Sprintf("%s.%s[%d]", FieldSpec, FieldResources, i)
		clientType := resource.Transport.GetClientType()

		switch clientType {
		case TransportClientKubernetes:
			if resource.Manifest == nil {
				v.errors.Add(path, "kubernetes transport requires 'manifest' field")
			}
			if len(resource.Manifests) > 0 {
				v.errors.Add(path, "kubernetes transport does not support 'manifests' array")
			}
		case TransportClientMaestro:
			if len(resource.Manifests) == 0 {
				v.errors.Add(path, "maestro transport requires 'manifests' array")
			}
			if resource.Manifest != nil {
				v.errors.Add(path, "maestro transport uses 'manifests' array, not 'manifest'")
			}
			// Validate each named manifest has either manifest or manifestRef
			for j, nm := range resource.Manifests {
				nmPath := fmt.Sprintf("%s.%s[%d]", path, FieldManifests, j)
				if nm.Manifest == nil && nm.ManifestRef == "" {
					v.errors.Add(nmPath, "named manifest requires either 'manifest' or 'manifestRef'")
				}
				if nm.Manifest != nil && nm.ManifestRef != "" {
					v.errors.Add(nmPath, "'manifest' and 'manifestRef' are mutually exclusive")
				}
			}
		}
	}
}

func (v *TaskConfigValidator) validateK8sManifests() {
	for i, resource := range v.config.Spec.Resources {
		clientType := resource.Transport.GetClientType()

		// For Kubernetes transport, validate the single manifest field
		if clientType == TransportClientKubernetes {
			path := fmt.Sprintf("%s.%s[%d].%s", FieldSpec, FieldResources, i, FieldManifest)

			if manifest, ok := resource.Manifest.(map[string]interface{}); ok {
				if ref, hasRef := manifest[FieldRef].(string); hasRef {
					if ref == "" {
						v.errors.Add(path+"."+FieldRef, "manifest ref cannot be empty")
					}
				} else {
					v.validateK8sManifest(manifest, path)
				}
			}
		}

		// For Maestro transport, validate each manifest in the manifests array
		if clientType == TransportClientMaestro {
			for j, nm := range resource.Manifests {
				path := fmt.Sprintf("%s.%s[%d].%s[%d].%s", FieldSpec, FieldResources, i, FieldManifests, j, FieldManifest)
				content := nm.GetManifestContent()
				if manifest, ok := content.(map[string]interface{}); ok {
					v.validateK8sManifest(manifest, path)
				}
			}
		}
	}
}

func (v *TaskConfigValidator) validateK8sManifest(manifest map[string]interface{}, path string) {
	requiredFields := []string{FieldAPIVersion, FieldKind, FieldMetadata}

	for _, field := range requiredFields {
		if _, ok := manifest[field]; !ok {
			v.errors.Add(path, fmt.Sprintf("missing required Kubernetes field %q", field))
		}
	}

	if metadata, ok := manifest[FieldMetadata].(map[string]interface{}); ok {
		if _, hasName := metadata[FieldName]; !hasName {
			v.errors.Add(path+"."+FieldMetadata, fmt.Sprintf("missing required field %q", FieldName))
		}
	}

	if apiVersion, ok := manifest[FieldAPIVersion].(string); ok {
		if apiVersion == "" {
			v.errors.Add(path+"."+FieldAPIVersion, "apiVersion cannot be empty")
		}
	}

	if kind, ok := manifest[FieldKind].(string); ok {
		if kind == "" {
			v.errors.Add(path+"."+FieldKind, "kind cannot be empty")
		}
	}
}

// validateTransportConfig validates transport configuration for all resources
func (v *TaskConfigValidator) validateTransportConfig() {
	for i, resource := range v.config.Spec.Resources {
		if resource.Transport == nil {
			continue
		}

		basePath := fmt.Sprintf("%s.%s[%d].%s", FieldSpec, FieldResources, i, FieldTransport)

		// Validate maestro config is present when client=maestro
		if resource.Transport.Client == TransportClientMaestro {
			if resource.Transport.Maestro == nil {
				v.errors.Add(basePath, "maestro configuration is required when client is 'maestro'")
				continue
			}

			// Validate targetCluster is present and validate its template variables
			maestroPath := basePath + "." + FieldMaestro
			if resource.Transport.Maestro.TargetCluster == "" {
				v.errors.Add(maestroPath, "targetCluster is required")
			} else {
				// Validate template variables in targetCluster
				v.validateTemplateString(resource.Transport.Maestro.TargetCluster, maestroPath+"."+FieldTargetCluster)
			}

			// Validate manifestWork.name template if present
			if resource.Transport.Maestro.ManifestWork != nil && resource.Transport.Maestro.ManifestWork.Name != "" {
				v.validateTemplateString(resource.Transport.Maestro.ManifestWork.Name, maestroPath+"."+FieldManifestWork+"."+FieldName)
			}
		}
	}
}

// =============================================================================
// HELPER FUNCTIONS
// =============================================================================

func isSliceOrArray(value interface{}) bool {
	if value == nil {
		return false
	}
	kind := reflect.TypeOf(value).Kind()
	return kind == reflect.Slice || kind == reflect.Array
}

// IsSupportedAPIVersion checks if the given apiVersion is supported
func IsSupportedAPIVersion(apiVersion string) bool {
	for _, v := range SupportedAPIVersions {
		if v == apiVersion {
			return true
		}
	}
	return false
}

// ValidateAdapterVersion validates the config's adapter version matches the expected version
func ValidateAdapterVersion(config *AdapterConfig, expectedVersion string) error {
	if expectedVersion == "" {
		return nil
	}

	configVersion := config.Spec.Adapter.Version
	if configVersion != expectedVersion {
		return fmt.Errorf("adapter version mismatch: config %q != adapter %q",
			configVersion, expectedVersion)
	}

	return nil
}
