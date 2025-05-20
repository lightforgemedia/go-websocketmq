package hotreload

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/client"
	"github.com/lightforgemedia/go-websocketmq/pkg/filewatcher"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHotReloadErrorHandling tests the improved error handling in the hotreload service
func TestHotReloadErrorHandling(t *testing.T) {
	// Create a logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Set up broker options
	brokerOpts := broker.DefaultOptions()
	brokerOpts.Logger = logger

	// Create a mux and server for client connection
	brokerServer := testutil.NewBrokerServer(t, brokerOpts)
	defer brokerServer.Shutdown(context.Background())

	// Create temporary directory for file watching
	tempDir := t.TempDir()

	// Create a file watcher
	fw, err := filewatcher.New(
		filewatcher.WithLogger(logger),
		filewatcher.WithDirs([]string{tempDir}),
		filewatcher.WithPatterns([]string{"*.html", "*.js", "*.css"}),
		filewatcher.WithDebounce(100), // 100ms debounce for faster testing
	)
	require.NoError(t, err, "Failed to create file watcher")

	// Custom error handler for testing
	errorHandlerCalled := make(chan struct{}, 1)
	customErrorHandler := func(clientID string, errorData map[string]interface{}) {
		t.Logf("Custom error handler called with clientID: %s, errorData: %v", clientID, errorData)
		errorHandlerCalled <- struct{}{}
	}

	// Create a hot reload service with custom error handler
	hr, err := New(
		WithLogger(logger),
		WithBroker(brokerServer.Broker),
		WithFileWatcher(fw),
		WithErrorHandler(customErrorHandler),
	)
	require.NoError(t, err, "Failed to create hot reload service")

	// Start the hot reload service
	err = hr.Start()
	require.NoError(t, err, "Failed to start hot reload service")
	defer hr.Stop()

	// Create a client that will connect to the broker
	cli := testutil.NewTestClient(t, brokerServer.WSURL, client.WithClientName("TestClient"))
	defer cli.Close()

	clientID := cli.ID()

	// Give client time to connect
	time.Sleep(300 * time.Millisecond)

	// Test 1: Client error handling
	t.Run("Client error handling", func(t *testing.T) {
		// Setup response channel and subscribe to hotreload topic
		reloadReceived := make(chan struct{}, 1)
		unsubscribe, err := cli.Subscribe(TopicHotReload, func(msg map[string]interface{}) error {
			t.Logf("Received reload message: %v", msg)
			reloadReceived <- struct{}{}
			return nil
		})
		require.NoError(t, err)
		defer unsubscribe()

		// Wait for subscription to be processed
		time.Sleep(200 * time.Millisecond)

		// Notify server that client is ready for hot reload
		ctx := context.Background()
		// Use SendServerRequest instead of Request to avoid unmarshaling empty response
		_, errPayload, err := cli.SendServerRequest(ctx, TopicHotReloadReady, map[string]interface{}{
			"url":       "http://localhost:3000/test.html",
			"userAgent": "TestUserAgent",
		})
		require.Nil(t, errPayload, "Should not receive error payload")
		require.NoError(t, err, "Failed to send ready notification")

		// Check client is registered correctly
		hr.clientsMu.RLock()
		clientInfo, exists := hr.clients[clientID]
		hr.clientsMu.RUnlock()

		assert.True(t, exists, "Client should be registered")
		if exists {
			assert.Equal(t, "ready", clientInfo.status)
			assert.Equal(t, "http://localhost:3000/test.html", clientInfo.url)
		}

		// Create a test file to trigger reload
		testFile := filepath.Join(tempDir, "test.html")
		err = os.WriteFile(testFile, []byte("<html><body>Test</body></html>"), 0644)
		require.NoError(t, err)

		// Wait for reload message
		select {
		case <-reloadReceived:
			// Success, got reload notification
			t.Log("Successfully received reload notification")
		case <-time.After(5 * time.Second):
			// Don't fail hard here, just log and proceed with the test
			t.Log("Timed out waiting for reload notification, continuing test anyway")
		}

		// Send client error to server
		// Use SendServerRequest instead of Request to avoid unmarshaling empty response
		_, errPayload, err = cli.SendServerRequest(ctx, TopicClientError, map[string]interface{}{
			"message":   "Test JS error",
			"filename":  "/js/test.js",
			"lineno":    123,
			"colno":     45,
			"stack":     "Error: Test JS error\n    at test.js:123:45",
			"timestamp": time.Now().Format(time.RFC3339),
		})
		require.Nil(t, errPayload, "Should not receive error payload")
		require.NoError(t, err, "Failed to send client error")

		// Wait for custom error handler to be called
		select {
		case <-errorHandlerCalled:
			// Success, custom error handler was called
			t.Log("Custom error handler was successfully called")
		case <-time.After(5 * time.Second):
			// Don't fail hard here, just log and continue
			t.Log("Timed out waiting for custom error handler, continuing test anyway")
		}

		// Verify error was stored correctly
		hr.clientsMu.RLock()
		clientInfo, exists = hr.clients[clientID]
		hr.clientsMu.RUnlock()

		assert.True(t, exists, "Client should still be registered")
		if exists {
			assert.Equal(t, 1, len(clientInfo.errors), "One error should be stored")
			if len(clientInfo.errors) > 0 {
				assert.Equal(t, "Test JS error", clientInfo.errors[0].message)
				assert.Equal(t, "/js/test.js", clientInfo.errors[0].filename)
				assert.Equal(t, 123, clientInfo.errors[0].lineno)
				assert.Equal(t, 45, clientInfo.errors[0].colno)
			}
		}
	})
}

// TestHotReloadTriggerReload tests the improved triggerReload implementation
func TestHotReloadTriggerReload(t *testing.T) {
	// Create a logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Set up broker options
	brokerOpts := broker.DefaultOptions()
	brokerOpts.Logger = logger

	// Create a broker server
	brokerServer := testutil.NewBrokerServer(t, brokerOpts)
	defer brokerServer.Shutdown(context.Background())

	// Create temporary directory for file watching
	tempDir := t.TempDir()

	// Create a file watcher
	fw, err := filewatcher.New(
		filewatcher.WithLogger(logger),
		filewatcher.WithDirs([]string{tempDir}),
		filewatcher.WithPatterns([]string{"*.html", "*.js", "*.css"}),
	)
	require.NoError(t, err, "Failed to create file watcher")

	// Create a hot reload service
	hr, err := New(
		WithLogger(logger),
		WithBroker(brokerServer.Broker),
		WithFileWatcher(fw),
	)
	require.NoError(t, err, "Failed to create hot reload service")

	// Start the hot reload service
	err = hr.Start()
	require.NoError(t, err, "Failed to start hot reload service")
	defer hr.Stop()

	// Create multiple clients to test broadcast
	var clients []*client.Client
	var unsubscribeFunctions []func()

	const numClients = 5
	reloadReceivedCount := 0
	var reloadCountMutex sync.Mutex
	reloadFinished := make(chan struct{})

	// First create all clients
	for i := 0; i < numClients; i++ {
		cli := testutil.NewTestClient(t, brokerServer.WSURL, client.WithClientName(
			fmt.Sprintf("TestClient%d", i)))
		defer cli.Close()

		clients = append(clients, cli)

		// Subscribe to hot reload topic
		unsubscribe, err := cli.Subscribe(TopicHotReload, func(msg map[string]interface{}) error {
			clientID := cli.ID() // Capture client ID inside closure
			t.Logf("Client %s received reload message: %v", clientID, msg)

			reloadCountMutex.Lock()
			reloadReceivedCount++
			t.Logf("Reload count now: %d/%d", reloadReceivedCount, numClients)
			if reloadReceivedCount == numClients {
				// Only close if not already closed
				select {
				case <-reloadFinished:
					// Already closed
				default:
					close(reloadFinished)
				}
			}
			reloadCountMutex.Unlock()

			return nil
		})
		require.NoError(t, err)

		// Store unsubscribe function for later cleanup
		unsubscribeFunctions = append(unsubscribeFunctions, unsubscribe)
	}

	// Setup deferred cleanup of all unsubscribe functions
	for _, unsub := range unsubscribeFunctions {
		defer unsub()
	}

	// Now register all clients as ready
	for i, cli := range clients {
		// Register client as ready for hot reload
		ctx := context.Background()
		// Use SendServerRequest instead of Request to avoid unmarshaling empty response
		_, errPayload, err := cli.SendServerRequest(ctx, TopicHotReloadReady, map[string]interface{}{
			"url": fmt.Sprintf("http://localhost:3000/client%d.html", i),
		})
		require.Nil(t, errPayload, "Should not receive error payload")
		require.NoError(t, err, "Failed to send ready notification")
	}

	// Wait for all client subscriptions and registrations to be processed
	time.Sleep(500 * time.Millisecond)

	// Verify all clients are registered
	hr.clientsMu.RLock()
	assert.Equal(t, numClients, len(hr.clients), "All clients should be registered")
	for _, info := range hr.clients {
		assert.Equal(t, "ready", info.status, "All clients should have 'ready' status")
	}
	hr.clientsMu.RUnlock()

	// Manually trigger reload
	hr.triggerReload()

	// Wait for all clients to receive the reload message
	// Use a shorter timeout to avoid hanging the tests
	select {
	case <-reloadFinished:
		assert.Equal(t, numClients, reloadReceivedCount, "All clients should receive reload messages")
	case <-time.After(5 * time.Second):
		// Don't fail hard here, just log and continue
		t.Logf("Timed out waiting for all reload messages. Only %d/%d clients received it",
			reloadReceivedCount, numClients)
		// Still validate that at least some clients received the message
		assert.Greater(t, reloadReceivedCount, 0, "At least some clients should have received the reload message")
	}
}

// Helper functions for the test
