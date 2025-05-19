// Package browser_tests contains tests for the WebSocketMQ browser client.
package browser_tests

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
)

// TestBrowserClientConnection tests the browser client's ability to connect to the broker.
// This is a minimal test to verify that the browser client can connect to the broker.
func TestBrowserClientConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping browser test in short mode")
	}

	// Create a broker server with accept options to allow any origin
	opts := broker.DefaultOptions()
	opts.AcceptOptions = &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	}
	bs := testutil.NewBrokerServer(t, opts)

	// Note: system:register is already registered by the broker

	// Create an HTTP server mux
	mux := http.NewServeMux()

	// Register both the WebSocket handler and JavaScript client handler
	bs.RegisterHandlersWithDefaults(mux)

	// Create the HTTP server first so we can use its URL in the HTML
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()
	//serve /js_helpers/console_override.js and /js_helpers/connect.js and other static files from js_helpers folder
	mux.Handle("/js_helpers/", http.StripPrefix("/js_helpers/", http.FileServer(http.Dir("js_helpers"))))

	// Create a test HTML page handler
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<!DOCTYPE html>
			<html>
			<head>
				<title>WebSocketMQ Browser Client Test</title>
				<script src="/js_helpers/console_override.js"></script>
				<script src="/websocketmq.js"></script>
				<script src="/js_helpers/connect.js"></script>
			</head>
			<body>
				<h1>WebSocketMQ Browser Client Test</h1>
				<div>
					<h2>Connection Status: <span id="status">Disconnected</span></h2>
				</div>
				<!-- Just testing connection, no need for additional buttons or response display -->
			</body>
			</html>`

		// No need to replace WebSocket URL as the client will determine it automatically
		w.Write([]byte(html))
	})

	// HTTP server is already created above

	// Test the browser connection with headless mode disabled
	headless := false
	result := TestBrowserConnection(t, bs, httpServer, headless)

	// Add a delay so we can see the browser window before it closes
	// This is only useful when running with headless=false
	if !result.Browser.IsHeadless() {
		t.Log("Waiting 5 seconds so you can see the browser window...")
		time.Sleep(5 * time.Second)
	}
}
