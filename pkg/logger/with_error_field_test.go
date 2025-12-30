package logger

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	apperrors "github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// -----------------------------------------------------------------------------
// WithErrorField Tests
// -----------------------------------------------------------------------------

func TestWithErrorField(t *testing.T) {
	t.Run("nil_error_returns_unchanged_context", func(t *testing.T) {
		ctx := context.Background()
		result := WithErrorField(ctx, nil)

		fields := GetLogFields(result)
		if fields != nil && fields["error"] != nil {
			t.Error("Expected no error field for nil error")
		}
	})

	t.Run("sets_error_message_in_context", func(t *testing.T) {
		ctx := context.Background()
		err := errors.New("test error message")
		result := WithErrorField(ctx, err)

		fields := GetLogFields(result)
		if fields == nil {
			t.Fatal("Expected log fields, got nil")
		}
		if fields["error"] != "test error message" {
			t.Errorf("Expected 'test error message', got %v", fields["error"])
		}
	})

	t.Run("captures_stack_trace_for_unexpected_error", func(t *testing.T) {
		ctx := context.Background()
		err := errors.New("unexpected internal error")
		result := WithErrorField(ctx, err)

		fields := GetLogFields(result)
		if fields == nil {
			t.Fatal("Expected log fields, got nil")
		}

		stackTrace, ok := fields["stack_trace"].([]string)
		if !ok || len(stackTrace) == 0 {
			t.Error("Expected stack_trace to be captured for unexpected error")
		}
	})

	t.Run("preserves_existing_context_fields", func(t *testing.T) {
		ctx := context.Background()
		ctx = WithEventID(ctx, "evt-123")
		ctx = WithClusterID(ctx, "cls-456")

		err := errors.New("test error")
		result := WithErrorField(ctx, err)

		fields := GetLogFields(result)
		if fields["event_id"] != "evt-123" {
			t.Errorf("Expected event_id=evt-123, got %v", fields["event_id"])
		}
		if fields["cluster_id"] != "cls-456" {
			t.Errorf("Expected cluster_id=cls-456, got %v", fields["cluster_id"])
		}
		if fields["error"] != "test error" {
			t.Errorf("Expected error='test error', got %v", fields["error"])
		}
	})
}

// -----------------------------------------------------------------------------
// Stack Trace Capture Decision Tests
// -----------------------------------------------------------------------------

func TestShouldCaptureStackTrace_ContextErrors(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		expectCapture bool
	}{
		{
			name:          "context.Canceled_skips_stack_trace",
			err:           context.Canceled,
			expectCapture: false,
		},
		{
			name:          "context.DeadlineExceeded_skips_stack_trace",
			err:           context.DeadlineExceeded,
			expectCapture: false,
		},
		{
			name:          "wrapped_context.Canceled_skips_stack_trace",
			err:           fmt.Errorf("operation failed: %w", context.Canceled),
			expectCapture: false,
		},
		{
			name:          "io.EOF_skips_stack_trace",
			err:           io.EOF,
			expectCapture: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldCaptureStackTrace(tt.err)
			if result != tt.expectCapture {
				t.Errorf("shouldCaptureStackTrace() = %v, want %v", result, tt.expectCapture)
			}
		})
	}
}

func TestShouldCaptureStackTrace_K8sAPIErrors(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		expectCapture bool
	}{
		{
			name:          "NotFound_skips_stack_trace",
			err:           apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "pods"}, "my-pod"),
			expectCapture: false,
		},
		{
			name:          "Conflict_skips_stack_trace",
			err:           apierrors.NewConflict(schema.GroupResource{Group: "", Resource: "pods"}, "my-pod", errors.New("conflict")),
			expectCapture: false,
		},
		{
			name:          "AlreadyExists_skips_stack_trace",
			err:           apierrors.NewAlreadyExists(schema.GroupResource{Group: "", Resource: "pods"}, "my-pod"),
			expectCapture: false,
		},
		{
			name:          "Forbidden_skips_stack_trace",
			err:           apierrors.NewForbidden(schema.GroupResource{Group: "", Resource: "pods"}, "my-pod", errors.New("forbidden")),
			expectCapture: false,
		},
		{
			name:          "Unauthorized_skips_stack_trace",
			err:           apierrors.NewUnauthorized("unauthorized"),
			expectCapture: false,
		},
		{
			name:          "BadRequest_skips_stack_trace",
			err:           apierrors.NewBadRequest("bad request"),
			expectCapture: false,
		},
		{
			name:          "ServiceUnavailable_skips_stack_trace",
			err:           apierrors.NewServiceUnavailable("service unavailable"),
			expectCapture: false,
		},
		{
			name:          "Timeout_skips_stack_trace",
			err:           apierrors.NewTimeoutError("timeout", 30),
			expectCapture: false,
		},
		{
			name:          "TooManyRequests_skips_stack_trace",
			err:           apierrors.NewTooManyRequestsError("too many requests"),
			expectCapture: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldCaptureStackTrace(tt.err)
			if result != tt.expectCapture {
				t.Errorf("shouldCaptureStackTrace() = %v, want %v", result, tt.expectCapture)
			}
		})
	}
}

func TestShouldCaptureStackTrace_K8sResourceDataErrors(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		expectCapture bool
	}{
		{
			name:          "K8sResourceKeyNotFoundError_skips_stack_trace",
			err:           apperrors.NewK8sResourceKeyNotFoundError("Secret", "default", "my-secret", "password"),
			expectCapture: false,
		},
		{
			name:          "K8sInvalidPathError_skips_stack_trace",
			err:           apperrors.NewK8sInvalidPathError("Secret", "invalid/path", "namespace.name.key"),
			expectCapture: false,
		},
		{
			name:          "K8sResourceDataError_skips_stack_trace",
			err:           apperrors.NewK8sResourceDataError("ConfigMap", "default", "my-config", "data field missing", nil),
			expectCapture: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldCaptureStackTrace(tt.err)
			if result != tt.expectCapture {
				t.Errorf("shouldCaptureStackTrace() = %v, want %v", result, tt.expectCapture)
			}
		})
	}
}

func TestShouldCaptureStackTrace_APIErrors(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		expectCapture bool
	}{
		{
			name:          "APIError_NotFound_skips_stack_trace",
			err:           apperrors.NewAPIError("GET", "/api/v1/clusters/123", 404, "Not Found", nil, 1, 0, errors.New("not found")),
			expectCapture: false,
		},
		{
			name:          "APIError_Unauthorized_skips_stack_trace",
			err:           apperrors.NewAPIError("GET", "/api/v1/clusters", 401, "Unauthorized", nil, 1, 0, errors.New("unauthorized")),
			expectCapture: false,
		},
		{
			name:          "APIError_Forbidden_skips_stack_trace",
			err:           apperrors.NewAPIError("POST", "/api/v1/clusters", 403, "Forbidden", nil, 1, 0, errors.New("forbidden")),
			expectCapture: false,
		},
		{
			name:          "APIError_BadRequest_skips_stack_trace",
			err:           apperrors.NewAPIError("POST", "/api/v1/clusters", 400, "Bad Request", nil, 1, 0, errors.New("bad request")),
			expectCapture: false,
		},
		{
			name:          "APIError_Conflict_skips_stack_trace",
			err:           apperrors.NewAPIError("PUT", "/api/v1/clusters/123", 409, "Conflict", nil, 1, 0, errors.New("conflict")),
			expectCapture: false,
		},
		{
			name:          "APIError_RateLimited_skips_stack_trace",
			err:           apperrors.NewAPIError("GET", "/api/v1/clusters", 429, "Too Many Requests", nil, 1, 0, errors.New("rate limited")),
			expectCapture: false,
		},
		{
			name:          "APIError_Timeout_skips_stack_trace",
			err:           apperrors.NewAPIError("GET", "/api/v1/clusters", 408, "Request Timeout", nil, 1, 0, errors.New("timeout")),
			expectCapture: false,
		},
		{
			name:          "APIError_ServerError_skips_stack_trace",
			err:           apperrors.NewAPIError("GET", "/api/v1/clusters", 503, "Service Unavailable", nil, 3, 0, errors.New("server error")),
			expectCapture: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldCaptureStackTrace(tt.err)
			if result != tt.expectCapture {
				t.Errorf("shouldCaptureStackTrace() = %v, want %v", result, tt.expectCapture)
			}
		})
	}
}

func TestShouldCaptureStackTrace_UnexpectedErrors(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		expectCapture bool
	}{
		{
			name:          "generic_error_captures_stack_trace",
			err:           errors.New("unexpected error"),
			expectCapture: true,
		},
		{
			name:          "wrapped_generic_error_captures_stack_trace",
			err:           fmt.Errorf("failed to process: %w", errors.New("internal error")),
			expectCapture: true,
		},
		{
			name:          "nil_error_does_not_capture",
			err:           nil,
			expectCapture: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldCaptureStackTrace(tt.err)
			if result != tt.expectCapture {
				t.Errorf("shouldCaptureStackTrace() = %v, want %v", result, tt.expectCapture)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// CaptureStackTrace Tests
// -----------------------------------------------------------------------------

func TestCaptureStackTrace(t *testing.T) {
	t.Run("captures_call_stack", func(t *testing.T) {
		stack := CaptureStackTrace(0)
		if len(stack) == 0 {
			t.Fatal("Expected non-empty stack trace")
		}

		// First frame should be from this test file
		found := false
		for _, frame := range stack {
			if strings.Contains(frame, "with_error_field_test.go") {
				found = true
				break
			}
		}
		if !found {
			t.Error("Expected stack trace to contain with_error_field_test.go")
		}
	})

	t.Run("skip_parameter_works", func(t *testing.T) {
		stack0 := CaptureStackTrace(0)
		stack1 := CaptureStackTrace(1)

		// Verify skip behavior: stack1 should either have fewer frames OR
		// (when maxFrames cap is reached) be equal to stack0 with the first frame removed.
		// This handles the case where CaptureStackTrace hits maxFrames limit.
		if len(stack1) < len(stack0) {
			// Normal case: skip=1 results in fewer frames
			return
		}

		if len(stack1) == len(stack0) && len(stack0) > 0 {
			// maxFrames cap reached: verify stack1 equals stack0[1:]
			// (the first frame was skipped, but a new frame was captured at the end)
			for i := 0; i < len(stack1)-1; i++ {
				if stack1[i] != stack0[i+1] {
					t.Errorf("Expected stack1[%d] to equal stack0[%d], got %q vs %q",
						i, i+1, stack1[i], stack0[i+1])
				}
			}
			return
		}

		t.Errorf("Expected skip=1 to result in fewer frames or shifted stack, got len(stack0)=%d, len(stack1)=%d",
			len(stack0), len(stack1))
	})

	t.Run("frames_contain_file_line_function", func(t *testing.T) {
		stack := CaptureStackTrace(0)
		if len(stack) == 0 {
			t.Fatal("Expected non-empty stack trace")
		}

		// Each frame should have format "file:line function"
		for _, frame := range stack {
			if !strings.Contains(frame, ":") {
				t.Errorf("Frame missing colon separator: %s", frame)
			}
			if !strings.Contains(frame, " ") {
				t.Errorf("Frame missing space separator: %s", frame)
			}
		}
	})
}

// -----------------------------------------------------------------------------
// Integration Tests
// -----------------------------------------------------------------------------

func TestWithErrorField_StackTraceIntegration(t *testing.T) {
	t.Run("unexpected_error_has_stack_trace", func(t *testing.T) {
		ctx := context.Background()
		err := errors.New("unexpected internal error")
		result := WithErrorField(ctx, err)

		fields := GetLogFields(result)
		if fields == nil {
			t.Fatal("Expected log fields")
		}

		// Should have error field
		if fields["error"] != "unexpected internal error" {
			t.Errorf("Expected error message, got %v", fields["error"])
		}

		// Should have stack_trace field
		stackTrace, ok := fields["stack_trace"].([]string)
		if !ok {
			t.Fatal("Expected stack_trace to be []string")
		}
		if len(stackTrace) == 0 {
			t.Error("Expected non-empty stack trace")
		}
	})

	t.Run("k8s_not_found_error_no_stack_trace", func(t *testing.T) {
		ctx := context.Background()
		err := apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "pods"}, "my-pod")
		result := WithErrorField(ctx, err)

		fields := GetLogFields(result)
		if fields == nil {
			t.Fatal("Expected log fields")
		}

		// Should have error field
		if fields["error"] == nil {
			t.Error("Expected error field")
		}

		// Should NOT have stack_trace field
		if fields["stack_trace"] != nil {
			t.Error("Expected no stack_trace for K8s NotFound error")
		}
	})

	t.Run("context_canceled_no_stack_trace", func(t *testing.T) {
		ctx := context.Background()
		result := WithErrorField(ctx, context.Canceled)

		fields := GetLogFields(result)
		if fields == nil {
			t.Fatal("Expected log fields")
		}

		// Should have error field
		if fields["error"] == nil {
			t.Error("Expected error field")
		}

		// Should NOT have stack_trace field
		if fields["stack_trace"] != nil {
			t.Error("Expected no stack_trace for context.Canceled")
		}
	})

	t.Run("api_error_server_error_no_stack_trace", func(t *testing.T) {
		ctx := context.Background()
		err := apperrors.NewAPIError("GET", "/api/v1/clusters", 500, "Internal Server Error", nil, 1, 0, errors.New("server error"))
		result := WithErrorField(ctx, err)

		fields := GetLogFields(result)
		if fields == nil {
			t.Fatal("Expected log fields")
		}

		// Should have error field
		if fields["error"] == nil {
			t.Error("Expected error field")
		}

		// Should NOT have stack_trace field (server errors are expected)
		if fields["stack_trace"] != nil {
			t.Error("Expected no stack_trace for API server error")
		}
	})
}
