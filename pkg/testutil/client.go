package testutil

import (
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/client"
)

// ClientOptions contains options for creating a test client
type ClientOptions struct {
	Logger               bool // Use the default logger
	RequestTimeout       time.Duration
	AutoReconnect        bool
	MaxReconnectAttempts int
	ReconnectMinDelay    time.Duration
	ReconnectMaxDelay    time.Duration
	ClientName           string
	ClientPingInterval   time.Duration
	WaitForConnection    bool // Wait for the connection to be established
	ConnectionTimeout    time.Duration
}

// DefaultClientOptions returns the default options for creating a test client
func DefaultClientOptions() ClientOptions {
	return ClientOptions{
		Logger:               true,
		RequestTimeout:       2 * time.Second,
		AutoReconnect:        true,
		MaxReconnectAttempts: 3,
		ReconnectMinDelay:    100 * time.Millisecond,
		ReconnectMaxDelay:    500 * time.Millisecond,
		WaitForConnection:    true,
		ConnectionTimeout:    1 * time.Second,
	}
}

// NewTestClient creates a new client connected to the given WebSocket URL.
// It applies the default options and any additional options provided.
func NewTestClient(t *testing.T, urlStr string, opts ...client.Option) *client.Client {
	t.Helper()
	// This function is kept for backward compatibility
	return NewTestClientWithOptions(t, urlStr, DefaultClientOptions(), opts...)
}

// NewTestClientWithOptions creates a new client with the specified options.
func NewTestClientWithOptions(t *testing.T, urlStr string, options ClientOptions, opts ...client.Option) *client.Client {
	t.Helper()

	// Create client.Options from the testutil.ClientOptions
	clientOpts := client.DefaultOptions()

	if options.Logger {
		clientOpts.Logger = DefaultLogger
	}

	if options.RequestTimeout > 0 {
		clientOpts.DefaultRequestTimeout = options.RequestTimeout
	}

	if options.AutoReconnect {
		clientOpts.AutoReconnect = true
		clientOpts.ReconnectAttempts = options.MaxReconnectAttempts
		clientOpts.ReconnectDelayMin = options.ReconnectMinDelay
		clientOpts.ReconnectDelayMax = options.ReconnectMaxDelay
	}

	if options.ClientName != "" {
		clientOpts.ClientName = options.ClientName
	}

	if options.ClientPingInterval != 0 {
		clientOpts.PingInterval = options.ClientPingInterval
	}

	// The client.ConnectWithOptions will handle the functional options

	// Connect the client with Options
	cli, err := client.ConnectWithOptions(urlStr, clientOpts, opts...)
	if err != nil && cli == nil { // If connect truly failed and didn't even return a client for reconnect
		t.Fatalf("Client Connect failed and returned nil client: %v", err)
	}
	if cli == nil {
		t.Fatal("Client Connect returned nil client unexpectedly")
	}

	// Wait for connection if requested
	if options.WaitForConnection {
		// Give a brief moment for connection to establish
		time.Sleep(100 * time.Millisecond)

		// We don't have a direct IsConnected method, so we'll just wait a bit
		// In a real implementation, we might try a ping or check some internal state
	}

	// Setup cleanup
	t.Cleanup(func() {
		cli.Close()
	})

	return cli
}
