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
	"net/url"
	"strconv"
	"testing"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/stretchr/testify/require"
)

// getPortFromURL extracts the port number from a URL
func getPortFromURL(urlStr string) int {
	u, err := url.Parse(urlStr)
	if err != nil {
		return 0
	}

	// If the port is explicitly specified in the URL
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil {
			return 0
		}
		return port
	}

	// Default ports based on scheme
	switch u.Scheme {
	case "http":
		return 80
	case "https":
		return 443
	default:
		return 0
	}
}

// TestJSClient_MinimalConnectivity is the simplest possible test to verify
// that we can load a page and display "Hello, World!".
func TestJSClient_MinimalConnectivity(t *testing.T) {
	// Create a file server to serve the test HTML file
	var fileServer *httptest.Server

	// Create a server handler function
	serverHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("File server request: %s", r.URL.Path)

		// Serve a simple HTML page with "Hello, World!"
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`
				<!DOCTYPE html>
				<html>
				<head>
					<title>Hello World Test</title>
				</head>
				<body>
					<h1>Hello, World!</h1>
					<p id="message">This is a test page.</p>
				</body>
				</html>
			`))
			return
		}

		// For other requests, try to serve from the file system
		serveTestFiles(t, w, r)
	})

	// Create a server and ensure it's not using port 8080
	for {
		fileServer = httptest.NewServer(serverHandler)

		// Check if the server is using port 8080
		port := getPortFromURL(fileServer.URL)
		if port != 8080 && port != 80 && port != 84 {
			break
		}

		// If it's using port 8080, close it and try again
		t.Logf("Server assigned port %d, which is not allowed. Restarting server...", port)
		fileServer.Close()
	}

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

	// Verify that the page loaded correctly
	h1Text, err := page.MustElement("h1").Text()
	require.NoError(t, err, "Error getting h1 text")
	require.Equal(t, "Hello, World!", h1Text, "h1 text mismatch")

	messageText, err := page.MustElement("#message").Text()
	require.NoError(t, err, "Error getting message text")
	require.Equal(t, "This is a test page.", messageText, "Message text mismatch")

	// Success! The page has loaded
	t.Logf("Test passed: Page loaded successfully")
}
