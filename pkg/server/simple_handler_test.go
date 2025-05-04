package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"nhooyr.io/websocket"
)

// SimpleHandler is a simplified version of the Handler for testing
type SimpleHandler struct {
	broker *ps.PubSubBroker
}

func NewSimpleHandler() *SimpleHandler {
	return &SimpleHandler{
		broker: ps.New(128),
	}
}

func (h *SimpleHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Accept the WebSocket connection
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusInternalError, "")

	// Create a context that's canceled when the connection is closed
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Handle incoming messages
	for {
		// Read a message
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}

		// Parse the message
		var msg model.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		// Handle the message based on its type
		switch msg.Header.Type {
		case "request":
			// Handle request
			resp := &model.Message{
				Header: model.MessageHeader{
					MessageID:     "resp-" + msg.Header.MessageID,
					CorrelationID: msg.Header.CorrelationID,
					Type:          "response",
					Topic:         msg.Header.CorrelationID,
					Timestamp:     time.Now().UnixMilli(),
				},
				Body: map[string]interface{}{
					"response": "value",
				},
			}
			respData, _ := json.Marshal(resp)
			if err := c.Write(ctx, websocket.MessageText, respData); err != nil {
				return
			}
		case "subscribe":
			// Handle subscription
			resp := &model.Message{
				Header: model.MessageHeader{
					MessageID:     "resp-" + msg.Header.MessageID,
					CorrelationID: msg.Header.MessageID,
					Type:          "response",
					Topic:         msg.Header.Topic,
					Timestamp:     time.Now().UnixMilli(),
				},
				Body: map[string]interface{}{
					"success": true,
				},
			}
			respData, _ := json.Marshal(resp)
			if err := c.Write(ctx, websocket.MessageText, respData); err != nil {
				return
			}
		default:
			// Handle other message types (publish)
			h.broker.Publish(ctx, &msg)
		}
	}
}

// TestSimpleHandler tests the SimpleHandler
func TestSimpleHandler(t *testing.T) {
	// Create a handler
	handler := NewSimpleHandler()

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
			t.Fatalf("Error connecting to WebSocket: %v", err)
		}
		conn.Close(websocket.StatusNormalClosure, "")
	})

	// Test 2: Send a request and get a response
	t.Run("Request-Response", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

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
			Body: map[string]interface{}{
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

	// Test 3: Subscribe to a topic
	t.Run("Subscribe", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Connect to the WebSocket
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatalf("Error connecting to WebSocket: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		// Create a subscription message
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
		if response.Header.Topic != subscribeMsg.Header.Topic {
			t.Fatalf("Expected topic '%s', got '%s'", subscribeMsg.Header.Topic, response.Header.Topic)
		}
	})
}
