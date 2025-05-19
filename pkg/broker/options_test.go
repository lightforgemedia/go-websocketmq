package broker_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultOptions(t *testing.T) {
	opts := broker.DefaultOptions()
	
	// Verify defaults
	assert.NotNil(t, opts.Logger)
	assert.NotNil(t, opts.AcceptOptions)
	assert.Greater(t, opts.ClientSendBuffer, 0)
	assert.Greater(t, opts.WriteTimeout, time.Duration(0))
	assert.Greater(t, opts.ReadTimeout, time.Duration(0))
	assert.Greater(t, opts.PingInterval, time.Duration(0))
	assert.Greater(t, opts.ServerRequestTimeout, time.Duration(0))
	
	// Verify ReadTimeout > PingInterval
	assert.Greater(t, opts.ReadTimeout, opts.PingInterval, "ReadTimeout should be greater than PingInterval")
}

func TestNewWithOptions_Valid(t *testing.T) {
	tests := []struct {
		name string
		opts broker.Options
	}{
		{
			name: "default options",
			opts: broker.DefaultOptions(),
		},
		{
			name: "custom values",
			opts: broker.Options{
				Logger:               slog.Default(),
				AcceptOptions:        &websocket.AcceptOptions{OriginPatterns: []string{"*"}},
				ClientSendBuffer:     32,
				WriteTimeout:         15 * time.Second,
				ReadTimeout:          90 * time.Second,
				PingInterval:         20 * time.Second,
				ServerRequestTimeout: 30 * time.Second,
			},
		},
		{
			name: "zero values (should use defaults)",
			opts: broker.Options{
				Logger:               nil,
				AcceptOptions:        nil,
				ClientSendBuffer:     0,
				WriteTimeout:         0,
				ReadTimeout:          0,
				PingInterval:         0,
				ServerRequestTimeout: 0,
			},
		},
		{
			name: "disabled ping (negative value)",
			opts: broker.Options{
				Logger:               slog.Default(),
				AcceptOptions:        &websocket.AcceptOptions{},
				ClientSendBuffer:     16,
				WriteTimeout:         10 * time.Second,
				ReadTimeout:          60 * time.Second,
				PingInterval:         -1, // Disabled
				ServerRequestTimeout: 10 * time.Second,
			},
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := broker.NewWithOptions(tt.opts)
			require.NoError(t, err)
			require.NotNil(t, b)
		})
	}
}

func TestNewWithOptions_Validation(t *testing.T) {
	tests := []struct {
		name        string
		opts        broker.Options
		expectError string
	}{
		{
			name: "negative ClientSendBuffer",
			opts: broker.Options{
				Logger:               slog.Default(),
				AcceptOptions:        &websocket.AcceptOptions{},
				ClientSendBuffer:     -1,
				WriteTimeout:         10 * time.Second,
				ReadTimeout:          60 * time.Second,
				PingInterval:         30 * time.Second,
				ServerRequestTimeout: 10 * time.Second,
			},
			expectError: "ClientSendBuffer must be non-negative",
		},
		{
			name: "negative WriteTimeout",
			opts: broker.Options{
				Logger:               slog.Default(),
				AcceptOptions:        &websocket.AcceptOptions{},
				ClientSendBuffer:     16,
				WriteTimeout:         -1 * time.Second,
				ReadTimeout:          60 * time.Second,
				PingInterval:         30 * time.Second,
				ServerRequestTimeout: 10 * time.Second,
			},
			expectError: "WriteTimeout must be non-negative",
		},
		{
			name: "negative ReadTimeout",
			opts: broker.Options{
				Logger:               slog.Default(),
				AcceptOptions:        &websocket.AcceptOptions{},
				ClientSendBuffer:     16,
				WriteTimeout:         10 * time.Second,
				ReadTimeout:          -1 * time.Second,
				PingInterval:         30 * time.Second,
				ServerRequestTimeout: 10 * time.Second,
			},
			expectError: "ReadTimeout must be non-negative",
		},
		{
			name: "negative ServerRequestTimeout",
			opts: broker.Options{
				Logger:               slog.Default(),
				AcceptOptions:        &websocket.AcceptOptions{},
				ClientSendBuffer:     16,
				WriteTimeout:         10 * time.Second,
				ReadTimeout:          60 * time.Second,
				PingInterval:         30 * time.Second,
				ServerRequestTimeout: -1 * time.Second,
			},
			expectError: "ServerRequestTimeout must be non-negative",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := broker.NewWithOptions(tt.opts)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectError)
		})
	}
}

func TestNewWithOptions_ZeroHandling(t *testing.T) {
	// Test that zero values don't override defaults
	opts := broker.Options{
		Logger:               nil,    // Should use default logger
		AcceptOptions:        nil,    // Should use default accept options
		ClientSendBuffer:     0,      // Should use default buffer size
		WriteTimeout:         0,      // Should use default timeout
		ReadTimeout:          0,      // Should use default timeout
		PingInterval:         0,      // Should use default interval
		ServerRequestTimeout: 0,      // Should use default timeout
	}
	
	// Create broker with zero values
	b, err := broker.NewWithOptions(opts)
	require.NoError(t, err)
	require.NotNil(t, b)
	
	// The broker should still work with defaults
	// (We can't directly inspect the internal config, but we can verify it doesn't panic)
}

// Test removed: functional options are no longer supported