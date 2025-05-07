package ps_test

import (
	"context"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps/testutil"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
)

// TestBasicPubSub tests the basic publish/subscribe functionality of the broker
func TestBasicPubSub(t *testing.T) {
	// Create a broker
	b := ps.New(testutil.NewTestLogger(t), broker.DefaultOptions())
	defer b.Close()

	// Create a channel to signal when the message is received
	received := make(chan struct{})

	// Subscribe to a topic
	err := b.Subscribe(context.Background(), "test.topic", func(ctx context.Context, msg *model.Message, _ string) (*model.Message, error) {
		t.Logf("Received message: %+v", msg)
		close(received)
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	// Publish a message to the topic
	err = b.Publish(context.Background(), model.NewEvent("test.topic", "test message"))
	if err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Wait for the message to be received
	select {
	case <-received:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for message")
	}
}

// TestBasicRequestResponse tests the basic request/response functionality of the broker
func TestBasicRequestResponse(t *testing.T) {
	// Create a broker
	b := ps.New(testutil.NewTestLogger(t), broker.DefaultOptions())
	defer b.Close()

	// Subscribe to a topic
	err := b.Subscribe(context.Background(), "test.echo", func(ctx context.Context, msg *model.Message, _ string) (*model.Message, error) {
		t.Logf("Received request: %+v", msg)
		return model.NewResponse(msg, "echo response"), nil
	})
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	// Create a request
	req := model.NewRequest("test.echo", "test request", 5000)

	// Send the request and wait for a response
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := b.Request(ctx, req, 5000)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	// Verify the response
	t.Logf("Received response: %+v", resp)
	if resp.Header.Type != model.KindResponse {
		t.Errorf("Expected response type %s, got %s", model.KindResponse, resp.Header.Type)
	}
	if resp.Header.CorrelationID != req.Header.CorrelationID {
		t.Errorf("Expected correlation ID %s, got %s", req.Header.CorrelationID, resp.Header.CorrelationID)
	}
}

// TestRequestTimeout tests that a request times out if no response is received
func TestRequestTimeout(t *testing.T) {
	// Create a broker
	b := ps.New(testutil.NewTestLogger(t), broker.DefaultOptions())
	defer b.Close()

	// Subscribe to a topic with a handler that doesn't respond
	err := b.Subscribe(context.Background(), "test.timeout", func(ctx context.Context, msg *model.Message, _ string) (*model.Message, error) {
		t.Logf("Received request: %+v", msg)
		// Don't return a response
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	// Create a request with a short timeout
	req := model.NewRequest("test.timeout", "test request", 500) // 500ms timeout

	// Send the request and wait for a response
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = b.Request(ctx, req, 500)
	if err == nil {
		t.Fatal("Expected request to time out, but it succeeded")
	}

	t.Logf("Request timed out as expected: %v", err)
}

// TestMultipleSubscribers tests that multiple subscribers to the same topic all receive the message
func TestMultipleSubscribers(t *testing.T) {
	// Create a broker
	b := ps.New(testutil.NewTestLogger(t), broker.DefaultOptions())
	defer b.Close()

	// Create channels to signal when the messages are received
	received1 := make(chan struct{})
	received2 := make(chan struct{})

	// Subscribe to the same topic with two different handlers
	err := b.Subscribe(context.Background(), "test.multi", func(ctx context.Context, msg *model.Message, _ string) (*model.Message, error) {
		t.Logf("Subscriber 1 received message: %+v", msg)
		close(received1)
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Failed to subscribe (1): %v", err)
	}

	err = b.Subscribe(context.Background(), "test.multi", func(ctx context.Context, msg *model.Message, _ string) (*model.Message, error) {
		t.Logf("Subscriber 2 received message: %+v", msg)
		close(received2)
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Failed to subscribe (2): %v", err)
	}

	// Publish a message to the topic
	err = b.Publish(context.Background(), model.NewEvent("test.multi", "test message"))
	if err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Wait for both subscribers to receive the message
	select {
	case <-received1:
		// Subscriber 1 received the message
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for subscriber 1")
	}

	select {
	case <-received2:
		// Subscriber 2 received the message
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for subscriber 2")
	}
}
