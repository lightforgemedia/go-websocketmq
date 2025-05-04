package ps

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/model"
)

func TestPubSubBroker_New(t *testing.T) {
	// Test with valid queue length
	broker := New(10)
	if broker == nil {
		t.Fatal("Expected broker to be created, got nil")
	}

	// Test with zero queue length (edge case)
	broker = New(0)
	if broker == nil {
		t.Fatal("Expected broker to be created with zero queue length, got nil")
	}

	// Test with negative queue length (should still work but treat as zero)
	broker = New(-1)
	if broker == nil {
		t.Fatal("Expected broker to be created with negative queue length, got nil")
	}
}

func TestPubSubBroker_Publish(t *testing.T) {
	// Base case: Publish a message
	t.Run("Base case", func(t *testing.T) {
		broker := New(10)
		ctx := context.Background()

		// Create a test message
		msg := model.NewEvent("test.topic", map[string]any{
			"key": "value",
		})

		// Publish the message
		err := broker.Publish(ctx, msg)
		if err != nil {
			t.Fatalf("Error publishing message: %v", err)
		}
	})

	// Edge case: Publish with nil message
	t.Run("Nil message", func(t *testing.T) {
		broker := New(10)
		ctx := context.Background()

		// Publish nil message (should not panic)
		err := broker.Publish(ctx, nil)
		if err != nil {
			// Our current implementation doesn't check for nil, but if it did,
			// this would be the expected behavior
			t.Logf("Got expected error when publishing nil message: %v", err)
		}
	})

	// Edge case: Publish with canceled context
	t.Run("Canceled context", func(t *testing.T) {
		broker := New(10)
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel the context

		// Create a test message
		msg := model.NewEvent("test.topic", map[string]any{
			"key": "value",
		})

		// Publish the message with canceled context
		// We now expect an error since we check for context cancellation
		err := broker.Publish(ctx, msg)
		if err == nil {
			t.Fatal("Expected error when publishing with canceled context, got nil")
		}
		if err != context.Canceled {
			t.Fatalf("Expected context.Canceled error, got: %v", err)
		}
	})
}

func TestPubSubBroker_Subscribe(t *testing.T) {
	// Happy path: Subscribe and receive a message
	t.Run("Happy path", func(t *testing.T) {
		broker := New(10)
		ctx := context.Background()

		// Create a channel to receive the message
		received := make(chan *model.Message, 1)

		// Subscribe to the topic
		topic := "test.topic"
		err := broker.Subscribe(ctx, topic, func(ctx context.Context, m *model.Message) (*model.Message, error) {
			received <- m
			return nil, nil
		})

		if err != nil {
			t.Fatalf("Error subscribing to topic: %v", err)
		}

		// Create a test message
		msg := model.NewEvent(topic, map[string]any{
			"key": "value",
		})

		// Publish the message
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
	})

	// Test subscribing to multiple topics
	t.Run("Multiple topics", func(t *testing.T) {
		broker := New(10)
		ctx := context.Background()

		// Create channels to receive messages
		received1 := make(chan *model.Message, 1)
		received2 := make(chan *model.Message, 1)

		// Subscribe to the first topic
		topic1 := "test.topic1"
		err := broker.Subscribe(ctx, topic1, func(ctx context.Context, m *model.Message) (*model.Message, error) {
			received1 <- m
			return nil, nil
		})

		if err != nil {
			t.Fatalf("Error subscribing to topic1: %v", err)
		}

		// Subscribe to the second topic
		topic2 := "test.topic2"
		err = broker.Subscribe(ctx, topic2, func(ctx context.Context, m *model.Message) (*model.Message, error) {
			received2 <- m
			return nil, nil
		})

		if err != nil {
			t.Fatalf("Error subscribing to topic2: %v", err)
		}

		// Create and publish a message to the first topic
		msg1 := model.NewEvent(topic1, map[string]any{
			"key": "value1",
		})
		err = broker.Publish(ctx, msg1)
		if err != nil {
			t.Fatalf("Error publishing message to topic1: %v", err)
		}

		// Create and publish a message to the second topic
		msg2 := model.NewEvent(topic2, map[string]any{
			"key": "value2",
		})
		err = broker.Publish(ctx, msg2)
		if err != nil {
			t.Fatalf("Error publishing message to topic2: %v", err)
		}

		// Wait for the first message
		select {
		case receivedMsg := <-received1:
			if receivedMsg.Header.Topic != topic1 {
				t.Fatalf("Expected topic %s, got %s", topic1, receivedMsg.Header.Topic)
			}
		case <-time.After(1 * time.Second):
			t.Fatal("Timed out waiting for message on topic1")
		}

		// Wait for the second message
		select {
		case receivedMsg := <-received2:
			if receivedMsg.Header.Topic != topic2 {
				t.Fatalf("Expected topic %s, got %s", topic2, receivedMsg.Header.Topic)
			}
		case <-time.After(1 * time.Second):
			t.Fatal("Timed out waiting for message on topic2")
		}
	})

	// Test handler returning an error
	t.Run("Handler error", func(t *testing.T) {
		broker := New(10)
		ctx := context.Background()

		// Subscribe to the topic with a handler that returns an error
		topic := "test.topic.error"
		err := broker.Subscribe(ctx, topic, func(ctx context.Context, m *model.Message) (*model.Message, error) {
			return nil, errors.New("handler error")
		})

		if err != nil {
			t.Fatalf("Error subscribing to topic: %v", err)
		}

		// Create and publish a message
		msg := model.NewEvent(topic, map[string]any{
			"key": "value",
		})
		err = broker.Publish(ctx, msg)
		if err != nil {
			t.Fatalf("Error publishing message: %v", err)
		}

		// Wait a bit to ensure the handler is called
		time.Sleep(100 * time.Millisecond)
		// If we get here without a panic, the test passes
	})
}

func TestPubSubBroker_Request(t *testing.T) {
	// Happy path: Send a request and get a response
	t.Run("Happy path", func(t *testing.T) {
		broker := New(10)
		ctx := context.Background()

		// Subscribe to the request topic
		topic := "test.request"
		correlationID := "456"

		err := broker.Subscribe(ctx, topic, func(ctx context.Context, m *model.Message) (*model.Message, error) {
			// Create a response message
			resp := &model.Message{
				Header: model.MessageHeader{
					MessageID:     "resp-123",
					CorrelationID: m.Header.CorrelationID,
					Type:          "response",
					Topic:         m.Header.CorrelationID,
					Timestamp:     time.Now().UnixMilli(),
				},
				Body: map[string]any{
					"response": "value",
				},
			}
			return resp, nil
		})

		if err != nil {
			t.Fatalf("Error subscribing to topic: %v", err)
		}

		// Create a request message with correlation ID
		msg := &model.Message{
			Header: model.MessageHeader{
				MessageID:     "123",
				CorrelationID: correlationID,
				Type:          "request",
				Topic:         topic,
				Timestamp:     time.Now().UnixMilli(),
				TTL:           1000,
			},
			Body: map[string]any{
				"key": "value",
			},
		}

		// Send the request
		resp, err := broker.Request(ctx, msg, 1000)
		if err != nil {
			t.Fatalf("Error sending request: %v", err)
		}

		// Check the response
		if resp == nil {
			t.Fatal("Expected response, got nil")
		}

		if resp.Header.CorrelationID != correlationID {
			t.Fatalf("Expected correlation ID %s, got %s", correlationID, resp.Header.CorrelationID)
		}
	})

	// Test request timeout
	t.Run("Request timeout", func(t *testing.T) {
		broker := New(10)
		ctx := context.Background()

		// Subscribe to the request topic but don't respond
		topic := "test.request.timeout"
		err := broker.Subscribe(ctx, topic, func(ctx context.Context, m *model.Message) (*model.Message, error) {
			// Don't respond, let it timeout
			return nil, nil
		})

		if err != nil {
			t.Fatalf("Error subscribing to topic: %v", err)
		}

		// Create a request message
		msg := &model.Message{
			Header: model.MessageHeader{
				MessageID:     "123",
				CorrelationID: "456",
				Type:          "request",
				Topic:         topic,
				Timestamp:     time.Now().UnixMilli(),
				TTL:           100, // Short timeout
			},
			Body: map[string]any{
				"key": "value",
			},
		}

		// Send the request
		_, err = broker.Request(ctx, msg, 100) // Short timeout
		if err == nil {
			t.Fatal("Expected timeout error, got nil")
		}
		if err != context.DeadlineExceeded {
			t.Fatalf("Expected DeadlineExceeded error, got %v", err)
		}
	})

	// Test context cancellation
	t.Run("Context cancellation", func(t *testing.T) {
		broker := New(10)
		ctx, cancel := context.WithCancel(context.Background())

		// Subscribe to the request topic but don't respond
		topic := "test.request.cancel"
		err := broker.Subscribe(ctx, topic, func(ctx context.Context, m *model.Message) (*model.Message, error) {
			// Don't respond, let the context be canceled
			return nil, nil
		})

		if err != nil {
			t.Fatalf("Error subscribing to topic: %v", err)
		}

		// Create a request message
		msg := &model.Message{
			Header: model.MessageHeader{
				MessageID:     "123",
				CorrelationID: "456",
				Type:          "request",
				Topic:         topic,
				Timestamp:     time.Now().UnixMilli(),
				TTL:           1000,
			},
			Body: map[string]any{
				"key": "value",
			},
		}

		// Start the request in a goroutine
		resultCh := make(chan struct {
			resp *model.Message
			err  error
		})
		go func() {
			resp, err := broker.Request(ctx, msg, 1000)
			resultCh <- struct {
				resp *model.Message
				err  error
			}{resp, err}
		}()

		// Cancel the context
		cancel()

		// Wait for the result
		select {
		case result := <-resultCh:
			if result.err == nil {
				t.Fatal("Expected error due to context cancellation, got nil")
			}
		case <-time.After(1 * time.Second):
			t.Fatal("Timed out waiting for request to complete")
		}
	})
}
