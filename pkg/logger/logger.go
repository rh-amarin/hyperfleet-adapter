package logger

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

type Logger interface {
	V(level int32) Logger
	Infof(format string, args ...interface{})
	Extra(key string, value interface{}) Logger
	Info(message string)
	Warning(message string)
	Error(message string)
	Fatal(message string)
}

var _ Logger = &logger{}

type extra map[string]interface{}

type logger struct {
	zapLogger *zap.Logger
	context   context.Context
	level     int32
	extra     extra
}

var defaultLogger *zap.Logger

// InitLogger initializes the default logger
func InitLogger() error {
	var err error
	defaultLogger, err = zap.NewProduction()
	if err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}
	return nil
}

// NewLogger creates a new logger instance with a default verbosity of 1
func NewLogger(ctx context.Context) Logger {
	if defaultLogger == nil {
		// Fallback to production logger if not initialized
		var err error
		defaultLogger, err = zap.NewProduction()
		if err != nil {
			panic(fmt.Sprintf("failed to create fallback logger: %v", err))
		}
	}

	return &logger{
		zapLogger: defaultLogger,
		context:   ctx,
		level:     1,
		extra:     make(extra),
	}
}

func (l *logger) prepareLogPrefix(message string, extra extra) string {
	prefix := " "

	if txid, ok := l.context.Value("txid").(int64); ok {
		prefix = fmt.Sprintf("[tx_id=%d]%s", txid, prefix)
	}

	if txid, ok := l.context.Value("txid").(string); ok {
		prefix = fmt.Sprintf("[tx_id=%s]%s", txid, prefix)
	}

	if adapterID, ok := l.context.Value(AdapterIDKey).(string); ok {
		prefix = fmt.Sprintf("[adapter_id=%s]%s", adapterID, prefix)
	}

	if clusterID, ok := l.context.Value(ClusterIDKey).(string); ok {
		prefix = fmt.Sprintf("[cluster_id=%s]%s", clusterID, prefix)
	}

	if opid, ok := l.context.Value(OpIDKey).(string); ok {
		prefix = fmt.Sprintf("[opid=%s]%s", opid, prefix)
	}

	var args []string
	for k, v := range extra {
		args = append(args, fmt.Sprintf("%s=%v", k, v))
	}

	return fmt.Sprintf("%s %s %s", prefix, message, strings.Join(args, " "))
}

func (l *logger) prepareLogPrefixf(format string, args ...interface{}) string {
	orig := fmt.Sprintf(format, args...)
	prefix := " "

	if txid, ok := l.context.Value("txid").(int64); ok {
		prefix = fmt.Sprintf("[tx_id=%d]%s", txid, prefix)
	}

	if txid, ok := l.context.Value("txid").(string); ok {
		prefix = fmt.Sprintf("[tx_id=%s]%s", txid, prefix)
	}

	if adapterID, ok := l.context.Value(AdapterIDKey).(string); ok {
		prefix = fmt.Sprintf("[adapter_id=%s]%s", adapterID, prefix)
	}

	if clusterID, ok := l.context.Value(ClusterIDKey).(string); ok {
		prefix = fmt.Sprintf("[cluster_id=%s]%s", clusterID, prefix)
	}

	if opid, ok := l.context.Value(OpIDKey).(string); ok {
		prefix = fmt.Sprintf("[opid=%s]%s", opid, prefix)
	}

	return fmt.Sprintf("%s%s", prefix, orig)
}

func (l *logger) V(level int32) Logger {
	return &logger{
		zapLogger: l.zapLogger,
		context:   l.context,
		level:     level,
		extra:     l.extra,
	}
}

// Infof doesn't trigger error tracking (matching rh-trex behavior)
func (l *logger) Infof(format string, args ...interface{}) {
	prefixed := l.prepareLogPrefixf(format, args...)
	l.zapLogger.Info(prefixed)
}

func (l *logger) Extra(key string, value interface{}) Logger {
	l.extra[key] = value
	return l
}

func (l *logger) Info(message string) {
	l.log(message, l.zapLogger.Info)
}

func (l *logger) Warning(message string) {
	l.log(message, l.zapLogger.Warn)
}

func (l *logger) Error(message string) {
	l.log(message, l.zapLogger.Error)
}

func (l *logger) Fatal(message string) {
	l.log(message, l.zapLogger.Fatal)
}

func (l *logger) log(message string, logFunc func(string, ...zap.Field)) {
	prefixed := l.prepareLogPrefix(message, l.extra)
	logFunc(prefixed)
}

const (
	// TxIDKey is the context key for transaction ID
	TxIDKey = "txid"
	// AdapterIDKey is the context key for adapter ID
	AdapterIDKey = "adapter_id"
	// ClusterIDKey is the context key for cluster ID
	ClusterIDKey = "cluster_id"
)
