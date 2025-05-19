package browser_tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
	"github.com/stretchr/testify/require"
)

func TestSimpleHandlerRegistration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping browser test in short mode")
	}

	// Create broker server
	bs := testutil.NewBrokerServer(t, broker.WithAcceptOptions(&websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	}))

	// Create HTTP server
	mux := http.NewServeMux()
	bs.RegisterHandlersWithDefaults(mux)
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	// Create a simple test page
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<!DOCTYPE html>
			<html>
			<head>
				<title>Simple Handler Test</title>
				<script src="/websocketmq.js"></script>
				<script>
					// Capture console logs
					console._logs = [];
					const origLog = console.log;
					const origError = console.error;
					console.log = function(...args) {
						console._logs.push({type: 'log', args: args});
						origLog.apply(console, args);
					};
					console.error = function(...args) {
						console._logs.push({type: 'error', args: args});
						origError.apply(console, args);
					};
					
					window.addEventListener('DOMContentLoaded', function() {
						console.log('DOM loaded');
						
						window.client = new WebSocketMQ.Client({});
						
						window.client.onConnect(() => {
							console.log('Connected');
							document.getElementById('status').textContent = 'Connected';
							
							window.client.handleServerRequest('test.echo', (payload) => {
								console.log('Echo handler called with:', payload);
								return { echo: payload.message };
							});
							
							console.log('Handler registered');
							document.getElementById('handler-status').textContent = 'Handler registered';
						});
						
						window.client.onerror = (err) => {
							console.error('Client error:', err);
						};
						
						window.client.connect();
					});
				</script>
			</head>
			<body>
				<h1>Simple Handler Test</h1>
				<div>Status: <span id="status">Disconnected</span></div>
				<div>Handler: <span id="handler-status">Not registered</span></div>
			</body>
			</html>`
		w.Write([]byte(html))
	})

	// Create browser and navigate
	browser := testutil.NewRodBrowser(t, testutil.WithHeadless(false))
	page := browser.MustPage(httpServer.URL).WaitForLoad()

	// Wait for connection
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		statusText, err := page.MustElement("#status").Text()
		if err == nil && statusText == "Connected" {
			break
		}
	}

	// Wait for handler registration
	for i := 0; i < 10; i++ {
		time.Sleep(100 * time.Millisecond)
		handlerText, err := page.MustElement("#handler-status").Text()
		if err == nil && handlerText == "Handler registered" {
			break
		}
	}

	// Get console messages
	consoleLogs, err := page.Eval(`() => JSON.stringify(console._logs || [])`)
	if err == nil && consoleLogs != nil {
		t.Logf("Console logs: %s", consoleLogs.Value)
	}

	// Get browser client ID
	var browserClientID string
	for i := 0; i < 30; i++ {
		bs.IterateClients(func(ch broker.ClientHandle) bool {
			if ch.ClientType() == "browser" {
				browserClientID = ch.ID()
				return false
			}
			return true
		})
		if browserClientID != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotEmpty(t, browserClientID, "Should find browser client")

	// Create control client
	controlClient := testutil.NewTestClient(t, bs.WSURL)
	require.NotNil(t, controlClient, "Control client should be created")

	// Send a proxy request
	proxyReq := shared_types.ProxyRequest{
		TargetID: browserClientID,
		Topic:    "test.echo",
		Payload:  []byte(`{"message": "Hello World"}`),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rawResp, errPayload, err := controlClient.SendServerRequest(ctx, shared_types.TopicProxyRequest, proxyReq)
	if err != nil {
		t.Fatalf("Proxy request failed: %v", err)
	}
	if errPayload != nil {
		t.Fatalf("Error payload: %v", errPayload)
	}

	t.Logf("Raw response: %s", string(*rawResp))

	// Test completed
}