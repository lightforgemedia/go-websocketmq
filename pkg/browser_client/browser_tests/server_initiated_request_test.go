// Package browser_tests contains tests for the WebSocketMQ browser client.
package browser_tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBrowserClientServerInitiatedRequest tests the browser client's ability to handle requests from the server.
func TestBrowserClientServerInitiatedRequest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping browser test in short mode")
	}

	// Create a broker server with accept options to allow any origin
	bs := testutil.NewBrokerServer(t, broker.WithAcceptOptions(&websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	}))

	// Create an HTTP server mux
	mux := http.NewServeMux()

	// Register both the WebSocket handler and JavaScript client handler
	bs.RegisterHandlersWithDefaults(mux)

	// Create the HTTP server first so we can use its URL in the HTML
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	// Serve the JavaScript helpers
	mux.Handle("/js_helpers/", http.StripPrefix("/js_helpers/", http.FileServer(http.Dir("js_helpers"))))

	// Create a test HTML page handler
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<!DOCTYPE html>
				<html>
				<head>
					<title>WebSocketMQ Server-Initiated Request Test</title>
					<script src="/js_helpers/console_override.js"></script>
					<script src="/websocketmq.js"></script>
					<script src="/js_helpers/connect.js"></script>
					<script>
						// Function to register a handler for server-initiated requests
						function registerStatusHandler() {
							console.log('Registering handler for client:get_status');

							// Make sure client is connected
							if (window.client && window.client.isConnected) {
								// Register the handler
								window.client.onRequest('client:get_status', (payload) => {
									console.log('Received request on topic client:get_status:', payload);

									// Update UI with request details
									document.getElementById('request').textContent = JSON.stringify(payload);
									document.getElementById('request-count').textContent =
										(parseInt(document.getElementById('request-count').textContent) || 0) + 1;

									// Return client status
									return {
										status: 'online',
										uptime: Math.floor(performance.now() / 1000) + ' seconds',
										url: window.location.href,
										userAgent: navigator.userAgent
									};
								});

								document.getElementById('handler-status').textContent = 'Registered';
								document.getElementById('handler-status').style.color = 'green';
							} else {
								console.error('Client not connected');
								document.getElementById('handler-status').textContent = 'Error: Client not connected';
								document.getElementById('handler-status').style.color = 'red';
							}
						}

						// Initialize the page when it loads
						document.addEventListener('DOMContentLoaded', () => {
							// Register the handler when the page loads
							const registerBtn = document.getElementById('register-btn');
							if (registerBtn) {
								registerBtn.addEventListener('click', registerStatusHandler);
							}

							// Auto-register the handler after connection
							// Use a polling approach to check for client connection
							const checkInterval = setInterval(() => {
								if (window.client && window.client.isConnected) {
									clearInterval(checkInterval);
									console.log('Client connected, registering handler');
									registerStatusHandler();
								}
							}, 100);
						});
					</script>
				</head>
				<body>
					<h1>WebSocketMQ Server-Initiated Request Test</h1>
					<div>
						<h2>Connection Status: <span id="status">Disconnected</span></h2>
					</div>
					<div>
						<h2>Handler Status: <span id="handler-status">Not Registered</span></h2>
						<button id="register-btn">Register Handler</button>
					</div>
					<div>
						<h2>Requests Received: <span id="request-count">0</span></h2>
						<h3>Last Request: <span id="request">None</span></h3>
					</div>
				</body>
				</html>`

		w.Write([]byte(html))
	})

	// Test the browser connection with headless mode disabled
	headless := false
	result := TestBrowserConnection(t, bs, httpServer, headless)

	// Wait for the handler to be registered
	time.Sleep(1 * time.Second)

	// Make a request from the server to the client
	t.Log("Making request from server to client")
	var clientHandle broker.ClientHandle
	bs.IterateClients(func(ch broker.ClientHandle) bool {
		clientHandle = ch
		return false // Stop after the first client
	})
	require.NotNil(t, clientHandle, "Should have a client handle")

	// Make a request to the client
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	requestData := map[string]interface{}{
		"requestId": "test-request",
		"timestamp": time.Now().Unix(),
	}

	var responseData map[string]interface{}
	err := clientHandle.SendClientRequest(ctx, "client:get_status", requestData, &responseData, 0)
	require.NoError(t, err, "Failed to make request to client")
	t.Logf("Response from client: %v", responseData)

	// Wait for the UI to update
	time.Sleep(500 * time.Millisecond)

	// Verify the request count has been updated
	requestCount, err := result.Page.MustElement("#request-count").Text()
	require.NoError(t, err, "Should be able to get request count")
	assert.Equal(t, "1", requestCount, "Should have received 1 request")

	// Verify the request details have been updated
	requestText, err := result.Page.MustElement("#request").Text()
	require.NoError(t, err, "Should be able to get request text")
	assert.NotEqual(t, "None", requestText, "Request should not be 'None'")
	assert.Contains(t, requestText, "requestId", "Request should contain requestId")

	// Check console logs for request handling
	logs := result.Page.GetConsoleLog()
	t.Logf("Console logs: %v", logs)

	// Check if we have log messages for the request handling
	handlerRegistered := false
	requestReceived := false
	for _, log := range logs {
		if strings.Contains(log, "Registering handler for client:get_status") {
			handlerRegistered = true
		}
		if strings.Contains(log, "Received request on topic client:get_status") {
			requestReceived = true
		}
	}

	assert.True(t, handlerRegistered, "Should have registered a handler for client:get_status")
	assert.True(t, requestReceived, "Should have received a request on client:get_status")

	// Add a delay so we can see the browser window before it closes
	// This is only useful when running with headless=false
	if !result.Browser.IsHeadless() {
		t.Log("Waiting 5 seconds so you can see the browser window...")
		time.Sleep(5 * time.Second)
	}
}
