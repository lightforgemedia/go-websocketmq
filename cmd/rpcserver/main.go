// cmd/rpcserver/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lightforgemedia/go-websocketmq"
	"github.com/lightforgemedia/go-websocketmq/cmd/rpcserver/api"
	"github.com/lightforgemedia/go-websocketmq/cmd/rpcserver/session"
)

// Simple logger implementation for the example
type AppLogger struct{}

func (l *AppLogger) Debug(msg string, args ...any) { log.Printf("DEBUG: "+msg, args...) }
func (l *AppLogger) Info(msg string, args ...any)  { log.Printf("INFO: "+msg, args...) }
func (l *AppLogger) Warn(msg string, args ...any)  { log.Printf("WARN: "+msg, args...) }
func (l *AppLogger) Error(msg string, args ...any) { log.Printf("ERROR: "+msg, args...) }

func main() {
	logger := &AppLogger{}
	logger.Info("Starting RPC WebSocketMQ Server...")

	// 1. Initialize Broker
	brokerOpts := websocketmq.DefaultBrokerOptions()
	broker := websocketmq.NewPubSubBroker(logger, brokerOpts)
	// Cast to access specific ps.PubSubBroker methods if needed for shutdown, e.g. WaitForShutdown
	// psBroker, _ := broker.(*ps.PubSubBroker) // Assuming ps is the concrete type

	// 2. Initialize Session Manager
	// The session manager needs the broker to listen to registration/deregistration events.
	sessionManager := session.NewManager(logger, broker)
	logger.Info("Session Manager initialized.")

	// 3. Initialize WebSocket Handler
	wsHandlerOpts := websocketmq.DefaultHandlerOptions()
	wsHandlerOpts.ClientRegisterTopic = "_client.register" // Ensure this matches JS client
	wsHandler := websocketmq.NewWebSocketHandler(broker, logger, wsHandlerOpts)
	logger.Info("WebSocket Handler initialized.")

	// 4. Initialize API Handler
	apiHandler := api.NewHandler(logger, broker, sessionManager)
	logger.Info("API Handler initialized.")

	// 5. Setup HTTP routes
	mux := http.NewServeMux()
	mux.Handle("/ws", wsHandler) // WebSocket connections
	mux.Handle("/wsmq/", http.StripPrefix("/wsmq/", websocketmq.ScriptHandler())) // Serve JS client

	// API routes for browser actions
	mux.HandleFunc("/api/click", apiHandler.HandleAction("browser.click"))
	mux.HandleFunc("/api/input", apiHandler.HandleAction("browser.input"))
	mux.HandleFunc("/api/navigate", apiHandler.HandleAction("browser.navigate"))
	mux.HandleFunc("/api/getText", apiHandler.HandleAction("browser.getText"))
	mux.HandleFunc("/api/screenshot", apiHandler.HandleAction("browser.screenshot"))
	// Add more actions as needed...

	// Serve static files for the example UI
	mux.Handle("/", http.FileServer(http.Dir("./cmd/rpcserver/static")))
	logger.Info("HTTP routes configured.")

	// Example server-side subscription (not directly related to RPC, but shows broker usage)
	broker.Subscribe(context.Background(), "server.ping", func(ctx context.Context, msg *websocketmq.Message, sourceBrokerID string) (*websocketmq.Message, error) {
		logger.Info("Received server.ping from source %s: %+v", sourceBrokerID, msg.Body)
		return websocketmq.NewResponse(msg, map[string]string{"reply": "pong from server handler"}), nil
	})

	// 6. Start HTTP server
	server := &http.Server{
		Addr:    ":9000",
		Handler: mux,
	}

	go func() {
		logger.Info("RPC Server listening on http://localhost:9000")
		fmt.Println("--- Open http://localhost:9000 in your browser to see the example ---")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server ListenAndServe error: %v", err)
			os.Exit(1)
		}
	}()

	// 7. Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("HTTP server shutdown error: %v", err)
	}

	// Close the broker (which should also handle closing connections and subscriptions)
	if err := broker.Close(); err != nil {
		logger.Error("Broker close error: %v", err)
	}
	// if psBroker != nil {
	// 	psBroker.WaitForShutdown() // If you have such a method
	// }

	logger.Info("Server gracefully stopped.")
}