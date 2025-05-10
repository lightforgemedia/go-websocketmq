// pkg/broker/ps/testutil/test_utils.go
package testutil

import (
	"fmt"
	"testing"
)

// TestLogger implements the websocketmq.Logger interface for testing purposes.
// It wraps testing.TB (which can be *testing.T or *testing.B) to log messages
// appropriately within the test execution context.
type TestLogger struct {
	t testing.TB
}

// NewTestLogger creates a new TestLogger instance.
func NewTestLogger(t testing.TB) *TestLogger {
	return &TestLogger{t: t}
}

// Debug logs a debug message using t.Logf, prefixed with "DEBUG: ".
func (l *TestLogger) Debug(msg string, args ...any) {
	l.t.Helper() // Marks this function as a test helper
	l.t.Logf("DEBUG: "+msg, args...)
}

// Info logs an informational message using t.Logf, prefixed with "INFO: ".
func (l *TestLogger) Info(msg string, args ...any) {
	l.t.Helper()
	l.t.Logf("INFO: "+msg, args...)
}

// Warn logs a warning message using t.Logf, prefixed with "WARN: ".
func (l *TestLogger) Warn(msg string, args ...any) {
	l.t.Helper()
	l.t.Logf("WARN: "+msg, args...)
}

// Error logs an error message using t.Errorf, prefixed with "ERROR: ".
// t.Errorf will also mark the test as failed.
func (l *TestLogger) Error(msg string, args ...any) {
	l.t.Helper()
	// Create the formatted message first to pass a single string to t.Errorf,
	// which then doesn't try to re-format.
	formattedMsg := fmt.Sprintf("ERROR: "+msg, args...)
	l.t.Error(formattedMsg) // This will mark the test as failed
}