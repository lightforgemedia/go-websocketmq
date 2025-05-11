package js_tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJSClient_Connection tests the JavaScript client connection functionality
func TestJSClient_Connection(t *testing.T) {
	// Create a test server
	server := NewTestServer(t)
	defer server.Close()

	// Start a browser and navigate to the test page
	page := server.StartBrowser()
	defer page.MustClose()

	// Test case 1: Happy path - Client connects to server successfully
	t.Run("HappyPath_ClientConnectsSuccessfully", func(t *testing.T) {
		// Connect the JavaScript client to the server
		server.ConnectJSClient()

		// Verify that the client is connected
		connectionStatus := server.WaitForElement("#connection-status", 5*time.Second)
		statusText, err := connectionStatus.Text()
		require.NoError(t, err, "Failed to get connection status text")
		assert.Equal(t, "Connected", statusText, "Connection status should be 'Connected'")

		// Verify that the client ID is set
		clientID := server.GetJSClientID()
		assert.NotEqual(t, "Not connected", clientID, "Client ID should be set")

		// Disconnect the client for the next test
		server.DisconnectJSClient()
	})

	// Test case 2: Base case - Client attempts to connect to server
	t.Run("BaseCase_ClientAttemptsToConnect", func(t *testing.T) {
		// Connect the JavaScript client to the server
		connectBtn := server.WaitForElement("#connect-btn", 5*time.Second)
		connectBtn.MustClick()

		// Verify that the client is connected
		connectionStatus := server.WaitForElement("#connection-status", 5*time.Second)
		statusText, err := connectionStatus.Text()
		require.NoError(t, err, "Failed to get connection status text")
		assert.Equal(t, "Connected", statusText, "Connection status should be 'Connected'")

		// Disconnect the client for the next test
		server.DisconnectJSClient()
	})

	// Test case 3: Negative case - Client attempts to connect to non-existent server
	t.Run("NegativeCase_ClientAttemptsToConnectToNonExistentServer", func(t *testing.T) {
		// Change the WebSocket URL to a non-existent server
		wsUrlInput := server.WaitForElement("#ws-url", 5*time.Second)
		wsUrlInput.MustSelectAllText().MustInput("ws://localhost:9999/ws")

		// Connect the JavaScript client to the non-existent server
		connectBtn := server.WaitForElement("#connect-btn", 5*time.Second)
		connectBtn.MustClick()

		// Wait for a moment to allow the connection attempt to fail
		time.Sleep(1 * time.Second)

		// Check the log for connection error
		logContainer := server.WaitForElement("#log-container", 5*time.Second)
		logText, err := logContainer.Text()
		require.NoError(t, err, "Failed to get log text")

		// The error message might vary depending on the browser and network stack,
		// so we just check for common error indicators
		assert.True(t,
			strings.Contains(logText, "error") ||
				strings.Contains(logText, "Error") ||
				strings.Contains(logText, "failed") ||
				strings.Contains(logText, "Failed"),
			"Log should contain error message")

		// Reset the WebSocket URL for the next test
		wsUrlInput = server.WaitForElement("#ws-url", 5*time.Second)
		wsUrlInput.MustSelectAllText().MustInput(strings.Replace(server.Server.URL, "http://", "ws://", 1) + "/ws")
	})

	// Test case 4: Client reconnects after disconnection
	t.Run("ClientReconnectsAfterDisconnection", func(t *testing.T) {
		// Connect the JavaScript client to the server
		server.ConnectJSClient()

		// Get the client ID
		clientID1 := server.GetJSClientID()
		assert.NotEqual(t, "Not connected", clientID1, "Client ID should be set")

		// Shutdown the server to force disconnection
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Broker.Shutdown(ctx)

		// Wait for disconnection to be detected
		time.Sleep(1 * time.Second)

		// Restart the server
		server = NewTestServer(t)
		defer server.Close()

		// Update the page with the new server URL
		page.MustNavigate(strings.Replace(server.FileServer.URL, "http://", "ws://", 1) + "/test_page.html?ws=" +
			strings.Replace(server.Server.URL, "http://", "ws://", 1) + "/ws")
		page.MustWaitLoad()

		// Connect the JavaScript client to the new server
		server.ConnectJSClient()

		// Get the new client ID
		clientID2 := server.GetJSClientID()
		assert.NotEqual(t, "Not connected", clientID2, "Client ID should be set after reconnection")
		assert.NotEqual(t, clientID1, clientID2, "Client ID should be different after reconnection")
	})
}
