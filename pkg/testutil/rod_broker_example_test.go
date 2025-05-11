package testutil

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	app_shared_types "github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This test demonstrates how to use the Rod helper with a BrokerServer
func TestRodWithBrokerServer(t *testing.T) {
	// Create a broker server with accept options to allow any origin
	bs := NewBrokerServer(t, broker.WithAcceptOptions(&websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	}))

	// Register a handler for the get_time request
	err := bs.OnRequest(app_shared_types.TopicGetTime,
		func(ch broker.ClientHandle, req app_shared_types.GetTimeRequest) (app_shared_types.GetTimeResponse, error) {
			t.Logf("Server received get_time request from client %s", ch.ID())
			return app_shared_types.GetTimeResponse{CurrentTime: time.Now().Format(time.RFC3339)}, nil
		},
	)
	require.NoError(t, err, "Failed to register server handler")

	// Create a simple HTTP server that serves a test page
	// This would typically be a file server serving your HTML/JS files
	fileServer := http.NewServeMux()
	fileServer.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<!DOCTYPE html>
			<html>
			<head>
				<title>WebSocketMQ Test</title>
				<script>
					// Simple WebSocket client for testing
					window.consoleLog = [];
					const originalConsole = console.log;
					console.log = function() {
						window.consoleLog.push(Array.from(arguments).join(' '));
						originalConsole.apply(console, arguments);
					};

					let ws = null;
					let messageId = 0;

					function connect() {
						const wsUrl = document.getElementById('wsUrl').value;
						console.log('Connecting to', wsUrl);

						ws = new WebSocket(wsUrl);

						ws.onopen = function() {
							console.log('Connected to WebSocket server');
							document.getElementById('status').textContent = 'Connected';
							document.getElementById('status').style.color = 'green';
						};

						ws.onclose = function() {
							console.log('Disconnected from WebSocket server');
							document.getElementById('status').textContent = 'Disconnected';
							document.getElementById('status').style.color = 'red';
							ws = null;
						};

						ws.onerror = function(error) {
							console.error('WebSocket error:', error);
							document.getElementById('status').textContent = 'Error';
							document.getElementById('status').style.color = 'red';
						};

						ws.onmessage = function(event) {
							console.log('Received message:', event.data);
							const message = JSON.parse(event.data);
							document.getElementById('response').textContent = JSON.stringify(message, null, 2);
						};
					}

					function sendRequest() {
						if (!ws) {
							console.error('Not connected');
							return;
						}

						const id = 'msg_' + (messageId++);
						const request = {
							id: id,
							type: 'request',
							topic: 'system:get_time',
							payload: {}
						};

						console.log('Sending request:', request);
						ws.send(JSON.stringify(request));
					}
				</script>
			</head>
			<body>
				<h1>WebSocketMQ Test</h1>
				<div>
					<label for="wsUrl">WebSocket URL:</label>
					<input type="text" id="wsUrl" value="WEBSOCKET_URL" style="width: 300px;" />
					<button onclick="connect()">Connect</button>
					<span id="status">Disconnected</span>
				</div>
				<div>
					<button id="sendRequestBtn" onclick="sendRequest()">Send Request</button>
				</div>
				<div>
					<h3>Response:</h3>
					<pre id="response"></pre>
				</div>
			</body>
			</html>`

		// Replace the WebSocket URL placeholder
		html = strings.Replace(html, "WEBSOCKET_URL", bs.WSURL, 1)
		w.Write([]byte(html))
	})
	httpServer := NewServer(t, fileServer)

	// Get the WebSocket URL from the broker server
	wsURL := bs.WSURL
	t.Logf("WebSocket URL: %s", wsURL)
	t.Logf("HTTP Server URL: %s", httpServer.URL)

	headless := true
	// Create a new Rod browser
	browser := NewRodBrowser(t, WithHeadless(headless))

	// Navigate to the test page
	page := browser.MustPage(httpServer.URL).WaitForLoad()

	// Verify the page loaded correctly
	page.VerifyPageLoaded("body")

	// Click the connect button
	page.Click("button")

	// Wait for connection to establish
	time.Sleep(500 * time.Millisecond)

	// Check connection status
	statusText, err := page.MustElement("#status").Text()
	require.NoError(t, err, "Should be able to get status text")
	assert.Equal(t, "Connected", statusText, "Status should be Connected")

	// Send a request
	page.Click("#sendRequestBtn")

	// Wait for response
	time.Sleep(500 * time.Millisecond)

	// Check response
	responseText, err := page.MustElement("#response").Text()
	require.NoError(t, err, "Should be able to get response text")
	assert.Contains(t, responseText, "system:get_time", "Response should contain the topic")
	assert.Contains(t, responseText, "currentTime", "Response should contain the current time")

	// Get console logs
	logs := page.GetConsoleLog()
	t.Logf("Console logs: %v", logs)

	// Verify a client connected to the broker
	var clientCount int
	bs.IterateClients(func(ch broker.ClientHandle) bool {
		clientCount++
		t.Logf("Client connected: ID=%s, Name=%s", ch.ID(), ch.Name())
		return true
	})
	assert.Equal(t, 1, clientCount, "Should have one client connected")
	if !headless {
		// Keep browser open for manual inspection
		t.Logf("Sleeping for 30 seconds to keep browser open for manual inspection")
		time.Sleep(30 * time.Second)
	}
}

// NewServer creates a new HTTP test server.
func NewServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.Close()
	})
	return server
}
