// cmd/devserver/main.go
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
	logger.Info("Starting WebSocketMQ dev server with hot-reload")

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
	mux.Handle("/", http.FileServer(http.Dir("./static")))

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
	
	// Handle manual trigger for server-to-client request
	broker.Subscribe(context.Background(), "trigger.server.request", func(ctx context.Context, m *websocketmq.Message) (*websocketmq.Message, error) {
		logger.Info("Received trigger for server-to-client request: %v", m.Body)
		
		// Extract the parameters from the message body
		bodyMap, ok := m.Body.(map[string]interface{})
		if !ok {
			logger.Error("Invalid message body format")
			return nil, fmt.Errorf("invalid message body format")
		}
		
		// Create a request to the client with a longer timeout
		reqMsg := websocketmq.NewRequest("client.calculate", bodyMap, 15000) // 15 seconds timeout
		
		logger.Info("Request message created with correlationID: %s", reqMsg.Header.CorrelationID)
		
		// Create a new context with a longer timeout
		requestCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		
		// Send the request and wait for response
		logger.Info("Sending calculation request to client, waiting for response...")
		resp, err := broker.Request(requestCtx, reqMsg, 15000)
		if err != nil {
			logger.Error("Failed to get calculation response: %v", err)
			return nil, err
		}
		
		logger.Info("Got calculation response from client: %v", resp.Body)
		
		// Send the response back to the client that triggered the request
		return websocketmq.NewResponse(m, resp.Body), nil
	})

	// Subscribe to JavaScript errors in dev mode
	broker.Subscribe(context.Background(), "_dev.js-error", func(ctx context.Context, m *websocketmq.Message) (*websocketmq.Message, error) {
		logger.Error("=== JavaScript Error ===")
		logger.Error("Message: %v", m.Body)
		
		// Try to extract detailed information
		if bodyMap, ok := m.Body.(map[string]interface{}); ok {
			if message, ok := bodyMap["message"].(string); ok {
				logger.Error("Error message: %s", message)
			}
			if stack, ok := bodyMap["stack"].(string); ok {
				logger.Error("Stack trace: \n%s", stack)
			}
			if source, ok := bodyMap["source"].(string); ok {
				line, _ := bodyMap["lineno"].(float64)
				col, _ := bodyMap["colno"].(float64)
				logger.Error("Location: %s:%d:%d", source, int(line), int(col))
			}
		}
		
		logger.Error("=======================")
		return nil, nil
	})

	// Start the development watcher
	watchOpts := websocketmq.DefaultDevWatchOptions()
	watchOpts.Paths = []string{"./static", "./templates"}
	stopWatcher, err := websocketmq.StartDevWatcher(context.Background(), broker, logger, watchOpts)
	if err != nil {
		logger.Error("Failed to start watcher: %v", err)
	} else {
		defer stopWatcher()
		logger.Info("Development watcher started")
	}

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

			// Every 10 seconds, send a request to the client
			if count % 2 == 0 {
				// Create request message
				reqMsg := websocketmq.NewRequest("client.calculate", map[string]interface{}{
					"operation": "add",
					"a": count,
					"b": 10,
				}, 15000) // 15 second timeout

				logger.Info("Sending calculation request to client: %v + %v", count, 10)
				
				// Send request and wait for response
				resp, err := broker.Request(context.Background(), reqMsg, 15000)
				if err != nil {
					logger.Error("Failed to get calculation response from ticker: %v", err)
				} else {
					logger.Info("Got calculation response from client in ticker: %v", resp.Body)
				}
			}
		}
	}()

	// Start the HTTP server
	server := &http.Server{
		Addr:    ":3001",
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
	logger.Info("Dev server listening on http://localhost:3001")
	fmt.Println("Open your browser to http://localhost:3001")
	fmt.Println("Press Ctrl+C to stop")

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		logger.Error("HTTP server error: %v", err)
	}
}