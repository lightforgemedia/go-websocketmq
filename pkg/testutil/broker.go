// Package testutil provides common test utilities for the go-websocketmq library.
package testutil

import (
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
)

var (
	// Default logger for tests
	defaultSlogHandler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: true,
	})
	DefaultLogger = slog.New(defaultSlogHandler)
)

// BrokerServer combines a broker and its HTTP server for testing
type BrokerServer struct {
	*broker.Broker
	HTTP  *httptest.Server
	WSURL string
}

// NewBrokerServer creates a new broker and httptest.Server for testing.
// It returns a BrokerServer that combines both.
func NewBrokerServer(t *testing.T, opts ...broker.Option) *BrokerServer {
	t.Helper()

	finalOpts := append([]broker.Option{broker.WithLogger(DefaultLogger)}, opts...)
	b, err := broker.New(finalOpts...)
	if err != nil {
		t.Fatalf("broker.New: %v", err)
	}
	srv := httptest.NewServer(b.UpgradeHandler())
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	t.Cleanup(func() {
		srv.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		b.Shutdown(ctx)
	})

	return &BrokerServer{Broker: b, HTTP: srv, WSURL: wsURL}
}

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
