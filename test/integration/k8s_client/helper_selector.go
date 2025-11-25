// This file contains helper functions for selecting the appropriate integration test environment.

package k8s_client_integration

import (
	"context"
	"testing"

	k8s_client "github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	"k8s.io/client-go/rest"
)

// TestEnv is a common interface for all integration test environments
type TestEnv interface {
	GetClient() *k8s_client.Client
	GetConfig() *rest.Config
	GetContext() context.Context
	GetLogger() logger.Logger
	Cleanup(t *testing.T)
}

// Ensure TestEnvPrebuilt satisfies the interface
var _ TestEnv = (*TestEnvPrebuilt)(nil)

// GetClient returns the k8s client
func (e *TestEnvPrebuilt) GetClient() *k8s_client.Client {
	return e.Client
}

// GetConfig returns the rest config
func (e *TestEnvPrebuilt) GetConfig() *rest.Config {
	return e.Config
}

// GetContext returns the context
func (e *TestEnvPrebuilt) GetContext() context.Context {
	return e.Ctx
}

// GetLogger returns the logger
func (e *TestEnvPrebuilt) GetLogger() logger.Logger {
	return e.Log
}

// isAlreadyExistsError checks if the error is an "already exists" error
func isAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), "already exists") || contains(err.Error(), "AlreadyExists")
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > len(substr) && containsSubstring(s, substr)))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

