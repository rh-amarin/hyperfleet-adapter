package logger

import (
	"context"
	"strings"
)

// contextKey is a custom type for context keys to avoid collisions
type contextKey string

const (
	// Correlation fields (distributed tracing)
	TraceIDKey contextKey = "trace_id"
	SpanIDKey  contextKey = "span_id"
	EventIDKey contextKey = "event_id"

	// Resource fields
	ClusterIDKey    contextKey = "cluster_id"
	ResourceTypeKey contextKey = "resource_type"
	ResourceIDKey   contextKey = "resource_id"

	// Adapter-specific fields
	AdapterKey            contextKey = "adapter"
	ObservedGenerationKey contextKey = "observed_generation"
	SubscriptionKey       contextKey = "subscription"

	// Dynamic log fields
	LogFieldsKey contextKey = "log_fields"
)

// LogFields holds dynamic key-value pairs for logging
type LogFields map[string]interface{}

// -----------------------------------------------------------------------------
// Context Setters
// -----------------------------------------------------------------------------

// WithLogField adds a single dynamic log field to the context
// These fields will be extracted and included in all log entries
func WithLogField(ctx context.Context, key string, value interface{}) context.Context {
	fields := GetLogFields(ctx)
	if fields == nil {
		fields = make(LogFields)
	}
	fields[key] = value
	return context.WithValue(ctx, LogFieldsKey, fields)
}

// WithLogFields adds multiple dynamic log fields to the context
// These fields will be extracted and included in all log entries
func WithLogFields(ctx context.Context, newFields LogFields) context.Context {
	fields := GetLogFields(ctx)
	if fields == nil {
		fields = make(LogFields)
	}
	for k, v := range newFields {
		fields[k] = v
	}
	return context.WithValue(ctx, LogFieldsKey, fields)
}

// WithDynamicResourceID adds a resource ID as a dynamic log field
// The field name is derived from the resource type (e.g., "Cluster" -> "cluster_id", "NodePool" -> "nodepool_id")
func WithDynamicResourceID(ctx context.Context, resourceType string, resourceID string) context.Context {
	fieldName := strings.ToLower(resourceType) + "_id"
	return WithLogField(ctx, fieldName, resourceID)
}

// WithTraceID returns a context with the trace ID set
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return WithLogField(ctx, string(TraceIDKey), traceID)
}

// WithSpanID returns a context with the span ID set
func WithSpanID(ctx context.Context, spanID string) context.Context {
	return WithLogField(ctx, string(SpanIDKey), spanID)
}

// WithEventID returns a context with the event ID set
func WithEventID(ctx context.Context, eventID string) context.Context {
	return WithLogField(ctx, string(EventIDKey), eventID)
}

// WithClusterID returns a context with the cluster ID set
func WithClusterID(ctx context.Context, clusterID string) context.Context {
	return WithLogField(ctx, string(ClusterIDKey), clusterID)
}

// WithResourceType returns a context with the resource type set
func WithResourceType(ctx context.Context, resourceType string) context.Context {
	return WithLogField(ctx, string(ResourceTypeKey), resourceType)
}

// WithResourceID returns a context with the resource ID set
func WithResourceID(ctx context.Context, resourceID string) context.Context {
	return WithLogField(ctx, string(ResourceIDKey), resourceID)
}

// WithAdapter returns a context with the adapter name set
func WithAdapter(ctx context.Context, adapter string) context.Context {
	return WithLogField(ctx, string(AdapterKey), adapter)
}

// WithObservedGeneration returns a context with the observed generation set
func WithObservedGeneration(ctx context.Context, generation int64) context.Context {
	return WithLogField(ctx, string(ObservedGenerationKey), generation)
}

// WithSubscription returns a context with the subscription name set
func WithSubscription(ctx context.Context, subscription string) context.Context {
	return WithLogField(ctx, string(SubscriptionKey), subscription)
}

// -----------------------------------------------------------------------------
// Context Getters
// -----------------------------------------------------------------------------

// GetLogFields returns the dynamic log fields from the context, or nil if not set
func GetLogFields(ctx context.Context) LogFields {
	if ctx == nil {
		return nil
	}
	if v, ok := ctx.Value(LogFieldsKey).(LogFields); ok {
		// Return a copy to avoid mutation
		fields := make(LogFields, len(v))
		for k, val := range v {
			fields[k] = val
		}
		return fields
	}
	return nil
}
