// pkg/broker/ps/js_client_minimal_test.go
//go:build js_client
// +build js_client

// This file contains a minimal test for JavaScript client connectivity.
// Run with: go test -tags=js_client -run TestJSClient_MinimalConnectivity

package ps_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"github.com/lightforgemedia/go-websocketmq/pkg/server"
	"github.com/stretchr/testify/require"
)

// TestJSClient_MinimalConnectivity is the simplest possible test to verify
// that we can load a page and establish a connection from client to server.
func TestJSClient_MinimalConnectivity(t *testing.T) {
	// Create a test server
	handlerOpts := server.DefaultHandlerOptions()
	server := NewTestServer(t, handlerOpts)
	defer server.Close()
	<-server.Ready
	t.Logf("Test server running at: %s", server.Server.URL)

	// Create a channel to receive client registration events
	clientRegistered := make(chan string, 1)

	// Subscribe to client registration events
	err := server.Broker.Subscribe(context.Background(), broker.TopicClientRegistered,
		func(ctx context.Context, msg *model.Message, _ string) (*model.Message, error) {
			t.Logf("Received client registration event: %+v", msg.Body)

			// Extract the broker client ID from the message
			bodyMap, ok := msg.Body.(map[string]interface{})
			if !ok {
				t.Logf("Unexpected body type: %T", msg.Body)
				return nil, nil
			}

			clientID, ok := bodyMap["brokerClientID"].(string)
			if !ok {
				t.Logf("Missing brokerClientID in event")
				return nil, nil
			}

			// Send the client ID to the channel
			clientRegistered <- clientID
			return nil, nil
		})
	require.NoError(t, err, "Failed to subscribe to client registration events")

	// Create a file server to serve the test HTML file
	fileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("File server request: %s", r.URL.Path)
		serveTestFiles(t, w, r)
	}))
	defer fileServer.Close()
	t.Logf("File server running at: %s", fileServer.URL)

	// Launch a browser
	l := launcher.New().Headless(true).MustLaunch()
	browser := rod.New().ControlURL(l).MustConnect()
	defer browser.MustClose()

	// Create the WebSocket URL for the client
	wsURL := strings.Replace(server.Server.URL, "http://", "ws://", 1) + "/ws"

	// Create the page URL with the WebSocket URL as a query parameter
	pageURL := fmt.Sprintf("%s/simple_test.html?ws=%s", fileServer.URL, wsURL)
	t.Logf("Loading page: %s", pageURL)

	// Load the page
	page := browser.MustPage(pageURL).MustWaitLoad()
	defer page.MustClose()
	t.Logf("Page loaded")

	// Wait for the client to register
	select {
	case clientID := <-clientRegistered:
		t.Logf("Client registered with ID: %s", clientID)
	case <-time.After(10 * time.Second):
		// If the test fails, log the browser console output
		logBrowserConsole(t, page)
		t.Fatal("Timed out waiting for client registration")
	}

	// Success! The client has connected and registered
	t.Logf("Test passed: Client connected and registered successfully")
}
