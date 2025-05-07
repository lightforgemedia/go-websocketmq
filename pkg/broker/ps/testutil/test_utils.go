package testutil

import (
	"testing"
)

// TestLogger implements the websocketmq.Logger interface for testing
type TestLogger struct {
	t testing.TB
}

func NewTestLogger(t testing.TB) *TestLogger {
	return &TestLogger{t: t}
}

func (l *TestLogger) Debug(msg string, args ...any) { l.t.Logf("DEBUG: "+msg, args...) }
func (l *TestLogger) Info(msg string, args ...any)  { l.t.Logf("INFO: "+msg, args...) }
func (l *TestLogger) Warn(msg string, args ...any)  { l.t.Logf("WARN: "+msg, args...) }
func (l *TestLogger) Error(msg string, args ...any) { l.t.Errorf("ERROR: "+msg, args...) }
