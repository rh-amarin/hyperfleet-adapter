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
)

// Kind constants for configuration types
const (
	ExpectedKindAdapter = "AdapterConfig"     // Deployment config kind
	ExpectedKindTask    = "AdapterTaskConfig" // Task config kind
	ExpectedKindConfig  = "Config"            // Unified merged config kind
)

// Environment variable for config file paths
const (
	EnvAdapterConfig  = "HYPERFLEET_ADAPTER_CONFIG" // Path to deployment config
	EnvTaskConfigPath = "HYPERFLEET_TASK_CONFIG"    // Path to task config
)

// SupportedAPIVersions contains all supported apiVersion values
var SupportedAPIVersions = []string{
	APIVersionV1Alpha1,
}

// ValidHTTPMethods defines allowed HTTP methods for API calls
var ValidHTTPMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

// -----------------------------------------------------------------------------
// Load Options (Functional Options Pattern)
// -----------------------------------------------------------------------------

// LoadOption configures the loading behavior
type LoadOption func(*loadOptions)

type loadOptions struct {
	adapterConfigPath      string
	taskConfigPath         string
	flags                  interface{} // *pflag.FlagSet
	adapterVersion         string
	skipSemanticValidation bool
}

// WithAdapterConfigPath sets the path to the deployment config file
func WithAdapterConfigPath(path string) LoadOption {
	return func(o *loadOptions) {
		o.adapterConfigPath = path
	}
}

// WithTaskConfigPath sets the path to the task config file
func WithTaskConfigPath(path string) LoadOption {
	return func(o *loadOptions) {
		o.taskConfigPath = path
	}
}

// WithFlags sets the CLI flags for Viper binding
func WithFlags(flags interface{}) LoadOption {
	return func(o *loadOptions) {
		o.flags = flags
	}
}

// WithAdapterVersion validates config against expected adapter version
func WithAdapterVersion(version string) LoadOption {
	return func(o *loadOptions) {
		o.adapterVersion = version
	}
}

// WithSkipSemanticValidation skips CEL, template, and K8s manifest validation
func WithSkipSemanticValidation() LoadOption {
	return func(o *loadOptions) {
		o.skipSemanticValidation = true
	}
}

// -----------------------------------------------------------------------------
// Public API
// -----------------------------------------------------------------------------

// LoadConfig loads both deployment and task configurations, validates them,
// and returns a unified Config struct.
// Priority for deployment config values: CLI flags > Environment variables > Config file > Defaults
func LoadConfig(opts ...LoadOption) (*Config, error) {
	o := &loadOptions{}
	for _, opt := range opts {
		opt(o)
	}

	// 1. Load AdapterConfig with Viper (env/CLI overrides)
	adapterCfg, err := loadAdapterConfigWithViperGeneric(o.adapterConfigPath, o.flags)
	if err != nil {
		return nil, fmt.Errorf("failed to load adapter config: %w", err)
	}

	// Get base directory from adapter config path for file references
	adapterConfigPath := o.adapterConfigPath
	if adapterConfigPath == "" {
		adapterConfigPath = os.Getenv(EnvAdapterConfig)
	}
	adapterBaseDir := ""
	if adapterConfigPath != "" {
		var errBaseDir error
		adapterBaseDir, errBaseDir = getBaseDir(adapterConfigPath)
		if errBaseDir != nil {
			return nil, fmt.Errorf("failed to get base directory for adapter config: %w", errBaseDir)
		}
	}

	// Validate AdapterConfig structure
	adapterValidator := NewAdapterConfigValidator(adapterCfg, adapterBaseDir)
	if err := adapterValidator.ValidateStructure(); err != nil {
		return nil, fmt.Errorf("adapter config validation failed: %w", err)
	}

	// Validate adapter version if specified
	if o.adapterVersion != "" {
		if err := ValidateAdapterVersion(adapterCfg, o.adapterVersion); err != nil {
			return nil, fmt.Errorf("adapter version validation failed: %w", err)
		}
	}

	// 2. Load AdapterTaskConfig from YAML (no env binding)
	taskCfg, err := loadTaskConfig(o.taskConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load task config: %w", err)
	}

	// Get base directory from task config path
	taskConfigPath := o.taskConfigPath
	if taskConfigPath == "" {
		taskConfigPath = os.Getenv(EnvTaskConfigPath)
	}
	taskBaseDir := ""
	if taskConfigPath != "" {
		var errBaseDir error
		taskBaseDir, errBaseDir = getBaseDir(taskConfigPath)
		if errBaseDir != nil {
			return nil, fmt.Errorf("failed to get base directory for task config: %w", errBaseDir)
		}
	}

	// Validate AdapterTaskConfig structure
	taskValidator := NewTaskConfigValidator(taskCfg, taskBaseDir)
	if err := taskValidator.ValidateStructure(); err != nil {
		return nil, fmt.Errorf("task config validation failed: %w", err)
	}

	// Validate and load file references in task config
	if taskBaseDir != "" {
		if err := taskValidator.ValidateFileReferences(); err != nil {
			return nil, fmt.Errorf("task config file reference validation failed: %w", err)
		}

		if err := loadTaskConfigFileReferences(taskCfg, taskBaseDir); err != nil {
			return nil, fmt.Errorf("failed to load task config file references: %w", err)
		}
	}

	// Semantic validation for task config (optional)
	if !o.skipSemanticValidation {
		if err := taskValidator.ValidateSemantic(); err != nil {
			return nil, fmt.Errorf("task config semantic validation failed: %w", err)
		}
	}

	// 3. Merge into unified Config
	config := Merge(adapterCfg, taskCfg)
	if config == nil {
		return nil, fmt.Errorf("failed to merge configurations")
	}

	return config, nil
}

// -----------------------------------------------------------------------------
// Internal Functions
// -----------------------------------------------------------------------------

// loadTaskConfigFileReferences loads content from file references into the task config
func loadTaskConfigFileReferences(config *AdapterTaskConfig, baseDir string) error {
	// Load manifest.ref in spec.resources
	for i := range config.Spec.Resources {
		resource := &config.Spec.Resources[i]
		ref := resource.GetManifestRef()
		if ref != "" {
			content, err := loadYAMLFile(baseDir, ref)
			if err != nil {
				return fmt.Errorf("%s.%s[%d].%s.%s: %w", FieldSpec, FieldResources, i, FieldManifest, FieldRef, err)
			}

			// Replace manifest with loaded content
			resource.Manifest = content
		}

		// Load manifestRef in manifests array (Maestro transport)
		for j := range resource.Manifests {
			namedManifest := &resource.Manifests[j]
			if namedManifest.ManifestRef != "" {
				content, err := loadYAMLFile(baseDir, namedManifest.ManifestRef)
				if err != nil {
					return fmt.Errorf("%s.%s[%d].%s[%d].%s: %w",
						FieldSpec, FieldResources, i, FieldManifests, j, FieldManifestRef, err)
				}
				namedManifest.ManifestRefContent = content
			}
		}

		// Load transport.maestro.manifestWork.ref
		if resource.Transport != nil && resource.Transport.Maestro != nil && resource.Transport.Maestro.ManifestWork != nil {
			if resource.Transport.Maestro.ManifestWork.Ref != "" {
				content, err := loadYAMLFile(baseDir, resource.Transport.Maestro.ManifestWork.Ref)
				if err != nil {
					return fmt.Errorf("%s.%s[%d].%s.%s.%s.%s: %w", FieldSpec, FieldResources, i, FieldTransport, FieldMaestro, FieldManifestWork, FieldRef, err)
				}
				resource.Transport.Maestro.ManifestWork.RefContent = content
			}
		}
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

// resolvePath resolves a path against the base directory.
// - Absolute paths are returned as-is (allows mounting files from /etc/adapter, etc.)
// - Relative paths are resolved against the base directory and validated to not escape it
func resolvePath(baseDir, refPath string) (string, error) {
	// Absolute paths are allowed without restriction
	// This supports Kubernetes deployments where files are mounted to fixed paths
	if filepath.IsAbs(refPath) {
		return filepath.Clean(refPath), nil
	}

	// Relative paths must be resolved against base directory
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve base directory: %w", err)
	}
	baseClean := filepath.Clean(baseAbs)

	targetPath := filepath.Clean(filepath.Join(baseClean, refPath))

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
