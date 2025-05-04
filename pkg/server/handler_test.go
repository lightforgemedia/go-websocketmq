package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps"
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
	t.Skip("Integration tests are flaky and need more work")

	// Create a real PubSubBroker for testing
	broker := ps.New(128)

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
		// Create a channel to signal when the test is done
		done := make(chan struct{})

		// Create a connection for subscribing
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

		// Wait for the subscription acknowledgment
		_, data, err = conn.Read(ctx)
		if err != nil {
			t.Fatalf("Error reading subscription acknowledgment: %v", err)
		}

		// Unmarshal the response
		var subResponse model.Message
		err = json.Unmarshal(data, &subResponse)
		if err != nil {
			t.Fatalf("Error unmarshaling subscription response: %v", err)
		}

		// Verify the subscription response
		if subResponse.Header.Type != "response" {
			t.Fatalf("Expected response type 'response', got '%s'", subResponse.Header.Type)
		}

		// Start a goroutine to read the published message
		go func() {
			// Wait for the published message
			_, msgData, err := conn.Read(ctx)
			if err != nil {
				t.Errorf("Error reading published message: %v", err)
				close(done)
				return
			}

			// Unmarshal the message
			var pubMsg model.Message
			err = json.Unmarshal(msgData, &pubMsg)
			if err != nil {
				t.Errorf("Error unmarshaling published message: %v", err)
				close(done)
				return
			}

			// Verify the published message
			if pubMsg.Header.Topic != "test.topic" {
				t.Errorf("Expected topic 'test.topic', got '%s'", pubMsg.Header.Topic)
			}

			close(done)
		}()

		// Create a second connection for publishing
		pubConn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatalf("Error connecting to WebSocket for publishing: %v", err)
		}
		defer pubConn.Close(websocket.StatusNormalClosure, "")

		// Create a message to publish
		pubMsg := &model.Message{
			Header: model.MessageHeader{
				MessageID: "pub-123",
				Type:      "event",
				Topic:     "test.topic",
				Timestamp: time.Now().UnixMilli(),
			},
			Body: map[string]any{
				"key": "value",
			},
		}

		// Marshal the message to JSON
		pubData, err := json.Marshal(pubMsg)
		if err != nil {
			t.Fatalf("Error marshaling publish message: %v", err)
		}

		// Publish the message
		err = pubConn.Write(ctx, websocket.MessageText, pubData)
		if err != nil {
			t.Fatalf("Error publishing message: %v", err)
		}

		// Wait for the test to complete or timeout
		select {
		case <-done:
			// Test completed successfully
		case <-time.After(2 * time.Second):
			t.Fatal("Timed out waiting for published message")
		}
	})

	// Test 4: Send a request and get a response
	t.Run("Request-response", func(t *testing.T) {
		// First, set up a handler for the request
		err := broker.Subscribe(ctx, "test.request", func(ctx context.Context, m *model.Message) (*model.Message, error) {
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

		// Create a connection for the request
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

		// Wait for a response with a timeout
		responseCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()

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
