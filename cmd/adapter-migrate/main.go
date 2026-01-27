// adapter-migrate is a CLI tool for migrating HyperFleet adapter configurations.
// It converts legacy AdapterConfig YAML files to native Serverless Workflow format.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/swf/converter"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/swf/loader"
	"github.com/serverlessworkflow/sdk-go/v3/model"
	"github.com/serverlessworkflow/sdk-go/v3/parser"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

// Build-time variables set via ldflags
var (
	version   = "0.1.0"
	commit    = "none"
	buildDate = "unknown"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "adapter-migrate",
		Short: "HyperFleet Adapter Migration Tool",
		Long: `A migration tool for HyperFleet adapter configurations.

This tool helps you migrate from legacy AdapterConfig YAML format to
native Serverless Workflow (SWF) format.

Commands:
  convert   - Convert an AdapterConfig to SWF workflow format
  validate  - Validate a workflow file (either format)
  detect    - Detect the format of a configuration file`,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
	}

	// Convert command
	convertCmd := &cobra.Command{
		Use:   "convert",
		Short: "Convert AdapterConfig to Serverless Workflow format",
		Long: `Convert a legacy AdapterConfig YAML file to native Serverless Workflow format.

Example:
  adapter-migrate convert --input adapter.yaml --output workflow.yaml
  adapter-migrate convert -i adapter.yaml -o workflow.yaml
  adapter-migrate convert -i adapter.yaml  # prints to stdout`,
		RunE: runConvert,
	}

	var inputFile, outputFile string
	var overwrite bool

	convertCmd.Flags().StringVarP(&inputFile, "input", "i", "", "Input AdapterConfig file (required)")
	convertCmd.Flags().StringVarP(&outputFile, "output", "o", "", "Output SWF workflow file (prints to stdout if not specified)")
	convertCmd.Flags().BoolVarP(&overwrite, "overwrite", "f", false, "Overwrite output file if it exists")
	_ = convertCmd.MarkFlagRequired("input")

	// Validate command
	validateCmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate a workflow configuration file",
		Long: `Validate a workflow configuration file (either AdapterConfig or SWF format).

Example:
  adapter-migrate validate --file adapter.yaml
  adapter-migrate validate -f workflow.yaml`,
		RunE: runValidate,
	}

	var validateFile string
	validateCmd.Flags().StringVarP(&validateFile, "file", "f", "", "Configuration file to validate (required)")
	_ = validateCmd.MarkFlagRequired("file")

	// Detect command
	detectCmd := &cobra.Command{
		Use:   "detect",
		Short: "Detect the format of a configuration file",
		Long: `Detect whether a file is in AdapterConfig or Serverless Workflow format.

Example:
  adapter-migrate detect --file config.yaml
  adapter-migrate detect -f config.yaml`,
		RunE: runDetect,
	}

	var detectFile string
	detectCmd.Flags().StringVarP(&detectFile, "file", "f", "", "Configuration file to detect format (required)")
	_ = detectCmd.MarkFlagRequired("file")

	// Version command
	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("HyperFleet Adapter Migration Tool\n")
			fmt.Printf("  Version:  %s\n", version)
			fmt.Printf("  Commit:   %s\n", commit)
			fmt.Printf("  Built:    %s\n", buildDate)
		},
	}

	// Add subcommands
	rootCmd.AddCommand(convertCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(detectCmd)
	rootCmd.AddCommand(versionCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runConvert(cmd *cobra.Command, args []string) error {
	inputFile, _ := cmd.Flags().GetString("input")
	outputFile, _ := cmd.Flags().GetString("output")
	overwrite, _ := cmd.Flags().GetBool("overwrite")

	// Read input file
	data, err := os.ReadFile(inputFile)
	if err != nil {
		return fmt.Errorf("failed to read input file: %w", err)
	}

	// Detect format
	format := loader.DetectFormat(data)
	if format == loader.FormatSWF {
		fmt.Fprintf(os.Stderr, "Note: %s is already in Serverless Workflow format\n", inputFile)
		if outputFile == "" {
			fmt.Print(string(data))
		} else {
			return writeOutput(outputFile, data, overwrite)
		}
		return nil
	}

	if format != loader.FormatAdapterConfig {
		return fmt.Errorf("unable to detect format of %s: must be AdapterConfig", inputFile)
	}

	// Parse AdapterConfig
	absPath, _ := filepath.Abs(inputFile)
	baseDir := filepath.Dir(absPath)

	adapterConfig, err := config_loader.Parse(data,
		config_loader.WithBaseDir(baseDir),
		config_loader.WithSkipSemanticValidation(),
	)
	if err != nil {
		return fmt.Errorf("failed to parse AdapterConfig: %w", err)
	}

	// Convert to SWF workflow
	workflow, err := converter.ConvertAdapterConfig(adapterConfig)
	if err != nil {
		return fmt.Errorf("failed to convert to workflow: %w", err)
	}

	// Serialize to YAML via JSON (SDK uses custom JSON marshaling)
	swfYAML, err := workflowToYAML(workflow)
	if err != nil {
		return fmt.Errorf("failed to serialize workflow: %w", err)
	}

	// Add header comment
	header := fmt.Sprintf(`# Serverless Workflow converted from AdapterConfig
# Original: %s
# Converted by: adapter-migrate v%s
#
# NOTE: This is an auto-generated workflow. Custom task types (hf:*) require
# the HyperFleet adapter runtime for execution.

`, filepath.Base(inputFile), version)

	output := []byte(header + string(swfYAML))

	if outputFile == "" {
		fmt.Print(string(output))
	} else {
		if err := writeOutput(outputFile, output, overwrite); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Converted %s to %s\n", inputFile, outputFile)
	}

	return nil
}

func runValidate(cmd *cobra.Command, args []string) error {
	file, _ := cmd.Flags().GetString("file")

	// Read file
	data, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	format := loader.DetectFormat(data)

	switch format {
	case loader.FormatSWF:
		// Validate as SWF
		workflow, err := parser.FromYAMLSource(data)
		if err != nil {
			fmt.Printf("INVALID: Serverless Workflow validation failed:\n  %s\n", err)
			return fmt.Errorf("validation failed")
		}
		fmt.Printf("VALID: Serverless Workflow\n")
		fmt.Printf("  Name:      %s\n", workflow.Document.Name)
		fmt.Printf("  Version:   %s\n", workflow.Document.Version)
		fmt.Printf("  Namespace: %s\n", workflow.Document.Namespace)
		if workflow.Do != nil {
			fmt.Printf("  Tasks:     %d\n", len(*workflow.Do))
		}

	case loader.FormatAdapterConfig:
		// Validate as AdapterConfig
		absPath, _ := filepath.Abs(file)
		baseDir := filepath.Dir(absPath)

		adapterConfig, err := config_loader.Parse(data, config_loader.WithBaseDir(baseDir))
		if err != nil {
			fmt.Printf("INVALID: AdapterConfig validation failed:\n  %s\n", err)
			return fmt.Errorf("validation failed")
		}
		fmt.Printf("VALID: AdapterConfig\n")
		fmt.Printf("  Name:          %s\n", adapterConfig.Metadata.Name)
		fmt.Printf("  Namespace:     %s\n", adapterConfig.Metadata.Namespace)
		fmt.Printf("  Version:       %s\n", adapterConfig.Spec.Adapter.Version)
		fmt.Printf("  Params:        %d\n", len(adapterConfig.Spec.Params))
		fmt.Printf("  Preconditions: %d\n", len(adapterConfig.Spec.Preconditions))
		fmt.Printf("  Resources:     %d\n", len(adapterConfig.Spec.Resources))

	default:
		fmt.Printf("UNKNOWN: Unable to detect format\n")
		fmt.Printf("  File must be either AdapterConfig (kind: AdapterConfig) or\n")
		fmt.Printf("  Serverless Workflow (document.dsl)\n")
		return fmt.Errorf("unknown format")
	}

	return nil
}

func runDetect(cmd *cobra.Command, args []string) error {
	file, _ := cmd.Flags().GetString("file")

	// Read file
	data, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	format := loader.DetectFormat(data)

	switch format {
	case loader.FormatSWF:
		fmt.Printf("Format: Serverless Workflow (SWF)\n")
		fmt.Printf("File:   %s\n", file)
		fmt.Printf("Status: Ready for use with HyperFleet adapter\n")

	case loader.FormatAdapterConfig:
		fmt.Printf("Format: AdapterConfig (legacy)\n")
		fmt.Printf("File:   %s\n", file)
		fmt.Printf("Status: Can be converted to SWF format\n")
		fmt.Printf("\nTo convert, run:\n")
		fmt.Printf("  adapter-migrate convert -i %s -o %s\n", file, suggestOutputName(file))

	default:
		fmt.Printf("Format: Unknown\n")
		fmt.Printf("File:   %s\n", file)
		fmt.Printf("Status: Not a recognized configuration format\n")
		return fmt.Errorf("unknown format")
	}

	return nil
}

func writeOutput(path string, data []byte, overwrite bool) error {
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("output file %s already exists (use -f to overwrite)", path)
		}
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write output file: %w", err)
	}

	return nil
}

func suggestOutputName(inputPath string) string {
	ext := filepath.Ext(inputPath)
	base := strings.TrimSuffix(inputPath, ext)

	// If it has "adapter" in the name, suggest "workflow"
	if strings.Contains(strings.ToLower(base), "adapter") {
		base = strings.Replace(base, "adapter", "workflow", 1)
		base = strings.Replace(base, "Adapter", "Workflow", 1)
	} else {
		base = base + "-workflow"
	}

	return base + ".yaml"
}

// workflowToYAML serializes a workflow to YAML using the SDK's custom JSON marshaling.
// The SWF SDK uses custom MarshalJSON for TaskItem/TaskList types, so we serialize
// to JSON first, then convert to YAML to preserve the correct structure.
func workflowToYAML(workflow *model.Workflow) ([]byte, error) {
	// Serialize to JSON (uses SDK's custom MarshalJSON)
	jsonBytes, err := json.Marshal(workflow)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal workflow to JSON: %w", err)
	}

	// Convert JSON to YAML
	yamlBytes, err := yaml.JSONToYAML(jsonBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to convert JSON to YAML: %w", err)
	}

	return yamlBytes, nil
}
