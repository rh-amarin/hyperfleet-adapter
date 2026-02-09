package client_factory

import (
	"context"
	"fmt"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/maestro_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
)

// CreateMaestroClient creates a Maestro client from the config
func CreateMaestroClient(ctx context.Context, maestroConfig *config_loader.MaestroClientConfig, log logger.Logger) (*maestro_client.Client, error) {
	clientConfig := &maestro_client.Config{
		MaestroServerAddr: maestroConfig.HTTPServerAddress,
		GRPCServerAddr:    maestroConfig.GRPCServerAddress,
		SourceID:          maestroConfig.SourceID,
		Insecure:          maestroConfig.Insecure,
	}

	// Parse timeout if specified
	if maestroConfig.Timeout != "" {
		timeout, err := time.ParseDuration(maestroConfig.Timeout)
		if err != nil {
			return nil, fmt.Errorf("invalid maestro timeout %q: %w", maestroConfig.Timeout, err)
		}
		clientConfig.HTTPTimeout = timeout
	}

	// Configure TLS if auth type is "tls"
	if maestroConfig.Auth.Type == "tls" {
		if maestroConfig.Auth.TLSConfig == nil {
			return nil, fmt.Errorf("maestro auth type is 'tls' but tlsConfig is not provided")
		}
		clientConfig.CAFile = maestroConfig.Auth.TLSConfig.CAFile
		clientConfig.ClientCertFile = maestroConfig.Auth.TLSConfig.CertFile
		clientConfig.ClientKeyFile = maestroConfig.Auth.TLSConfig.KeyFile
	}

	return maestro_client.NewMaestroClient(ctx, clientConfig, log)
}
