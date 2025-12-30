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
// Validator
// -----------------------------------------------------------------------------

// Validator performs all validation on AdapterConfig:
// - Structural validation (required fields, formats)
// - Semantic validation (CEL expressions, templates, operators, K8s manifests)
type Validator struct {
	config      *AdapterConfig
	baseDir     string // Base directory for file reference validation
	errors      *ValidationErrors
	definedVars map[string]bool // All defined variables (params + resources + captures)
	celEnv      *cel.Env
}

// NewValidator creates a new Validator for the given config
func NewValidator(config *AdapterConfig, baseDir string) *Validator {
	return &Validator{
		config:  config,
		baseDir: baseDir,
		errors:  &ValidationErrors{},
	}
}

// ValidateStructure performs structural validation (required fields, formats).
// Uses go-playground/validator for basic struct validation, then runs custom validations.
// Returns error on first failure (fail-fast).
func (v *Validator) ValidateStructure() error {
	// Phase 1: Struct tag validation (required fields, formats, enums)
	if errs := ValidateStruct(v.config); errs != nil && errs.HasErrors() {
		return fmt.Errorf("%s", errs.First())
	}

	// Phase 2: Custom validations that can't be expressed via struct tags
	if err := v.validateAPIVersionSupported(); err != nil {
		return err
	}

	return nil
}

// ValidateFileReferences validates that all file references exist.
// Only meaningful if baseDir is set.
func (v *Validator) ValidateFileReferences() error {
	if v.baseDir == "" {
		return nil
	}
	return v.validateFileReferences()
}

// ValidateSemantic performs semantic validation (CEL, templates, operators, K8s).
// Collects all errors rather than failing fast.
func (v *Validator) ValidateSemantic() error {
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
	v.validateK8sManifests()

	if v.errors.HasErrors() {
		return v.errors
	}
	return nil
}

// =============================================================================
// CUSTOM STRUCTURAL VALIDATION
// =============================================================================
// These validations can't be expressed via struct tags and run after tag validation.

// validateAPIVersionSupported checks that the apiVersion is in the supported list
func (v *Validator) validateAPIVersionSupported() error {
	if !IsSupportedAPIVersion(v.config.APIVersion) {
		return fmt.Errorf("unsupported apiVersion %q (supported: %s)",
			v.config.APIVersion, strings.Join(SupportedAPIVersions, ", "))
	}
	return nil
}

// =============================================================================
// FILE REFERENCE VALIDATION
// =============================================================================

func (v *Validator) validateFileReferences() error {
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
	}

	if len(errors) > 0 {
		return fmt.Errorf("file reference errors:\n  - %s", strings.Join(errors, "\n  - "))
	}
	return nil
}

func (v *Validator) validateFileExists(refPath, configPath string) error {
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

// =============================================================================
// SEMANTIC VALIDATION: Operators
// =============================================================================

func (v *Validator) collectDefinedVariables() {
	v.definedVars = v.config.GetDefinedVariables()
}

// =============================================================================
// SEMANTIC VALIDATION: Condition Values
// =============================================================================

func (v *Validator) validateConditionValues() {
	// Operator validation (required, valid enum) is handled by struct validator.
	// This validates condition values based on operator requirements.
	for i, precond := range v.config.Spec.Preconditions {
		for j, cond := range precond.Conditions {
			path := fmt.Sprintf("%s.%s[%d].%s[%d]", FieldSpec, FieldPreconditions, i, FieldConditions, j)
			v.validateConditionValue(cond.Operator, cond.Value, path)
		}
	}
}

func (v *Validator) validateConditionValue(operator string, value interface{}, path string) {
	op := criteria.Operator(operator)

	if op == criteria.OperatorExists {
		// "exists" operator checks for field presence, value should not be set
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

func isSliceOrArray(value interface{}) bool {
	if value == nil {
		return false
	}
	kind := reflect.TypeOf(value).Kind()
	return kind == reflect.Slice || kind == reflect.Array
}

// =============================================================================
// SEMANTIC VALIDATION: Capture Field CEL Expressions
// =============================================================================

func (v *Validator) validateCaptureFieldExpressions() {
	// Structural validation (name required, field/expression mutual exclusivity)
	// is handled by struct validator tags in types.go.
	// This validates CEL expression syntax only.
	for i, precond := range v.config.Spec.Preconditions {
		for j, capture := range precond.Capture {
			if capture.Expression != "" && v.celEnv != nil {
				path := fmt.Sprintf("%s.%s[%d].%s[%d].%s", FieldSpec, FieldPreconditions, i, FieldCapture, j, FieldExpression)
				v.validateCELExpression(capture.Expression, path)
			}
		}
	}
}

// =============================================================================
// SEMANTIC VALIDATION: Template Variables
// =============================================================================

func (v *Validator) validateTemplateVariables() {
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
		if manifest, ok := resource.Manifest.(map[string]interface{}); ok {
			v.validateTemplateMap(manifest, resourcePath+"."+FieldManifest)
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

func (v *Validator) validateTemplateString(s string, path string) {
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

func (v *Validator) isVariableDefined(varName string) bool {
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

func (v *Validator) validateTemplateMap(m map[string]interface{}, path string) {
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

// =============================================================================
// SEMANTIC VALIDATION: CEL Expressions
// =============================================================================

func (v *Validator) initCELEnv() error {
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

func (v *Validator) validateCELExpressions() {
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

func (v *Validator) validateCELExpression(expr string, path string) {
	if expr == "" {
		return
	}

	expr = strings.TrimSpace(expr)

	_, issues := v.celEnv.Parse(expr)
	if issues != nil && issues.Err() != nil {
		v.errors.Add(path, fmt.Sprintf("CEL parse error: %v", issues.Err()))
	}
}

func (v *Validator) validateBuildExpressions(m map[string]interface{}, path string) {
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

// =============================================================================
// SEMANTIC VALIDATION: Kubernetes Manifests
// =============================================================================

func (v *Validator) validateK8sManifests() {
	for i, resource := range v.config.Spec.Resources {
		path := fmt.Sprintf("%s.%s[%d].%s", FieldSpec, FieldResources, i, FieldManifest)

		if manifest, ok := resource.Manifest.(map[string]interface{}); ok {
			if ref, hasRef := manifest[FieldRef].(string); hasRef {
				if ref == "" {
					v.errors.Add(path+"."+FieldRef, "manifest ref cannot be empty")
				}
			} else {
				// Embedded manifest - validate K8s structure
				v.validateK8sManifest(manifest, path)
			}
		}
	}
}

func (v *Validator) validateK8sManifest(manifest map[string]interface{}, path string) {
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

// =============================================================================
// HELPER FUNCTIONS
// =============================================================================

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

// =============================================================================
// TEST HELPERS
// =============================================================================

// newValidator creates a Validator without baseDir (for tests)
func newValidator(config *AdapterConfig) *Validator {
	return NewValidator(config, "")
}

// Validate is a convenience method that runs semantic validation
func (v *Validator) Validate() error {
	return v.ValidateSemantic()
}

// validateFileReferences validates file references exist (for tests)
func validateFileReferences(config *AdapterConfig, baseDir string) error {
	return NewValidator(config, baseDir).ValidateFileReferences()
}
