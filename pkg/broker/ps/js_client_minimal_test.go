// pkg/broker/ps/js_client_minimal_test.go
//go:build js_client
// +build js_client

// This file contains a minimal test for JavaScript client connectivity.
// Run with: go test -tags=js_client -run TestJSClient_MinimalConnectivity

package ps_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"github.com/lightforgemedia/go-websocketmq/pkg/server"
	"github.com/stretchr/testify/require"
)

// TestJSClient_MinimalConnectivity is the simplest possible test to verify
// that we can load a page and establish a WebSocket connection.
func TestJSClient_MinimalConnectivity(t *testing.T) {
	// Create a test server with WebSocket handler
	handlerOpts := server.DefaultHandlerOptions()
	testServer := NewTestServer(t, handlerOpts)
	defer testServer.Close()
	<-testServer.Ready
	t.Logf("WebSocket server running at: %s", testServer.Server.URL)

	// Create a channel to receive client registration events
	clientRegistered := make(chan string, 1)

	// Subscribe to client registration events
	err := testServer.Broker.Subscribe(context.Background(), broker.TopicClientRegistered,
		func(ctx context.Context, msg *model.Message, _ string) (*model.Message, error) {
			t.Logf("Received client registration event: %+v", msg.Body)

			// Extract the broker client ID from the message
			bodyMap, ok := msg.Body.(map[string]interface{})
			if !ok {
				t.Logf("Unexpected body type: %T", msg.Body)
				return nil, nil
			}

			clientID, ok := bodyMap["brokerClientID"].(string)
			if !ok {
				t.Logf("Missing brokerClientID in event")
				return nil, nil
			}

			// Send the client ID to the channel
			clientRegistered <- clientID
			return nil, nil
		})
	require.NoError(t, err, "Failed to subscribe to client registration events")

	// Create a file server to serve the test HTML file
	fileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("File server request: %s", r.URL.Path)

		// Serve a simple HTML page with WebSocketMQ client
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			wsURL := strings.Replace(testServer.Server.URL, "http://", "ws://", 1) + "/ws"

			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(fmt.Sprintf(`
				<!DOCTYPE html>
				<html>
				<head>
					<title>WebSocketMQ Connection Test</title>
					<script src="/wsmq/websocketmq.js"></script>
					<script>
						// Set up console history for test debugging
						if (!window.console.history) {
							window.console.history = [];
							const oldLog = console.log;
							const oldInfo = console.info;
							const oldWarn = console.warn;
							const oldError = console.error;

							console.log = function() {
								window.console.history.push({type: 'log', args: Array.from(arguments)});
								oldLog.apply(console, arguments);
							};
							console.info = function() {
								window.console.history.push({type: 'info', args: Array.from(arguments)});
								oldInfo.apply(console, arguments);
							};
							console.warn = function() {
								window.console.history.push({type: 'warn', args: Array.from(arguments)});
								oldWarn.apply(console, arguments);
							};
							console.error = function() {
								window.console.history.push({type: 'error', args: Array.from(arguments)});
								oldError.apply(console, arguments);
							};
						}

						document.addEventListener('DOMContentLoaded', function() {
							console.log('DOM loaded, initializing WebSocketMQ client');

							const statusEl = document.getElementById('status');
							const clientIdEl = document.getElementById('client-id');

							statusEl.textContent = 'Connecting...';

							// Generate a page session ID
							const pageSessionId = 'page-' + Math.random().toString(36).substring(2, 15);

							try {
								// Initialize WebSocketMQ client
								const client = new WebSocketMQ({
									url: '%s',
									pageSessionId: pageSessionId,
									debug: true,
									reconnect: true,
									reconnectInterval: 2000,
									maxReconnectAttempts: 5
								});

								// Store client in window for debugging
								window.wsmqClient = client;

								// Set up event handlers
								client.on('open', () => {
									console.log('WebSocket connection opened');
									statusEl.textContent = 'Connected';
								});

								client.on('close', () => {
									console.log('WebSocket connection closed');
									statusEl.textContent = 'Disconnected';
								});

								client.on('error', (error) => {
									console.error('WebSocket error:', error);
									statusEl.textContent = 'Error';
								});

								// Listen for client registration ACK to get the broker client ID
								client.subscribe('_client.registered', (data) => {
									console.log('Registration ACK received:', data);
									if (data && data.brokerClientID) {
										clientIdEl.textContent = data.brokerClientID;
									}
								});

								console.log('WebSocketMQ client initialized with page session ID:', pageSessionId);
							} catch (e) {
								console.error('Error initializing WebSocketMQ client:', e);
								statusEl.textContent = 'Initialization Error';
							}
						});
					</script>
				</head>
				<body>
					<h1>WebSocketMQ Connection Test</h1>
					<div>
						<strong>Status:</strong> <span id="status">Loading...</span>
					</div>
					<div>
						<strong>Client ID:</strong> <span id="client-id">N/A</span>
					</div>
				</body>
				</html>
			`, wsURL)))
			return
		}

		// For other requests, try to serve from the file system
		serveTestFiles(t, w, r)
	}))
	defer fileServer.Close()
	t.Logf("File server running at: %s", fileServer.URL)

	// Launch a browser
	l := launcher.New().Headless(true).MustLaunch()
	browser := rod.New().ControlURL(l).MustConnect()
	defer browser.MustClose()

	// Create the page URL
	pageURL := fmt.Sprintf("%s/index.html", fileServer.URL)
	t.Logf("Loading page: %s", pageURL)

	// Load the page
	page := browser.MustPage(pageURL).MustWaitLoad()
	defer page.MustClose()
	t.Logf("Page loaded")

	// Wait for the client to register
	var clientID string
	select {
	case clientID = <-clientRegistered:
		t.Logf("Client registered with ID: %s", clientID)
	case <-time.After(10 * time.Second):
		// If the test fails, log the browser console output
		logBrowserConsole(t, page)
		t.Fatal("Timed out waiting for client registration")
	}

	// Verify that the status is "Connected"
	deadline := time.Now().Add(5 * time.Second)
	var statusText string
	var textErr error
	for time.Now().Before(deadline) {
		statusText, textErr = page.MustElement("#status").Text()
		if textErr == nil && statusText == "Connected" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.NoError(t, textErr, "Error getting status text")
	require.Equal(t, "Connected", statusText, "Status text mismatch")

	// Verify that the client ID is displayed on the page
	clientIDText, err := page.MustElement("#client-id").Text()
	require.NoError(t, err, "Error getting client ID text")
	require.Equal(t, clientID, clientIDText, "Client ID mismatch")

	// Success! The client has connected and registered
	t.Logf("Test passed: Client connected and registered successfully")
}
