// pkg/broker/ps/js_client_simple_test.go
//go:build js_client
// +build js_client

// This file contains tests for the JavaScript client simple connectivity.
// Run with: go test -tags=js_client

package ps_test

import (
	"context"
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
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"github.com/lightforgemedia/go-websocketmq/pkg/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJSClient_SimpleConnectivity uses simple_test.html and the refined websocketmq.js
func TestJSClient_SimpleConnectivity(t *testing.T) {
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
