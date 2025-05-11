// Package testutil provides common test utilities for the go-websocketmq library.
package testutil

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
)

// WaitForClient waits for a client to connect to the broker.
// It returns the client handle and nil if the client connects within the timeout.
// It returns nil and an error if the client does not connect within the timeout.
func WaitForClient(t *testing.T, b *broker.Broker, clientID string, timeout time.Duration) (broker.ClientHandle, error) {
	t.Helper()

	// First, give a short delay to allow the client to connect
	time.Sleep(200 * time.Millisecond)

	// Try to get the client immediately
	handle, err := b.GetClient(clientID)
	if err == nil && handle != nil {
		t.Logf("WaitForClient: Found client %s immediately", clientID)
		return handle, nil
	}

	// If not found, wait with polling
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		handle, err := b.GetClient(clientID)
		if err == nil && handle != nil {
			t.Logf("WaitForClient: Found client %s after polling", clientID)
			return handle, nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Log all connected clients for debugging
	var connectedClients []string
	b.IterateClients(func(ch broker.ClientHandle) bool {
		connectedClients = append(connectedClients, ch.ID())
		return true
	})

	t.Logf("WaitForClient: Client %s not found. Connected clients: %v", clientID, connectedClients)
	return nil, fmt.Errorf("client %s did not connect within %v", clientID, timeout)
}

// WaitForClientDisconnect waits for a client to disconnect from the broker.
// It returns nil if the client disconnects within the timeout.
// It returns an error if the client does not disconnect within the timeout.
func WaitForClientDisconnect(t *testing.T, b *broker.Broker, clientID string, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := b.GetClient(clientID)
		if err != nil {
			return nil // Client is gone, success
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("client %s did not disconnect within %v", clientID, timeout)
}

// WaitFor is a generic utility to wait for a condition to be true.
// It returns nil if the condition becomes true within the timeout.
// It returns an error if the condition does not become true within the timeout.
func WaitFor(t *testing.T, description string, timeout time.Duration, condition func() bool) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return nil // Condition is true, success
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("condition '%s' not met within %v", description, timeout)
}

// WaitForWithContext is a generic utility to wait for a condition to be true with context support.
// It returns nil if the condition becomes true before the context is canceled or times out.
// It returns an error if the condition does not become true before the context is canceled or times out.
func WaitForWithContext(ctx context.Context, t *testing.T, description string, condition func() bool) error {
	t.Helper()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context canceled while waiting for condition '%s': %v", description, ctx.Err())
		case <-ticker.C:
			if condition() {
				return nil // Condition is true, success
			}
		}
	}
}
