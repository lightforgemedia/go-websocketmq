package testutil

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRodBrowser(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping browser test in short mode")
	}

	// Create a simple HTTP server that serves a test page
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`
			<!DOCTYPE html>
			<html>
			<head>
				<title>Test Page</title>
				<script>
					// Store console logs
					window.consoleLog = [];
					const originalConsole = console.log;
					console.log = function() {
						window.consoleLog.push(Array.from(arguments).join(' '));
						originalConsole.apply(console, arguments);
					};

					// Log something when the page loads
					console.log('Page loaded at', new Date().toISOString());

					// Add a button click handler
					window.addEventListener('DOMContentLoaded', () => {
						const button = document.getElementById('testButton');
						if (button) {
							button.addEventListener('click', () => {
								console.log('Button clicked!');
								document.getElementById('result').textContent = 'Button was clicked!';
							});
						}
					});
				</script>
			</head>
			<body>
				<h1>Test Page for Rod Browser</h1>
				<div id="content">
					<p>This is a test page for the Rod Browser helper.</p>
					<button id="testButton">Click Me</button>
					<div id="result"></div>
					<ul id="list">
						<li class="item">Item 1</li>
						<li class="item">Item 2</li>
						<li class="item">Item 3</li>
					</ul>
				</div>
			</body>
			</html>
		`))
	}))
	defer server.Close()

	// Create a new Rod browser
	browser := NewRodBrowser(t, WithHeadless(true))

	// Navigate to the test page
	page := browser.MustPage(server.URL).WaitForLoad()

	// Verify the page loaded correctly
	page.VerifyPageLoaded("#content")

	// Check for specific elements
	assert.True(t, page.MustHas("h1"), "Page should have an h1 element")
	assert.True(t, page.MustHas("#testButton"), "Page should have a button")

	// Get the page title
	title, err := page.page.Eval("() => document.title")
	require.NoError(t, err, "Should be able to get page title")
	var titleStr string
	err = title.Value.Unmarshal(&titleStr)
	require.NoError(t, err, "Should be able to unmarshal title")
	assert.Equal(t, "Test Page", titleStr, "Page title should match")

	// Test MustElements to find multiple elements
	items := page.MustElements(".item")
	assert.Equal(t, 3, len(items), "Should find 3 list items")
	
	// Test that each item has the expected text
	for i, item := range items {
		text, err := item.Text()
		require.NoError(t, err, "Should be able to get item text")
		expectedText := fmt.Sprintf("Item %d", i+1)
		assert.Equal(t, expectedText, text, "Item text should match")
	}

	// Click the button
	page.Click("#testButton")

	// Wait for the result to appear
	time.Sleep(100 * time.Millisecond)

	// Check the result
	resultText, err := page.MustElement("#result").Text()
	require.NoError(t, err, "Should be able to get result text")
	assert.Equal(t, "Button was clicked!", resultText, "Result text should match")
	
	// Test Eval method 
	evalResult, err := page.Eval("() => document.getElementsByClassName('item').length")
	require.NoError(t, err, "Should be able to evaluate JavaScript")
	var itemCount int
	err = evalResult.Value.Unmarshal(&itemCount)
	require.NoError(t, err, "Should be able to unmarshal eval result")
	assert.Equal(t, 3, itemCount, "Eval should return 3 items")

	// Get console logs
	logs := page.GetConsoleLog()
	assert.GreaterOrEqual(t, len(logs), 1, "Should have at least one console log entry")
	assert.Contains(t, logs[len(logs)-1], "Button clicked!", "Last log should contain button click message")
}

func TestGetBrokerWSURL(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	}))
	defer server.Close()

	// Test with default path
	wsURL := GetBrokerWSURL(server, "")
	assert.True(t, wsURL != "", "WebSocket URL should not be empty")
	assert.True(t, wsURL[:5] == "ws://", "WebSocket URL should start with ws://")
	assert.True(t, wsURL[len(wsURL)-3:] == "/ws", "WebSocket URL should end with /ws")

	// Test with custom path
	wsURL = GetBrokerWSURL(server, "wsmq")
	assert.True(t, wsURL != "", "WebSocket URL should not be empty")
	assert.True(t, wsURL[:5] == "ws://", "WebSocket URL should start with ws://")
	assert.True(t, wsURL[len(wsURL)-5:] == "/wsmq", "WebSocket URL should end with /wsmq")

	// Test with path that already has a slash
	wsURL = GetBrokerWSURL(server, "/custom")
	assert.True(t, wsURL != "", "WebSocket URL should not be empty")
	assert.True(t, wsURL[:5] == "ws://", "WebSocket URL should start with ws://")
	assert.True(t, wsURL[len(wsURL)-7:] == "/custom", "WebSocket URL should end with /custom")
}
