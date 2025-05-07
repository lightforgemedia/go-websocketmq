// Package broker defines the interface and types for message routing in WebSocketMQ.
//
// This package provides the core Broker interface that all message broker implementations
// must satisfy, along with supporting types like Logger and Handler.
package broker

import (
	"context"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
)

// Logger defines the interface for logging messages from the WebSocketMQ system.
// Implement this interface to integrate with your application's logging system.
type Logger interface {
	// Debug logs debugging information typically only relevant to developers
	Debug(msg string, args ...any)
	
	// Info logs informational messages about normal operation
	Info(msg string, args ...any)
	
	// Warn logs potentially problematic situations that don't prevent operation
	Warn(msg string, args ...any)
	
	// Error logs error conditions that might require attention
	Error(msg string, args ...any)
}

// Handler is a function type that processes messages and optionally returns a response.
// Handlers are registered with the broker to process messages for specific topics.
//
// Parameters:
//   - ctx: A context for cancellation and propagating deadlines
//   - m: The incoming message to process
//
// Returns:
//   - A response message (or nil if no response is needed)
//   - An error (or nil if processing was successful)
type Handler func(ctx context.Context, m *model.Message) (*model.Message, error)

// Broker defines the interface for message routing in WebSocketMQ.
// The broker is responsible for dispatching messages to subscribers
// and handling request-response communication.
type Broker interface {
	// Publish sends a message to all subscribers of the specified topic.
	// The message's topic is determined by its Header.Topic field.
	//
	// Parameters:
	//   - ctx: A context for cancellation and propagating deadlines
	//   - m: The message to publish
	//
	// Returns an error if publishing fails.
	Publish(ctx context.Context, m *model.Message) error
	
	// Subscribe registers a handler for a specific topic. The handler will be
	// called whenever a message is published to the matching topic.
	//
	// Parameters:
	//   - ctx: A context for cancellation
	//   - topic: The topic to subscribe to (can include wildcards depending on implementation)
	//   - fn: The handler function to process matching messages
	//
	// Returns an error if subscription fails.
	Subscribe(ctx context.Context, topic string, fn Handler) error
	
	// Request sends a request and waits for a response with the given timeout.
	// This implements the request-response pattern on top of publish-subscribe.
	//
	// Parameters:
	//   - ctx: A context for cancellation and propagating deadlines
	//   - req: The request message to send
	//   - timeoutMs: The maximum time to wait for a response, in milliseconds
	//
	// Returns:
	//   - The response message if received within the timeout
	//   - An error if the request fails or times out
	Request(ctx context.Context, req *model.Message, timeoutMs int64) (*model.Message, error)
	
	// Close terminates the broker and cleans up resources.
	// After calling Close, no more messages can be published or received.
	//
	// Returns an error if shutdown fails.
	Close() error
}

// Options configures the behavior of the message broker.
type Options struct {
	// QueueLength is the buffer size for each subscription queue.
	// A larger queue can handle more pending messages before blocking
	// or dropping messages, at the cost of increased memory usage.
	QueueLength int
}

// DefaultOptions returns the default broker options suitable for most applications.
//
// Default values:
//   - QueueLength: 128 messages
func DefaultOptions() Options {
	return Options{
		QueueLength: 128,
	}
}