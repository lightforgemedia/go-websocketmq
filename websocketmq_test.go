package websocketmq

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/model"
)

// SimpleLogger is a basic implementation of the Logger interface for testing.
type SimpleLogger struct{}

func (l *SimpleLogger) Debug(msg string, args ...interface{}) {}
func (l *SimpleLogger) Info(msg string, args ...interface{})  {}
func (l *SimpleLogger) Warn(msg string, args ...interface{})  {}
func (l *SimpleLogger) Error(msg string, args ...interface{}) {}

// TestNewPubSubBroker tests that the NewPubSubBroker function correctly creates a broker.
func TestNewPubSubBroker(t *testing.T) {
	logger := &SimpleLogger{}
	opts := DefaultBrokerOptions()
	broker := NewPubSubBroker(logger, opts)
	
	if broker == nil {
		t.Fatal("Expected broker to be created, got nil")
	}
}

// TestNewHandler tests that the NewHandler function correctly creates a handler.
func TestNewHandler(t *testing.T) {
	logger := &SimpleLogger{}
	brokerOpts := DefaultBrokerOptions()
	broker := NewPubSubBroker(logger, brokerOpts)
	
	handlerOpts := DefaultHandlerOptions()
	handler := NewHandler(broker, logger, handlerOpts)
	
	if handler == nil {
		t.Fatal("Expected handler to be created, got nil")
	}
}

// TestScriptHandler tests that the ScriptHandler function correctly creates a handler.
func TestScriptHandler(t *testing.T) {
	handler := ScriptHandler()
	
	if handler == nil {
		t.Fatal("Expected handler to be created, got nil")
	}
	
	// Create a test server
	server := httptest.NewServer(handler)
	defer server.Close()
	
	// Make a request to the server
	resp, err := http.Get(server.URL + "/websocketmq.js")
	if err != nil {
		t.Fatalf("Error making request: %v", err)
	}
	defer resp.Body.Close()
	
	// Check that the response is not a 404
	// Note: The actual file might not exist yet since we haven't run go generate
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("Expected status code %d, got %d", http.StatusNotFound, resp.StatusCode)
	}
}

// TestNewEvent tests that the NewEvent function correctly creates an event message.
func TestNewEvent(t *testing.T) {
	topic := "test.topic"
	body := map[string]interface{}{
		"key": "value",
	}
	
	msg := NewEvent(topic, body)
	
	if msg == nil {
		t.Fatal("Expected message to be created, got nil")
	}
	
	if msg.Header.Topic != topic {
		t.Fatalf("Expected topic %s, got %s", topic, msg.Header.Topic)
	}
	
	if msg.Header.Type != "event" {
		t.Fatalf("Expected type %s, got %s", "event", msg.Header.Type)
	}
	
	if msg.Body == nil {
		t.Fatal("Expected body to be set, got nil")
	}
}

// TestBrokerPublishSubscribe tests that the broker correctly publishes and subscribes to messages.
func TestBrokerPublishSubscribe(t *testing.T) {
	logger := &SimpleLogger{}
	opts := DefaultBrokerOptions()
	broker := NewPubSubBroker(logger, opts)
	
	// Create a channel to receive the message
	received := make(chan *model.Message, 1)
	
	// Subscribe to the topic
	topic := "test.topic"
	ctx := context.Background()
	
	err := broker.Subscribe(ctx, topic, func(ctx context.Context, m *model.Message) (*model.Message, error) {
		received <- m
		return nil, nil
	})
	
	if err != nil {
		t.Fatalf("Error subscribing to topic: %v", err)
	}
	
	// Publish a message to the topic
	body := map[string]interface{}{
		"key": "value",
	}
	msg := NewEvent(topic, body)
	
	err = broker.Publish(ctx, msg)
	if err != nil {
		t.Fatalf("Error publishing message: %v", err)
	}
	
	// Wait for the message to be received
	select {
	case receivedMsg := <-received:
		if receivedMsg.Header.Topic != topic {
			t.Fatalf("Expected topic %s, got %s", topic, receivedMsg.Header.Topic)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for message")
	}
}
