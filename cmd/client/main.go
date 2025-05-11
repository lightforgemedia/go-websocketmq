// client/main.go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/client"
	app_shared_types "github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
)

func main() {
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

	startTime := time.Now()

	cli, err := client.Connect("ws://localhost:8081/ws",
		client.WithLogger(logger),
		client.WithDefaultRequestTimeout(5*time.Second),
		client.WithAutoReconnect(5, 1*time.Second, 15*time.Second), // More attempts, longer max delay
		client.WithClientPingInterval(20*time.Second),              // Enable client pings for testing this feature
	)
	if err != nil { // Connect now returns client even on initial failure if reconnect is on
		logger.Warn("Client: Initial connection attempt failed or pending", "error", err, "clientID", cli.ID())
	}
	if cli == nil { // Should not happen
		logger.Error("Client: Failed to get client instance from Connect.")
		os.Exit(1)
	}
	logger.Info("Client instance created", "clientID", cli.ID())

	// Allow time for connection, especially if server starts slower or first attempt fails
	time.Sleep(1 * time.Second)

	// 2. Make requests using the new generic Request method
	ctx := context.Background() // Parent context for operations

	// Request server time (no request payload)
	logger.Info("Client: Requesting server time...")
	reqCtxTime, cancelTime := context.WithTimeout(ctx, 3*time.Second)
	timeResp, err := client.GenericRequest[app_shared_types.GetTimeResponse](cli, reqCtxTime, app_shared_types.TopicGetTime)
	cancelTime()
	if err != nil {
		logger.Error("Client: GetTime request failed", "error", err)
	} else {
		logger.Info("Client: Server time received", "time", timeResp.CurrentTime)
	}

	// Request user details (with request payload)
	logger.Info("Client: Requesting user details for 'user123'...")
	reqCtxUser, cancelUser := context.WithTimeout(ctx, 3*time.Second)
	detailsReq := app_shared_types.GetUserDetailsRequest{UserID: "user123"}
	userDetails, err := client.GenericRequest[app_shared_types.UserDetailsResponse](cli, reqCtxUser, app_shared_types.TopicUserDetails, detailsReq)
	cancelUser()
	if err != nil {
		logger.Error("Client: GetUserDetails request failed", "error", err)
	} else {
		logger.Info("Client: UserDetails received", "name", userDetails.Name, "email", userDetails.Email)
	}

	// Request non-existent user (expecting server error)
	logger.Info("Client: Requesting details for 'user999' (expecting server error)...")
	reqCtxUserNF, cancelUserNF := context.WithTimeout(ctx, 3*time.Second)
	_, err = client.GenericRequest[app_shared_types.UserDetailsResponse](cli, reqCtxUserNF, app_shared_types.TopicUserDetails, app_shared_types.GetUserDetailsRequest{UserID: "user999"})
	cancelUserNF()
	if err != nil {
		logger.Info("Client: GetUserDetails for non-existent user got expected error", "error", err)
	} else {
		logger.Warn("Client: GetUserDetails for non-existent user UNEXPECTEDLY succeeded.")
	}

	// 3. Subscribe to server announcements
	logger.Info("Client: Subscribing to server announcements", "topic", app_shared_types.TopicServerAnnounce)
	unsubscribeAnnouncements, err := cli.Subscribe(app_shared_types.TopicServerAnnounce,
		func(announcement app_shared_types.ServerAnnouncement) error { // Type is concrete
			logger.Info("Client: Received server announcement", "message", announcement.Message, "timestamp", announcement.Timestamp)
			return nil
		},
	)
	if err != nil {
		logger.Error("Client: Failed to subscribe to announcements", "error", err)
	} else {
		defer unsubscribeAnnouncements()
	}

	// 4. Handle requests *from* the server
	err = cli.OnRequest(app_shared_types.TopicClientGetStatus,
		func(req app_shared_types.ClientStatusQuery) (app_shared_types.ClientStatusReport, error) { // Types are concrete
			logger.Info("Client: Received server request for client status", "query", req.QueryDetailLevel)
			return app_shared_types.ClientStatusReport{
				ClientID: cli.ID(),
				Status:   "Client All Systems Go!",
				Uptime:   time.Since(startTime).String(),
			}, nil
		},
	)
	if err != nil {
		logger.Error("Client: Failed to register handler for server requests on client status", "error", err)
	}

	err = cli.OnRequest(app_shared_types.TopicSlowClientRequest,
		func(req app_shared_types.SlowClientRequest) (app_shared_types.SlowClientResponse, error) {
			delay := time.Duration(req.DelayMilliseconds) * time.Millisecond
			logger.Info("Client: Received server request for slow client response", "delay", delay)
			time.Sleep(delay)
			return app_shared_types.SlowClientResponse{Message: "Client finally responded after server-requested delay"}, nil
		},
	)
	if err != nil {
		logger.Error("Client: Failed to register handler for slow client request", "error", err)
	}

	// Test error propagation from server (using TopicErrorTest)
	logger.Info("Client: Requesting error test (should succeed without error)...")
	reqCtxErrOK, cancelErrOK := context.WithTimeout(ctx, 3*time.Second)
	_, err = client.GenericRequest[app_shared_types.ErrorTestResponse](cli, reqCtxErrOK, app_shared_types.TopicErrorTest, app_shared_types.ErrorTestRequest{ShouldError: false})
	cancelErrOK()
	if err != nil {
		logger.Error("Client: Error test (success case) FAILED unexpectedly", "error", err)
	} else {
		logger.Info("Client: Error test (success case) PASSED.")
	}

	logger.Info("Client: Requesting error test (should receive server error)...")
	reqCtxErrFail, cancelErrFail := context.WithTimeout(ctx, 3*time.Second)
	_, err = client.GenericRequest[app_shared_types.ErrorTestResponse](cli, reqCtxErrFail, app_shared_types.TopicErrorTest, app_shared_types.ErrorTestRequest{ShouldError: true})
	cancelErrFail()
	if err != nil {
		logger.Info("Client: Error test (failure case) PASSED with expected error", "error", err)
	} else {
		logger.Warn("Client: Error test (failure case) FAILED (no error received).")
	}

	// Test client request timeout due to slow server
	logger.Info("Client: Requesting slow server response (expecting client timeout)...")
	// Client's default request timeout is 5s, but this context makes it 100ms.
	reqCtxClientTimeout, cancelClientTimeout := context.WithTimeout(ctx, 100*time.Millisecond)
	_, err = client.GenericRequest[app_shared_types.SlowServerResponse](cli, reqCtxClientTimeout, app_shared_types.TopicSlowServerRequest, app_shared_types.SlowServerRequest{DelayMilliseconds: 500})
	cancelClientTimeout()
	if err != nil {
		logger.Info("Client: Slow server request test PASSED with expected client timeout", "error", err)
	} else {
		logger.Warn("Client: Slow server request test FAILED (no client timeout error received).")
	}

	logger.Info("Client running. Press Ctrl+C to exit.", "clientID", cli.ID())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	logger.Info("Client shutting down...", "clientID", cli.ID())
	if cli != nil {
		if closeErr := cli.Close(); closeErr != nil {
			logger.Error("Error during client close", "error", closeErr, "clientID", cli.ID())
		}
	}
	logger.Info("Client shut down gracefully.", "clientID", cli.ID())
}
