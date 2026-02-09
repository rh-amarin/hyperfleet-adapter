package client_factory

import (
	"fmt"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
)

// CreateAPIClient creates a HyperFleet API client from the config
func CreateAPIClient(apiConfig config_loader.HyperfleetAPIConfig, log logger.Logger) (hyperfleet_api.Client, error) {
	var opts []hyperfleet_api.ClientOption

	// Set base URL if configured (env fallback handled in NewClient)
	if apiConfig.BaseURL != "" {
		opts = append(opts, hyperfleet_api.WithBaseURL(apiConfig.BaseURL))
	}

	// Set timeout if configured (0 means use default)
	if apiConfig.Timeout > 0 {
		opts = append(opts, hyperfleet_api.WithTimeout(apiConfig.Timeout))
	}

	// Set retry attempts
	if apiConfig.RetryAttempts > 0 {
		opts = append(opts, hyperfleet_api.WithRetryAttempts(apiConfig.RetryAttempts))
	}

	// Set retry backoff strategy
	if apiConfig.RetryBackoff != "" {
		switch apiConfig.RetryBackoff {
		case hyperfleet_api.BackoffExponential, hyperfleet_api.BackoffLinear, hyperfleet_api.BackoffConstant:
			opts = append(opts, hyperfleet_api.WithRetryBackoff(apiConfig.RetryBackoff))
		default:
			return nil, fmt.Errorf("invalid retry backoff strategy %q (supported: exponential, linear, constant)", apiConfig.RetryBackoff)
		}
	}

	// Set retry base delay
	if apiConfig.BaseDelay > 0 {
		opts = append(opts, hyperfleet_api.WithBaseDelay(apiConfig.BaseDelay))
	}

	// Set retry max delay
	if apiConfig.MaxDelay > 0 {
		opts = append(opts, hyperfleet_api.WithMaxDelay(apiConfig.MaxDelay))
	}

	// Set default headers
	for key, value := range apiConfig.DefaultHeaders {
		opts = append(opts, hyperfleet_api.WithDefaultHeader(key, value))
	}

	return hyperfleet_api.NewClient(log, opts...)
}
