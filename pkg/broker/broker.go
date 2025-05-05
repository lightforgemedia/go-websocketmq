// pkg/broker/broker.go
package broker

import (
	"context"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
)

// Logger interface for broker components
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// Handler function that processes messages and optionally returns a response
type Handler func(ctx context.Context, m *model.Message) (*model.Message, error)

// Broker is the interface for message routing
type Broker interface {
	// Publish sends a message to the specified topic
	Publish(ctx context.Context, m *model.Message) error
	
	// Subscribe registers a handler for a specific topic
	Subscribe(ctx context.Context, topic string, fn Handler) error
	
	// Request sends a request and waits for a response with the given timeout
	Request(ctx context.Context, req *model.Message, timeoutMs int64) (*model.Message, error)
	
	// Close terminates the broker and cleans up resources
	Close() error
}

// Options for broker configuration
type Options struct {
	QueueLength int // Buffer size for each subscription queue
}

// DefaultOptions returns the default broker options
func DefaultOptions() Options {
	return Options{
		QueueLength: 128,
	}
}