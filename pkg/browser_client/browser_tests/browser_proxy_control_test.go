// Package browser_tests contains tests for the WebSocketMQ browser client proxy control functionality.
package browser_tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/client"
	"github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBrowserProxyControl tests the proxy control functionality for browser clients
func TestBrowserProxyControl(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping browser test in short mode")
	}

	// Create a broker server with accept options to allow any origin
	bs := testutil.NewBrokerServer(t, broker.WithAcceptOptions(&websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	}))

	// Create an HTTP server mux
	mux := http.NewServeMux()

	// Register both the WebSocket handler and JavaScript client handler
	bs.RegisterHandlersWithDefaults(mux)

	// Create the HTTP server
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	// Serve JavaScript helpers
	mux.Handle("/js_helpers/", http.StripPrefix("/js_helpers/", http.FileServer(http.Dir("js_helpers"))))

	// Create a test HTML page with proxy control handlers
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<!DOCTYPE html>
			<html>
			<head>
				<title>Browser Proxy Control Test</title>
				<script src="/js_helpers/console_override.js"></script>
				<script src="/websocketmq.js"></script>
				<script>
					window.commandHistory = [];
					window.formData = {};
					
					async function setupProxyHandlers() {
						console.log('Setting up proxy handlers');
						
						// Browser navigation handler
						window.client.handleServerRequest('browser.navigate', async (req) => {
							const url = req.url;
							console.log('Navigate command received:', url);
							window.commandHistory.push({type: 'navigate', url: url});
							// Simulate navigation by updating a status element
							document.getElementById('current-url').textContent = url;
							return { success: true, message: 'Navigated to ' + url };
						});
						
						// Alert handler
						window.client.handleServerRequest('browser.alert', async (req) => {
							const message = req.message;
							console.log('Alert command received:', message);
							window.commandHistory.push({type: 'alert', message: message});
							// Simulate alert by updating UI instead of showing real alert
							document.getElementById('last-alert').textContent = message;
							return { success: true, message: 'Alert shown' };
						});
						
						// JavaScript execution handler
						window.client.handleServerRequest('browser.exec', async (req) => {
							const code = req.code;
							console.log('Execute command received:', code);
							window.commandHistory.push({type: 'exec', code: code});
							try {
								const result = eval(code);
								return { success: true, result: String(result) };
							} catch (error) {
								return { success: false, error: error.message };
							}
						});
						
						// Browser info handler
						window.client.handleServerRequest('browser.info', async (req) => {
							console.log('Info request received');
							window.commandHistory.push({type: 'info'});
							return {
								userAgent: navigator.userAgent,
								url: window.location.href,
								title: document.title,
								viewport: {
									width: window.innerWidth,
									height: window.innerHeight
								}
							};
						});
						
						// Form fill handler
						window.client.handleServerRequest('browser.fillForm', async (req) => {
							const fields = req.fields;
							console.log('Fill form command received:', fields);
							window.commandHistory.push({type: 'fillForm', fields: fields});
							let filled = [];
							
							for (const [fieldId, value] of Object.entries(fields)) {
								const field = document.getElementById(fieldId);
								if (field) {
									field.value = value;
									window.formData[fieldId] = value;
									filled.push(fieldId);
								}
							}
							
							return { success: true, filled: filled };
						});
						
						// Click handler
						window.client.handleServerRequest('browser.click', async (req) => {
							const selector = req.selector;
							console.log('Click command received:', selector);
							window.commandHistory.push({type: 'click', selector: selector});
							
							const element = document.querySelector(selector);
							if (element) {
								// Simulate click by setting a data attribute
								element.setAttribute('data-clicked', 'true');
								return { success: true, element: selector };
							} else {
								return { success: false, error: 'Element not found: ' + selector };
							}
						});
						
						console.log('Proxy handlers setup complete');
					}
					
					// Wait for WebSocketMQ to be available and connect
					window.addEventListener('DOMContentLoaded', function() {
						console.log('DOM loaded, initializing WebSocketMQ client');
						
						// Create a WebSocketMQ client - matches the API from connect.js
						try {
							const wsUrl = window.location.origin.replace('http', 'ws') + '/wsmq';
							console.log('Using WebSocket URL:', wsUrl);
							
							window.client = new WebSocketMQ.Client({});
							console.log('Client created successfully');
							
							window.client.onConnect(() => {
								console.log('Connected to WebSocket server with ID:', window.client.getID());
								document.getElementById('status').textContent = 'Connected';
								document.getElementById('client-id').textContent = window.client.getID();
								
								// Set up proxy handlers after connection
								setupProxyHandlers();
							});
							
							window.client.onDisconnect(() => {
								console.log('Disconnected from WebSocket server');
								document.getElementById('status').textContent = 'Disconnected';
							});
							
							window.client.onError((error) => {
								console.error('WebSocket error:', error);
							});
							
							// Connect automatically
							console.log('Calling client.connect()');
							window.client.connect();
							console.log('client.connect() called successfully');
						} catch (err) {
							console.error('Error creating/connecting client:', err);
						}
					});
				</script>
			</head>
			<body>
				<h1>Browser Proxy Control Test</h1>
				<div>
					<h2>Connection Status: <span id="status">Disconnected</span></h2>
					<h2>Client ID: <span id="client-id">Unknown</span></h2>
				</div>
				<div>
					<h3>Current URL: <span id="current-url">Not Set</span></h3>
					<h3>Last Alert: <span id="last-alert">None</span></h3>
				</div>
				<form id="test-form">
					<input type="text" id="name" />
					<input type="email" id="email" />
				</form>
				<button id="test-button">Test Button</button>
			</body>
			</html>`
		w.Write([]byte(html))
	})

	// Create Rod browser (headless for CI)
	headless := true
	browser := testutil.NewRodBrowser(t, testutil.WithHeadless(headless))

	// Navigate to the test page
	page := browser.MustPage(httpServer.URL).WaitForLoad()

	// Wait for WebSocket connection
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		statusText, err := page.MustElement("#status").Text()
		if err == nil && statusText == "Connected" {
			t.Logf("Browser connected after %d attempts", i+1)
			break
		}
	}

	// Wait for browser client to connect and get ID from the broker
	var browserClientID string
	var browserHandle broker.ClientHandle
	for i := 0; i < 30; i++ {
		bs.IterateClients(func(ch broker.ClientHandle) bool {
			if ch.ClientType() == "browser" {
				browserHandle = ch
				browserClientID = ch.ID()
				return false
			}
			return true
		})
		if browserHandle != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotNil(t, browserHandle, "Browser client should be connected")
	require.NotEmpty(t, browserClientID, "Browser client ID should not be empty")
	t.Logf("Browser client ID from broker: %s", browserClientID)

	// Create a control client
	controlClient := testutil.NewTestClient(t, bs.WSURL,
		client.WithClientName("Control Client"),
		client.WithClientType("cli"),
	)

	// Wait for control client to be connected
	var controlHandle broker.ClientHandle
	for i := 0; i < 30; i++ {
		bs.IterateClients(func(ch broker.ClientHandle) bool {
			if ch.ID() == controlClient.ID() {
				controlHandle = ch
				return false
			}
			return true
		})
		if controlHandle != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotNil(t, controlHandle, "Control client should be connected")

	// Test Navigate command
	t.Run("Navigate", func(t *testing.T) {
		navigateReq := map[string]string{"url": "https://example.com"}
		resp, err := sendProxyCommand(t, controlClient, browserClientID, "browser.navigate", navigateReq)
		require.NoError(t, err, "Navigate command should succeed")
		assert.True(t, resp["success"].(bool), "Navigate should succeed")

		// Verify the URL was updated in the browser
		time.Sleep(100 * time.Millisecond)
		currentURL, err := page.MustElement("#current-url").Text()
		require.NoError(t, err)
		assert.Equal(t, "https://example.com", currentURL, "URL should be updated")
	})

	// Test Alert command
	t.Run("Alert", func(t *testing.T) {
		alertReq := map[string]string{"message": "Test alert message"}
		resp, err := sendProxyCommand(t, controlClient, browserClientID, "browser.alert", alertReq)
		require.NoError(t, err, "Alert command should succeed")
		assert.True(t, resp["success"].(bool), "Alert should succeed")

		// Verify the alert was shown in the browser
		time.Sleep(100 * time.Millisecond)
		lastAlert, err := page.MustElement("#last-alert").Text()
		require.NoError(t, err)
		assert.Equal(t, "Test alert message", lastAlert, "Alert message should be displayed")
	})

	// Test Execute JavaScript command
	t.Run("Execute", func(t *testing.T) {
		execReq := map[string]string{"code": "2 + 2"}
		resp, err := sendProxyCommand(t, controlClient, browserClientID, "browser.exec", execReq)
		require.NoError(t, err, "Execute command should succeed")
		assert.True(t, resp["success"].(bool), "Execute should succeed")
		assert.Equal(t, "4", resp["result"], "Should get correct result")
	})

	// Test Info command
	t.Run("Info", func(t *testing.T) {
		infoReq := map[string]string{}
		resp, err := sendProxyCommand(t, controlClient, browserClientID, "browser.info", infoReq)
		require.NoError(t, err, "Info command should succeed")

		// Verify response has expected fields
		assert.NotEmpty(t, resp["userAgent"], "Should have user agent")
		assert.NotEmpty(t, resp["url"], "Should have URL")
		assert.NotEmpty(t, resp["title"], "Should have title")
		viewport := resp["viewport"].(map[string]interface{})
		assert.Greater(t, viewport["width"], float64(0), "Should have viewport width")
		assert.Greater(t, viewport["height"], float64(0), "Should have viewport height")
	})

	// Test Fill Form command
	t.Run("FillForm", func(t *testing.T) {
		fillReq := map[string]interface{}{
			"fields": map[string]string{
				"name":  "John Doe",
				"email": "john@example.com",
			},
		}
		resp, err := sendProxyCommand(t, controlClient, browserClientID, "browser.fillForm", fillReq)
		require.NoError(t, err, "Fill form command should succeed")
		assert.True(t, resp["success"].(bool), "Fill form should succeed")
		filled := resp["filled"].([]interface{})
		assert.Len(t, filled, 2, "Should fill 2 fields")

		// Verify form fields were filled
		time.Sleep(100 * time.Millisecond)
		nameValue, err := page.MustElement("#name").Property("value")
		require.NoError(t, err)
		assert.Equal(t, "John Doe", nameValue.String(), "Name field should be filled")
		emailValue, err := page.MustElement("#email").Property("value")
		require.NoError(t, err)
		assert.Equal(t, "john@example.com", emailValue.String(), "Email field should be filled")
	})

	// Test Click command
	t.Run("Click", func(t *testing.T) {
		clickReq := map[string]string{"selector": "#test-button"}
		resp, err := sendProxyCommand(t, controlClient, browserClientID, "browser.click", clickReq)
		require.NoError(t, err, "Click command should succeed")
		assert.True(t, resp["success"].(bool), "Click should succeed")

		// Verify the element was clicked
		time.Sleep(100 * time.Millisecond)
		clickedAttr, err := page.MustElement("#test-button").Attribute("data-clicked")
		require.NoError(t, err)
		assert.Equal(t, "true", *clickedAttr, "Button should be marked as clicked")
	})

	// Test error handling - invalid target ID
	t.Run("InvalidTargetID", func(t *testing.T) {
		req := map[string]string{"url": "https://example.com"}
		_, err := sendProxyCommand(t, controlClient, "invalid-client-id", "browser.navigate", req)
		assert.Error(t, err, "Should get error for invalid client ID")
	})

	// Test error handling - JavaScript execution error
	t.Run("ExecutionError", func(t *testing.T) {
		execReq := map[string]string{"code": "throw new Error('Test error')"}
		resp, err := sendProxyCommand(t, controlClient, browserClientID, "browser.exec", execReq)
		require.NoError(t, err, "Command should complete without network error")
		assert.False(t, resp["success"].(bool), "Execution should fail")
		assert.Contains(t, resp["error"], "Test error", "Should return error message")
	})

	// Test error handling - nonexistent element
	t.Run("ElementNotFound", func(t *testing.T) {
		clickReq := map[string]string{"selector": "#nonexistent"}
		resp, err := sendProxyCommand(t, controlClient, browserClientID, "browser.click", clickReq)
		require.NoError(t, err, "Command should complete without network error")
		assert.False(t, resp["success"].(bool), "Click should fail")
		assert.Contains(t, resp["error"], "Element not found", "Should return error message")
	})

	// Verify command history was recorded
	result, err := page.Eval("() => window.commandHistory")
	require.NoError(t, err, "Should get command history")
	var history []map[string]interface{}
	err = result.Value.Unmarshal(&history)
	require.NoError(t, err, "Should unmarshal command history")
	assert.GreaterOrEqual(t, len(history), 6, "Should have at least 6 commands in history")

	// Get console logs for debugging
	logs := page.GetConsoleLog()
	t.Logf("Browser console logs: %v", logs)

	// Optional: Show browser for debugging (if not headless)
	if !browser.IsHeadless() {
		t.Log("Waiting 5 seconds so you can see the browser window...")
		time.Sleep(5 * time.Second)
	}
}

// sendProxyCommand sends a proxy command from control client to browser client
func sendProxyCommand(t *testing.T, controlClient *client.Client, targetID, topic string, payload interface{}) (map[string]interface{}, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %v", err)
	}

	proxyReq := shared_types.ProxyRequest{
		TargetID: targetID,
		Topic:    topic,
		Payload:  payloadBytes,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rawResp, errPayload, err := controlClient.SendServerRequest(ctx, shared_types.TopicProxyRequest, proxyReq)
	if err != nil {
		return nil, err
	}
	if errPayload != nil {
		return nil, fmt.Errorf("error payload: %v", errPayload)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(*rawResp, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	return resp, nil
}

// TestBrowserProxyControlAdvanced tests more advanced proxy control scenarios
func TestBrowserProxyControlAdvanced(t *testing.T) {
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

	// Create a test page with more complex scenarios
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<!DOCTYPE html>
			<html>
			<head>
				<title>Advanced Browser Proxy Control Test</title>
				<script src="/websocketmq.js"></script>
				<script>
					window.commandLog = [];
					
					// Track command execution timing
					async function timedHandler(name, handler) {
						return async (req) => {
							const start = Date.now();
							window.commandLog.push({command: name, start: start});
							try {
								const result = await handler(req);
								const duration = Date.now() - start;
								window.commandLog[window.commandLog.length - 1].duration = duration;
								window.commandLog[window.commandLog.length - 1].success = true;
								return result;
							} catch (error) {
								const duration = Date.now() - start;
								window.commandLog[window.commandLog.length - 1].duration = duration;
								window.commandLog[window.commandLog.length - 1].success = false;
								window.commandLog[window.commandLog.length - 1].error = error.message;
								throw error;
							}
						};
					}
					
					window.addEventListener('DOMContentLoaded', function() {
						try {
							window.client = new WebSocketMQ.Client({});
							console.log('Advanced client created');
							
							window.client.onConnect(() => {
								console.log('Connected to WebSocket server');
								document.getElementById('status').textContent = 'Connected';
								
								// Register handlers with timing
								window.client.handleServerRequest('browser.slowOperation', timedHandler('slowOperation', async (req) => {
									const delay = req.delay || 1000;
									await new Promise(resolve => setTimeout(resolve, delay));
									return { success: true, delayed: delay };
								}));
								
								window.client.handleServerRequest('browser.multiStep', timedHandler('multiStep', async (req) => {
									const steps = req.steps || 3;
									const results = [];
									for (let i = 0; i < steps; i++) {
										await new Promise(resolve => setTimeout(resolve, 100));
										results.push('Step ' + (i + 1) + ' completed');
									}
									return { success: true, results: results };
								}));
								
								window.client.handleServerRequest('browser.errorTest', timedHandler('errorTest', async (req) => {
									if (req.shouldError) {
										throw new Error(req.errorMessage || 'Test error');
									}
									return { success: true };
								}));
							});
							
							window.client.connect();
						} catch (err) {
							console.error('Error setting up advanced client:', err);
						}
					});
				</script>
			</head>
			<body>
				<h1>Advanced Browser Proxy Control Test</h1>
				<div>
					<h2>Status: <span id="status">Disconnected</span></h2>
				</div>
			</body>
			</html>`
		w.Write([]byte(html))
	})

	// Create browser and navigate
	browser := testutil.NewRodBrowser(t, testutil.WithHeadless(true))
	page := browser.MustPage(httpServer.URL).WaitForLoad()

	// Wait for connection
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		statusText, err := page.MustElement("#status").Text()
		if err == nil && statusText == "Connected" {
			break
		}
	}

	// Wait for browser client to connect and get ID from the broker
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
	
	// Give additional time for browser to complete handler registration after connection
	time.Sleep(500 * time.Millisecond)

	// Create control client
	controlClient := testutil.NewTestClient(t, bs.WSURL)
	require.NotNil(t, controlClient, "Control client should be created")

	// Wait for control client to be connected
	var controlClientID string
	for i := 0; i < 30; i++ {
		if controlClient == nil {
			t.Fatal("Control client is nil")
		}
		bs.IterateClients(func(ch broker.ClientHandle) bool {
			if ch.ID() == controlClient.ID() {
				controlClientID = ch.ID()
				return false
			}
			return true
		})
		if controlClientID != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotEmpty(t, controlClientID, "Should find control client")

	// Test timeout handling
	t.Run("Timeout", func(t *testing.T) {
		require.NotNil(t, controlClient, "Control client should not be nil in sub-test")
		req := map[string]interface{}{"delay": 6000} // 6 second delay
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		_, _, err := controlClient.SendServerRequest(ctx, shared_types.TopicProxyRequest, shared_types.ProxyRequest{
			TargetID: browserClientID,
			Topic:    "browser.slowOperation",
			Payload:  mustMarshal(req),
		})
		assert.Error(t, err, "Should timeout")
		if err != nil {
			assert.True(t, strings.Contains(err.Error(), "context") || strings.Contains(err.Error(), "timeout"),
				"Error should be timeout related")
		}
	})

	// Test successful slow operation
	t.Run("SlowOperation", func(t *testing.T) {
		req := map[string]interface{}{"delay": 500} // 500ms delay
		resp, err := sendProxyCommand(t, controlClient, browserClientID, "browser.slowOperation", req)
		require.NoError(t, err, "Slow operation should succeed")
		
		// Debug output
		if resp == nil {
			t.Fatal("Response is nil")
		}
		
		// Check if success field exists
		successVal, hasSuccess := resp["success"]
		if !hasSuccess {
			t.Fatalf("Response missing 'success' field. Got: %#v", resp)
		}
		
		// Type assert with check
		successBool, ok := successVal.(bool)
		if !ok {
			t.Fatalf("success field is not bool, got type %T with value %v", successVal, successVal)
		}
		
		assert.True(t, successBool, "Should succeed")
		assert.Equal(t, float64(500), resp["delayed"], "Should return delay value")
	})

	// Test multi-step operation
	t.Run("MultiStep", func(t *testing.T) {
		req := map[string]interface{}{"steps": 5}
		resp, err := sendProxyCommand(t, controlClient, browserClientID, "browser.multiStep", req)
		require.NoError(t, err, "Multi-step operation should succeed")
		
		// Check response validity
		if resp == nil {
			t.Fatal("Response is nil")
		}
		
		// Check success field
		successVal, hasSuccess := resp["success"]
		if !hasSuccess {
			t.Fatalf("Response missing 'success' field. Got: %#v", resp)
		}
		
		successBool, ok := successVal.(bool)
		if !ok {
			t.Fatalf("success field is not bool, got type %T with value %v", successVal, successVal)
		}
		assert.True(t, successBool, "Should succeed")
		
		// Check results field
		resultsVal, hasResults := resp["results"]
		if !hasResults {
			t.Fatalf("Response missing 'results' field. Got: %#v", resp)
		}
		
		results, ok := resultsVal.([]interface{})
		if !ok {
			t.Fatalf("results field is not array, got type %T with value %v", resultsVal, resultsVal)
		}
		assert.Len(t, results, 5, "Should have 5 results")
	})

	// Test error propagation
	t.Run("ErrorPropagation", func(t *testing.T) {
		req := map[string]interface{}{
			"shouldError":  true,
			"errorMessage": "Custom error message",
		}
		resp, err := sendProxyCommand(t, controlClient, browserClientID, "browser.errorTest", req)
		require.NoError(t, err, "Network operation should succeed")
		assert.NotNil(t, resp["error"], "Should have error in response")
		assert.Contains(t, resp["error"], "Custom error message", "Should contain custom error message")
	})

	// Get command log to verify timing
	result, err := page.Eval("() => window.commandLog")
	require.NoError(t, err, "Should get command log")
	var commandLog []map[string]interface{}
	err = result.Value.Unmarshal(&commandLog)
	require.NoError(t, err, "Should unmarshal command log")

	// Verify command execution timing
	for _, cmd := range commandLog {
		t.Logf("Command: %s, Duration: %.0fms, Success: %v",
			cmd["command"], cmd["duration"], cmd["success"])
		if cmd["error"] != nil {
			t.Logf("  Error: %s", cmd["error"])
		}
	}

	assert.GreaterOrEqual(t, len(commandLog), 3, "Should have executed at least 3 commands")
}

func mustMarshal(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}