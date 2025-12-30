package config_loader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// -----------------------------------------------------------------------------
// Constants
// -----------------------------------------------------------------------------

// API version constants
const (
	APIVersionV1Alpha1 = "hyperfleet.redhat.com/v1alpha1"
	ExpectedKind       = "AdapterConfig"
)

// Environment variable for config file path
const EnvConfigPath = "ADAPTER_CONFIG_PATH"

// SupportedAPIVersions contains all supported apiVersion values
var SupportedAPIVersions = []string{
	APIVersionV1Alpha1,
}

// ValidHTTPMethods defines allowed HTTP methods for API calls
var ValidHTTPMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

// -----------------------------------------------------------------------------
// Loader Options (Functional Options Pattern)
// -----------------------------------------------------------------------------

// LoaderOption configures the loader behavior
type LoaderOption func(*loaderConfig)

type loaderConfig struct {
	adapterVersion         string
	skipSemanticValidation bool
	baseDir                string // Base directory for resolving relative paths (buildRef, manifest.ref)
}

// WithAdapterVersion validates config against expected adapter version
func WithAdapterVersion(version string) LoaderOption {
	return func(c *loaderConfig) {
		c.adapterVersion = version
	}
}

// WithSkipSemanticValidation skips CEL, template, and K8s manifest validation
func WithSkipSemanticValidation() LoaderOption {
	return func(c *loaderConfig) {
		c.skipSemanticValidation = true
	}
}

// WithBaseDir sets the base directory for resolving relative paths (buildRef, manifest.ref)
func WithBaseDir(dir string) LoaderOption {
	return func(c *loaderConfig) {
		c.baseDir = dir
	}
}

// -----------------------------------------------------------------------------
// Public API
// -----------------------------------------------------------------------------

// ConfigPathFromEnv returns the config file path from the ADAPTER_CONFIG_PATH environment variable
func ConfigPathFromEnv() string {
	return os.Getenv(EnvConfigPath)
}

// Load loads an adapter configuration from a YAML file.
// If filePath is empty, it will read from ADAPTER_CONFIG_PATH environment variable.
// The base directory for relative paths (buildRef, manifest.ref) is automatically
// set to the config file's directory.
func Load(filePath string, opts ...LoaderOption) (*AdapterConfig, error) {
	if filePath == "" {
		filePath = ConfigPathFromEnv()
	}
	if filePath == "" {
		return nil, fmt.Errorf("config file path is required (pass as parameter or set %s environment variable)", EnvConfigPath)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", filePath, err)
	}

	// Automatically set base directory from config file path
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for %q: %w", filePath, err)
	}
	baseDir := filepath.Dir(absPath)

	// Prepend WithBaseDir option so it can be overridden by user opts
	allOpts := append([]LoaderOption{WithBaseDir(baseDir)}, opts...)
	return Parse(data, allOpts...)
}

// Parse parses adapter configuration from YAML bytes
func Parse(data []byte, opts ...LoaderOption) (*AdapterConfig, error) {
	cfg := &loaderConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	var config AdapterConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("YAML parse error: %w", err)
	}

	if err := runValidationPipeline(&config, cfg); err != nil {
		return nil, err
	}

	return &config, nil
}

// -----------------------------------------------------------------------------
// Validation Pipeline
// -----------------------------------------------------------------------------

// runValidationPipeline executes all validators in sequence
func runValidationPipeline(config *AdapterConfig, cfg *loaderConfig) error {
	validator := NewValidator(config, cfg.baseDir)

	// 1. Structural validation (fail-fast)
	if err := validator.ValidateStructure(); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	// 2. Adapter version validation (optional)
	if cfg.adapterVersion != "" {
		if err := ValidateAdapterVersion(config, cfg.adapterVersion); err != nil {
			return fmt.Errorf("adapter version validation failed: %w", err)
		}
	}

	// 3. File reference validation and loading (only if baseDir is set)
	if cfg.baseDir != "" {
		if err := validator.ValidateFileReferences(); err != nil {
			return fmt.Errorf("file reference validation failed: %w", err)
		}

		if err := loadFileReferences(config, cfg.baseDir); err != nil {
			return fmt.Errorf("failed to load file references: %w", err)
		}
	}

	// 4. Semantic validation (optional, can be skipped for performance)
	if !cfg.skipSemanticValidation {
		if err := validator.ValidateSemantic(); err != nil {
			return fmt.Errorf("semantic validation failed: %w", err)
		}
	}

	return nil
}

// -----------------------------------------------------------------------------
// File Reference Loading
// -----------------------------------------------------------------------------

// loadFileReferences loads content from file references into the config
func loadFileReferences(config *AdapterConfig, baseDir string) error {
	// Load manifest.ref in spec.resources
	for i := range config.Spec.Resources {
		resource := &config.Spec.Resources[i]
		ref := resource.GetManifestRef()
		if ref == "" {
			continue
		}

		content, err := loadYAMLFile(baseDir, ref)
		if err != nil {
			return fmt.Errorf("%s.%s[%d].%s.%s: %w", FieldSpec, FieldResources, i, FieldManifest, FieldRef, err)
		}

		// Replace manifest with loaded content
		resource.Manifest = content
	}

	// Load buildRef in spec.post.payloads
	if config.Spec.Post != nil {
		for i := range config.Spec.Post.Payloads {
			payload := &config.Spec.Post.Payloads[i]
			if payload.BuildRef != "" {
				content, err := loadYAMLFile(baseDir, payload.BuildRef)
				if err != nil {
					return fmt.Errorf("%s.%s.%s[%d].%s: %w", FieldSpec, FieldPost, FieldPayloads, i, FieldBuildRef, err)
				}
				payload.BuildRefContent = content
			}
		}
	}

	return nil
}

// loadYAMLFile loads and parses a YAML file
func loadYAMLFile(baseDir, refPath string) (map[string]interface{}, error) {
	fullPath, err := resolvePath(baseDir, refPath)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %q: %w", fullPath, err)
	}

	var content map[string]interface{}
	if err := yaml.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("failed to parse YAML file %q: %w", fullPath, err)
	}

	return content, nil
}

// resolvePath resolves a relative path against the base directory and validates
// that the resolved path does not escape the base directory.
func resolvePath(baseDir, refPath string) (string, error) {
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve base directory: %w", err)
	}
	baseClean := filepath.Clean(baseAbs)

	var targetPath string
	if filepath.IsAbs(refPath) {
		targetPath = filepath.Clean(refPath)
	} else {
		targetPath = filepath.Clean(filepath.Join(baseClean, refPath))
	}

	// Check if target path is within base directory
	rel, err := filepath.Rel(baseClean, targetPath)
	if err != nil {
		return "", fmt.Errorf("path %q escapes base directory", refPath)
	}

	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path %q escapes base directory", refPath)
	}

	return targetPath, nil
}
