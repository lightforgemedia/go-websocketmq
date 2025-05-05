//go:build integration
// +build integration

package server

import (
"context"
"encoding/json"
"net/http/httptest"
"strings"
"testing"
"time"

"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps"
"github.com/lightforgemedia/go-websocketmq/pkg/model"
"nhooyr.io/websocket"
)

// TestStableIntegration contains integration tests for the WebSocket handler.
// Run with: go test -tags=integration ./pkg/server
func TestStableIntegration(t *testing.T) {
// Test 1: Simple connection test
t.Run("Connect", func(t *testing.T) {
// Create a test environment
broker := ps.New(128)
defer broker.Close()

server, wsURL := newServer(t, broker)
defer server.Close()

// Connect to the WebSocket
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

conn := mustDial(t, ctx, wsURL)
defer conn.Close(websocket.StatusNormalClosure, "")

// If we got here, the connection was successful
t.Log("Successfully connected to WebSocket")
})

// Test 2: Simple publish test
t.Run("Publish", func(t *testing.T) {
// Create a test environment
broker := ps.New(128)
defer broker.Close()

server, wsURL := newServer(t, broker)
defer server.Close()

// Create a channel to signal when the message is received
messageReceived := make(chan struct{})

// Subscribe to the topic on the broker side
topic := "test.topic"
err := broker.Subscribe(context.Background(), topic, func(ctx context.Context, m *model.Message) (*model.Message, error) {
// Signal that the message was received
close(messageReceived)
return nil, nil
})
if err != nil {
t.Fatalf("Failed to subscribe to topic: %v", err)
}

// Connect to the WebSocket
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

conn := mustDial(t, ctx, wsURL)
defer conn.Close(websocket.StatusNormalClosure, "")

// Create and publish a message
msg := &model.Message{
Header: model.MessageHeader{
MessageID: "test-123",
Type:      "event",
Topic:     topic,
Timestamp: time.Now().UnixMilli(),
},
Body: map[string]any{
"key": "value",
},
}

// Marshal and send the message
data, err := json.Marshal(msg)
if err != nil {
t.Fatalf("Failed to marshal message: %v", err)
}

err = conn.Write(ctx, websocket.MessageText, data)
if err != nil {
t.Fatalf("Failed to send message: %v", err)
}

// Wait for the message to be received by the broker
select {
case <-messageReceived:
// Message was received
t.Log("Message was successfully received by the broker")
case <-time.After(2 * time.Second):
t.Fatal("Timed out waiting for message to be received")
}
})
}

// Helper function to create a test server and return its WebSocket URL
func newServer(t *testing.T, broker *ps.PubSubBroker) (*httptest.Server, string) {
t.Helper()

// Create a handler with default options
handler := New(broker)

// Create a test server
server := httptest.NewServer(handler)

// Create a WebSocket URL
wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

return server, wsURL
}

// Helper function to dial a WebSocket connection
func mustDial(t *testing.T, ctx context.Context, url string) *websocket.Conn {
t.Helper()

conn, _, err := websocket.Dial(ctx, url, nil)
if err != nil {
t.Fatalf("Failed to dial WebSocket: %v", err)
}

return conn
}
