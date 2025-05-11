// pkg/broker/ps/js_client_rpc_test.go
//go:build js_client
// +build js_client

// This file contains tests for the JavaScript client RPC functionality.
// Run with: go test -tags=js_client

package ps_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps/testutil"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"github.com/lightforgemedia/go-websocketmq/pkg/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJSClient_FullRPCSuite uses cmd/rpcserver/static/index.html and assets/dist/websocketmq.js
// This test simulates the full application flow.
func TestJSClient_FullRPCSuite(t *testing.T) {
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
