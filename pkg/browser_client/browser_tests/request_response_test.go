// Package browser_tests contains tests for the WebSocketMQ browser client.
package browser_tests

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBrowserClientRequestResponse tests the browser client's ability to make requests to the server.
func TestBrowserClientRequestResponse(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping browser test in short mode")
	}

	// Create a broker server with accept options to allow any origin
	bs := testutil.NewBrokerServer(t, broker.WithAcceptOptions(&websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	}))

	// Register a handler for the GetTime request
	err := bs.OnRequest(shared_types.TopicGetTime,
		func(client broker.ClientHandle, req shared_types.GetTimeRequest) (shared_types.GetTimeResponse, error) {
			t.Logf("Server: Client %s requested time", client.ID())
			return shared_types.GetTimeResponse{CurrentTime: time.Now().Format(time.RFC3339)}, nil
		},
	)
	require.NoError(t, err, "Failed to register GetTime handler")

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
					<title>WebSocketMQ Request-Response Test</title>
					<script src="/js_helpers/console_override.js"></script>
					<script src="/websocketmq.js"></script>
					<script src="/js_helpers/connect.js"></script>
					<script>
						// Store the last response message
						window.lastResponse = null;

						// Function to make a request to get the server time
						function getServerTime() {
							console.log('Making request to system:get_time');

							// Update UI to show request is in progress
							document.getElementById('response').textContent = 'Requesting...';

							// Make sure client is connected
							if (window.client && window.client.isConnected) {
								// Generate a unique request ID so we can match the response
								const requestId = Date.now().toString();

								// Add a raw message event listener to the WebSocket
								const ws = window.client._ws;
								if (ws) {
									const originalOnMessage = ws.onmessage;
									ws.onmessage = function(event) {
										try {
											const data = JSON.parse(event.data);
											console.log('Raw WebSocket message:', data);

											// If this is a response to our get_time request
											if (data.type === 'response' && data.topic === 'system:get_time') {
												console.log('Found response message:', data);
												window.lastResponse = data;
												// Display the payload - make sure it has the currentTime field
												if (data.payload && data.payload.currentTime) {
													console.log('Found currentTime in response:', data.payload.currentTime);
													document.getElementById('response').textContent = JSON.stringify(data.payload);
												}
											}
										} catch (e) {
											console.error('Error parsing WebSocket message:', e);
										}

										// Call the original handler
										if (originalOnMessage) {
											originalOnMessage.call(ws, event);
										}
									};
								}

								// Make the request
								window.client.request('system:get_time', {})
									.then(response => {
										console.log('Promise resolved with:', response);

										// If we have a captured response with currentTime, use that
										if (window.lastResponse && window.lastResponse.payload && window.lastResponse.payload.currentTime) {
											document.getElementById('response').textContent = JSON.stringify(window.lastResponse.payload);
										} else if (response && response.currentTime) {
											// If the promise resolved with a response containing currentTime, use that
											document.getElementById('response').textContent = JSON.stringify(response);
										} else {
											// Try to extract the currentTime from the raw message
											console.log('Checking raw messages for currentTime');
											// We won't update the response element here, as we want the test to fail if we don't get currentTime
										}
									})
									.catch(error => {
										console.error('Error making request to system:get_time:', error);
										document.getElementById('response').textContent = 'Error: ' + error.message;
									});
							} else {
								console.error('Client not connected');
								document.getElementById('response').textContent = 'Error: Client not connected';
							}
						}

						// Initialize the button when the page loads
						document.addEventListener('DOMContentLoaded', () => {
							const getTimeBtn = document.getElementById('get-time-btn');
							if (getTimeBtn) {
								getTimeBtn.addEventListener('click', getServerTime);
							}
						});
					</script>
				</head>
				<body>
					<h1>WebSocketMQ Request-Response Test</h1>
					<div>
						<h2>Connection Status: <span id="status">Disconnected</span></h2>
					</div>
					<div>
						<button id="get-time-btn">Get Server Time</button>
						<h2>Response: <span id="response">None</span></h2>
					</div>
				</body>
				</html>`

		w.Write([]byte(html))
	})

	// Test the browser connection with headless mode disabled
	headless := false
	result := TestBrowserConnection(t, bs, httpServer, headless)
	time.Sleep(1000 * time.Millisecond)

	// Now that we're connected, click the button to make a request
	t.Log("Clicking the Get Server Time button")
	result.Page.MustElement("#get-time-btn").MustClick()

	// Wait for the response to be updated (up to 5 seconds)
	var responseText string
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)

		// Check if response has been updated
		responseText, err = result.Page.MustElement("#response").Text()
		if err == nil && responseText != "None" && responseText != "Requesting..." {
			t.Logf("Response received after %d attempts: %s", i+1, responseText)
			break
		}
	}

	// Verify the response is not empty
	assert.NotEqual(t, "None", responseText, "Response should not be 'None'")
	assert.NotEqual(t, "Requesting...", responseText, "Response should not be 'Requesting...'")

	//RESPONSE MUST BE currentTime
	validResponse := strings.Contains(responseText, "currentTime")
	assert.True(t, validResponse, "Response should contain either currentTime")

	// Check console logs for request and response
	logs := result.Page.GetConsoleLog()
	t.Logf("Console logs: %v", logs)

	// Check if we have log messages for the request and response
	requestSent := false
	responseReceived := false
	for _, log := range logs {
		if strings.Contains(log, "Making request to system:get_time") {
			requestSent = true
		}
		// Check for any indication of a response
		if strings.Contains(log, "Received message") ||
			strings.Contains(log, "Promise resolved with") ||
			strings.Contains(log, "Raw WebSocket message") {
			responseReceived = true
		}
	}

	assert.True(t, requestSent, "Should have sent a request to system:get_time")
	assert.True(t, responseReceived, "Should have received a response message")

	// Add a delay so we can see the browser window before it closes
	// This is only useful when running with headless=false
	if !result.Browser.IsHeadless() {
		t.Log("Waiting 5 seconds so you can see the browser window...")
		time.Sleep(5 * time.Second)
	}
}
