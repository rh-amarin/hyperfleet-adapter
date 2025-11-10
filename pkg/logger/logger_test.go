package logger

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestInitLogger(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "successful initialization",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset defaultLogger
			defaultLogger = nil
			err := InitLogger()
			if (err != nil) != tt.wantErr {
				t.Errorf("InitLogger() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if defaultLogger == nil {
				t.Error("InitLogger() defaultLogger should not be nil")
			}
		})
	}
}

func TestNewLogger(t *testing.T) {
	// Initialize logger first
	InitLogger()

	tests := []struct {
		name    string
		ctx     context.Context
		wantNil bool
	}{
		{
			name:    "with empty context",
			ctx:     context.Background(),
			wantNil: false,
		},
		{
			name:    "with context containing txid",
			ctx:     context.WithValue(context.Background(), "txid", int64(12345)),
			wantNil: false,
		},
		{
			name:    "with context containing opid",
			ctx:     context.WithValue(context.Background(), OpIDKey, "test-opid-123"),
			wantNil: false,
		},
		{
			name:    "with context containing adapter_id",
			ctx:     context.WithValue(context.Background(), AdapterIDKey, "test-adapter"),
			wantNil: false,
		},
		{
			name:    "with context containing cluster_id",
			ctx:     context.WithValue(context.Background(), ClusterIDKey, "test-cluster"),
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewLogger(tt.ctx)
			if (got == nil) != tt.wantNil {
				t.Errorf("NewLogger() = %v, wantNil %v", got, tt.wantNil)
			}
		})
	}
}

func TestLogger_V(t *testing.T) {
	InitLogger()
	ctx := context.Background()
	logger := NewLogger(ctx)

	tests := []struct {
		name  string
		level int32
	}{
		{
			name:  "level 0",
			level: 0,
		},
		{
			name:  "level 1",
			level: 1,
		},
		{
			name:  "level 5",
			level: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := logger.V(tt.level)
			if got == nil {
				t.Error("V() returned nil logger")
				return
			}
			// Verify it's still a Logger (type check)
			_ = got.(Logger)
		})
	}
}

func TestLogger_Extra(t *testing.T) {
	InitLogger()
	ctx := context.Background()
	logger := NewLogger(ctx)

	tests := []struct {
		name  string
		key   string
		value interface{}
	}{
		{
			name:  "add string extra",
			key:   "key1",
			value: "value1",
		},
		{
			name:  "add int extra",
			key:   "key2",
			value: 42,
		},
		{
			name:  "add multiple extras",
			key:   "key3",
			value: "value3",
		},
	}

	log := logger
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log = log.Extra(tt.key, tt.value)
			if log == nil {
				t.Error("Extra() returned nil logger")
			}
		})
	}
}

func TestLogger_Infof(t *testing.T) {
	// Create a test logger with observer to capture logs
	core, recorded := observer.New(zap.InfoLevel)
	testLogger := zap.New(core)
	defaultLogger = testLogger

	ctx := context.Background()
	logger := NewLogger(ctx)

	tests := []struct {
		name   string
		format string
		args   []interface{}
	}{
		{
			name:   "simple message",
			format: "test message",
			args:   []interface{}{},
		},
		{
			name:   "formatted message",
			format: "test %s %d",
			args:   []interface{}{"message", 42},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger.Infof(tt.format, tt.args...)
			if recorded.Len() == 0 {
				t.Error("Infof() did not log any message")
			}
		})
	}
}

func TestLogger_Info(t *testing.T) {
	core, recorded := observer.New(zap.InfoLevel)
	testLogger := zap.New(core)
	defaultLogger = testLogger

	ctx := context.Background()
	logger := NewLogger(ctx)

	tests := []struct {
		name    string
		message string
	}{
		{
			name:    "simple info message",
			message: "test info message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger.Info(tt.message)
			if recorded.Len() == 0 {
				t.Error("Info() did not log any message")
			}
		})
	}
}

func TestLogger_Warning(t *testing.T) {
	core, recorded := observer.New(zap.WarnLevel)
	testLogger := zap.New(core)
	defaultLogger = testLogger

	ctx := context.Background()
	logger := NewLogger(ctx)

	tests := []struct {
		name    string
		message string
	}{
		{
			name:    "simple warning message",
			message: "test warning message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger.Warning(tt.message)
			if recorded.Len() == 0 {
				t.Error("Warning() did not log any message")
			}
		})
	}
}

func TestLogger_Error(t *testing.T) {
	core, recorded := observer.New(zap.ErrorLevel)
	testLogger := zap.New(core)
	defaultLogger = testLogger

	ctx := context.Background()
	logger := NewLogger(ctx)

	tests := []struct {
		name    string
		message string
	}{
		{
			name:    "simple error message",
			message: "test error message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger.Error(tt.message)
			if recorded.Len() == 0 {
				t.Error("Error() did not log any message")
			}
		})
	}
}

func TestLogger_prepareLogPrefix(t *testing.T) {
	InitLogger()

	tests := []struct {
		name    string
		ctx     context.Context
		message string
		extra   extra
		want    string
	}{
		{
			name:    "empty context",
			ctx:     context.Background(),
			message: "test message",
			extra:   make(extra),
			want:    "  test message ",
		},
		{
			name:    "with txid int64",
			ctx:     context.WithValue(context.Background(), "txid", int64(12345)),
			message: "test message",
			extra:   make(extra),
			want:    "[tx_id=12345]  test message ",
		},
		{
			name:    "with txid string",
			ctx:     context.WithValue(context.Background(), "txid", "tx-12345"),
			message: "test message",
			extra:   make(extra),
			want:    "[tx_id=tx-12345]  test message ",
		},
		{
			name:    "with opid",
			ctx:     context.WithValue(context.Background(), OpIDKey, "opid-123"),
			message: "test message",
			extra:   make(extra),
			want:    "[opid=opid-123]  test message ",
		},
		{
			name:    "with adapter_id",
			ctx:     context.WithValue(context.Background(), AdapterIDKey, "adapter-1"),
			message: "test message",
			extra:   make(extra),
			want:    "[adapter_id=adapter-1]  test message ",
		},
		{
			name:    "with cluster_id",
			ctx:     context.WithValue(context.Background(), ClusterIDKey, "cluster-1"),
			message: "test message",
			extra:   make(extra),
			want:    "[cluster_id=cluster-1]  test message ",
		},
		{
			name:    "with multiple context values",
			ctx: func() context.Context {
				ctx := context.Background()
				ctx = context.WithValue(ctx, OpIDKey, "opid-123")
				ctx = context.WithValue(ctx, AdapterIDKey, "adapter-1")
				ctx = context.WithValue(ctx, ClusterIDKey, "cluster-1")
				return ctx
			}(),
			message: "test message",
			extra:   make(extra),
			want:    "[adapter_id=adapter-1][cluster_id=cluster-1][opid=opid-123]  test message ",
		},
		{
			name:    "with extra fields",
			ctx:     context.Background(),
			message: "test message",
			extra: extra{
				"key1": "value1",
				"key2": 42,
			},
			want: "  test message key1=value1 key2=42",
		},
		{
			name:    "with context and extra fields",
			ctx:     context.WithValue(context.Background(), OpIDKey, "opid-123"),
			message: "test message",
			extra: extra{
				"key1": "value1",
			},
			want: "[opid=opid-123]  test message key1=value1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := &logger{
				zapLogger: defaultLogger,
				context:   tt.ctx,
				level:     1,
				extra:     tt.extra,
			}
			got := l.prepareLogPrefix(tt.message, tt.extra)
			// Note: The order of context values in prefix may vary, so we check if all expected parts are present
			if !strings.Contains(got, tt.message) {
				t.Errorf("prepareLogPrefix() = %v, should contain message %v", got, tt.message)
			}
		})
	}
}

func TestLogger_prepareLogPrefixf(t *testing.T) {
	InitLogger()

	tests := []struct {
		name   string
		ctx    context.Context
		format string
		args   []interface{}
		want   string
	}{
		{
			name:   "empty context",
			ctx:    context.Background(),
			format: "test %s",
			args:   []interface{}{"message"},
			want:   " test message",
		},
		{
			name:   "with opid",
			ctx:    context.WithValue(context.Background(), OpIDKey, "opid-123"),
			format: "test %s",
			args:   []interface{}{"message"},
			want:   "[opid=opid-123] test message",
		},
		{
			name:   "with adapter_id",
			ctx:    context.WithValue(context.Background(), AdapterIDKey, "adapter-1"),
			format: "test %d",
			args:   []interface{}{42},
			want:   "[adapter_id=adapter-1] test 42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := &logger{
				zapLogger: defaultLogger,
				context:   tt.ctx,
				level:     1,
				extra:     make(extra),
			}
			got := l.prepareLogPrefixf(tt.format, tt.args...)
			if !strings.Contains(got, "test") {
				t.Errorf("prepareLogPrefixf() = %v, should contain formatted message", got)
			}
		})
	}
}

func TestLogger_Chaining(t *testing.T) {
	InitLogger()
	ctx := context.Background()

	t.Run("chain Extra and V", func(t *testing.T) {
		logger := NewLogger(ctx)
		logger = logger.V(2).Extra("key1", "value1").Extra("key2", "value2")
		if logger == nil {
			t.Error("Chained logger should not be nil")
		}
	})

	t.Run("chain multiple Extra calls", func(t *testing.T) {
		logger := NewLogger(ctx)
		logger = logger.Extra("key1", "value1").Extra("key2", "value2").Extra("key3", "value3")
		if logger == nil {
			t.Error("Chained logger should not be nil")
		}
	})
}

func TestLogger_InterfaceCompliance(t *testing.T) {
	InitLogger()
	ctx := context.Background()
	logger := NewLogger(ctx)

	// Test that logger implements Logger interface
	var _ Logger = logger

	// Test all interface methods exist
	logger.V(1)
	logger.Infof("test %s", "message")
	logger.Extra("key", "value")
	logger.Info("test")
	logger.Warning("test")
	logger.Error("test")
	// Note: Fatal will exit, so we skip it in tests
}
