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

// TestMinimalWebSocket tests just the WebSocket connection and basic message passing.
func TestMinimalWebSocket(t *testing.T) {
	// Create a broker
	broker := ps.New(128)
	defer broker.Close()

	// Create a handler
	handler := New(broker)

	// Create a test server
	server := httptest.NewServer(handler)
	defer server.Close()

	// Create a WebSocket URL
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// Test 1: Connect to WebSocket
	t.Run("Connect", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatalf("Failed to connect to WebSocket: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		t.Log("Successfully connected to WebSocket")
	})

	// Test 2: Direct broker publish
	t.Run("DirectBrokerPublish", func(t *testing.T) {
		// Create a channel to signal when the message is received
		messageReceived := make(chan struct{})

		// Subscribe to the topic
		topic := "test.direct"
		err := broker.Subscribe(context.Background(), topic, func(ctx context.Context, m *model.Message) (*model.Message, error) {
			t.Logf("Received message on topic %s: %v", topic, m.Header.MessageID)
			close(messageReceived)
			return nil, nil
		})
		if err != nil {
			t.Fatalf("Failed to subscribe to topic: %v", err)
		}

		// Create a message
		msg := &model.Message{
			Header: model.MessageHeader{
				MessageID: "test-direct",
				Type:      "event",
				Topic:     topic,
				Timestamp: time.Now().UnixMilli(),
			},
			Body: map[string]any{
				"test": "direct",
			},
		}

		// Publish directly to the broker
		t.Log("Publishing message directly to broker...")
		err = broker.Publish(context.Background(), msg)
		if err != nil {
			t.Fatalf("Failed to publish message: %v", err)
		}

		// Wait for the message to be received
		select {
		case <-messageReceived:
			t.Log("Message received by broker")
		case <-time.After(2 * time.Second):
			t.Fatal("Timed out waiting for message to be received")
		}
	})

	// Test 3: Send a message via WebSocket
	t.Run("WebSocketPublish", func(t *testing.T) {
		// Create a channel to signal when the message is received
		messageReceived := make(chan struct{})

		// Subscribe to the topic
		topic := "test.websocket"
		err := broker.Subscribe(context.Background(), topic, func(ctx context.Context, m *model.Message) (*model.Message, error) {
			t.Logf("Received message on topic %s: %v", topic, m.Header.MessageID)
			close(messageReceived)
			return nil, nil
		})
		if err != nil {
			t.Fatalf("Failed to subscribe to topic: %v", err)
		}

		// Connect to the WebSocket
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatalf("Failed to connect to WebSocket: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		// Create a message
		msg := &model.Message{
			Header: model.MessageHeader{
				MessageID: "test-websocket",
				Type:      "event",
				Topic:     topic,
				Timestamp: time.Now().UnixMilli(),
			},
			Body: map[string]any{
				"test": "websocket",
			},
		}

		// Marshal the message
		data, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("Failed to marshal message: %v", err)
		}

		// Send the message
		t.Log("Sending message via WebSocket...")
		err = conn.Write(ctx, websocket.MessageText, data)
		if err != nil {
			t.Fatalf("Failed to send message: %v", err)
		}

		// Wait for the message to be received
		select {
		case <-messageReceived:
			t.Log("Message received by broker")
		case <-time.After(2 * time.Second):
			t.Fatal("Timed out waiting for message to be received")
		}
	})
}
