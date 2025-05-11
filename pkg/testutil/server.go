// Package testutil provides common test utilities for the go-websocketmq library.
package testutil

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
)

// TestServer represents a test server for testing.
type TestServer struct {
	T      *testing.T
	Broker *broker.Broker
	Server *httptest.Server
	Ready  chan struct{}
	WsURL  string
}

// NewTestServer creates a new test server for testing.
func NewTestServer(t *testing.T, opts ...broker.Option) *TestServer {
	t.Helper()

	// Create a broker
	finalOpts := append([]broker.Option{broker.WithLogger(DefaultLogger)}, opts...)
	b, err := broker.New(finalOpts...)
	if err != nil {
		t.Fatalf("Failed to create broker: %v", err)
	}

	// Create a server
	s := httptest.NewServer(b.UpgradeHandler())
	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")

	// Create a ready channel
	ready := make(chan struct{}, 1)
	ready <- struct{}{}

	ts := &TestServer{
		T:      t,
		Broker: b,
		Server: s,
		Ready:  ready,
		WsURL:  wsURL,
	}

	// Setup cleanup
	t.Cleanup(func() {
		ts.Close()
	})

	return ts
}

// Close closes the test server.
func (s *TestServer) Close() {
	if s.Server != nil {
		s.Server.Close()
	}
	if s.Broker != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.Broker.Shutdown(ctx)
	}
}
