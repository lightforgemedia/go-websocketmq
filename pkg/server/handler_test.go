// pkg/server/handler_test.go
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"nhooyr.io/websocket"
)

// Simple test logger
type testLogger struct{}

func (l *testLogger) Debug(msg string, args ...any) {}
func (l *testLogger) Info(msg string, args ...any)  {}
func (l *testLogger) Warn(msg string, args ...any)  {}
func (l *testLogger) Error(msg string, args ...any) {}

// Helper for WebSocket connections in tests
type testClient struct {
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
}

func newTestClient(t *testing.T, url string) *testClient {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	
	return &testClient{
		conn:   conn,
		ctx:    ctx,
		cancel: cancel,
	}
}

func (c *testClient) close() {
	c.conn.Close(websocket.StatusNormalClosure, "test complete")
	c.cancel()
}

func (c *testClient) writeJSON(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.conn.Write(c.ctx, websocket.MessageText, data)
}

func (c *testClient) readJSON(v interface{}) error {
	_, data, err := c.conn.Read(c.ctx)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func TestWebSocketHandler_PublishSubscribe(t *testing.T) {
	logger := &testLogger{}
	brokerOpts := broker.Options{QueueLength: 10}
	broker := ps.New(logger, brokerOpts)
	defer broker.Close()
	
	// Create a handler with default options
	handler := NewHandler(broker, logger, DefaultHandlerOptions())
	defer handler.Close()
	
	// Create test server
	server := httptest.NewServer(handler)
	defer server.Close()
	
	// Replace http with ws in the URL
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/"
	
	// Create two WebSocket clients
	client1 := newTestClient(t, wsURL)
	defer client1.close()
	
	client2 := newTestClient(t, wsURL)
	defer client2.close()
	
	// Subscribe client2 to a topic
	topic := "test.topic"
	subscribeMsg := model.Message{
		Header: model.MessageHeader{
			MessageID: "sub1",
			Type:      "subscribe",
			Topic:     "subscribe",
			Timestamp: time.Now().UnixMilli(),
		},
		Body: topic,
	}
	
	err := client2.writeJSON(subscribeMsg)
	if err != nil {
		t.Fatalf("Failed to send subscribe message: %v", err)
	}
	
	// Give the subscription time to process
	time.Sleep(100 * time.Millisecond)
	
	// Client1 publishes a message to the topic
	publishMsg := model.NewEvent(topic, map[string]interface{}{
		"message": "hello from client1",
	})
	
	err = client1.writeJSON(publishMsg)
	if err != nil {
		t.Fatalf("Failed to send publish message: %v", err)
	}
	
	// Check that client2 receives the message
	var receivedMsg model.Message
	
	// Set a timeout for receiving the message
	receiveCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	
	// Use a goroutine to read the message
	var readErr error
	readDone := make(chan struct{})
	
	go func() {
		readErr = client2.readJSON(&receivedMsg)
		close(readDone)
	}()
	
	// Wait for either the message to be received or the timeout
	select {
	case <-readDone:
		if readErr != nil {
			t.Fatalf("Failed to read message: %v", readErr)
		}
	case <-receiveCtx.Done():
		t.Fatal("Timed out waiting for message")
	}
	
	// Verify the message content
	if receivedMsg.Header.Topic != topic {
		t.Errorf("Expected topic %s, got %s", topic, receivedMsg.Header.Topic)
	}
	
	bodyMap, ok := receivedMsg.Body.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected body to be a map, got %T", receivedMsg.Body)
	}
	
	if val, ok := bodyMap["message"]; !ok || val != "hello from client1" {
		t.Errorf("Expected message 'hello from client1', got %v", val)
	}
}

func TestWebSocketHandler_RequestResponse(t *testing.T) {
	logger := &testLogger{}
	brokerOpts := broker.Options{QueueLength: 10}
	broker := ps.New(logger, brokerOpts)
	defer broker.Close()
	
	// Create a handler with default options
	handler := NewHandler(broker, logger, DefaultHandlerOptions())
	defer handler.Close()
	
	// Register a handler for a test request topic
	reqTopic := "test.echo"
	ctx := context.Background()
	
	broker.Subscribe(ctx, reqTopic, func(ctx context.Context, m *model.Message) (*model.Message, error) {
		// Echo back what was received with a prefix
		reqBody, ok := m.Body.(map[string]interface{})
		if !ok {
			return nil, nil
		}
		
		respBody := map[string]interface{}{
			"echo": "server says: " + reqBody["message"].(string),
		}
		
		return model.NewResponse(m, respBody), nil
	})
	
	// Create test server
	server := httptest.NewServer(handler)
	defer server.Close()
	
	// Replace http with ws in the URL
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/"
	
	// Create a WebSocket client
	client := newTestClient(t, wsURL)
	defer client.close()
	
	// Client sends a request
	reqMsg := model.NewRequest(reqTopic, map[string]interface{}{
		"message": "hello from client",
	}, 5000)
	
	err := client.writeJSON(reqMsg)
	if err != nil {
		t.Fatalf("Failed to send request message: %v", err)
	}
	
	// Check that client receives the response
	var respMsg model.Message
	
	// Set a timeout for receiving the response
	receiveCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	
	// Use a goroutine to read the response
	var readErr error
	readDone := make(chan struct{})
	
	go func() {
		readErr = client.readJSON(&respMsg)
		close(readDone)
	}()
	
	// Wait for either the response to be received or the timeout
	select {
	case <-readDone:
		if readErr != nil {
			t.Fatalf("Failed to read response: %v", readErr)
		}
	case <-receiveCtx.Done():
		t.Fatal("Timed out waiting for response")
	}
	
	// Verify the response content
	if respMsg.Header.CorrelationID != reqMsg.Header.CorrelationID {
		t.Errorf("Expected correlation ID %s, got %s", reqMsg.Header.CorrelationID, respMsg.Header.CorrelationID)
	}
	
	bodyMap, ok := respMsg.Body.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected body to be a map, got %T", respMsg.Body)
	}
	
	expected := "server says: hello from client"
	if val, ok := bodyMap["echo"]; !ok || val != expected {
		t.Errorf("Expected echo '%s', got %v", expected, val)
	}
}

func TestWebSocketHandler_MultipleClients(t *testing.T) {
	logger := &testLogger{}
	brokerOpts := broker.Options{QueueLength: 10}
	broker := ps.New(logger, brokerOpts)
	defer broker.Close()
	
	// Create a handler with default options
	handler := NewHandler(broker, logger, DefaultHandlerOptions())
	defer handler.Close()
	
	// Create test server
	server := httptest.NewServer(handler)
	defer server.Close()
	
	// Replace http with ws in the URL
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/"
	
	// Create multiple clients
	numClients := 5
	clients := make([]*testClient, numClients)
	for i := 0; i < numClients; i++ {
		clients[i] = newTestClient(t, wsURL)
		defer clients[i].close()
	}
	
	// Have all clients subscribe to the same topic
	topic := "test.broadcast"
	for i, client := range clients {
		subscribeMsg := model.Message{
			Header: model.MessageHeader{
				MessageID: "sub",
				Type:      "subscribe",
				Topic:     "subscribe",
				Timestamp: time.Now().UnixMilli(),
			},
			Body: topic,
		}
		
		err := client.writeJSON(subscribeMsg)
		if err != nil {
			t.Fatalf("Client %d failed to send subscribe message: %v", i, err)
		}
	}
	
	// Give the subscriptions time to process
	time.Sleep(100 * time.Millisecond)
	
	// Publish a message from the server
	broadcastMsg := model.NewEvent(topic, map[string]interface{}{
		"broadcast": "message to all clients",
	})
	
	err := broker.Publish(context.Background(), broadcastMsg)
	if err != nil {
		t.Fatalf("Failed to publish message from server: %v", err)
	}
	
	// Check that each client receives the message
	var wg sync.WaitGroup
	wg.Add(numClients)
	
	// Set a timeout for all receives
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	
	errors := make(chan error, numClients)
	
	for i, client := range clients {
		go func(i int, client *testClient) {
			defer wg.Done()
			
			var receivedMsg model.Message
			
			// Create a channel to indicate read completion
			readDone := make(chan struct{})
			var readErr error
			
			go func() {
				readErr = client.readJSON(&receivedMsg)
				close(readDone)
			}()
			
			// Wait for either read completion or timeout
			select {
			case <-readDone:
				if readErr != nil {
					errors <- readErr
					return
				}
				
				// Verify the message
				if receivedMsg.Header.Topic != topic {
					errors <- fmt.Errorf("client %d: expected topic %s, got %s", i, topic, receivedMsg.Header.Topic)
					return
				}
				
				bodyMap, ok := receivedMsg.Body.(map[string]interface{})
				if !ok {
					errors <- fmt.Errorf("client %d: expected body to be a map, got %T", i, receivedMsg.Body)
					return
				}
				
				if val, ok := bodyMap["broadcast"]; !ok || val != "message to all clients" {
					errors <- fmt.Errorf("client %d: expected message 'message to all clients', got %v", i, val)
					return
				}
				
			case <-timeoutCtx.Done():
				errors <- fmt.Errorf("client %d: timed out waiting for message", i)
				return
			}
		}(i, client)
	}
	
	// Wait for all clients to receive their messages
	wg.Wait()
	close(errors)
	
	// Check for any errors
	for err := range errors {
		if err != nil {
			t.Error(err)
		}
	}
}