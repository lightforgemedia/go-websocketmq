package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"nhooyr.io/websocket"
)

// DebugHandler is a version of the Handler with detailed logging for debugging
type DebugHandler struct {
	Broker *ps.PubSubBroker
	// Channel to receive debug messages
	DebugCh chan string
}

// NewDebugHandler creates a new debug handler
func NewDebugHandler() *DebugHandler {
	return &DebugHandler{
		Broker:  ps.New(128),
		DebugCh: make(chan string, 100), // Buffer for debug messages
	}
}

func (h *DebugHandler) log(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	log.Println(msg)
	select {
	case h.DebugCh <- msg:
		// Message sent to channel
	default:
		// Channel is full, log to console only
		log.Println("Debug channel full, logging to console only:", msg)
	}
}

func (h *DebugHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.log("ServeHTTP: Handling new WebSocket connection")

	// Accept the WebSocket connection
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		h.log("ServeHTTP: Error accepting WebSocket connection: %v", err)
		return
	}
	h.log("ServeHTTP: WebSocket connection accepted")

	// Create a context that's canceled when the connection is closed
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Handle the connection in the same goroutine
	h.handleConnection(ctx, c)
}

func (h *DebugHandler) handleConnection(ctx context.Context, ws *websocket.Conn) {
	h.log("handleConnection: Starting to handle connection")
	defer h.log("handleConnection: Connection handling complete")
	defer ws.Close(websocket.StatusNormalClosure, "Connection closed")

	for {
		h.log("handleConnection: Waiting for message")
		_, data, err := ws.Read(ctx)
		if err != nil {
			h.log("handleConnection: Error reading message: %v", err)
			return
		}
		h.log("handleConnection: Received message: %s", string(data))

		var m model.Message
		if err := json.Unmarshal(data, &m); err != nil {
			h.log("handleConnection: Error unmarshaling message: %v", err)
			continue
		}
		h.log("handleConnection: Parsed message: %+v", m)

		// Handle the message based on its type
		switch m.Header.Type {
		case "request":
			h.log("handleConnection: Handling request message")
			resp, err := h.Broker.Request(ctx, &m, 5000)
			if err != nil {
				h.log("handleConnection: Error making request: %v", err)
				continue
			}
			if resp == nil {
				h.log("handleConnection: No response received")
				continue
			}
			h.log("handleConnection: Received response: %+v", resp)

			raw, err := json.Marshal(resp)
			if err != nil {
				h.log("handleConnection: Error marshaling response: %v", err)
				continue
			}
			h.log("handleConnection: Sending response: %s", string(raw))

			if err := ws.Write(ctx, websocket.MessageText, raw); err != nil {
				h.log("handleConnection: Error writing response: %v", err)
				return
			}
			h.log("handleConnection: Response sent successfully")

		case "subscribe":
			h.log("handleConnection: Handling subscribe message")
			err := h.Broker.Subscribe(ctx, m.Header.Topic, func(msgCtx context.Context, msg *model.Message) (*model.Message, error) {
				h.log("Subscription handler: Received message on topic %s: %+v", m.Header.Topic, msg)

				raw, err := json.Marshal(msg)
				if err != nil {
					h.log("Subscription handler: Error marshaling message: %v", err)
					return nil, err
				}
				h.log("Subscription handler: Forwarding message: %s", string(raw))

				if err := ws.Write(ctx, websocket.MessageText, raw); err != nil {
					h.log("Subscription handler: Error writing message: %v", err)
					return nil, err
				}
				h.log("Subscription handler: Message forwarded successfully")

				return nil, nil
			})

			if err != nil {
				h.log("handleConnection: Error subscribing to topic: %v", err)
				continue
			}
			h.log("handleConnection: Subscribed to topic: %s", m.Header.Topic)

			// Send a response to acknowledge the subscription
			resp := &model.Message{
				Header: model.MessageHeader{
					MessageID:     "resp-" + m.Header.MessageID,
					CorrelationID: m.Header.MessageID,
					Type:          "response",
					Topic:         m.Header.Topic,
					Timestamp:     time.Now().UnixMilli(),
				},
				Body: map[string]any{
					"success": true,
				},
			}

			raw, err := json.Marshal(resp)
			if err != nil {
				h.log("handleConnection: Error marshaling subscription response: %v", err)
				continue
			}
			h.log("handleConnection: Sending subscription response: %s", string(raw))

			if err := ws.Write(ctx, websocket.MessageText, raw); err != nil {
				h.log("handleConnection: Error writing subscription response: %v", err)
				return
			}
			h.log("handleConnection: Subscription response sent successfully")

		default:
			h.log("handleConnection: Handling publish message")
			err := h.Broker.Publish(ctx, &m)
			if err != nil {
				h.log("handleConnection: Error publishing message: %v", err)
				continue
			}
			h.log("handleConnection: Message published successfully to topic: %s", m.Header.Topic)
		}
	}
}

// TestDebugHandler tests the WebSocket handler with detailed logging
func TestDebugHandler(t *testing.T) {
	// Create a debug handler
	handler := NewDebugHandler()

	// Create a test server
	server := httptest.NewServer(handler)
	defer server.Close()

	// Create a WebSocket URL
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// Test 1: Connect to WebSocket
	t.Run("Connect", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		t.Log("Connecting to WebSocket...")
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatalf("Error connecting to WebSocket: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		t.Log("Connected to WebSocket")

		// Wait for debug messages
		time.Sleep(100 * time.Millisecond)

		// Print debug messages
		t.Log("Debug messages:")
		for {
			select {
			case msg := <-handler.DebugCh:
				t.Log(msg)
			default:
				// No more messages
				goto done
			}
		}
	done:
	})

	// Test 2: Publish a message
	t.Run("Publish", func(t *testing.T) {
		// Create a channel to signal when the message is received
		messageReceived := make(chan struct{})

		// Subscribe to the topic
		topic := "test.debug"
		err := handler.Broker.Subscribe(context.Background(), topic, func(ctx context.Context, m *model.Message) (*model.Message, error) {
			t.Logf("Broker received message on topic %s: %v", topic, m.Header.MessageID)
			close(messageReceived)
			return nil, nil
		})
		if err != nil {
			t.Fatalf("Error subscribing to topic: %v", err)
		}

		// Connect to the WebSocket
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		t.Log("Connecting to WebSocket...")
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatalf("Error connecting to WebSocket: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		t.Log("Connected to WebSocket")

		// Create a message
		msg := &model.Message{
			Header: model.MessageHeader{
				MessageID: "test-debug",
				Type:      "event",
				Topic:     topic,
				Timestamp: time.Now().UnixMilli(),
			},
			Body: map[string]any{
				"test": "debug",
			},
		}

		// Marshal the message
		data, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("Error marshaling message: %v", err)
		}

		// Send the message
		t.Log("Sending message...")
		err = conn.Write(ctx, websocket.MessageText, data)
		if err != nil {
			t.Fatalf("Error sending message: %v", err)
		}
		t.Log("Message sent")

		// Wait for the message to be received
		t.Log("Waiting for message to be received...")
		select {
		case <-messageReceived:
			t.Log("Message received by broker")
		case <-time.After(2 * time.Second):
			t.Fatal("Timed out waiting for message to be received")
		}

		// Wait for debug messages
		time.Sleep(100 * time.Millisecond)

		// Print debug messages
		t.Log("Debug messages:")
		for {
			select {
			case msg := <-handler.DebugCh:
				t.Log(msg)
			default:
				// No more messages
				goto done
			}
		}
	done:
	})

	// Test 3: Subscribe to a topic
	t.Run("Subscribe", func(t *testing.T) {
		// Connect to the WebSocket
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		t.Log("Connecting to WebSocket...")
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatalf("Error connecting to WebSocket: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		t.Log("Connected to WebSocket")

		// Create a subscription message
		topic := "test.subscribe"
		subscribeMsg := &model.Message{
			Header: model.MessageHeader{
				MessageID: "sub-debug",
				Type:      "subscribe",
				Topic:     topic,
				Timestamp: time.Now().UnixMilli(),
			},
		}

		// Marshal the message
		data, err := json.Marshal(subscribeMsg)
		if err != nil {
			t.Fatalf("Error marshaling subscription message: %v", err)
		}

		// Send the subscription message
		t.Log("Sending subscription message...")
		err = conn.Write(ctx, websocket.MessageText, data)
		if err != nil {
			t.Fatalf("Error sending subscription message: %v", err)
		}
		t.Log("Subscription message sent")

		// Wait for the subscription acknowledgment
		t.Log("Waiting for subscription acknowledgment...")
		_, respData, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("Error reading subscription acknowledgment: %v", err)
		}
		t.Logf("Received subscription acknowledgment: %s", string(respData))

		// Create a message to publish
		publishMsg := &model.Message{
			Header: model.MessageHeader{
				MessageID: "pub-debug",
				Type:      "event",
				Topic:     topic,
				Timestamp: time.Now().UnixMilli(),
			},
			Body: map[string]any{
				"test": "subscribe",
			},
		}

		// Publish the message directly to the broker
		t.Log("Publishing message directly to broker...")
		err = handler.Broker.Publish(ctx, publishMsg)
		if err != nil {
			t.Fatalf("Error publishing message: %v", err)
		}
		t.Log("Message published to broker")

		// Wait for the message to be received by the WebSocket client
		t.Log("Waiting for message to be received by WebSocket client...")
		_, msgData, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("Error reading message: %v", err)
		}
		t.Logf("Received message: %s", string(msgData))

		// Wait for debug messages
		time.Sleep(100 * time.Millisecond)

		// Print debug messages
		t.Log("Debug messages:")
		for {
			select {
			case msg := <-handler.DebugCh:
				t.Log(msg)
			default:
				// No more messages
				goto done
			}
		}
	done:
	})
}
