// pkg/broker/ps/ps_test.go
package ps

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
)

// Simple test logger
type testLogger struct{}

func (l *testLogger) Debug(msg string, args ...any) { fmt.Printf("DEBUG: "+msg+"\n", args...) }
func (l *testLogger) Info(msg string, args ...any)  { fmt.Printf("INFO: "+msg+"\n", args...) }
func (l *testLogger) Warn(msg string, args ...any)  { fmt.Printf("WARN: "+msg+"\n", args...) }
func (l *testLogger) Error(msg string, args ...any) { fmt.Printf("ERROR: "+msg+"\n", args...) }

func TestPubSubBroker_PublishSubscribe(t *testing.T) {
	logger := &testLogger{}
	opts := broker.Options{QueueLength: 10}
	b := New(logger, opts)
	defer b.Close()
	
	topic := "test.topic"
	message := model.NewEvent(topic, map[string]interface{}{"test": "data"})
	
	// Create a wait group to ensure the subscriber receives the message
	var wg sync.WaitGroup
	wg.Add(1)
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	var receivedMsg *model.Message
	
	// Subscribe to the topic
	err := b.Subscribe(ctx, topic, func(ctx context.Context, m *model.Message) (*model.Message, error) {
		receivedMsg = m
		wg.Done()
		return nil, nil
	})
	
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}
	
	// Publish a message
	err = b.Publish(ctx, message)
	if err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}
	
	// Wait for the subscriber to receive the message
	waitDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitDone)
	}()
	
	select {
	case <-waitDone:
		// Success, continue
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for message to be received")
	}
	
	// Verify the message content
	if receivedMsg == nil {
		t.Fatal("No message received")
	}
	
	if receivedMsg.Header.Topic != topic {
		t.Errorf("Expected topic %s, got %s", topic, receivedMsg.Header.Topic)
	}
	
	bodyMap, ok := receivedMsg.Body.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected body to be a map, got %T", receivedMsg.Body)
	}
	
	if val, ok := bodyMap["test"]; !ok || val != "data" {
		t.Errorf("Expected body to have test=data, got %v", bodyMap)
	}
}

func TestPubSubBroker_RequestResponse(t *testing.T) {
	logger := &testLogger{}
	opts := broker.Options{QueueLength: 10}
	b := New(logger, opts)
	defer b.Close()
	
	topic := "test.request"
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	// Register a handler for the request
	err := b.Subscribe(ctx, topic, func(ctx context.Context, m *model.Message) (*model.Message, error) {
		// Echo back what we received with a prefix
		bodyMap, ok := m.Body.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("expected body to be a map")
		}
		
		respBody := map[string]interface{}{
			"echo": "response: " + bodyMap["message"].(string),
		}
		
		return model.NewResponse(m, respBody), nil
	})
	
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}
	
	// Create a request
	reqBody := map[string]interface{}{
		"message": "hello",
	}
	req := model.NewRequest(topic, reqBody, 2000)
	
	// Send the request and wait for a response
	resp, err := b.Request(ctx, req, 2000)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	
	// Verify the response
	if resp == nil {
		t.Fatal("No response received")
	}
	
	if resp.Header.CorrelationID != req.Header.CorrelationID {
		t.Errorf("Expected correlation ID %s, got %s", req.Header.CorrelationID, resp.Header.CorrelationID)
	}
	
	bodyMap, ok := resp.Body.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected body to be a map, got %T", resp.Body)
	}
	
	expected := "response: hello"
	if val, ok := bodyMap["echo"]; !ok || val != expected {
		t.Errorf("Expected echo=%s, got %v", expected, val)
	}
}

func TestPubSubBroker_RequestTimeout(t *testing.T) {
	logger := &testLogger{}
	opts := broker.Options{QueueLength: 10}
	b := New(logger, opts)
	defer b.Close()
	
	topic := "test.timeout"
	ctx := context.Background()
	
	// Create a request to a topic with no handlers
	req := model.NewRequest(topic, map[string]interface{}{"message": "hello"}, 500)
	
	// Request should time out after 500ms
	start := time.Now()
	_, err := b.Request(ctx, req, 500)
	
	elapsed := time.Since(start)
	
	if err != context.DeadlineExceeded {
		t.Errorf("Expected deadline exceeded error, got: %v", err)
	}
	
	// Verify that the timeout occurred within a reasonable time
	// Allow for some wiggle room in the timing
	if elapsed < 400*time.Millisecond || elapsed > 1000*time.Millisecond {
		t.Errorf("Expected timeout around 500ms, got %v", elapsed)
	}
}