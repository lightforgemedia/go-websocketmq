// Package browser_tests contains tests for the WebSocketMQ browser client.
package browser_tests

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConnectionResult contains the results of a browser client connection test
type TestConnectionResult struct {
	Browser     *testutil.RodBrowser
	Page        *testutil.RodPage
	ClientID    string
	ClientURL   string
	ClientCount int
	BrowserURL  string
	ConsoleLog  []string
}

// TestBrowserConnection is a helper function that tests browser client connection
// It opens a browser, navigates to the test page, and verifies the connection
func TestBrowserConnection(t *testing.T, bs *testutil.BrokerServer, httpServer *httptest.Server, headless bool) *TestConnectionResult {
	// Get the WebSocket URL
	wsURL := strings.Replace(httpServer.URL, "http://", "ws://", 1) + "/wsmq"
	t.Logf("WebSocket URL: %s", wsURL)
	t.Logf("HTTP Server URL: %s", httpServer.URL)

	// Create a new Rod browser with headless mode disabled so we can see the browser
	browser := testutil.NewRodBrowser(t, testutil.WithHeadless(headless))

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
		if strings.Contains(log, "Connected to WebSocket server") {
			connectionSuccess = true
			break
		}
	}

	assert.True(t, connectionSuccess, "Should have connected to the WebSocket server")

	// Verify a client connected to the broker
	var clientCount int
	var clientID string
	var clientURL string
	// Find the browser client
	var browserClient broker.ClientHandle
	bs.IterateClients(func(ch broker.ClientHandle) bool {
		clientCount++
		t.Logf("Client connected: ID=%s, Name=%s, Type=%s, URL=%s",
			ch.ID(), ch.Name(), ch.ClientType(), ch.ClientURL())

		// Look for the browser client (has a non-empty URL)
		if ch.ClientType() == "browser" && ch.ClientURL() != "" {
			clientID = ch.ID()
			clientURL = ch.ClientURL()
			browserClient = ch
		}
		return true
	})

	// Make sure we found a browser client
	assert.NotNil(t, browserClient, "Should have a browser client connected")
	assert.NotEmpty(t, clientURL, "Browser client URL should not be empty")

	// Verify the URL is updated with client ID
	// Wait a bit for the URL to be updated
	time.Sleep(500 * time.Millisecond)

	urlStr, err := page.GetCurrentURL()
	require.NoError(t, err, "Should be able to get current URL")
	t.Logf("Current page URL: %s", urlStr)
	assert.Contains(t, urlStr, "client_id=", "URL should contain client_id parameter")

	// Return the test results
	return &TestConnectionResult{
		Browser:     browser,
		Page:        page,
		ClientID:    clientID,
		ClientURL:   clientURL,
		ClientCount: clientCount,
		BrowserURL:  urlStr,
		ConsoleLog:  logs,
	}
}
