// pkg/broker/ps/js_client_test.go
package ps_test // Use the same package as integration_test to reuse TestServer/TestClient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest" // Use httptest for serving test files
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps/testutil"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"github.com/lightforgemedia/go-websocketmq/pkg/server" // For server.DefaultHandlerOptions
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	jsTestdataRoot string
	jsTestOnce     sync.Once
)

func setupJSTestdataRoot() {
	_, filename, _, _ := runtime.Caller(0)
	// Assumes js_client_test.go is in pkg/broker/ps/
	// testdata is ../../../testdata/
	// assets are ../../../assets/
	jsTestdataRoot = filepath.Join(filepath.Dir(filename), "..", "..", "..") // Project root
}

func getJSTestdataRoot() string {
	jsTestOnce.Do(setupJSTestdataRoot)
	return jsTestdataRoot
}

// serveTestFiles serves files from the project's testdata and assets directories.
func serveTestFiles(t *testing.T, w http.ResponseWriter, r *http.Request) {
	projectRoot := getJSTestdataRoot()
	path := r.URL.Path
	t.Logf("File server request for: %s", path)

	var servePath string
	if strings.HasPrefix(path, "/wsmq/") { // For websocketmq.js from assets
		// This matches how cmd/rpcserver/main.go serves it via assets.ScriptHandler
		// We need to serve assets/dist/websocketmq.js when /wsmq/websocketmq.js is requested.
		assetName := strings.TrimPrefix(path, "/wsmq/")
		servePath = filepath.Join(projectRoot, "assets", "dist", assetName)
	} else if strings.HasSuffix(path, ".js") && !strings.Contains(path, "testdata") {
		// General JS files might be from static mocks
		servePath = filepath.Join(projectRoot, "cmd", "rpcserver", "static", strings.TrimPrefix(path, "/"))
	} else { // HTML, CSS from testdata or cmd/rpcserver/static
		// Prioritize testdata for test-specific HTML like simple_test.html
		testdataFilePath := filepath.Join(projectRoot, "testdata", strings.TrimPrefix(path, "/"))
		if _, err := os.Stat(testdataFilePath); err == nil {
			servePath = testdataFilePath
		} else {
			// Fallback to cmd/rpcserver/static for general static assets like style.css
			servePath = filepath.Join(projectRoot, "cmd", "rpcserver", "static", strings.TrimPrefix(path, "/"))
		}
	}

	t.Logf("Attempting to serve file: %s", servePath)
	http.ServeFile(w, r, servePath)
}

// TestJSClient_SimpleConnectivity uses simple_test.html and the refined websocketmq.js
func TestJSClient_SimpleConnectivity(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping browser test in short mode.")
	}

	handlerOpts := server.DefaultHandlerOptions() // Use defaults that JS client expects
	server := NewTestServer(t, handlerOpts)
	defer server.Close()
	<-server.Ready
	t.Logf("Test server for JS SimpleConnectivity running at: %s", server.Server.URL)

	// This channel will receive the BrokerClientID when the server processes the registration event
	// published by server.Handler upon client's _client.register message.
	clientRegisteredOnServer := make(chan string, 1)
	err := server.Broker.Subscribe(context.Background(), broker.TopicClientRegistered, func(ctx context.Context, msg *model.Message, _ string) (*model.Message, error) {
		t.Logf("Server: Event on %s received: %+v", broker.TopicClientRegistered, msg.Body)
		bodyMap, ok := msg.Body.(map[string]interface{}) // ps.PubSubBroker publishes map[string]string, json unmarshals to this
		if !ok {
			t.Errorf("Server: Bad body type for %s: %T", broker.TopicClientRegistered, msg.Body)
			return nil, fmt.Errorf("bad body type for %s", broker.TopicClientRegistered)
		}
		clientID, ok := bodyMap["brokerClientID"].(string)
		if !ok {
			t.Errorf("Server: Missing brokerClientID in %s event: %+v", broker.TopicClientRegistered, bodyMap)
			return nil, fmt.Errorf("missing brokerClientID in %s event", broker.TopicClientRegistered)
		}
		clientRegisteredOnServer <- clientID
		return nil, nil
	})
	require.NoError(t, err, "Failed to subscribe to TopicClientRegistered on server broker")

	httpTestServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveTestFiles(t, w, r)
	}))
	defer httpTestServer.Close()
	t.Logf("File server for JS test assets running at: %s", httpTestServer.URL)

	l := launcher.New().Headless(true).MustLaunch()
	browser := rod.New().ControlURL(l).MustConnect()
	defer browser.MustClose()

	wsURLForJS := strings.Replace(server.Server.URL, "http://", "ws://", 1) + "/ws"
	// simple_test.html expects websocketmq.js to be at /wsmq/websocketmq.js
	// but our file server serves it from /assets/dist/websocketmq.js if requested as /websocketmq.js
	// For this test, simple_test.html is modified to load from `/wsmq/websocketmq.js`
	// The file server needs to handle this path correctly.
	pageURL := fmt.Sprintf("%s/simple_test.html?ws=%s", httpTestServer.URL, wsURLForJS)
	t.Logf("Navigating browser to: %s", pageURL)

	page := browser.MustPage(pageURL).MustWaitLoad()
	defer page.MustClose()

	var receivedBrokerIDFromServer string
	select {
	case receivedBrokerIDFromServer = <-clientRegisteredOnServer:
		t.Logf("Server confirmed JS client registration via broker event, assigned BrokerClientID: %s", receivedBrokerIDFromServer)
		require.NotEmpty(t, receivedBrokerIDFromServer)
	case <-time.After(20 * time.Second): // Increased timeout for browser init + registration
		logBrowserConsole(t, page)
		t.Fatalf("Timeout waiting for JS client registration confirmation from server (via %s event)", broker.TopicClientRegistered)
	}

	// Verify status on the page (simple_test.html should update #status and #client-id)
	// Use a simple polling approach instead of EventuallyWithT
	statusEl := page.MustElement("#status")
	deadline := time.Now().Add(15 * time.Second)
	var statusText string
	var textErr error
	for time.Now().Before(deadline) {
		statusText, textErr = statusEl.Text()
		if textErr == nil && statusText == "Connected" {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	require.NoError(t, textErr, "Error getting status text")
	assert.Equal(t, "Connected", statusText, "JS client status on page did not become 'Connected'")

	clientIDOnPageEl := page.MustElement("#client-id")
	deadline = time.Now().Add(15 * time.Second)
	var idText string
	for time.Now().Before(deadline) {
		idText, textErr = clientIDOnPageEl.Text()
		if textErr == nil && idText == receivedBrokerIDFromServer {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	require.NoError(t, textErr, "Error getting client ID text")
	assert.Equal(t, receivedBrokerIDFromServer, idText, "BrokerClientID on page should match server-assigned ID from ACK")

	t.Log("JS Client simple connectivity and registration ACK verified.")

	t.Cleanup(func() {
		if t.Failed() {
			logBrowserConsole(t, page)
			path := filepath.Join(getJSTestdataRoot(), "testdata", "simple_connectivity_failure.png")
			_ = page.MustScreenshot(path)
			t.Logf("Screenshot saved to %s", path)
		}
	})
}

// TestJSClient_FullRPCSuite uses cmd/rpcserver/static/index.html and assets/dist/websocketmq.js
// This test simulates the full application flow.
func TestJSClient_FullRPCSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping full browser RPC suite in short mode.")
	}

	// Setup server similar to cmd/rpcserver/main.go
	logger := testutil.NewTestLogger(t)
	brokerOpts := broker.DefaultOptions()
	brokerInstance := ps.New(logger, brokerOpts)

	sessionManager := NewMockSessionManager(logger) // Using a mock session manager for this test

	wsHandlerOpts := server.DefaultHandlerOptions()
	wsHandlerOpts.ClientRegisterTopic = "_client.register"
	wsHandlerOpts.ClientRegisteredAckTopic = broker.TopicClientRegistered // JS client listens on this for ACK
	wsHandler := server.NewHandler(brokerInstance, logger, wsHandlerOpts)

	// Mock API handler setup (not strictly needed if we trigger RPCs directly, but good for context)
	// For this test, we'll mostly interact via server.Broker.RequestToClient or listen to client-sent RPCs.

	// Subscribe to server-side topics the JS client will call
	err := brokerInstance.Subscribe(context.Background(), "server.ping", func(ctx context.Context, msg *model.Message, sourceBrokerID string) (*model.Message, error) {
		t.Logf("Server: 'server.ping' handler received from BrokerClientID %s, Body: %+v", sourceBrokerID, msg.Body)
		return model.NewResponse(msg, map[string]interface{}{"reply": "pong from Go server.ping", "client_payload": msg.Body}), nil
	})
	require.NoError(t, err)

	// HTTP server setup
	mux := http.NewServeMux()
	mux.Handle("/ws", wsHandler)
	// Serve static files (index.html, browser_automation_mock.js, style.css, and websocketmq.js)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		serveTestFiles(t, w, r) // Use the common file server
	})

	testHTTPServer := httptest.NewServer(mux)
	defer testHTTPServer.Close()
	t.Logf("Test server for Full RPC Suite running at: %s", testHTTPServer.URL)

	// Setup Rod browser
	l := launcher.New().Headless(true).Delete("disable-gpu").MustLaunch() // Headless for CI
	browser := rod.New().ControlURL(l).MustConnect()
	defer browser.MustClose()

	wsURLForJS := strings.Replace(testHTTPServer.URL, "http://", "ws://", 1) + "/ws"
	// index.html is served from cmd/rpcserver/static by our file server
	// It loads /wsmq/websocketmq.js and /browser_automation_mock.js
	pageURL := fmt.Sprintf("%s/index.html?ws=%s", testHTTPServer.URL, wsURLForJS) // index.html is in cmd/rpcserver/static
	t.Logf("Navigating browser to: %s", pageURL)

	page := browser.MustPage(pageURL).MustWaitLoad()
	defer page.MustClose()

	// --- Wait for client to connect and register ---
	var jsPageSessionID, jsBrokerClientID string

	// Use a simple polling approach instead of EventuallyWithT
	deadline := time.Now().Add(25 * time.Second)
	var pageID, brokerID string
	var pageErr, brokerErr error
	var success bool

	for time.Now().Before(deadline) && !success {
		pageID, pageErr = page.MustElement("#page-session-id").Text()
		if pageErr != nil || pageID == "" {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		brokerID, brokerErr = page.MustElement("#broker-client-id").Text()
		if brokerErr != nil || brokerID == "" || brokerID == "N/A" {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// Both values are valid
		jsPageSessionID = pageID
		jsBrokerClientID = brokerID
		success = true
	}

	require.True(t, success, "JS client did not display PageSessionID and BrokerClientID within timeout")
	require.NotEmpty(t, jsPageSessionID, "PageSessionID not displayed on page")
	require.NotEmpty(t, jsBrokerClientID, "BrokerClientID not displayed on page")
	require.NotEqual(t, "N/A", jsBrokerClientID, "BrokerClientID is N/A, registration ACK not received/processed by JS")

	// Update mock session manager with this mapping
	sessionManager.Store(jsPageSessionID, jsBrokerClientID)
	t.Logf("JS Client on index.html connected: PageSessionID=%s, BrokerClientID=%s", jsPageSessionID, jsBrokerClientID)

	// --- Test Scenarios ---

	// 1. Client-Initiated RPC to Server (server.ping)
	t.Run("JSClientToServer_Ping", func(t *testing.T) {
		page.MustElement("#client-ping-server").MustClick()
		pingResponseEl := page.MustElement("#client-ping-response")

		// Use a simple polling approach instead of EventuallyWithT
		deadline := time.Now().Add(10 * time.Second)
		var respText string
		var respErr error
		var success bool

		for time.Now().Before(deadline) && !success {
			respText, respErr = pingResponseEl.Text()
			if respErr != nil {
				time.Sleep(200 * time.Millisecond)
				continue
			}

			if strings.Contains(respText, "pong from server handler") &&
				strings.Contains(respText, "ping from client") {
				success = true
				break
			}

			time.Sleep(200 * time.Millisecond)
		}

		require.NoError(t, respErr, "Error getting ping response text")
		require.True(t, success, "Client ping response not updated in UI within timeout")
		assert.Contains(t, respText, "pong from server handler", "Ping response mismatch")
		assert.Contains(t, respText, "ping from client", "Ping response did not echo client data")

		t.Log("JSClientToServer_Ping: Passed")
	})

	// 2. Server-Initiated RPC to JS Client (e.g., browser.click, simulated via API call path)
	//    For this, we'll directly use brokerInstance.RequestToClient, simulating what api.Handler would do.
	t.Run("ServerToJSClient_BrowserClick", func(t *testing.T) {
		actionTopic := "browser.click"
		actionParams := map[string]interface{}{"selector": "#test-button-1"}

		rpcCtx, rpcCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer rpcCancel()

		respMsg, err := brokerInstance.RequestToClient(rpcCtx, jsBrokerClientID, model.NewRequest(actionTopic, actionParams, 5000), 5000)
		require.NoError(t, err, "RequestToClient for browser.click failed")
		require.NotNil(t, respMsg, "Response from browser.click was nil")
		assert.Equal(t, model.KindResponse, respMsg.Header.Type)

		respData, ok := respMsg.Body.(map[string]interface{})
		require.True(t, ok, "browser.click response body not a map")
		assert.True(t, respData["success"].(bool), "browser.click mock did not return success:true")
		assert.Contains(t, respData["message"].(string), "#test-button-1", "browser.click response message mismatch")

		// Check UI log on page for this action
		actionLogEl := page.MustElement("#action-log")

		// Use a simple polling approach instead of EventuallyWithT
		deadline := time.Now().Add(5 * time.Second)
		var logHTML string
		var logErr error
		var success bool

		for time.Now().Before(deadline) && !success {
			logHTML, logErr = actionLogEl.HTML()
			if logErr != nil {
				time.Sleep(200 * time.Millisecond)
				continue
			}

			if strings.Contains(logHTML, "Received RPC: browser.click") &&
				strings.Contains(logHTML, "#test-button-1") {
				success = true
				break
			}

			time.Sleep(200 * time.Millisecond)
		}

		require.NoError(t, logErr, "Error getting action log HTML")
		require.True(t, success, "Action log for browser.click not updated within timeout")
		assert.Contains(t, logHTML, "Received RPC: browser.click", "browser.click not logged in action log")
		assert.Contains(t, logHTML, "#test-button-1", "selector not logged for browser.click")

		t.Log("ServerToJSClient_BrowserClick: Passed")
	})

	// 3. Server-Initiated RPC to JS Client - Handler throws error
	t.Run("ServerToJSClient_HandlerError", func(t *testing.T) {
		// For simplicity, let's assume a hypothetical "browser.forceError" action.
		// We'll register a temporary handler in JS via Evaluate to simulate this.
		page.MustEvaluate(rod.Eval(`
			client.subscribe("browser.forceError", async (params) => {
				clientLogger.info("JS handler for browser.forceError called with:", params);
				throw new Error("Simulated JS handler error for browser.forceError");
			});
			clientLogger.info("Temporary JS handler for browser.forceError registered.");
		`))
		time.Sleep(200 * time.Millisecond) // Give time for eval to register

		rpcCtx, rpcCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer rpcCancel()

		respMsg, err := brokerInstance.RequestToClient(rpcCtx, jsBrokerClientID, model.NewRequest("browser.forceError", nil, 5000), 5000)
		require.NoError(t, err, "RequestToClient for browser.forceError should not error itself")
		require.NotNil(t, respMsg, "Response from browser.forceError was nil")
		assert.Equal(t, model.KindError, respMsg.Header.Type, "Expected KindError response")

		errData, ok := respMsg.Body.(map[string]interface{})
		require.True(t, ok, "Error response body not a map")
		assert.Contains(t, errData["error"].(string), "Simulated JS handler error", "Error message mismatch")
		t.Log("ServerToJSClient_HandlerError: Passed")
	})

	// 4. Server-Initiated RPC to JS Client - Timeout (JS handler doesn't respond)
	t.Run("ServerToJSClient_Timeout", func(t *testing.T) {
		page.MustEvaluate(rod.Eval(`
			client.subscribe("browser.forceTimeout", async (params) => {
				clientLogger.info("JS handler for browser.forceTimeout called. Will not respond.");
				// No return, no throw, just hang.
				return new Promise(() => {}); // Promise that never resolves
			});
			clientLogger.info("Temporary JS handler for browser.forceTimeout registered.");
		`))
		time.Sleep(200 * time.Millisecond)

		rpcCtx, rpcCancel := context.WithTimeout(context.Background(), 3*time.Second) // Overall test timeout
		defer rpcCancel()

		// RPC call with a shorter timeout for the broker.RequestToClient itself
		_, err := brokerInstance.RequestToClient(rpcCtx, jsBrokerClientID, model.NewRequest("browser.forceTimeout", nil, 1500), 1500) // 1.5s timeout
		require.Error(t, err, "Expected RequestToClient to timeout")
		assert.True(t, errors.Is(err, broker.ErrRequestTimeout) || errors.Is(err, context.DeadlineExceeded), "Expected ErrRequestTimeout or context.DeadlineExceeded, got: %v", err)
		t.Log("ServerToJSClient_Timeout: Passed")
	})

	t.Log("JS Client Full RPC Suite appears to have passed relevant scenarios.")
	t.Cleanup(func() {
		if t.Failed() {
			logBrowserConsole(t, page)
			path := filepath.Join(getJSTestdataRoot(), "testdata", "js_full_rpc_suite_failure.png")
			_ = page.MustScreenshot(path)
			t.Logf("Screenshot saved to %s", path)
		}
		// Clean up broker and session manager if they were specifically created for this test block
		// For now, server.Close() handles brokerInstance.Close()
	})
}

// Helper to log browser console output for debugging failed tests
func logBrowserConsole(t *testing.T, page *rod.Page) {
	t.Helper()
	t.Logf("--- Browser Console Logs for URL: %s ---", page.MustInfo().URL)
	entries, err := page.Eval("() => { const logs = []; window.console.history = window.console.history || []; window.console.history.forEach(l => logs.push(l)); return logs; }")
	if err != nil {
		t.Logf("Error getting console logs: %v", err)
		// Fallback or try another method if console.history is not populated as expected
		// This part might need adjustment based on how `clientLogger` in index.html actually stores logs.
		// For now, assume it might populate a global array or use a more direct Rod method if available.
		return
	}

	if entries.Value.Str() == "null" || entries.Value.Str() == "" {
		t.Log("(No console logs captured via console.history or array is empty)")
		// Rod doesn't have a direct MustOnConsole method like page.MustOnConsole
		// Instead, we'll use a simpler approach to get console logs

		// Try to get console logs via JavaScript evaluation
		consoleLogsJS, err := page.Eval(`() => {
			if (window.console && window.console.logs) {
				return window.console.logs;
			}
			return [];
		}`)

		if err == nil {
			t.Logf("Console logs from JavaScript: %v", consoleLogsJS.Value)
		} else {
			t.Log("Could not retrieve console logs via JavaScript")
		}

		// Trigger a console log for debugging
		page.MustEvaluate(rod.Eval(`() => console.log("--- Go test requesting browser logs ---")`))

		t.Log("(No console logs captured via DevTools Protocol listener)")
		return
	}

	var logs []map[string]interface{}
	if err := json.Unmarshal([]byte(entries.Value.Str()), &logs); err == nil {
		for _, logEntry := range logs {
			t.Logf("Browser Log: [%s] %v", logEntry["type"], logEntry["args"])
		}
	} else {
		t.Logf("Could not parse console.history: %s", entries.Value.Str())
	}
	t.Logf("--- End Browser Console Logs ---")
}

// MockSessionManager for JS tests that don't need full session manager logic
type MockSessionManager struct {
	logger       broker.Logger
	mu           sync.RWMutex
	pageToBroker map[string]string
	brokerToPage map[string]string
}

func NewMockSessionManager(logger broker.Logger) *MockSessionManager {
	return &MockSessionManager{
		logger:       logger,
		pageToBroker: make(map[string]string),
		brokerToPage: make(map[string]string),
	}
}
func (m *MockSessionManager) Store(pageID, brokerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pageToBroker[pageID] = brokerID
	m.brokerToPage[brokerID] = pageID
	m.logger.Debug("MockSessionManager: Stored %s -> %s", pageID, brokerID)
}
func (m *MockSessionManager) GetBrokerClientID(pageSessionID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, found := m.pageToBroker[pageSessionID]
	m.logger.Debug("MockSessionManager: GetBrokerClientID for %s -> (%s, %v)", pageSessionID, id, found)
	return id, found
}
func (m *MockSessionManager) GetPageSessionID(brokerClientID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, found := m.brokerToPage[brokerClientID]
	return id, found
}

// Add other methods if needed by API handler, or ensure API handler tests mock session manager directly.
