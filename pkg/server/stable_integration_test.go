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
// Run with: go test -tags=integration ./pkg/server -v
func TestStableIntegration(t *testing.T) {
	t.Log("Starting stable integration tests...")
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

	// Test 3: Request-Response pattern with direct broker communication
	t.Run("RequestResponse", func(t *testing.T) {
		// Create a test environment
		broker := ps.New(128)
		defer broker.Close()

		server, wsURL := newServer(t, broker)
		defer server.Close()

		// Create a channel to signal when the request is received
		requestReceived := make(chan *model.Message, 1)

		// Set up a handler for the request topic
		requestTopic := "test.request"
		err := broker.Subscribe(context.Background(), requestTopic, func(ctx context.Context, m *model.Message) (*model.Message, error) {
			// Store the request for verification
			requestReceived <- m

			// Create a response message
			return &model.Message{
				Header: model.MessageHeader{
					MessageID:     "resp-" + m.Header.MessageID,
					CorrelationID: m.Header.CorrelationID,
					Type:          "response",
					Topic:         m.Header.CorrelationID, // Use correlation ID as the response topic
					Timestamp:     time.Now().UnixMilli(),
				},
				Body: map[string]any{
					"response": "echo",
				},
			}, nil
		})
		if err != nil {
			t.Fatalf("Failed to subscribe to request topic: %v", err)
		}

		// Connect to the WebSocket
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		conn := mustDial(t, ctx, wsURL)
		defer conn.Close(websocket.StatusNormalClosure, "")

		// Create a request message with a correlation ID
		correlationID := "corr-123"
		requestMsg := &model.Message{
			Header: model.MessageHeader{
				MessageID:     "req-123",
				CorrelationID: correlationID,
				Type:          "request",
				Topic:         requestTopic,
				Timestamp:     time.Now().UnixMilli(),
			},
			Body: map[string]any{
				"request": "echo",
			},
		}

		// Marshal and send the request
		data, err := json.Marshal(requestMsg)
		if err != nil {
			t.Fatalf("Failed to marshal request: %v", err)
		}

		t.Log("Sending request message...")
		err = conn.Write(ctx, websocket.MessageText, data)
		if err != nil {
			t.Fatalf("Failed to send request: %v", err)
		}

		// Wait for the request to be received by the broker
		select {
		case req := <-requestReceived:
			t.Logf("Request received by broker: %v", req.Header.MessageID)

			// Now wait for the response
			t.Log("Waiting for response...")
			responseCtx, responseCancel := context.WithTimeout(ctx, 2*time.Second)
			defer responseCancel()

			_, responseData, err := conn.Read(responseCtx)
			if err != nil {
				t.Fatalf("Failed to read response: %v", err)
			}

			// Unmarshal the response
			var responseMsg model.Message
			err = json.Unmarshal(responseData, &responseMsg)
			if err != nil {
				t.Fatalf("Failed to unmarshal response: %v", err)
			}

			// Verify the response
			if responseMsg.Header.Type != "response" {
				t.Errorf("Expected response type %q, got %q", "response", responseMsg.Header.Type)
			}
			if responseMsg.Header.CorrelationID != correlationID {
				t.Errorf("Expected correlation ID %q, got %q", correlationID, responseMsg.Header.CorrelationID)
			}

			t.Log("Request-response test completed successfully")

		case <-time.After(2 * time.Second):
			t.Fatal("Timed out waiting for request to be received")
		}
	})

	// Test 4: Subscribe and receive a message
	t.Run("SubscribeAndReceive", func(t *testing.T) {
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

		// Create a channel to signal when the subscription acknowledgment is received
		subscriptionAcknowledged := make(chan struct{})

		// Create a channel to signal when the message is received
		messageReceived := make(chan struct{})

		// Start a goroutine to read messages
		go func() {
			// First, read the subscription acknowledgment
			_, data, err := conn.Read(ctx)
			if err != nil {
				t.Errorf("Failed to read subscription acknowledgment: %v", err)
				return
			}

			// Unmarshal the acknowledgment
			var ack model.Message
			err = json.Unmarshal(data, &ack)
			if err != nil {
				t.Errorf("Failed to unmarshal subscription acknowledgment: %v", err)
				return
			}

			if ack.Header.Type != "response" {
				t.Errorf("Expected acknowledgment type %q, got %q", "response", ack.Header.Type)
				return
			}

			// Signal that the subscription was acknowledged
			close(subscriptionAcknowledged)

			// Now read the actual message
			_, msgData, err := conn.Read(ctx)
			if err != nil {
				t.Errorf("Failed to read message: %v", err)
				return
			}

			// Unmarshal the message
			var msg model.Message
			err = json.Unmarshal(msgData, &msg)
			if err != nil {
				t.Errorf("Failed to unmarshal message: %v", err)
				return
			}

			if msg.Header.Topic != "test.subscribe" {
				t.Errorf("Expected topic %q, got %q", "test.subscribe", msg.Header.Topic)
				return
			}

			// Signal that the message was received
			close(messageReceived)
		}()

		// Subscribe to the topic
		subscribeMsg := &model.Message{
			Header: model.MessageHeader{
				MessageID: "sub-123",
				Type:      "subscribe",
				Topic:     "test.subscribe",
				Timestamp: time.Now().UnixMilli(),
			},
		}

		// Marshal and send the subscription message
		data, err := json.Marshal(subscribeMsg)
		if err != nil {
			t.Fatalf("Failed to marshal subscription message: %v", err)
		}

		t.Log("Sending subscription message...")
		err = conn.Write(ctx, websocket.MessageText, data)
		if err != nil {
			t.Fatalf("Failed to send subscription message: %v", err)
		}

		// Wait for the subscription to be acknowledged
		select {
		case <-subscriptionAcknowledged:
			t.Log("Subscription acknowledged")
		case <-time.After(2 * time.Second):
			t.Fatal("Timed out waiting for subscription acknowledgment")
		}

		// Publish a message to the topic using the broker directly
		publishMsg := &model.Message{
			Header: model.MessageHeader{
				MessageID: "pub-123",
				Type:      "event",
				Topic:     "test.subscribe",
				Timestamp: time.Now().UnixMilli(),
			},
			Body: map[string]any{
				"key": "value",
			},
		}

		t.Log("Publishing message directly to broker...")
		err = broker.Publish(ctx, publishMsg)
		if err != nil {
			t.Fatalf("Failed to publish message: %v", err)
		}

		// Wait for the message to be received
		select {
		case <-messageReceived:
			t.Log("Message received")
		case <-time.After(2 * time.Second):
			t.Fatal("Timed out waiting for message")
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
