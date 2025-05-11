package js_tests

import (
	"context"
	"testing"
	"time"

	app_shared_types "github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJSClient_Handler tests the JavaScript client request handler functionality
func TestJSClient_Handler(t *testing.T) {
	// Create a test server
	server := NewTestServer(t)
	defer server.Close()

	// Start a browser and navigate to the test page
	page := server.StartBrowser()
	defer page.MustClose()

	// Connect the JavaScript client to the server
	server.ConnectJSClient()

	// Test case 1: Happy path - Server requests client status and client responds
	t.Run("HappyPath_ServerRequestsClientStatus", func(t *testing.T) {
		// Get the client ID
		clientID := server.GetJSClientID()
		require.NotEqual(t, "Not connected", clientID, "Client ID should be set")

		// Get the client handle
		clientHandle, err := server.Broker.GetClient(clientID)
		require.NoError(t, err, "Failed to get client handle")

		// Create a request context
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Send a request to the client
		var response app_shared_types.ClientStatusReport
		err = clientHandle.Request(ctx, app_shared_types.TopicClientGetStatus,
			app_shared_types.ClientStatusQuery{QueryDetailLevel: "full"}, &response, 0)
		require.NoError(t, err, "Failed to send request to client")

		// Verify the response
		assert.Equal(t, clientID, response.ClientID, "Response should contain the correct client ID")
		assert.Equal(t, "Client All Systems Go!", response.Status, "Response should contain the expected status")
		assert.NotEmpty(t, response.Uptime, "Response should contain an uptime value")

		// Verify that the request was displayed on the page
		time.Sleep(500 * time.Millisecond)
		requestsText := server.GetJSClientServerRequests()
		assert.Contains(t, requestsText, "client:get_status", "Server requests should show the client status request")
	})

	// Test case 2: Base case - Server requests client status with different query level
	t.Run("BaseCase_ServerRequestsClientStatusWithDifferentQueryLevel", func(t *testing.T) {
		// Get the client ID
		clientID := server.GetJSClientID()
		require.NotEqual(t, "Not connected", clientID, "Client ID should be set")

		// Get the client handle
		clientHandle, err := server.Broker.GetClient(clientID)
		require.NoError(t, err, "Failed to get client handle")

		// Create a request context
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Send a request to the client with a different query level
		var response app_shared_types.ClientStatusReport
		err = clientHandle.Request(ctx, app_shared_types.TopicClientGetStatus,
			app_shared_types.ClientStatusQuery{QueryDetailLevel: "basic"}, &response, 0)
		require.NoError(t, err, "Failed to send request to client")

		// Verify the response
		assert.Equal(t, clientID, response.ClientID, "Response should contain the correct client ID")
		assert.Equal(t, "Client All Systems Go!", response.Status, "Response should contain the expected status")
		assert.NotEmpty(t, response.Uptime, "Response should contain an uptime value")

		// Verify that the request was displayed on the page
		time.Sleep(500 * time.Millisecond)
		requestsText := server.GetJSClientServerRequests()
		assert.Contains(t, requestsText, "client:get_status", "Server requests should show the client status request")
		assert.Contains(t, requestsText, "basic", "Server requests should show the query detail level")
	})

	// Test case 3: Negative case - Server requests non-existent handler
	t.Run("NegativeCase_ServerRequestsNonExistentHandler", func(t *testing.T) {
		// Get the client ID
		clientID := server.GetJSClientID()
		require.NotEqual(t, "Not connected", clientID, "Client ID should be set")

		// Get the client handle
		clientHandle, err := server.Broker.GetClient(clientID)
		require.NoError(t, err, "Failed to get client handle")

		// Create a request context
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Send a request to a non-existent handler
		var response interface{}
		err = clientHandle.Request(ctx, "non:existent:handler", nil, &response, 0)

		// The error might be different depending on the implementation, but there should be an error
		assert.Error(t, err, "Request to non-existent handler should fail")
	})

	// Test case 4: Server requests client status with timeout
	t.Run("ServerRequestsClientStatusWithTimeout", func(t *testing.T) {
		// Get the client ID
		clientID := server.GetJSClientID()
		require.NotEqual(t, "Not connected", clientID, "Client ID should be set")

		// Get the client handle
		clientHandle, err := server.Broker.GetClient(clientID)
		require.NoError(t, err, "Failed to get client handle")

		// Create a request context with a very short timeout
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
		defer cancel()

		// Send a request to the client with a timeout that's too short
		var response app_shared_types.ClientStatusReport
		err = clientHandle.Request(ctx, app_shared_types.TopicClientGetStatus,
			app_shared_types.ClientStatusQuery{QueryDetailLevel: "full"}, &response, 0)

		// The request should fail due to the timeout
		assert.Error(t, err, "Request with very short timeout should fail")
	})

	// Test case 5: Server requests client status after client disconnects
	t.Run("ServerRequestsClientStatusAfterClientDisconnects", func(t *testing.T) {
		// Get the client ID
		clientID := server.GetJSClientID()
		require.NotEqual(t, "Not connected", clientID, "Client ID should be set")

		// Disconnect the client
		server.DisconnectJSClient()

		// Wait for the broker to detect the disconnection
		time.Sleep(1 * time.Second)

		// Try to get the client handle (should fail)
		_, err := server.Broker.GetClient(clientID)
		assert.Error(t, err, "Getting client handle after disconnection should fail")
	})
}
