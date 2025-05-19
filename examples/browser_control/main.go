package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
)

//go:embed static/*
var staticFiles embed.FS

func main() {
	// Create broker using the Options pattern
	opts := broker.DefaultOptions()
	// The DefaultLogger is already set in DefaultOptions
	
	b, err := broker.NewWithOptions(opts)
	if err != nil {
		log.Fatalf("Failed to create broker: %v", err)
	}

	// Create HTTP server
	mux := http.NewServeMux()

	// Register both WebSocket handler and JavaScript client handler
	// Using custom options to match the previous endpoint
	handlerOpts := broker.DefaultWebSocketMQHandlerOptions()
	handlerOpts.WebSocketPath = "/ws" // to match existing setup
	b.RegisterHandlers(mux, handlerOpts)

	// Serve static files
	mux.Handle("/", http.FileServer(http.FS(staticFiles)))

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Server starting on http://localhost:8080")
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	<-sigChan

	log.Println("Shutting down...")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}

	if err := b.Shutdown(ctx); err != nil {
		log.Printf("Broker shutdown error: %v", err)
	}

	fmt.Println("Server stopped")
}