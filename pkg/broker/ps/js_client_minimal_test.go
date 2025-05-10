// pkg/broker/ps/js_client_minimal_test.go
//go:build js_client
// +build js_client

// This file contains a minimal test for JavaScript client connectivity.
// Run with: go test -tags=js_client -run TestJSClient_MinimalConnectivity

package ps_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/stretchr/testify/require"
)

// TestJSClient_MinimalConnectivity is the simplest possible test to verify
// that we can load the WebSocketMQ library.
func TestJSClient_MinimalConnectivity(t *testing.T) {
	// Create a file server to serve the test HTML file
	fileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("File server request: %s", r.URL.Path)

		// Serve a simple HTML page that just loads the WebSocketMQ library
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`
				<!DOCTYPE html>
				<html>
				<head>
					<title>WebSocketMQ Library Test</title>
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
							console.log('DOM loaded, checking WebSocketMQ library');

							const statusEl = document.getElementById('status');

							try {
								console.log('WebSocketMQ script loaded:', typeof WebSocketMQ);

								if (typeof WebSocketMQ === 'function') {
									console.log('WebSocketMQ is a constructor function');
									statusEl.textContent = 'WebSocketMQ Loaded';
									statusEl.className = 'success';
								} else {
									console.error('WebSocketMQ is not a constructor function:', WebSocketMQ);
									statusEl.textContent = 'WebSocketMQ Not Found';
									statusEl.className = 'error';
								}
							} catch (e) {
								console.error('Error checking WebSocketMQ:', e);
								console.error('Error details:', e.message);
								console.error('Error stack:', e.stack);
								statusEl.textContent = 'Error: ' + e.message;
								statusEl.className = 'error';
							}
						});
					</script>
					<style>
						.success { color: green; }
						.error { color: red; }
					</style>
				</head>
				<body>
					<h1>WebSocketMQ Library Test</h1>
					<div>
						<strong>Status:</strong> <span id="status">Loading...</span>
					</div>
				</body>
				</html>
			`))
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

	// Wait for the status to be updated
	deadline := time.Now().Add(5 * time.Second)
	var statusText string
	var textErr error
	for time.Now().Before(deadline) {
		statusText, textErr = page.MustElement("#status").Text()
		if textErr == nil && statusText != "Loading..." {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Log the browser console output
	logBrowserConsole(t, page)

	// Check if the WebSocketMQ library was loaded
	require.NoError(t, textErr, "Error getting status text")
	require.Equal(t, "WebSocketMQ Loaded", statusText, "WebSocketMQ library not loaded")

	// Success! The WebSocketMQ library was loaded
	t.Logf("Test passed: WebSocketMQ library loaded successfully")
}
