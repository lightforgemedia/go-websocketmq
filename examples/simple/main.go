// Example of using WebSocketMQ in a simple web application
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lightforgemedia/go-websocketmq"
)

// SimpleLogger implements the websocketmq.Logger interface
type SimpleLogger struct{}

func (l *SimpleLogger) Debug(msg string, args ...interface{}) { log.Printf("DEBUG: "+msg, args...) }
func (l *SimpleLogger) Info(msg string, args ...interface{})  { log.Printf("INFO: "+msg, args...) }
func (l *SimpleLogger) Warn(msg string, args ...interface{})  { log.Printf("WARN: "+msg, args...) }
func (l *SimpleLogger) Error(msg string, args ...interface{}) { log.Printf("ERROR: "+msg, args...) }

func init() {
	// Set up logging
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}

func main() {
	// Create a logger
	logger := &SimpleLogger{}
	logger.Info("Starting WebSocketMQ example server")

	// Create a broker
	brokerOpts := websocketmq.DefaultBrokerOptions()
	broker := websocketmq.NewPubSubBroker(logger, brokerOpts)

	// Create a WebSocket handler
	handlerOpts := websocketmq.DefaultHandlerOptions()
	handler := websocketmq.NewHandler(broker, logger, handlerOpts)

	// Set up HTTP routes
	mux := http.NewServeMux()
	mux.Handle("/ws", handler)
	mux.Handle("/wsmq/", http.StripPrefix("/wsmq/", websocketmq.ScriptHandler()))
	mux.Handle("/", http.FileServer(http.Dir("examples/simple/static")))

	// Subscribe to a topic
	broker.Subscribe(context.Background(), "user.login", func(ctx context.Context, m *websocketmq.Message) (*websocketmq.Message, error) {
		logger.Info("User logged in: %v", m.Body)
		return nil, nil
	})

	// Subscribe to echo requests
	broker.Subscribe(context.Background(), "server.echo", func(ctx context.Context, m *websocketmq.Message) (*websocketmq.Message, error) {
		logger.Info("Echo request received: %v", m.Body)
		return websocketmq.NewEvent(m.Header.CorrelationID, m.Body), nil
	})

	// Start a ticker to publish messages periodically
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case t := <-ticker.C:
				msg := websocketmq.NewEvent("server.tick", map[string]interface{}{
					"time": t.Format(time.RFC3339),
				})
				if err := broker.Publish(context.Background(), msg); err != nil {
					logger.Error("Failed to publish tick: %v", err)
				} else {
					logger.Info("Published tick")
				}
			}
		}
	}()

	// Start development watcher (optional)
	devMode := true
	if devMode {
		watchOpts := websocketmq.DefaultDevWatchOptions()
		watchOpts.Paths = []string{"examples/simple/static"}
		stopWatcher, err := websocketmq.StartDevWatcher(context.Background(), broker, logger, watchOpts)
		if err != nil {
			logger.Error("Failed to start watcher: %v", err)
		} else {
			defer stopWatcher()
		}
	}

	// Start the server
	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	// Handle graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		logger.Info("Shutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			logger.Error("Server shutdown error: %v", err)
		}
	}()

	// Start the server
	logger.Info("Server starting on http://localhost:8080")
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		logger.Error("Server error: %v", err)
		os.Exit(1)
	}
}
