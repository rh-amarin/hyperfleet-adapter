// Package loader provides unified loading of workflow configurations.
// It supports both legacy AdapterConfig format and native Serverless Workflow YAML.
package loader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/swf/converter"
	"github.com/serverlessworkflow/sdk-go/v3/model"
	"github.com/serverlessworkflow/sdk-go/v3/parser"
	"gopkg.in/yaml.v3"
)

// WorkflowFormat represents the detected format of a workflow configuration.
type WorkflowFormat string

const (
	// FormatAdapterConfig is the legacy HyperFleet adapter configuration format.
	FormatAdapterConfig WorkflowFormat = "adapter-config"
	// FormatSWF is the native Serverless Workflow format.
	FormatSWF WorkflowFormat = "swf"
	// FormatUnknown indicates the format could not be detected.
	FormatUnknown WorkflowFormat = "unknown"
)

// LoadResult contains the result of loading a workflow configuration.
type LoadResult struct {
	// Workflow is the parsed Serverless Workflow model.
	Workflow *model.Workflow

	// AdapterConfig is the original AdapterConfig (nil if loaded from SWF format).
	AdapterConfig *config_loader.AdapterConfig

	// Format indicates which format was detected and loaded.
	Format WorkflowFormat

	// FilePath is the path from which the config was loaded.
	FilePath string
}

// LoadOption configures the loader behavior.
type LoadOption func(*loaderConfig)

type loaderConfig struct {
	adapterVersion         string
	skipSemanticValidation bool
}

// WithAdapterVersion validates config against expected adapter version (for AdapterConfig format).
func WithAdapterVersion(version string) LoadOption {
	return func(c *loaderConfig) {
		c.adapterVersion = version
	}
}

// WithSkipSemanticValidation skips semantic validation (for AdapterConfig format).
func WithSkipSemanticValidation() LoadOption {
	return func(c *loaderConfig) {
		c.skipSemanticValidation = true
	}
}

// Load loads a workflow from a file, automatically detecting the format.
// It supports both legacy AdapterConfig YAML and native Serverless Workflow YAML.
func Load(filePath string, opts ...LoadOption) (*LoadResult, error) {
	if filePath == "" {
		filePath = config_loader.ConfigPathFromEnv()
	}
	if filePath == "" {
		return nil, fmt.Errorf("config file path is required (pass as parameter or set %s environment variable)", config_loader.EnvConfigPath)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", filePath, err)
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for %q: %w", filePath, err)
	}

	return Parse(data, absPath, opts...)
}

// Parse parses workflow configuration from YAML bytes.
func Parse(data []byte, filePath string, opts ...LoadOption) (*LoadResult, error) {
	cfg := &loaderConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	format := DetectFormat(data)

	switch format {
	case FormatSWF:
		return loadSWF(data, filePath)
	case FormatAdapterConfig:
		return loadAdapterConfig(data, filePath, cfg)
	default:
		return nil, fmt.Errorf("unable to detect config format: file must be either AdapterConfig (kind: AdapterConfig) or Serverless Workflow (document.dsl)")
	}
}

// DetectFormat determines whether the YAML data is AdapterConfig or SWF format.
func DetectFormat(data []byte) WorkflowFormat {
	// Try to detect format by parsing minimal structure
	var probe struct {
		Kind     string `yaml:"kind"`
		Document struct {
			DSL string `yaml:"dsl"`
		} `yaml:"document"`
	}

	if err := yaml.Unmarshal(data, &probe); err != nil {
		return FormatUnknown
	}

	// Check for SWF format (has document.dsl)
	if probe.Document.DSL != "" {
		return FormatSWF
	}

	// Check for AdapterConfig format (has kind: AdapterConfig)
	if probe.Kind == config_loader.ExpectedKind {
		return FormatAdapterConfig
	}

	// Additional heuristics
	content := string(data)

	// Look for SWF indicators
	if strings.Contains(content, "document:") && strings.Contains(content, "dsl:") {
		return FormatSWF
	}

	// Look for AdapterConfig indicators
	if strings.Contains(content, "apiVersion:") && strings.Contains(content, "kind:") {
		return FormatAdapterConfig
	}

	return FormatUnknown
}

// loadSWF loads a native Serverless Workflow YAML file.
func loadSWF(data []byte, filePath string) (*LoadResult, error) {
	workflow, err := parser.FromYAMLSource(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Serverless Workflow: %w", err)
	}

	return &LoadResult{
		Workflow: workflow,
		Format:   FormatSWF,
		FilePath: filePath,
	}, nil
}

// loadAdapterConfig loads a legacy AdapterConfig and converts it to SWF.
func loadAdapterConfig(data []byte, filePath string, cfg *loaderConfig) (*LoadResult, error) {
	var loaderOpts []config_loader.LoaderOption

	if cfg.adapterVersion != "" {
		loaderOpts = append(loaderOpts, config_loader.WithAdapterVersion(cfg.adapterVersion))
	}
	if cfg.skipSemanticValidation {
		loaderOpts = append(loaderOpts, config_loader.WithSkipSemanticValidation())
	}

	// Set base directory for resolving relative paths
	baseDir := filepath.Dir(filePath)
	loaderOpts = append([]config_loader.LoaderOption{config_loader.WithBaseDir(baseDir)}, loaderOpts...)

	adapterConfig, err := config_loader.Parse(data, loaderOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to parse AdapterConfig: %w", err)
	}

	// Convert to SWF workflow
	workflow, err := converter.ConvertAdapterConfig(adapterConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to convert AdapterConfig to workflow: %w", err)
	}

	return &LoadResult{
		Workflow:      workflow,
		AdapterConfig: adapterConfig,
		Format:        FormatAdapterConfig,
		FilePath:      filePath,
	}, nil
}

// LoadWorkflow is a convenience function that loads and returns just the workflow.
func LoadWorkflow(filePath string, opts ...LoadOption) (*model.Workflow, error) {
	result, err := Load(filePath, opts...)
	if err != nil {
		return nil, err
	}
	return result.Workflow, nil
}

// LoadWorkflowFromBytes parses workflow from bytes and returns just the workflow.
func LoadWorkflowFromBytes(data []byte, opts ...LoadOption) (*model.Workflow, error) {
	result, err := Parse(data, "", opts...)
	if err != nil {
		return nil, err
	}
	return result.Workflow, nil
}

// IsNativeWorkflow checks if a file contains a native SWF workflow.
func IsNativeWorkflow(filePath string) (bool, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return false, err
	}
	return DetectFormat(data) == FormatSWF, nil
}

// IsAdapterConfig checks if a file contains a legacy AdapterConfig.
func IsAdapterConfig(filePath string) (bool, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return false, err
	}
	return DetectFormat(data) == FormatAdapterConfig, nil
}
