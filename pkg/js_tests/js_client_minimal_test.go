package js_tests

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJSClient_Minimal is a minimal test to verify that the JavaScript client tests can run
func TestJSClient_Minimal(t *testing.T) {
	// Create a file server to serve a simple HTML page
	fileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("File server request: %s", r.URL.Path)

		// Serve a simple HTML page
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`
			<!DOCTYPE html>
			<html>
			<head>
				<title>Minimal Test</title>
			</head>
			<body>
				<h1>Hello, World!</h1>
				<p id="message">This is a minimal test page.</p>
			</body>
			</html>
		`))
	}))
	defer fileServer.Close()

	// Launch a browser
	l := launcher.New().Headless(false).MustLaunch()
	browser := rod.New().ControlURL(l).MustConnect()
	defer browser.MustClose()

	// Navigate to the test page
	pageURL := fmt.Sprintf("%s/", fileServer.URL)
	t.Logf("Loading page: %s", pageURL)
	page := browser.MustPage(pageURL).MustWaitLoad()
	defer page.MustClose()

	// Verify that the page loaded correctly
	h1Text, err := page.MustElement("h1").Text()
	require.NoError(t, err, "Error getting h1 text")
	assert.Equal(t, "Hello, World!", h1Text, "h1 text mismatch")

	messageText, err := page.MustElement("#message").Text()
	require.NoError(t, err, "Error getting message text")
	assert.Equal(t, "This is a minimal test page.", messageText, "Message text mismatch")

	// Success!
	t.Logf("Minimal test passed")
}
