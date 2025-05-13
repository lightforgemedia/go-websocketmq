// server/main.go
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	app_shared_types "github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
)

func main() {
	// Setup structured logging (slog)
	logHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true, // Include source file and line number
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
	slog.SetDefault(logger) // Optional: set as global default for other packages

	// Adapt slog for ErgoSockets library

	// 1. Create a new ErgoSockets Broker
	ergoBroker, err := broker.New(
		broker.WithLogger(logger),
		broker.WithAcceptOptions(&websocket.AcceptOptions{OriginPatterns: []string{"localhost:*"}}),
		broker.WithPingInterval(15*time.Second), // Server pings every 15s
		broker.WithClientSendBuffer(32),         // Slightly larger buffer
	)
	if err != nil {
		logger.Error("Failed to create broker", "error", err)
		os.Exit(1)
	}

	// 2. Define handlers
	err = ergoBroker.HandleClientRequest(app_shared_types.TopicGetTime,
		func(client broker.ClientHandle, req app_shared_types.GetTimeRequest) (app_shared_types.GetTimeResponse, error) {
			logger.Info("Server: Client requested time", "clientID", client.ID())
			return app_shared_types.GetTimeResponse{CurrentTime: time.Now().Format(time.RFC3339)}, nil
		},
	)
	if err != nil {
		logger.Error("Failed to register handler", "topic", app_shared_types.TopicGetTime, "error", err)
		os.Exit(1)
	}

	err = ergoBroker.HandleClientRequest(app_shared_types.TopicUserDetails,
		func(client broker.ClientHandle, req app_shared_types.GetUserDetailsRequest) (app_shared_types.UserDetailsResponse, error) {
			logger.Info("Server: Client requested user details", "clientID", client.ID(), "requestedUserID", req.UserID)
			if req.UserID == "user123" {
				return app_shared_types.UserDetailsResponse{UserID: req.UserID, Name: "Jane Doe (Server)", Email: "jane.server@example.com"}, nil
			}
			return app_shared_types.UserDetailsResponse{}, fmt.Errorf("user with ID '%s' not found", req.UserID)
		},
	)
	if err != nil {
		logger.Error("Failed to register handler", "topic", app_shared_types.TopicUserDetails, "error", err)
		os.Exit(1)
	}

	err = ergoBroker.HandleClientRequest(app_shared_types.TopicErrorTest,
		func(client broker.ClientHandle, req app_shared_types.ErrorTestRequest) (app_shared_types.ErrorTestResponse, error) {
			logger.Info("Server: Client requested error test", "clientID", client.ID(), "shouldError", req.ShouldError)
			if req.ShouldError {
				return app_shared_types.ErrorTestResponse{}, fmt.Errorf("simulated server error as requested by client")
			}
			return app_shared_types.ErrorTestResponse{Message: "No error triggered by server"}, nil
		},
	)
	if err != nil {
		logger.Error("Failed to register handler", "topic", app_shared_types.TopicErrorTest, "error", err)
		os.Exit(1)
	}

	err = ergoBroker.HandleClientRequest(app_shared_types.TopicSlowServerRequest,
		func(client broker.ClientHandle, req app_shared_types.SlowServerRequest) (app_shared_types.SlowServerResponse, error) {
			delay := time.Duration(req.DelayMilliseconds) * time.Millisecond
			logger.Info("Server: Client requested slow response", "clientID", client.ID(), "delay", delay)
			time.Sleep(delay)
			return app_shared_types.SlowServerResponse{Message: "Server finally responded after intentional delay"}, nil
		},
	)
	if err != nil {
		logger.Error("Failed to register handler", "topic", app_shared_types.TopicSlowServerRequest, "error", err)
		os.Exit(1)
	}

	// 3. Periodically publish a server announcement
	serverAnnouncementCtx, cancelServerAnnouncements := context.WithCancel(ergoBroker.Context()) // Use broker's context
	defer cancelServerAnnouncements()

	go func(ctx context.Context) {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		logger.Info("Server: Starting announcement publisher goroutine.")
		for {
			select {
			case <-ticker.C:
				announcement := app_shared_types.ServerAnnouncement{
					Message:   "Server Periodic Announcement!",
					Timestamp: time.Now().Format(time.RFC3339Nano),
				}
				logger.Info("Server: Publishing announcement", "topic", app_shared_types.TopicServerAnnounce)
				if pubErr := ergoBroker.Publish(context.Background(), app_shared_types.TopicServerAnnounce, announcement); pubErr != nil {
					logger.Error("Server: Error publishing announcement", "error", pubErr)
				}
			case <-ctx.Done(): // Listen to the passed context (derived from broker's main context)
				logger.Info("Server: Announcement publisher stopping due to context cancellation.")
				return
			}
		}
	}(serverAnnouncementCtx)

	// Example: Server requesting status from a client after a delay
	// This requires knowing a client ID. For testing, a client might announce itself,
	// or the test harness could provide an ID.
	go func(ctx context.Context) {
		select {
		case <-time.After(10 * time.Second): // Wait for a client to potentially connect
		case <-ctx.Done():
			return
		}

		// This part is tricky without a robust way to get a specific client ID for demo.
		// In a real app, clientID might come from an auth system or client registration.
		// For now, we'll just log that we would attempt it.
		// To test this, the test suite (broker_test.go) directly gets a client handle.
		logger.Info("Server: (Demo) Would attempt to request client status if a client ID was known.")
		/*
			var knownClientID string // = get a client ID somehow
			if knownClientID != "" {
				clientHandle, errGet := ergoBroker.GetClient(knownClientID)
				if errGet == nil {
					logger.Info("Server: Requesting status from client", "clientID", knownClientID)
					var statusReport app_shared_types.ClientStatusReport
					reqCtx, reqCancel := context.WithTimeout(ctx, 5*time.Second)

					// The ClientHandle.Request takes responsePayloadPtr as interface{}
					errReq := clientHandle.Request(reqCtx, app_shared_types.TopicClientGetStatus,
						app_shared_types.ClientStatusQuery{QueryDetailLevel: "full"}, &statusReport, 0)
					reqCancel()

					if errReq != nil {
						logger.Error("Server: Error requesting status from client", "clientID", knownClientID, "error", errReq)
					} else {
						logger.Info("Server: Received status from client", "clientID", knownClientID, "status", statusReport)
					}
				} else {
					logger.Warn("Server: Could not get client handle for demo request", "clientID", knownClientID, "error", errGet)
				}
			}
		*/
	}(serverAnnouncementCtx)

	// 4. Start the HTTP server
	mux := http.NewServeMux()
	mux.Handle("/ws", ergoBroker.UpgradeHandler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "OK") })

	httpServer := &http.Server{
		Addr:         ":8081",
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	logger.Info("ErgoSockets server starting", "address", httpServer.Addr+"/ws")
	fmt.Println("ErgoSockets server starting on", httpServer.Addr+"/ws")

	serverErrChan := make(chan error, 1)
	go func() {
		fmt.Println("Starting HTTP server on", httpServer.Addr)
		err := httpServer.ListenAndServe()
		fmt.Println("HTTP server error:", err)
		serverErrChan <- err
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrChan:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	case sig := <-sigChan:
		logger.Info("Received signal, shutting down...", "signal", sig.String())
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second) // Increased timeout
	defer shutdownCancel()

	logger.Info("Attempting to shut down broker...")
	if err := ergoBroker.Shutdown(shutdownCtx); err != nil {
		logger.Error("Broker shutdown error", "error", err)
	} else {
		logger.Info("Broker shut down successfully.")
	}

	logger.Info("Attempting to shut down HTTP server...")
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err)
	} else {
		logger.Info("HTTP server shut down successfully.")
	}
	logger.Info("Server shutdown process complete.")
}
