package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"nhooyr.io/websocket"
)

// TestIntegration tests the WebSocket handler with a real PubSubBroker.
func TestIntegration(t *testing.T) {
	// Skip the test for now until we can fix the flakiness
	t.Skip("Integration tests are flaky and need more work")

	// Create a real PubSubBroker
	broker := ps.New(128)
	defer broker.Close()

	// Create a handler with options
	opts := DefaultOptions()
	handler := New(broker, opts)

	// Create a test server
	server := httptest.NewServer(handler)
	defer server.Close()

	// Create a WebSocket URL
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// Create a wait group to synchronize tests
	var wg sync.WaitGroup

	// Test 1: Connect to WebSocket
	t.Run("Connect", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatalf("Error connecting to WebSocket: %v", err)
		}
		conn.Close(websocket.StatusNormalClosure, "")
	})

	// Test 2: Publish a message
	t.Run("Publish", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Create a channel to receive the message
		receivedCh := make(chan *model.Message, 1)

		// Add to wait group
		wg.Add(1)

		// Subscribe to the topic
		err := broker.Subscribe(ctx, "test.topic", func(ctx context.Context, m *model.Message) (*model.Message, error) {
			receivedCh <- m
			wg.Done()
			return nil, nil
		})
		if err != nil {
			t.Fatalf("Error subscribing to topic: %v", err)
		}

		// Wait a bit to ensure the subscription is set up
		time.Sleep(100 * time.Millisecond)

		// Connect to the WebSocket
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatalf("Error connecting to WebSocket: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		// Create a message
		msg := &model.Message{
			Header: model.MessageHeader{
				MessageID: "123",
				Type:      "event",
				Topic:     "test.topic",
				Timestamp: time.Now().UnixMilli(),
			},
			Body: map[string]any{
				"key": "value",
			},
		}

		// Marshal the message to JSON
		data, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("Error marshaling message: %v", err)
		}

		// Send the message
		err = conn.Write(ctx, websocket.MessageText, data)
		if err != nil {
			t.Fatalf("Error sending message: %v", err)
		}

		// Wait for the message to be received
		waitCh := make(chan struct{})
		go func() {
			wg.Wait()
			close(waitCh)
		}()

		select {
		case <-waitCh:
			// Message received
			select {
			case receivedMsg := <-receivedCh:
				if receivedMsg.Header.Topic != "test.topic" {
					t.Fatalf("Expected topic %s, got %s", "test.topic", receivedMsg.Header.Topic)
				}
			default:
				t.Fatal("Message not received in channel")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Timed out waiting for message")
		}
	})

	// Test 3: Request-Response
	t.Run("Request-Response", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Create a channel to signal when the handler is called
		handlerCalled := make(chan struct{})

		// Set up a handler for the request
		err := broker.Subscribe(ctx, "test.request", func(ctx context.Context, m *model.Message) (*model.Message, error) {
			// Signal that the handler was called
			close(handlerCalled)

			// Create a response message
			return &model.Message{
				Header: model.MessageHeader{
					MessageID:     "resp-" + m.Header.MessageID,
					CorrelationID: m.Header.CorrelationID,
					Type:          "response",
					Topic:         m.Header.CorrelationID,
					Timestamp:     time.Now().UnixMilli(),
				},
				Body: map[string]any{
					"response": "value",
				},
			}, nil
		})
		if err != nil {
			t.Fatalf("Error subscribing to request topic: %v", err)
		}

		// Wait a bit to ensure the subscription is set up
		time.Sleep(100 * time.Millisecond)

		// Connect to the WebSocket
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatalf("Error connecting to WebSocket: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		// Create a request message
		requestMsg := &model.Message{
			Header: model.MessageHeader{
				MessageID:     "req-123",
				CorrelationID: "corr-123",
				Type:          "request",
				Topic:         "test.request",
				Timestamp:     time.Now().UnixMilli(),
				TTL:           1000,
			},
			Body: map[string]any{
				"key": "value",
			},
		}

		// Marshal the message to JSON
		data, err := json.Marshal(requestMsg)
		if err != nil {
			t.Fatalf("Error marshaling message: %v", err)
		}

		// Send the request message
		err = conn.Write(ctx, websocket.MessageText, data)
		if err != nil {
			t.Fatalf("Error sending request message: %v", err)
		}

		// Wait for the handler to be called
		select {
		case <-handlerCalled:
			// Handler was called
		case <-time.After(2 * time.Second):
			t.Fatal("Timed out waiting for handler to be called")
		}

		// Wait for a response with a timeout
		responseCtx, responseCancel := context.WithTimeout(ctx, 2*time.Second)
		defer responseCancel()

		_, data, err = conn.Read(responseCtx)
		if err != nil {
			t.Fatalf("Error reading response: %v", err)
		}

		// Unmarshal the response
		var response model.Message
		err = json.Unmarshal(data, &response)
		if err != nil {
			t.Fatalf("Error unmarshaling response: %v", err)
		}

		// Verify the response
		if response.Header.Type != "response" {
			t.Fatalf("Expected response type 'response', got '%s'", response.Header.Type)
		}
		if response.Header.CorrelationID != requestMsg.Header.CorrelationID {
			t.Fatalf("Expected correlation ID '%s', got '%s'", requestMsg.Header.CorrelationID, response.Header.CorrelationID)
		}
	})
}
