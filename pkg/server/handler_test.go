package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"nhooyr.io/websocket"
)

// MockBroker is a mock implementation of the broker.Broker interface
type MockBroker struct {
	PublishFunc   func(ctx context.Context, m *model.Message) error
	SubscribeFunc func(ctx context.Context, topic string, fn broker.Handler) error
	RequestFunc   func(ctx context.Context, req *model.Message, timeoutMs int64) (*model.Message, error)
	CloseFunc     func() error
}

func (b *MockBroker) Publish(ctx context.Context, m *model.Message) error {
	return b.PublishFunc(ctx, m)
}

func (b *MockBroker) Subscribe(ctx context.Context, topic string, fn broker.Handler) error {
	return b.SubscribeFunc(ctx, topic, fn)
}

func (b *MockBroker) Request(ctx context.Context, req *model.Message, timeoutMs int64) (*model.Message, error) {
	return b.RequestFunc(ctx, req, timeoutMs)
}

func (b *MockBroker) Close() error {
	if b.CloseFunc != nil {
		return b.CloseFunc()
	}
	return nil
}

func TestHandler_ServeHTTP(t *testing.T) {
	// Create a mock broker
	mockBroker := &MockBroker{
		PublishFunc: func(ctx context.Context, m *model.Message) error {
			return nil
		},
		SubscribeFunc: func(ctx context.Context, topic string, fn broker.Handler) error {
			return nil
		},
		RequestFunc: func(ctx context.Context, req *model.Message, timeoutMs int64) (*model.Message, error) {
			return nil, nil
		},
		CloseFunc: func() error {
			return nil
		},
	}

	// Create a handler
	handler := New(mockBroker)

	// Create a test server
	server := httptest.NewServer(handler)
	defer server.Close()

	// Create a WebSocket URL
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// Connect to the WebSocket
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Error connecting to WebSocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
}

func TestHandler_Integration(t *testing.T) {
	// Create an in-memory broker for testing
	broker := &MockBroker{
		PublishFunc: func(ctx context.Context, m *model.Message) error {
			return nil
		},
		SubscribeFunc: func(ctx context.Context, topic string, fn broker.Handler) error {
			// Store the handler and call it when a message is published
			if topic == "test.topic" {
				// Simulate a message being published
				go func() {
					time.Sleep(100 * time.Millisecond) // Give the test time to set up
					msg := &model.Message{
						Header: model.MessageHeader{
							MessageID: "response-123",
							Type:      "response",
							Topic:     "test.topic",
							Timestamp: time.Now().UnixMilli(),
						},
						Body: map[string]any{
							"key": "value",
						},
					}
					fn(context.Background(), msg)
				}()
			}
			return nil
		},
		RequestFunc: func(ctx context.Context, req *model.Message, timeoutMs int64) (*model.Message, error) {
			// Simulate a response
			return &model.Message{
				Header: model.MessageHeader{
					MessageID:     "response-123",
					CorrelationID: req.Header.CorrelationID,
					Type:          "response",
					Topic:         req.Header.CorrelationID,
					Timestamp:     time.Now().UnixMilli(),
				},
				Body: map[string]any{
					"response": "value",
				},
			}, nil
		},
		CloseFunc: func() error {
			return nil
		},
	}

	// Create a handler
	handler := New(broker)

	// Create a test server
	server := httptest.NewServer(handler)
	defer server.Close()

	// Create a WebSocket URL
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// Connect to the WebSocket
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Test 1: Connect to WebSocket
	t.Run("Connect", func(t *testing.T) {
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatalf("Error connecting to WebSocket: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
	})

	// Test 2: Send a message
	t.Run("Send message", func(t *testing.T) {
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

		// Wait for a response (we're not actually expecting one in this test)
		time.Sleep(200 * time.Millisecond)
	})

	// Test 3: Subscribe to a topic and receive a message
	t.Run("Subscribe and receive", func(t *testing.T) {
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatalf("Error connecting to WebSocket: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		// Subscribe to a topic
		subscribeMsg := &model.Message{
			Header: model.MessageHeader{
				MessageID: "sub-123",
				Type:      "subscribe",
				Topic:     "test.topic",
				Timestamp: time.Now().UnixMilli(),
			},
			Body: nil,
		}

		// Marshal the message to JSON
		data, err := json.Marshal(subscribeMsg)
		if err != nil {
			t.Fatalf("Error marshaling message: %v", err)
		}

		// Send the subscription message
		err = conn.Write(ctx, websocket.MessageText, data)
		if err != nil {
			t.Fatalf("Error sending subscription message: %v", err)
		}

		// Wait for a response
		_, data, err = conn.Read(ctx)
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
	})

	// Test 4: Send a request and get a response
	t.Run("Request-response", func(t *testing.T) {
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

		// Wait for a response
		_, data, err = conn.Read(ctx)
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
