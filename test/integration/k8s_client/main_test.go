// main_test.go provides shared test setup for integration tests.
// It starts a single envtest container that is reused across all test functions.

package k8s_client_integration

import (
	"context"
	"flag"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

// sharedEnv holds the shared test environment for all integration tests
var sharedEnv TestEnv

// setupErr holds any error that occurred during setup
var setupErr error

// TestMain runs before all tests to set up the shared container
func TestMain(m *testing.M) {
	flag.Parse()

	// Check if we should skip integration tests
	if testing.Short() {
		os.Exit(m.Run())
	}

	// Check if SKIP_K8S_INTEGRATION_TESTS is set
	if os.Getenv("SKIP_K8S_INTEGRATION_TESTS") == "true" {
		println("⚠️  SKIP_K8S_INTEGRATION_TESTS is set, skipping k8s_client integration tests")
		os.Exit(m.Run()) // Run tests (they will skip via GetSharedEnv)
	}

	// Quick check if testcontainers can work
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	provider, err := testcontainers.NewDockerProvider()

	if err != nil {
		setupErr = err
		println("⚠️  Warning: Could not connect to container runtime:", err.Error())
		println("   Tests will be skipped")
	} else {
		info, err := provider.DaemonHost(ctx)
		_ = provider.Close()

		if err != nil {
			setupErr = err
			println("⚠️  Warning: Could not get container runtime info:", err.Error())
			println("   Tests will be skipped")
		} else {
			println("✅ Container runtime available:", info)
			println("🚀 Starting shared envtest container for all integration tests...")

			// Set up the shared environment
			env, err := setupSharedTestEnv()
			if err != nil {
				setupErr = err
				println("❌ Failed to set up shared environment:", err.Error())
				println("   Tests will be skipped")
			} else {
				sharedEnv = env
				println("✅ Shared envtest container ready!")
			}
		}
	}
	println()

	// Run tests (they will skip if setupErr != nil)
	exitCode := m.Run()

	// Cleanup after all tests
	if sharedEnv != nil {
		println()
		println("🧹 Cleaning up shared envtest container...")
		if env, ok := sharedEnv.(*TestEnvPrebuilt); ok {
			env.CleanupSharedEnv()
		}
	}

	os.Exit(exitCode)
}

// GetSharedEnv returns the shared test environment.
// If setup failed, the test will be failed with the setup error.
func GetSharedEnv(t *testing.T) TestEnv {
	t.Helper()
	require.NoError(t, setupErr, "Shared environment setup failed")
	require.NotNil(t, sharedEnv, "Shared test environment is not initialized")
	return sharedEnv
}

