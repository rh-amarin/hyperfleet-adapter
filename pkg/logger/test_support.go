package logger

import (
	"bytes"
	"strings"
	"sync"
)

var (
	testLoggerInstance Logger
	testLoggerOnce     sync.Once
)

// NewTestLogger returns a shared logger instance suitable for tests.
// Uses singleton pattern to avoid creating multiple logger instances.
// Configured with "error" level to minimize noise during test runs.
func NewTestLogger() Logger {
	testLoggerOnce.Do(func() {
		var err error
		testLoggerInstance, err = NewLogger(Config{
			Level:     "error",
			Format:    "text",
			Output:    "stderr",
			Component: "test",
			Version:   "test",
		})
		if err != nil {
			panic(err)
		}
	})
	return testLoggerInstance
}

// LogCapture wraps a buffer to capture and inspect log output.
type LogCapture struct {
	buf *bytes.Buffer
	mu  sync.Mutex
}

// Messages returns all captured log output as a string.
func (c *LogCapture) Messages() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// Contains checks if the captured log output contains the given substring.
func (c *LogCapture) Contains(substr string) bool {
	return strings.Contains(c.Messages(), substr)
}

// Reset clears all captured messages.
func (c *LogCapture) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf.Reset()
}

// NewCaptureLogger creates a real logger that writes to a buffer for testing.
// Returns the logger and a LogCapture to inspect the output.
//
// Example:
//
//	log, capture := logger.NewCaptureLogger()
//	// ... use log in your code ...
//	if capture.Contains("expected message") { ... }
func NewCaptureLogger() (Logger, *LogCapture) {
	buf := &bytes.Buffer{}
	capture := &LogCapture{buf: buf}

	log, err := NewLogger(Config{
		Level:     "debug", // Capture all levels
		Format:    "text",
		Writer:    buf,
		Component: "test",
		Version:   "test",
	})
	if err != nil {
		panic(err)
	}

	return log, capture
}
