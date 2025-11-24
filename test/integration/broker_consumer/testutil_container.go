package broker_consumer_integration

// testutil_container.go provides utilities for setting up and managing
// the Google Pub/Sub emulator container for integration tests.

import (
	"fmt"
	"testing"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/test/integration/testutil"
	"github.com/stretchr/testify/require"
)

const (
	// PubSubEmulatorImage is the Docker image for the Google Pub/Sub emulator
	PubSubEmulatorImage = "gcr.io/google.com/cloudsdktool/cloud-sdk:emulators"

	// PubSubEmulatorPort is the port the emulator listens on
	PubSubEmulatorPort = "8085/tcp"

	// PubSubEmulatorReadyLog is the log message indicating the emulator is ready
	PubSubEmulatorReadyLog = "[pubsub] This is the Google Pub/Sub fake."
)

// setupPubSubEmulatorContainer starts a Google Pub/Sub emulator container
// and returns the project ID, emulator host, and cleanup function.
// The container is automatically stopped and removed after the test completes
// (including on test failure) using t.Cleanup().
func setupPubSubEmulatorContainer(t *testing.T) (string, string, func()) {
	t.Log("========================================")
	t.Log("Starting Google Pub/Sub emulator container...")
	t.Log("Note: First run will download ~2GB image (this may take several minutes)")
	t.Log("========================================")

	projectID := "test-project"

	// Configure container using shared utility
	config := testutil.DefaultContainerConfig()
	config.Name = "Pub/Sub emulator"
	config.Image = PubSubEmulatorImage
	config.ExposedPorts = []string{PubSubEmulatorPort}
	config.Cmd = []string{
		"gcloud",
		"beta",
		"emulators",
		"pubsub",
		"start",
		fmt.Sprintf("--project=%s", projectID),
		"--host-port=0.0.0.0:8085",
	}
	// Wait for both the log message and the port to be listening
	config.WaitStrategy = testutil.WaitStrategies.ForLogAndPort(
		PubSubEmulatorReadyLog,
		PubSubEmulatorPort,
		180*time.Second,
	)

	t.Log("Pulling/starting container (this may take a while on first run)...")
	result, err := testutil.StartContainer(t, config)
	require.NoError(t, err, "Failed to start Pub/Sub emulator container")

	emulatorHost := result.GetEndpoint(PubSubEmulatorPort)
	t.Logf("Pub/Sub emulator started: %s (project: %s)", emulatorHost, projectID)

	// Return a no-op cleanup function for backwards compatibility with existing tests
	// The actual cleanup is handled by t.Cleanup() in testutil.StartContainer
	cleanup := func() {
		// No-op: cleanup is now handled by testutil.StartContainer
	}

	return projectID, emulatorHost, cleanup
}

