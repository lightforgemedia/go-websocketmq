// Package browser_tests contains tests for the WebSocketMQ browser client.
package browser_tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBrowserClientPublishSubscribe tests the browser client's ability to subscribe to topics and receive published messages.
func TestBrowserClientPublishSubscribe(t *testing.T) {
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
					<title>WebSocketMQ Publish-Subscribe Test</title>
					<script src="/js_helpers/console_override.js"></script>
					<script src="/websocketmq.js"></script>
					<script src="/js_helpers/connect.js"></script>
					<script>
						// Initialize message counter
						window.messageCount = 0;
						
						// Function to subscribe to a topic
						function subscribeTopic(topic) {
							console.log('Subscribing to topic:', topic);
							
							// Make sure client is connected
							if (window.client && window.client.isConnected) {
								// Subscribe to the topic
								window.unsubscribeFn = window.client.subscribe(topic, message => {
									console.log('Received message on topic ' + topic + ':', message);
									window.messageCount++;
									
									// Update UI with message count
									document.getElementById('message-count').textContent = window.messageCount;
									
									// Add message to messages container
									const messagesElement = document.getElementById('messages');
									if (messagesElement) {
										const messageElement = document.createElement('div');
										messageElement.className = 'message';
										messageElement.textContent = typeof message === 'object' 
											? JSON.stringify(message) 
											: message;
										messagesElement.appendChild(messageElement);
									}
								});
								
								document.getElementById('subscription-status').textContent = 'Subscribed to ' + topic;
								document.getElementById('subscription-status').style.color = 'green';
							} else {
								console.error('Client not connected');
								document.getElementById('subscription-status').textContent = 'Error: Client not connected';
								document.getElementById('subscription-status').style.color = 'red';
							}
						}
						
						// Function to unsubscribe from a topic
						function unsubscribeTopic() {
							console.log('Unsubscribing from topic');
							
							if (window.unsubscribeFn) {
								window.unsubscribeFn();
								window.unsubscribeFn = null;
								document.getElementById('subscription-status').textContent = 'Unsubscribed';
								document.getElementById('subscription-status').style.color = 'red';
							} else {
								console.warn('Not subscribed to any topic');
							}
						}
						
						// Initialize the page when it loads
						document.addEventListener('DOMContentLoaded', () => {
							// Set up subscription buttons
							const subscribeBtn = document.getElementById('subscribe-btn');
							if (subscribeBtn) {
								subscribeBtn.addEventListener('click', () => subscribeTopic('test:broadcast'));
							}
							
							const unsubscribeBtn = document.getElementById('unsubscribe-btn');
							if (unsubscribeBtn) {
								unsubscribeBtn.addEventListener('click', unsubscribeTopic);
							}
							
							// Auto-subscribe after connection
							// Use a polling approach to check for client connection
							const checkInterval = setInterval(() => {
								if (window.client && window.client.isConnected) {
									clearInterval(checkInterval);
									console.log('Client connected, auto-subscribing to test:broadcast');
									subscribeTopic('test:broadcast');
								}
							}, 100);
						});
					</script>
				</head>
				<body>
					<h1>WebSocketMQ Publish-Subscribe Test</h1>
					<div>
						<h2>Connection Status: <span id="status">Disconnected</span></h2>
					</div>
					<div>
						<h2>Subscription Status: <span id="subscription-status">Not Subscribed</span></h2>
						<button id="subscribe-btn">Subscribe</button>
						<button id="unsubscribe-btn">Unsubscribe</button>
					</div>
					<div>
						<h2>Messages Received: <span id="message-count">0</span></h2>
						<div id="messages"></div>
					</div>
				</body>
				</html>`

		w.Write([]byte(html))
	})

	// Test the browser connection with headless mode disabled
	headless := false
	result := TestBrowserConnection(t, bs, httpServer, headless)

	// Wait for the subscription to be established
	time.Sleep(1 * time.Second)

	// Publish a message to the topic
	t.Log("Publishing message to test:broadcast")
	ctx := context.Background()
	err := bs.Broker.Publish(ctx, "test:broadcast", map[string]interface{}{
		"messageId": "test-message-1",
		"timestamp": time.Now().Format(time.RFC3339),
		"content":   "Hello from the server!",
	})
	require.NoError(t, err, "Failed to publish message")

	// Wait a bit for the message to be received
	time.Sleep(500 * time.Millisecond)

	// Verify the message count has been updated
	messageCount, err := result.Page.MustElement("#message-count").Text()
	require.NoError(t, err, "Should be able to get message count")
	assert.Equal(t, "1", messageCount, "Should have received 1 message")

	// Publish another message
	t.Log("Publishing second message to test:broadcast")
	err = bs.Broker.Publish(ctx, "test:broadcast", map[string]interface{}{
		"messageId": "test-message-2",
		"timestamp": time.Now().Format(time.RFC3339),
		"content":   "Second message from the server!",
	})
	require.NoError(t, err, "Failed to publish second message")

	// Wait a bit for the message to be received
	time.Sleep(500 * time.Millisecond)

	// Verify the message count has been updated
	messageCount, err = result.Page.MustElement("#message-count").Text()
	require.NoError(t, err, "Should be able to get message count")
	assert.Equal(t, "2", messageCount, "Should have received 2 messages")

	messages, err := result.Page.MustElement("#messages").Text()
	require.NoError(t, err, "Should be able to get message count")
	t.Logf("Messages: %v", messages)
	assert.Contains(t, messages, "Hello from the server!")
	assert.Contains(t, messages, "Second message from the server!")

	// test - message - 2

	// Check console logs for subscription and messages
	logs := result.Page.GetConsoleLog()
	t.Logf("Console logs: %v", logs)

	// Check if we have log messages for the subscription and messages
	subscribed := false
	messageReceived := false
	for _, log := range logs {
		if strings.Contains(log, "Subscribing to topic: test:broadcast") {
			subscribed = true
		}
		if strings.Contains(log, "Received message on topic test:broadcast") {
			messageReceived = true
		}
	}

	assert.True(t, subscribed, "Should have subscribed to test:broadcast")
	assert.True(t, messageReceived, "Should have received a message on test:broadcast")

	// Add a delay so we can see the browser window before it closes
	// This is only useful when running with headless=false
	if !result.Browser.IsHeadless() {
		t.Log("Waiting 5 seconds so you can see the browser window...")
		time.Sleep(5 * time.Second)
	}
}
