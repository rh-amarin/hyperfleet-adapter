package logger

import (
	"context"
	"fmt"
	"strings"

	"github.com/golang/glog"
)

type Logger interface {
	V(level int32) Logger
	Infof(format string, args ...interface{})
	Extra(key string, value interface{}) Logger
	Info(message string)
	Warning(message string)
	Error(message string)
	Fatal(message string)
	Flush() // Flush pending log entries immediately
}

var _ Logger = &logger{}

type extra map[string]interface{}

type logger struct {
	context   context.Context
	level     int32
	extra     extra
}

// NewLogger creates a new logger instance with a default verbosity of 1
func NewLogger(ctx context.Context) Logger {
	logger := &logger{
		context: ctx,
		level:   1,
		extra:   make(extra),
	}
	return logger
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

	if evtid, ok := l.context.Value(EvtIDKey).(string); ok {
		prefix = fmt.Sprintf("[%s=%s]%s", EvtIDKey, evtid, prefix)
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

	if evtid, ok := l.context.Value(EvtIDKey).(string); ok {
		prefix = fmt.Sprintf("[%s=%s]%s", EvtIDKey, evtid, prefix)
	}

	return fmt.Sprintf("%s%s", prefix, orig)
}

// copyExtra creates a deep copy of the extra map to avoid shared state bugs
func copyExtra(e extra) extra {
	if e == nil {
		return make(extra)
	}
	newExtra := make(extra, len(e))
	for k, v := range e {
		newExtra[k] = v
	}
	return newExtra
}

func (l *logger) V(level int32) Logger {
	return &logger{
		context: l.context,
		level:   level,
		extra:   copyExtra(l.extra),
	}
}

// Infof doesn't trigger error tracking (matching Sentinel behavior)
func (l *logger) Infof(format string, args ...interface{}) {
	prefixed := l.prepareLogPrefixf(format, args...)
	glog.V(glog.Level(l.level)).Infof("%s", prefixed)
}

func (l *logger) Extra(key string, value interface{}) Logger {
	l.extra[key] = value
	return l
}

func (l *logger) Info(message string) {
	l.log(message, glog.V(glog.Level(l.level)).Infoln)
}

func (l *logger) Warning(message string) {
	l.log(message, glog.Warningln)
}

func (l *logger) Error(message string) {
	l.log(message, glog.Errorln)
}

func (l *logger) Fatal(message string) {
	l.log(message, glog.Fatalln)
}

// Flush flushes pending log entries immediately
func (l *logger) Flush() {
	glog.Flush()
}

func (l *logger) log(message string, glogFunc func(args ...interface{})) {
	prefixed := l.prepareLogPrefix(message, l.extra)
	glogFunc(prefixed)
}

// Flush flushes all pending log I/O
func Flush() {
	glog.Flush()
}

// contextKey is a custom type for context keys to avoid collisions
type contextKey string

const (
	// TxIDKey is the context key for transaction ID
	TxIDKey contextKey = "txid"
	// AdapterIDKey is the context key for adapter ID
	AdapterIDKey contextKey = "adapter_id"
	// ClusterIDKey is the context key for cluster ID
	ClusterIDKey contextKey = "cluster_id"
	// EvtIDKey is the context key for event ID
	EvtIDKey contextKey = "evt_id"
)

// WithEventID wraps a logger to add event ID to all log messages as evtid.
// This works with any Logger implementation (including test loggers).
func WithEventID(log Logger, eventID string) Logger {
	// If the logger is our internal logger type, create a new one with updated context
	if l, ok := log.(*logger); ok {
		ctx := context.WithValue(l.context, EvtIDKey, eventID)
		return &logger{
			context: ctx,
			level:   l.level,
			extra:   copyExtra(l.extra),
		}
	}
	// For other logger implementations (like test loggers), return as-is
	// They should extract event ID from context if needed
	return log
}
