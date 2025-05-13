package hotreload

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/filewatcher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHotReload(t *testing.T) {
	// Create a logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Create a broker
	b, err := broker.New(broker.WithLogger(logger))
	require.NoError(t, err, "Failed to create broker")

	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create a file watcher
	fw, err := filewatcher.New(
		filewatcher.WithLogger(logger),
		filewatcher.WithDirs([]string{tempDir}),
		filewatcher.WithPatterns([]string{"*.html", "*.js", "*.css"}),
		filewatcher.WithDebounce(100), // 100ms debounce for faster testing
	)
	require.NoError(t, err, "Failed to create file watcher")

	// Create a hot reload service
	hr, err := New(
		WithLogger(logger),
		WithBroker(b),
		WithFileWatcher(fw),
	)
	require.NoError(t, err, "Failed to create hot reload service")

	// Start the hot reload service
	err = hr.Start()
	require.NoError(t, err, "Failed to start hot reload service")
	defer hr.Stop()

	// Create a channel to receive reload notifications
	reloadCh := make(chan struct{}, 1)

	// Create a mock client
	mockClientID := "test-client"

	// Add a callback to the file watcher to detect changes
	fw.AddCallback(func(file string) {
		t.Logf("File change detected: %s", file)
		reloadCh <- struct{}{}
	})

	err = b.HandleClientRequest(TopicHotReload, func(client broker.ClientHandle, payload interface{}) error {
		t.Logf("Reload request received for client: %s", client.ID())
		if client.ID() == mockClientID {
			reloadCh <- struct{}{}
		}
		return nil
	})

	// No need for a client handle, we'll just call the handler directly

	// Manually call the handler to simulate a client ready notification
	hr.handleClientReady(mockClientID, map[string]interface{}{
		"url":       "http://localhost:8090/test.html",
		"userAgent": "Test Agent",
	})

	// Wait a bit for the notification to be processed
	time.Sleep(100 * time.Millisecond)

	// Verify the client was registered
	hr.clientsMu.RLock()
	client, exists := hr.clients[mockClientID]
	hr.clientsMu.RUnlock()
	assert.True(t, exists, "Client should be registered")
	if exists {
		assert.Equal(t, "ready", client.status, "Client status should be ready")
		assert.Equal(t, "http://localhost:8090/test.html", client.url, "Client URL should be set")
	}

	// Create a test file in the watched directory
	testFile := tempDir + "/test.html"
	err = os.WriteFile(testFile, []byte("<html><body>Test</body></html>"), 0644)
	require.NoError(t, err, "Failed to write test file")

	// Wait for the reload notification
	select {
	case <-reloadCh:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for reload notification")
	}

	// Manually call the handler to simulate a client error
	hr.handleClientError(mockClientID, map[string]interface{}{
		"message":   "Test error",
		"filename":  "test.js",
		"lineno":    10,
		"colno":     20,
		"stack":     "Error: Test error\n    at test.js:10:20",
		"timestamp": time.Now().Format(time.RFC3339),
	})

	// Wait a bit for the error to be processed
	time.Sleep(100 * time.Millisecond)

	// Verify the error was recorded
	hr.clientsMu.RLock()
	client, exists = hr.clients[mockClientID]
	hr.clientsMu.RUnlock()
	assert.True(t, exists, "Client should still be registered")
	if exists {
		assert.Equal(t, 1, len(client.errors), "Client should have one error")
		if len(client.errors) > 0 {
			assert.Equal(t, "Test error", client.errors[0].message, "Error message should be set")
		}
	}
}
