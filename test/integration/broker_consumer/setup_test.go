package broker_consumer_integration

// setup_test.go provides test fixtures and setup utilities:
// - TestMain for pre-test validation and environment setup
// - setupTestEnvironment for creating config files and setting env vars
// - Common test infrastructure shared across test cases

import (
	"context"
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

// TestMain runs before all tests to check if containers can be started
func TestMain(m *testing.M) {
	_ = flag.Set("alsologtostderr", "true")
	_ = flag.Set("v", "2") // Enable verbose logging
	flag.Parse()

	// Check if we should skip integration tests
	if testing.Short() {
		os.Exit(m.Run())
	}

	// Check if SKIP_BROKER_TESTS is set
	if os.Getenv("SKIP_BROKER_TESTS") == "true" {
		println("⚠️  SKIP_BROKER_TESTS is set, skipping broker integration tests")
		os.Exit(0)
	}

	// Quick check if testcontainers can work
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Try to get docker info to see if container runtime is available
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		println("⚠️  Warning: Could not connect to container runtime:", err.Error())
		println("   Set SKIP_BROKER_TESTS=true to skip these tests")
		os.Exit(1)
	}
	defer provider.Close()

	info, err := provider.DaemonHost(ctx)
	if err != nil {
		println("⚠️  Warning: Could not get container runtime info:", err.Error())
		println("   Set SKIP_BROKER_TESTS=true to skip these tests")
		os.Exit(1)
	}

	println("✅ Container runtime available:", info)
	println("📦 Note: First run will download Pub/Sub emulator image (~2GB)")
	println("   This may take several minutes depending on your internet connection")
	println()

	// Run tests
	os.Exit(m.Run())
}

// setupTestEnvironment creates a temporary broker config file and sets up environment variables
func setupTestEnvironment(t *testing.T, projectID, emulatorHost, subscriptionID string) (configPath string, cleanup func()) {
	t.Helper()

	// Create temporary broker config file
	configFile, err := os.CreateTemp("", "broker-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}

	configContent := fmt.Sprintf(`
broker:
  type: googlepubsub
  googlepubsub:
    project_id: "%s"
    subscription: "%s"
`, projectID, subscriptionID)

	if _, err := configFile.WriteString(configContent); err != nil {
		os.Remove(configFile.Name())
		t.Fatalf("Failed to write config file: %v", err)
	}
	configFile.Close()

	// Configure environment
	os.Setenv("BROKER_CONFIG_FILE", configFile.Name())
	os.Setenv("PUBSUB_EMULATOR_HOST", emulatorHost)
	os.Setenv("BROKER_GOOGLEPUBSUB_PROJECT_ID", projectID)

	cleanup = func() {
		os.Remove(configFile.Name())
	}

	return configFile.Name(), cleanup
}

