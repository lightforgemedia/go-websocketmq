// cmd/rpcserver/main.go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Use specific subpackages from the library for concrete types/interfaces
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps" // Concrete PubSubBroker implementation
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"github.com/lightforgemedia/go-websocketmq/pkg/server" // For server.Handler and server.DefaultHandlerOptions

	// Local application packages
	"github.com/lightforgemedia/go-websocketmq/pkg/api"
	"github.com/lightforgemedia/go-websocketmq/pkg/assets" // For ScriptHandler
	"github.com/lightforgemedia/go-websocketmq/pkg/session"
)

// AppLogger provides a simple logger satisfying broker.Logger interface.
type AppLogger struct{}

func (l *AppLogger) Debug(msg string, args ...any) { log.Printf("DEBUG: "+msg, args...) }
func (l *AppLogger) Info(msg string, args ...any)  { log.Printf("INFO: "+msg, args...) }
func (l *AppLogger) Warn(msg string, args ...any)  { log.Printf("WARN: "+msg, args...) }
func (l *AppLogger) Error(msg string, args ...any) { log.Printf("ERROR: "+msg, args...) }

func main() {
	logger := &AppLogger{}
	logger.Info("Starting RPC WebSocketMQ Server...")

	// 1. Initialize Broker
	brokerOpts := broker.DefaultOptions()        // Uses pkg/broker/broker.go
	brokerInstance := ps.New(logger, brokerOpts) // Uses pkg/broker/ps/ps.go
	logger.Info("PubSubBroker initialized.")

	// 2. Initialize Session Manager
	sessionManager := session.NewManager(logger, brokerInstance)
	logger.Info("Session Manager initialized.")

	// 3. Initialize WebSocket Handler
	wsHandlerOpts := server.DefaultHandlerOptions() // Uses pkg/server/handler.go
	// Ensure topics match JS client and session manager expectations
	wsHandlerOpts.ClientRegisterTopic = "_client.register"
	wsHandlerOpts.ClientRegisteredAckTopic = broker.TopicClientRegistered // Server sends ACK on this

	wsHandler := server.NewHandler(brokerInstance, logger, wsHandlerOpts)
	logger.Info("WebSocket Handler initialized.")

	// 4. Initialize API Handler
	apiHandler := api.NewHandler(logger, brokerInstance, sessionManager)
	logger.Info("API Handler initialized.")

	// 5. Setup HTTP routes
	mux := http.NewServeMux()
	mux.Handle("/ws", wsHandler)                                             // WebSocket connections
	mux.Handle("/wsmq/", http.StripPrefix("/wsmq/", assets.ScriptHandler())) // Serve JS client from embedded assets

	// API routes for browser actions
	mux.HandleFunc("/api/click", apiHandler.HandleAction("browser.click"))
	mux.HandleFunc("/api/input", apiHandler.HandleAction("browser.input"))
	mux.HandleFunc("/api/navigate", apiHandler.HandleAction("browser.navigate"))
	mux.HandleFunc("/api/getText", apiHandler.HandleAction("browser.getText"))
	mux.HandleFunc("/api/screenshot", apiHandler.HandleAction("browser.screenshot"))
	mux.HandleFunc("/api/getPageSource", apiHandler.HandleAction("browser.getPageSource"))
	logger.Info("API and static asset routes configured.")

	// Serve static files for the example UI (index.html, style.css, browser_automation_mock.js)
	mux.Handle("/", http.FileServer(http.Dir("./cmd/rpcserver/static")))
	logger.Info("Static file server for UI configured for ./cmd/rpcserver/static")

	// Example server-side subscription for client-initiated RPC
	err := brokerInstance.Subscribe(context.Background(), "server.ping", func(ctx context.Context, msg *model.Message, sourceBrokerID string) (*model.Message, error) {
		logger.Info("Handler for 'server.ping': Received from BrokerClientID %s, Body: %+v", sourceBrokerID, msg.Body)
		// Echo back the client's payload along with a server message
		responseBody := map[string]interface{}{
			"reply":       "pong from server handler",
			"client_data": msg.Body,
		}
		return model.NewResponse(msg, responseBody), nil
	})
	if err != nil {
		logger.Error("Failed to subscribe to 'server.ping': %v", err)
	} else {
		logger.Info("Subscribed to 'server.ping' topic for client RPCs.")
	}

	// 6. Start HTTP server
	httpServer := &http.Server{
		Addr:    ":9000",
		Handler: mux,
	}

	go func() {
		logger.Info("RPC Server listening on http://localhost:9000")
		fmt.Println("--- Open http://localhost:9000 in your browser to see the example ---")
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server ListenAndServe error: %v", err)
			// Consider a more graceful shutdown of other components if ListenAndServe fails critically
			os.Exit(1)
		}
	}()

	// 7. Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	logger.Info("Received signal: %s. Shutting down server...", sig)

	// Context for shutdown operations
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second) // Increased timeout
	defer shutdownCancel()

	// Shutdown HTTP server
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error: %v", err)
	} else {
		logger.Info("HTTP server gracefully stopped.")
	}

	// Close the broker
	logger.Info("Closing broker...")
	if err := brokerInstance.Close(); err != nil {
		logger.Error("Broker close error: %v", err)
	} else {
		logger.Info("Broker close initiated.")
	}

	// Wait for broker to fully shutdown if it supports it (PubSubBroker does)
	brokerInstance.WaitForShutdown()
	logger.Info("Broker fully shutdown.")

	logger.Info("Server gracefully stopped.")
}
