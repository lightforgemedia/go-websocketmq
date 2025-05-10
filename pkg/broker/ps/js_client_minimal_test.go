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
// that we can load a page.
func TestJSClient_MinimalConnectivity(t *testing.T) {
	// Create a file server to serve the test HTML file
	fileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("File server request: %s", r.URL.Path)

		// Serve a simple HTML page directly
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`
				<!DOCTYPE html>
				<html>
				<head>
					<title>Minimal Test</title>
				</head>
				<body>
					<h1>Minimal Test Page</h1>
					<p id="status">Page Loaded</p>
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
	statusText, err := page.MustElement("#status").Text()
	require.NoError(t, err, "Error getting status text")
	require.Equal(t, "Page Loaded", statusText, "Status text mismatch")

	// Success! The page has loaded
	t.Logf("Test passed: Page loaded successfully")
}
