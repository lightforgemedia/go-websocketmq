// pkg/broker/broker.go
package broker

import (
	"context"
	"errors"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/model" // Use model from within the library
)

// Predefined error types
var (
	ErrClientNotFound      = errors.New("client not found")
	ErrRequestTimeout      = errors.New("request timed out")
	ErrConnectionWrite     = errors.New("connection write error")
	ErrBrokerClosed        = errors.New("broker is closed")
	ErrInvalidMessage      = errors.New("invalid message")
	ErrHandlerNotFound     = errors.New("handler not found for topic") // Added as per test plan
)

// Constants for internal broker events
const (
	// TopicClientRegistered is published when a client registers its PageSessionID.
	// Body: map[string]string{"pageSessionID": "...", "brokerClientID": "..."}
	TopicClientRegistered = "_internal.client.registered"

	// TopicClientDeregistered is published when a client connection is deregistered from the broker.
	// Body: map[string]string{"brokerClientID": "..."}
	TopicClientDeregistered = "_internal.client.deregistered"
)


// Logger defines the interface for logging messages.
// This should be compatible with the Logger in the root of go-websocketmq.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// MessageHandler is a function type that processes messages.
// For requests, returning a non-nil *model.Message will be sent as a response.
// The sourceBrokerClientID is provided if the message originated from a known client connection.
type MessageHandler func(ctx context.Context, msg *model.Message, sourceBrokerClientID string) (*model.Message, error)

// ConnectionWriter defines an interface for writing messages to a specific connection.
// This is used by the broker to send messages to individual WebSocket clients.
type ConnectionWriter interface {
	WriteMessage(ctx context.Context, msg *model.Message) error
	BrokerClientID() string
	Close() error // Close the underlying connection
}

// Broker defines the interface for message routing.
type Broker interface {
	// Publish sends a message. If the message is a response/error, it's routed via
	// its CorrelationID. It's also dispatched to any server-side handlers subscribed
	// to its Topic.
	// For client-originated messages, SourceBrokerClientID in msg.Header should be set.
	Publish(ctx context.Context, msg *model.Message) error

	// Subscribe registers a handler for a specific topic.
	// The sourceBrokerClientID in the MessageHandler will be empty for messages not originating from a client connection.
	Subscribe(ctx context.Context, topic string, handler MessageHandler) error
	
	// Unsubscribe is not part of this iteration's interface for simplicity,
	// relying on context cancellation of subscriptions for cleanup.
	// Unsubscribe(ctx context.Context, topic string, handlerID interface{}) error

	// Request sends a request on a topic to a server-side handler and waits for a response.
	Request(ctx context.Context, req *model.Message, timeoutMs int64) (*model.Message, error)

	// RequestToClient sends a request message directly to a specific client identified by brokerClientID
	// and waits for a response. The req.Header.Topic should be the "action name" the client is listening to.
	RequestToClient(ctx context.Context, brokerClientID string, req *model.Message, timeoutMs int64) (*model.Message, error)

	// RegisterConnection informs the broker about a new client connection.
	// The broker will use the ConnectionWriter to send messages to this client.
	RegisterConnection(conn ConnectionWriter) error

	// DeregisterConnection informs the broker that a client connection has closed.
	DeregisterConnection(brokerClientID string) error

	// Close shuts down the broker and cleans up resources.
	Close() error
}

// Options configures the behavior of the message broker.
// This should be compatible with Options in the root of go-websocketmq.
type Options struct {
	QueueLength int
	// DefaultRequestTimeout is used if a request's TTL is not set or is zero.
	DefaultRequestTimeout time.Duration
}

// DefaultOptions returns default broker options.
// This should be compatible with DefaultOptions in the root of go-websocketmq.
func DefaultOptions() Options {
	return Options{
		QueueLength:           256, // Queue length for internal cskr/pubsub bus
		DefaultRequestTimeout: 10 * time.Second,
	}
}