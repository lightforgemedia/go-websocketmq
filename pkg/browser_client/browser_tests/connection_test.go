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
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBrowserClientConnection tests the browser client's ability to connect to the broker.
// This is a minimal test to verify that the browser client can connect to the broker.
func TestBrowserClientConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping browser test in short mode")
	}

	// Create a broker server with accept options to allow any origin
	bs := testutil.NewBrokerServer(t, broker.WithAcceptOptions(&websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	}))

	// Note: system:register is already registered by the broker

	// Create an HTTP server mux
	mux := http.NewServeMux()

	// Register both the WebSocket handler and JavaScript client handler
	bs.RegisterHandlersWithDefaults(mux)

	// Create the HTTP server first so we can use its URL in the HTML
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	// Create a test HTML page handler
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<!DOCTYPE html>
			<html>
			<head>
				<title>WebSocketMQ Browser Client Test</title>
				<script>
					// Store console logs
					window.consoleLog = [];
					const originalConsole = console.log;
					console.log = function() {
						window.consoleLog.push(Array.from(arguments).join(' '));
						originalConsole.apply(console, arguments);
					};
				</script>
				<script src="/websocketmq.js"></script>
				<script>
					// Initialize the WebSocketMQ client when the page loads
					let client;

					// Set up the client and connect automatically when the page loads
					window.addEventListener('DOMContentLoaded', () => {
						// Get the WebSocket URL from the data attribute
						const wsUrl = document.getElementById('wsUrl').getAttribute('data-url');
						console.log('Page loaded, connecting to', wsUrl);

						// Create a new WebSocketMQ client with explicit WebSocket URL
						client = new WebSocketMQ.Client({
							url: wsUrl
						});

						client.onConnect(() => {
							console.log('Connected to WebSocket server with ID:', client.getID());
							document.getElementById('status').textContent = 'Connected';
							document.getElementById('status').style.color = 'green';
						});

						client.onDisconnect(() => {
							console.log('Disconnected from WebSocket server');
							document.getElementById('status').textContent = 'Disconnected';
							document.getElementById('status').style.color = 'red';
						});

						// Connect automatically
						client.connect();
					});
				</script>
			</head>
			<body>
				<h1>WebSocketMQ Browser Client Test</h1>
				<div id="wsUrl" data-url="WEBSOCKET_URL" style="display: none;"></div>
				<div>
					<h2>Connection Status: <span id="status">Disconnected</span></h2>
				</div>
				<!-- Just testing connection, no need for additional buttons or response display -->
			</body>
			</html>`

		// Replace the WebSocket URL placeholder with the actual WebSocket URL
		wsURL := strings.Replace(httpServer.URL, "http://", "ws://", 1) + "/wsmq"
		html = strings.Replace(html, "WEBSOCKET_URL", wsURL, 1)
		w.Write([]byte(html))
	})

	// HTTP server is already created above

	// Get the WebSocket URL
	wsURL := strings.Replace(httpServer.URL, "http://", "ws://", 1) + "/wsmq"
	t.Logf("WebSocket URL: %s", wsURL)
	t.Logf("HTTP Server URL: %s", httpServer.URL)

	// Create a new Rod browser
	browser := testutil.NewRodBrowser(t, testutil.WithHeadless(true))

	// Navigate to the test page
	page := browser.MustPage(httpServer.URL).WaitForLoad()

	// Verify the page loaded correctly
	page.VerifyPageLoaded("body")

	// Wait for connection to establish (up to 3 seconds)
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)

		// Check connection status
		statusText, err := page.MustElement("#status").Text()
		if err == nil && statusText == "Connected" {
			t.Logf("Connection established after %d attempts", i+1)
			break
		}
	}

	// Check final connection status
	statusText, err := page.MustElement("#status").Text()
	require.NoError(t, err, "Should be able to get status text")
	assert.Equal(t, "Connected", statusText, "Status should be Connected")

	// Get console logs to verify connection
	logs := page.GetConsoleLog()
	t.Logf("Console logs: %v", logs)

	// Check if we have a log message indicating successful connection
	connectionSuccess := false
	for _, log := range logs {
		if strings.Contains(log, "Connected to WebSocket server with ID:") {
			connectionSuccess = true
			break
		}
	}

	assert.True(t, connectionSuccess, "Should have connected to the WebSocket server")

	// We already got the console logs above

	// Verify a client connected to the broker
	var clientCount int
	bs.IterateClients(func(ch broker.ClientHandle) bool {
		clientCount++
		t.Logf("Client connected: ID=%s, Name=%s, Type=%s, URL=%s",
			ch.ID(), ch.Name(), ch.ClientType(), ch.ClientURL())
		return true
	})
	assert.Equal(t, 1, clientCount, "Should have one client connected")
}
