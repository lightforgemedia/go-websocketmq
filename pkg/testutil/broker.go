// Package testutil provides common test utilities for the go-websocketmq library.
package testutil

import (
	"context"
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
	Ready chan struct{} // Channel to signal when the server is ready
}

// NewBrokerServer creates a new broker and httptest.Server for testing.
// It returns a BrokerServer that combines both.
func NewBrokerServer(t *testing.T, brokerOpts ...broker.Options) *BrokerServer {
	t.Helper()

	// Use default options or provided options
	opts := broker.DefaultOptions()
	if len(brokerOpts) > 0 {
		opts = brokerOpts[0]
	}
	// Always use our test logger
	opts.Logger = DefaultLogger
	
	b, err := broker.NewWithOptions(opts)
	if err != nil {
		t.Fatalf("broker.NewWithOptions: %v", err)
	}
	srv := httptest.NewServer(b.UpgradeHandler())
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	t.Cleanup(func() {
		srv.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		b.Shutdown(ctx)
	})

	// Create a ready channel and signal that the server is ready
	ready := make(chan struct{}, 1)
	ready <- struct{}{}

	return &BrokerServer{Broker: b, HTTP: srv, WSURL: wsURL, Ready: ready}
}

// WaitForClient and WaitForClientDisconnect have been moved to wait.go
