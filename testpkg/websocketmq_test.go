package testpkg

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/client"
)

// Simple message types for testing
type TestRequest struct {
	Message string `json:"message"`
}

type TestResponse struct {
	Reply string `json:"reply"`
}

// Simple logger implementation
type testLogger struct{}

func (l *testLogger) Printf(format string, v ...interface{}) {
	fmt.Printf(format+"\n", v...)
}

func TestWebSocketMQ(t *testing.T) {
	// Create a simple logger
	logHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelInfo,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.SourceKey {
				file := a.Value.Any().(*slog.Source).File
				line := a.Value.Any().(*slog.Source).Line
				function := a.Value.Any().(*slog.Source).Function
				fp := filepath.Base(file)

				fmt.Println("Source", file, line, function)
				a.Value = slog.StringValue(fmt.Sprintf("%s:%d (%s)", fp, line, function))
			}
			return a
		},
	})
	logger := slog.New(logHandler)

	// Create a broker
	b, err := broker.New(
		broker.WithLogger(logger),
		broker.WithPingInterval(5*time.Second),
	)
	if err != nil {
		t.Fatalf("Failed to create broker: %v", err)
	}

	// Register a request handler
	err = b.OnRequest("test:echo", func(client broker.ClientHandle, req *TestRequest) (TestResponse, error) {
		fmt.Printf("Server received request: %s from client %s\n", req.Message, client.ID())
		return TestResponse{Reply: "Echo: " + req.Message}, nil
	})
	if err != nil {
		t.Fatalf("Failed to register handler: %v", err)
	}

	// Start the HTTP server in a goroutine
	_, shutdown := startTestServer(t, b)
	defer shutdown()

	// Connect a client
	cli, err := client.Connect("ws://localhost:8083/ws",
		client.WithLogger(logger),
		client.WithDefaultRequestTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatalf("Failed to connect client: %v", err)
	}
	defer cli.Close()

	// Wait for connection to establish
	time.Sleep(1 * time.Second)

	// Send a request
	req := TestRequest{Message: "Hello from test"}
	ctx := context.Background()
	resp, err := client.GenericRequest[TestResponse](cli, ctx, "test:echo", req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	// Verify response
	if resp.Reply != "Echo: Hello from test" {
		t.Errorf("Unexpected response: %s", resp.Reply)
	} else {
		fmt.Printf("Test passed! Received response: %s\n", resp.Reply)
	}
}

func startTestServer(t *testing.T, b *broker.Broker) (server *http.Server, shutdown func()) {
	// Create an HTTP server
	mux := http.NewServeMux()
	mux.Handle("/ws", b.UpgradeHandler())

	server = &http.Server{
		Addr:    ":8083",
		Handler: mux,
	}

	// Start the server in a goroutine
	go func() {
		fmt.Println("Starting test server on :8083/ws")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Logf("Server error: %v", err)
		}
	}()

	// Return a shutdown function
	shutdown = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		fmt.Println("Shutting down test server...")
		if err := server.Shutdown(ctx); err != nil {
			t.Logf("Server shutdown error: %v", err)
		}

		if err := b.Shutdown(ctx); err != nil {
			t.Logf("Broker shutdown error: %v", err)
		}
	}

	return server, shutdown
}
