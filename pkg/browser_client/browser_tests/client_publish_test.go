// Package browser_tests contains tests for the WebSocketMQ browser client.
package browser_tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBrowserClientPublish tests the browser client's ability to publish messages to topics.
func TestBrowserClientPublish(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping browser test in short mode")
	}

	// Create a broker server with accept options to allow any origin
	opts := broker.DefaultOptions()
	opts.AcceptOptions = &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	}
	bs := testutil.NewBrokerServer(t, opts)

	// Create a channel to receive published messages
	messagesCh := make(chan map[string]interface{}, 10)
	var messagesLock sync.Mutex
	var receivedMessages []map[string]interface{}

	// Create a context for the subscription
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up a goroutine to monitor for published messages
	go func() {
		// Create a client to subscribe to the topic
		cli := testutil.NewTestClient(t, bs.WSURL)
		defer cli.Close()

		// Subscribe to the test:broadcast topic
		_, err := cli.Subscribe("test:broadcast", func(payload map[string]interface{}) error {
			t.Logf("Server received message: %v", payload)
			messagesCh <- payload
			messagesLock.Lock()
			receivedMessages = append(receivedMessages, payload)
			messagesLock.Unlock()
			return nil
		})
		require.NoError(t, err, "Failed to subscribe to test:broadcast")

		// Wait for the context to be cancelled
		<-ctx.Done()
	}()

	// Wait a bit for the subscription to be established
	time.Sleep(500 * time.Millisecond)

	// Create an HTTP server mux
	mux := http.NewServeMux()

	// Register both the WebSocket handler and JavaScript client handler
	bs.RegisterHandlersWithDefaults(mux)

	// Create the HTTP server first so we can use its URL in the HTML
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	// Serve the JavaScript helpers
	mux.Handle("/js_helpers/", http.StripPrefix("/js_helpers/", http.FileServer(http.Dir("js_helpers"))))

	// Create a test HTML page handler
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<!DOCTYPE html>
				<html>
				<head>
					<title>WebSocketMQ Client Publish Test</title>
					<script src="/js_helpers/console_override.js"></script>
					<script src="/websocketmq.js"></script>
					<script src="/js_helpers/connect.js"></script>
					<script src="/js_helpers/publish_helpers.js"></script>
				</head>
				<body>
					<h1>WebSocketMQ Client Publish Test</h1>
					<div>
						<h2>Connection Status: <span id="status">Disconnected</span></h2>
					</div>
					<div>
						<button id="publish-btn">Publish Message</button>
						<button id="multi-publish-btn">Publish Multiple Messages</button>
						<h2>Publish Status: <span id="publish-status">Not Published</span></h2>
					</div>
				</body>
				</html>`

		w.Write([]byte(html))
	})

	// Test the browser connection with headless mode disabled
	headless := false
	result := TestBrowserConnection(t, bs, httpServer, headless)

	// Now that we're connected, click the button to publish a message
	t.Log("Clicking the Publish Message button")
	result.Page.MustElement("#publish-btn").MustClick()

	// Wait for the message to be published and received by the server
	var receivedMessage map[string]interface{}
	select {
	case receivedMessage = <-messagesCh:
		t.Logf("Received message: %v", receivedMessage)
	case <-time.After(5 * time.Second):
		t.Fatal("Timed out waiting for message")
	}

	// Verify the message contains the expected fields
	assert.Contains(t, receivedMessage, "messageId", "Message should contain messageId")
	assert.Contains(t, receivedMessage, "timestamp", "Message should contain timestamp")
	assert.Contains(t, receivedMessage, "content", "Message should contain content")
	assert.Equal(t, "Hello from the client!", receivedMessage["content"], "Message content should match")

	// Wait for the publish status to be updated
	var publishStatus string
	var err error
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)

		// Check if publish status has been updated
		publishStatus, err = result.Page.MustElement("#publish-status").Text()
		if err == nil && publishStatus == "Published successfully" {
			t.Logf("Publish status updated after %d attempts: %s", i+1, publishStatus)
			break
		}
	}

	// Verify the publish status
	assert.Equal(t, "Published successfully", publishStatus, "Publish status should be 'Published successfully'")

	// Now test publishing multiple messages
	t.Log("Clicking the Publish Multiple Messages button")
	result.Page.MustElement("#multi-publish-btn").MustClick()

	// Wait for multiple messages to be received
	messageCount := 1 // We already received one message
	timeout := time.After(5 * time.Second)
	done := false

	for !done {
		select {
		case msg := <-messagesCh:
			t.Logf("Received additional message: %v", msg)
			messageCount++
			if messageCount >= 4 { // 1 initial + 3 from multi-publish
				done = true
			}
		case <-timeout:
			t.Logf("Timed out waiting for all messages, received %d so far", messageCount)
			done = true
		}
	}

	// Verify we received at least 3 messages (the initial one + at least 2 from multi-publish)
	assert.GreaterOrEqual(t, messageCount, 3, "Should have received at least 3 messages total")

	// Check console logs for publish operations
	logs := result.Page.GetConsoleLog()
	t.Logf("Console logs: %v", logs)

	// Check if we have log messages for publishing
	publishAttempted := false
	publishSucceeded := false
	for _, log := range logs {
		if strings.Contains(log, "Publishing message to test:broadcast") {
			publishAttempted = true
		}
		if strings.Contains(log, "Published message to test:broadcast") {
			publishSucceeded = true
		}
	}

	assert.True(t, publishAttempted, "Should have attempted to publish to test:broadcast")
	assert.True(t, publishSucceeded, "Should have successfully published to test:broadcast")

	// Add a delay so we can see the browser window before it closes
	// This is only useful when running with headless=false
	if !result.Browser.IsHeadless() {
		t.Log("Waiting 5 seconds so you can see the browser window...")
		time.Sleep(5 * time.Second)
	}
}
