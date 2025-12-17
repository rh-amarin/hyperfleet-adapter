package logger

import (
	"context"
	"testing"
)

func TestNewLogger(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{
			name:   "create_logger_with_default_config",
			config: DefaultConfig(),
		},
		{
			name: "create_logger_with_json_format",
			config: Config{
				Level:     "debug",
				Format:    "json",
				Output:    "stdout",
				Component: "test-adapter",
				Version:   "v1.0.0",
			},
		},
		{
			name: "create_logger_with_text_format",
			config: Config{
				Level:     "info",
				Format:    "text",
				Output:    "stderr",
				Component: "test-adapter",
				Version:   "v1.0.0",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, err := NewLogger(tt.config)
			if err != nil {
				t.Fatalf("NewLogger returned error: %v", err)
			}
			if log == nil {
				t.Fatal("New returned nil")
			}

			// Type assertion to check implementation
			if _, ok := log.(*logger); !ok {
				t.Error("New didn't return *logger type")
			}
		})
	}
}

func TestNewLoggerInvalidOutput(t *testing.T) {
	_, err := NewLogger(Config{
		Level:     "info",
		Format:    "text",
		Output:    "invalid_output",
		Component: "test",
		Version:   "v1.0.0",
	})
	if err == nil {
		t.Fatal("Expected error for invalid output, got nil")
	}
}

func TestLoggerWith(t *testing.T) {
	log, err := NewLogger(DefaultConfig())
	if err != nil {
		t.Fatalf("NewLogger returned error: %v", err)
	}

	tests := []struct {
		name  string
		key   string
		value interface{}
	}{
		{
			name:  "add_string_field",
			key:   "request_id",
			value: "12345",
		},
		{
			name:  "add_int_field",
			key:   "status_code",
			value: 200,
		},
		{
			name:  "add_bool_field",
			key:   "success",
			value: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := log.With(tt.key, tt.value)
			if result == nil {
				t.Fatal("With() returned nil")
			}

			// Verify it returns a Logger
			impl, ok := result.(*logger)
			if !ok {
				t.Error("With() didn't return *logger type")
			}

			// Verify the field was added
			if impl.fields[tt.key] != tt.value {
				t.Errorf("Expected field %s=%v, got %v", tt.key, tt.value, impl.fields[tt.key])
			}
		})
	}
}

func TestLoggerWithFields(t *testing.T) {
	log, err := NewLogger(DefaultConfig())
	if err != nil {
		t.Fatalf("NewLogger returned error: %v", err)
	}

	fields := map[string]interface{}{
		"cluster_id": "cls-123",
		"event_id":   "evt-456",
		"count":      42,
	}

	result := log.WithFields(fields)
	if result == nil {
		t.Fatal("WithFields() returned nil")
	}

	impl, ok := result.(*logger)
	if !ok {
		t.Error("WithFields() didn't return *logger type")
	}

	for k, v := range fields {
		if impl.fields[k] != v {
			t.Errorf("Expected field %s=%v, got %v", k, v, impl.fields[k])
		}
	}
}

func TestLoggerWithError(t *testing.T) {
	log, err := NewLogger(DefaultConfig())
	if err != nil {
		t.Fatalf("NewLogger returned error: %v", err)
	}

	t.Run("with_error", func(t *testing.T) {
		err := &testError{msg: "test error message"}
		result := log.WithError(err)

		impl, ok := result.(*logger)
		if !ok {
			t.Fatal("WithError() didn't return *logger type")
		}

		if impl.fields["error"] != "test error message" {
			t.Errorf("Expected error field, got %v", impl.fields["error"])
		}
	})

	t.Run("with_nil_error", func(t *testing.T) {
		result := log.WithError(nil)
		// Should return same logger when error is nil
		if result != log {
			t.Error("WithError(nil) should return same logger")
		}
	})
}

type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}

func TestLoggerMethods(t *testing.T) {
	// These tests verify the methods don't panic
	log, err := NewLogger(Config{
		Level:     "debug", // Enable all levels
		Format:    "text",
		Output:    "stdout",
		Component: "test",
		Version:   "v1.0.0",
	})
	if err != nil {
		t.Fatalf("NewLogger returned error: %v", err)
	}
	ctx := context.Background()

	t.Run("Debug_does_not_panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Debug panicked: %v", r)
			}
		}()
		log.Debug(ctx, "Test debug message")
	})

	t.Run("Debugf_does_not_panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Debugf panicked: %v", r)
			}
		}()
		log.Debugf(ctx, "Test debug: %s", "value")
	})

	t.Run("Info_does_not_panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Info panicked: %v", r)
			}
		}()
		log.Info(ctx, "Test info message")
	})

	t.Run("Infof_does_not_panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Infof panicked: %v", r)
			}
		}()
		log.Infof(ctx, "Test info: %s", "value")
	})

	t.Run("Warn_does_not_panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Warn panicked: %v", r)
			}
		}()
		log.Warn(ctx, "Test warning")
	})

	t.Run("Warnf_does_not_panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Warnf panicked: %v", r)
			}
		}()
		log.Warnf(ctx, "Test warning: %s", "value")
	})

	t.Run("Error_does_not_panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Error panicked: %v", r)
			}
		}()
		log.Error(ctx, "Test error")
	})

	t.Run("Errorf_does_not_panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Errorf panicked: %v", r)
			}
		}()
		log.Errorf(ctx, "Test error: %s", "value")
	})
}

func TestLoggerChaining(t *testing.T) {
	log, err := NewLogger(DefaultConfig())
	if err != nil {
		t.Fatalf("NewLogger returned error: %v", err)
	}
	ctx := context.Background()

	t.Run("chain_With_multiple_times", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Chaining panicked: %v", r)
			}
		}()

		log.With("key1", "value1").With("key2", "value2").Info(ctx, "Test chaining")
	})

	t.Run("chain_WithFields_and_With", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Chaining panicked: %v", r)
			}
		}()

		log.WithFields(map[string]interface{}{"a": 1}).With("b", 2).Info(ctx, "Test mixed chaining")
	})

	t.Run("chain_WithError", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Chaining panicked: %v", r)
			}
		}()

		err := &testError{msg: "test error"}
		log.WithError(err).With("extra", "info").Error(ctx, "Error with context")
	})
}

func TestContextKeys(t *testing.T) {
	tests := []struct {
		name     string
		key      contextKey
		expected string
	}{
		{
			name:     "TraceIDKey",
			key:      TraceIDKey,
			expected: "trace_id",
		},
		{
			name:     "SpanIDKey",
			key:      SpanIDKey,
			expected: "span_id",
		},
		{
			name:     "EventIDKey",
			key:      EventIDKey,
			expected: "event_id",
		},
		{
			name:     "ClusterIDKey",
			key:      ClusterIDKey,
			expected: "cluster_id",
		},
		{
			name:     "AdapterKey",
			key:      AdapterKey,
			expected: "adapter",
		},
		{
			name:     "SubscriptionKey",
			key:      SubscriptionKey,
			expected: "subscription",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.key) != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, string(tt.key))
			}
		})
	}
}

func TestContextHelpers(t *testing.T) {
	ctx := context.Background()

	t.Run("WithEventID", func(t *testing.T) {
		ctx := WithEventID(ctx, "evt-123")
		fields := GetLogFields(ctx)
		if fields["event_id"] != "evt-123" {
			t.Errorf("Expected evt-123, got %v", fields["event_id"])
		}
	})

	t.Run("WithClusterID", func(t *testing.T) {
		ctx := WithClusterID(ctx, "cls-456")
		fields := GetLogFields(ctx)
		if fields["cluster_id"] != "cls-456" {
			t.Errorf("Expected cls-456, got %v", fields["cluster_id"])
		}
	})

	t.Run("WithTraceID", func(t *testing.T) {
		ctx := WithTraceID(ctx, "trace-789")
		fields := GetLogFields(ctx)
		if fields["trace_id"] != "trace-789" {
			t.Errorf("Expected trace-789, got %v", fields["trace_id"])
		}
	})
}

func TestConfigFromEnv(t *testing.T) {
	t.Run("defaults_without_env_vars", func(t *testing.T) {
		cfg := ConfigFromEnv()
		if cfg.Level != "info" {
			t.Errorf("Expected default level 'info', got %s", cfg.Level)
		}
		if cfg.Format != "text" {
			t.Errorf("Expected default format 'text', got %s", cfg.Format)
		}
		if cfg.Output != "stdout" {
			t.Errorf("Expected default output 'stdout', got %s", cfg.Output)
		}
	})

	t.Run("reads_LOG_LEVEL_env_var", func(t *testing.T) {
		t.Setenv("LOG_LEVEL", "DEBUG")
		cfg := ConfigFromEnv()
		if cfg.Level != "debug" {
			t.Errorf("Expected level 'debug', got %s", cfg.Level)
		}
	})

	t.Run("reads_LOG_FORMAT_env_var", func(t *testing.T) {
		t.Setenv("LOG_FORMAT", "JSON")
		cfg := ConfigFromEnv()
		if cfg.Format != "json" {
			t.Errorf("Expected format 'json', got %s", cfg.Format)
		}
	})

	t.Run("reads_LOG_OUTPUT_env_var", func(t *testing.T) {
		t.Setenv("LOG_OUTPUT", "stderr")
		cfg := ConfigFromEnv()
		if cfg.Output != "stderr" {
			t.Errorf("Expected output 'stderr', got %s", cfg.Output)
		}
	})
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"debug", "DEBUG"},
		{"DEBUG", "DEBUG"},
		{"info", "INFO"},
		{"INFO", "INFO"},
		{"warn", "WARN"},
		{"warning", "WARN"},
		{"error", "ERROR"},
		{"ERROR", "ERROR"},
		{"invalid", "INFO"}, // Default to INFO
		{"", "INFO"},        // Default to INFO
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			level := parseLevel(tt.input)
			if level.String() != tt.expected {
				t.Errorf("parseLevel(%q) = %s, want %s", tt.input, level.String(), tt.expected)
			}
		})
	}
}


func TestLoggerContextExtraction(t *testing.T) {
	log, err := NewLogger(Config{
		Level:     "debug",
		Format:    "text",
		Output:    "stdout",
		Component: "test",
		Version:   "v1.0.0",
	})
	if err != nil {
		t.Fatalf("NewLogger returned error: %v", err)
	}

	// Build context with various values
	ctx := context.Background()
	ctx = WithTraceID(ctx, "trace-123")
	ctx = WithEventID(ctx, "evt-456")
	ctx = WithClusterID(ctx, "cls-789")

	// This should not panic and should include context values in log
	t.Run("logs_with_context_values", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Logging with context panicked: %v", r)
			}
		}()
		log.Info(ctx, "Test message with context")
	})
}
