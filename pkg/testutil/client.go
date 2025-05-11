package testutil

import (
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/client"
)

// NewTestClient creates a new client connected to the given WebSocket URL.
func NewTestClient(t *testing.T, urlStr string, opts ...client.Option) *client.Client {
	t.Helper()
	defaultOpts := []client.Option{
		client.WithLogger(DefaultLogger),
		client.WithDefaultRequestTimeout(2 * time.Second),
		client.WithAutoReconnect(3, 100*time.Millisecond, 500*time.Millisecond), // Enable auto-reconnect for tests
	}
	finalOpts := append(defaultOpts, opts...)

	cli, err := client.Connect(urlStr, finalOpts...)
	if err != nil && cli == nil { // If connect truly failed and didn't even return a client for reconnect
		t.Fatalf("Client Connect failed and returned nil client: %v", err)
	}
	if cli == nil {
		t.Fatal("Client Connect returned nil client unexpectedly")
	}

	// Setup cleanup
	t.Cleanup(func() {
		cli.Close()
	})

	return cli
}
