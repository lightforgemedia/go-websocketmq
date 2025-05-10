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

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/stretchr/testify/require"
)

// TestJSClient_MinimalConnectivity is the simplest possible test to verify
// that we can load a page and display "Hello, World!".
func TestJSClient_MinimalConnectivity(t *testing.T) {
	// Create a file server to serve the test HTML file
	fileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
