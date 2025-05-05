// examples/simple/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/lightforgemedia/go-websocketmq"
)

// Simple logger implementation
type Logger struct{}

func (l *Logger) Debug(msg string, args ...any) { log.Printf("DEBUG: "+msg, args...) }
func (l *Logger) Info(msg string, args ...any)  { log.Printf("INFO: "+msg, args...) }
func (l *Logger) Warn(msg string, args ...any)  { log.Printf("WARN: "+msg, args...) }
func (l *Logger) Error(msg string, args ...any) { log.Printf("ERROR: "+msg, args...) }

func main() {
	// Create a logger
	logger := &Logger{}
	logger.Info("Starting WebSocketMQ example server")

	// Create a broker with default options
	brokerOpts := websocketmq.DefaultBrokerOptions()
	broker := websocketmq.NewPubSubBroker(logger, brokerOpts)

	// Create a WebSocket handler
	handlerOpts := websocketmq.DefaultHandlerOptions()
	wsHandler := websocketmq.NewHandler(broker, logger, handlerOpts)

	// Create a server mux
	mux := http.NewServeMux()

	// Mount WebSocket handler
	mux.Handle("/ws", wsHandler)

	// Mount JavaScript client
	mux.Handle("/wsmq/", http.StripPrefix("/wsmq/", websocketmq.ScriptHandler()))

	// Mount static file server
	mux.Handle("/", http.FileServer(http.Dir("./examples/simple/static")))

	// Subscribe to client messages
	broker.Subscribe(context.Background(), "client.hello", func(ctx context.Context, m *websocketmq.Message) (*websocketmq.Message, error) {
		logger.Info("Received client hello: %v", m.Body)
		return nil, nil
	})

	// Handle echo requests
	broker.Subscribe(context.Background(), "server.echo", func(ctx context.Context, m *websocketmq.Message) (*websocketmq.Message, error) {
		logger.Info("Echo request: %v", m.Body)
		return websocketmq.NewResponse(m, m.Body), nil
	})

	// Start a ticker to send periodic messages
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		count := 0
		for range ticker.C {
			count++
			msg := websocketmq.NewEvent("server.tick", map[string]interface{}{
				"time":  time.Now().Format(time.RFC3339),
				"count": count,
			})
			if err := broker.Publish(context.Background(), msg); err != nil {
				logger.Error("Failed to publish tick: %v", err)
			} else {
				logger.Debug("Published tick #%d", count)
			}
		}
	}()

	// Start the HTTP server
	server := &http.Server{
		Addr:    ":9000",
		Handler: mux,
	}

	// Handle graceful shutdown
	go func() {
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, os.Interrupt)
		<-sigint

		logger.Info("Shutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			logger.Error("HTTP server shutdown error: %v", err)
		}

		if err := broker.Close(); err != nil {
			logger.Error("Broker close error: %v", err)
		}

		if err := wsHandler.Close(); err != nil {
			logger.Error("WebSocket handler close error: %v", err)
		}
	}()

	// Start the server
	logger.Info("Server listening on http://localhost:9000")
	fmt.Println("Open your browser to http://localhost:9000")
	fmt.Println("Press Ctrl+C to stop")

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		logger.Error("HTTP server error: %v", err)
	}
}