package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker_client"
	"github.com/lightforgemedia/go-websocketmq/pkg/client"
	"github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
)

// Example demonstrating the Options pattern and GenericClientRequest

// Request and response types for our example
type EchoRequest struct {
	Message string `json:"message"`
}

type EchoResponse struct {
	Echo      string    `json:"echo"`
	Timestamp time.Time `json:"timestamp"`
	ClientID  string    `json:"clientId"`
}

type CalculateRequest struct {
	A int `json:"a"`
	B int `json:"b"`
}

type CalculateResponse struct {
	Result int `json:"result"`
}

func main() {
	// Create a custom logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Method 1: Using DefaultOptions with modifications
	opts := broker.DefaultOptions()
	opts.Logger = logger
	opts.PingInterval = 15 * time.Second
	opts.ServerRequestTimeout = 30 * time.Second
	opts.AcceptOptions = &websocket.AcceptOptions{
		Subprotocols: []string{"websocketmq"},
	}

	// Create broker with NewWithOptions
	broker1, err := broker.NewWithOptions(opts)
	if err != nil {
		log.Fatal("Failed to create broker with options:", err)
	}

	// Add some custom functional options on top
	broker2, err := broker.NewWithOptions(opts, 
		broker.WithClientSendBuffer(32),  // Override the buffer size
		broker.WithWriteTimeout(15 * time.Second),
	)
	if err != nil {
		log.Fatal("Failed to create broker with mixed options:", err)
	}

	// Register handlers on broker2
	err = broker2.HandleClientRequest("echo", func(ch broker.ClientHandle, req EchoRequest) (EchoResponse, error) {
		return EchoResponse{
			Echo:      req.Message,
			Timestamp: time.Now(),
			ClientID:  ch.ID(),
		}, nil
	})
	if err != nil {
		log.Fatal("Failed to register echo handler:", err)
	}

	err = broker2.HandleClientRequest("calculate", func(ch broker.ClientHandle, req CalculateRequest) (CalculateResponse, error) {
		return CalculateResponse{
			Result: req.A + req.B,
		}, nil
	})
	if err != nil {
		log.Fatal("Failed to register calculate handler:", err)
	}

	// Setup HTTP server with broker2
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", broker2.UpgradeHandler())
	mux.Handle("/", broker_client.Handler("/"))

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	// Start server
	go func() {
		logger.Info("Server starting on :8080")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server error:", err)
		}
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Now demonstrate using GenericClientRequest from a client
	ctx := context.Background()
	wsClient, err := client.NewClient(
		"ws://localhost:8080/ws",
		client.WithLogger(logger),
		client.WithReconnection(true),
	)
	if err != nil {
		log.Fatal("Failed to create client:", err)
	}

	// Connect the client
	err = wsClient.Connect(ctx)
	if err != nil {
		log.Fatal("Failed to connect client:", err)
	}

	// Wait for registration
	time.Sleep(500 * time.Millisecond)

	// Now get the client handle from the broker
	var clientHandle broker.ClientHandle
	broker2.IterateClients(func(ch broker.ClientHandle) bool {
		clientHandle = ch
		return false // stop after first client
	})

	if clientHandle == nil {
		log.Fatal("No client found")
	}

	// Use GenericClientRequest for type-safe RPC calls
	echoResp, err := broker.GenericClientRequest[EchoResponse](
		clientHandle,
		ctx,
		"echo",
		EchoRequest{Message: "Hello from server using GenericClientRequest!"},
		5*time.Second,
	)
	if err != nil {
		log.Fatal("Echo request failed:", err)
	}
	fmt.Printf("Echo response: %+v\n", echoResp)

	// Another example with calculate
	calcResp, err := broker.GenericClientRequest[CalculateResponse](
		clientHandle,
		ctx,
		"calculate",
		CalculateRequest{A: 10, B: 20},
		5*time.Second,
	)
	if err != nil {
		log.Fatal("Calculate request failed:", err)
	}
	fmt.Printf("Calculate response: %+v\n", calcResp)

	// Example with nil response (when we don't care about the response)
	_, err = broker.GenericClientRequest[json.RawMessage](
		clientHandle,
		ctx,
		"log",
		map[string]string{"level": "info", "message": "Server initiated log"},
		5*time.Second,
	)
	if err != nil {
		// This will fail if client doesn't have a handler for "log"
		fmt.Printf("Log request failed (expected): %v\n", err)
	}

	// Shutdown gracefully
	fmt.Println("\nPress Ctrl+C to shutdown...")
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nShutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Shutdown sequence
	wsClient.Disconnect()
	broker2.Shutdown(shutdownCtx)
	broker1.Shutdown(shutdownCtx)
	server.Shutdown(shutdownCtx)

	fmt.Println("Shutdown complete")
}