package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
)

// HTTPTaskRunner implements the hf:http task for HTTP API calls.
// It wraps the HyperFleet API client to make HTTP requests.
type HTTPTaskRunner struct {
	apiClient hyperfleet_api.Client
}

// NewHTTPTaskRunner creates a new HTTP task runner.
func NewHTTPTaskRunner(deps *Dependencies) (TaskRunner, error) {
	var apiClient hyperfleet_api.Client
	if deps != nil && deps.APIClient != nil {
		var ok bool
		apiClient, ok = deps.APIClient.(hyperfleet_api.Client)
		if !ok {
			return nil, fmt.Errorf("invalid APIClient type")
		}
	}

	return &HTTPTaskRunner{
		apiClient: apiClient,
	}, nil
}

func (r *HTTPTaskRunner) Name() string {
	return TaskHTTP
}

// Run executes an HTTP request.
// Args should contain:
//   - method: HTTP method (GET, POST, PUT, PATCH, DELETE)
//   - url: URL to call (can be relative if apiClient has baseURL)
//   - headers: Optional map of headers
//   - body: Optional request body (string or map)
//   - timeout: Optional timeout duration string (e.g., "30s")
//   - retryAttempts: Optional number of retry attempts
//   - retryBackoff: Optional backoff strategy (exponential, linear, constant)
//
// Returns a map with:
//   - statusCode: HTTP status code
//   - status: HTTP status string
//   - body: Response body (parsed as JSON if possible)
//   - rawBody: Response body as string
//   - duration: Request duration
//   - attempts: Number of attempts made
func (r *HTTPTaskRunner) Run(ctx context.Context, args map[string]any, input map[string]any) (map[string]any, error) {
	if r.apiClient == nil {
		return nil, fmt.Errorf("API client not configured")
	}

	// Extract method
	method, ok := args["method"].(string)
	if !ok || method == "" {
		return nil, fmt.Errorf("method is required for hf:http task")
	}

	// Extract URL
	url, ok := args["url"].(string)
	if !ok || url == "" {
		return nil, fmt.Errorf("url is required for hf:http task")
	}

	// Build request options
	var opts []hyperfleet_api.RequestOption

	// Add headers
	if headers, ok := args["headers"].(map[string]any); ok {
		headerMap := make(map[string]string)
		for k, v := range headers {
			if s, ok := v.(string); ok {
				headerMap[k] = s
			}
		}
		opts = append(opts, hyperfleet_api.WithHeaders(headerMap))
	}

	// Add timeout
	if timeoutStr, ok := args["timeout"].(string); ok && timeoutStr != "" {
		if timeout, err := time.ParseDuration(timeoutStr); err == nil {
			opts = append(opts, hyperfleet_api.WithRequestTimeout(timeout))
		}
	}

	// Add retry attempts
	if attempts, ok := args["retryAttempts"].(int); ok {
		opts = append(opts, hyperfleet_api.WithRequestRetryAttempts(attempts))
	} else if attemptsFloat, ok := args["retryAttempts"].(float64); ok {
		opts = append(opts, hyperfleet_api.WithRequestRetryAttempts(int(attemptsFloat)))
	}

	// Add retry backoff
	if backoff, ok := args["retryBackoff"].(string); ok && backoff != "" {
		opts = append(opts, hyperfleet_api.WithRequestRetryBackoff(hyperfleet_api.BackoffStrategy(backoff)))
	}

	// Prepare body
	var body []byte
	if bodyArg, ok := args["body"]; ok && bodyArg != nil {
		switch v := bodyArg.(type) {
		case string:
			body = []byte(v)
		case []byte:
			body = v
		case map[string]any:
			jsonBody, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal body: %w", err)
			}
			body = jsonBody
		}
	}

	// Execute request
	var resp *hyperfleet_api.Response
	var err error

	switch method {
	case "GET":
		resp, err = r.apiClient.Get(ctx, url, opts...)
	case "POST":
		resp, err = r.apiClient.Post(ctx, url, body, opts...)
	case "PUT":
		resp, err = r.apiClient.Put(ctx, url, body, opts...)
	case "PATCH":
		resp, err = r.apiClient.Patch(ctx, url, body, opts...)
	case "DELETE":
		resp, err = r.apiClient.Delete(ctx, url, opts...)
	default:
		return nil, fmt.Errorf("unsupported HTTP method: %s", method)
	}

	// Build output
	output := make(map[string]any)

	// Copy input for continuation
	for k, v := range input {
		output[k] = v
	}

	if err != nil {
		output["error"] = err.Error()
		if resp != nil {
			output["statusCode"] = resp.StatusCode
			output["status"] = resp.Status
		}
		return output, nil // Don't return error, let workflow handle it
	}

	output["statusCode"] = resp.StatusCode
	output["status"] = resp.Status
	output["rawBody"] = resp.BodyString()
	output["duration"] = resp.Duration.String()
	output["attempts"] = resp.Attempts
	output["success"] = resp.IsSuccess()

	// Try to parse body as JSON
	if len(resp.Body) > 0 {
		var jsonBody map[string]any
		if err := json.Unmarshal(resp.Body, &jsonBody); err == nil {
			output["body"] = jsonBody
		} else {
			// Try as array
			var jsonArray []any
			if err := json.Unmarshal(resp.Body, &jsonArray); err == nil {
				output["body"] = jsonArray
			} else {
				output["body"] = resp.BodyString()
			}
		}
	}

	return output, nil
}

func init() {
	// Register the HTTP task runner in the default registry
	_ = RegisterDefault(TaskHTTP, NewHTTPTaskRunner)
}
