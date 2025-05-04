// Package websocketmq provides a lightweight, embeddable WebSocket message-queue for Go web apps.
//
// It enables real-time, bidirectional messaging between Go web applications and browser clients
// using WebSockets with no external dependencies for its core functionality.
//
//go:generate go run ./internal/buildjs
package websocketmq

import (
	"context"
	"net/http"

	"github.com/lightforgemedia/go-websocketmq/assets"
	"github.com/lightforgemedia/go-websocketmq/internal/devwatch"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker/nats"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"github.com/lightforgemedia/go-websocketmq/pkg/server"
	natspkg "github.com/nats-io/nats.go"
)

// Logger defines the interface for logging within the websocketmq package.
// This allows users to provide their own logging implementation.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// HandlerOptions contains configuration options for the WebSocket handler.
type HandlerOptions struct {
	// MaxMessageSize is the maximum size of a message in bytes.
	// Default: 1MB
	MaxMessageSize int64

	// AllowedOrigins is a list of allowed origins for WebSocket connections.
	// If empty, all origins are allowed (not recommended for production).
	AllowedOrigins []string

	// DevMode enables development features like hot-reload.
	DevMode bool
}

// DefaultHandlerOptions returns the default options for the WebSocket handler.
func DefaultHandlerOptions() HandlerOptions {
	return HandlerOptions{
		MaxMessageSize: 1024 * 1024, // 1MB
		AllowedOrigins: []string{},  // Empty means all origins allowed
		DevMode:        false,
	}
}

// BrokerOptions contains configuration options for the broker.
type BrokerOptions struct {
	// QueueLength is the length of the message queue for each subscription.
	// Default: 128
	QueueLength int
}

// DefaultBrokerOptions returns the default options for the broker.
func DefaultBrokerOptions() BrokerOptions {
	return BrokerOptions{
		QueueLength: 128,
	}
}

// NATSBrokerOptions contains configuration options for the NATS broker.
type NATSBrokerOptions struct {
	// URL is the NATS server URL.
	// Default: nats://localhost:4222
	URL string

	// QueueName is the name of the queue group to use for subscriptions.
	// If empty, a unique queue name will be generated.
	QueueName string

	// ConnectionOptions are additional options for the NATS connection.
	ConnectionOptions []natspkg.Option
}

// DefaultNATSBrokerOptions returns the default options for the NATS broker.
func DefaultNATSBrokerOptions() NATSBrokerOptions {
	return NATSBrokerOptions{
		URL:               natspkg.DefaultURL,
		QueueName:         "",
		ConnectionOptions: []natspkg.Option{},
	}
}

// NewPubSubBroker creates a new in-memory broker using the cskr/pubsub package.
func NewPubSubBroker(logger Logger, opts BrokerOptions) broker.Broker {
	return ps.New(opts.QueueLength)
}

// NewNATSBroker creates a new NATS broker.
func NewNATSBroker(logger Logger, opts NATSBrokerOptions) (broker.Broker, error) {
	return nats.New(nats.Options{
		URL:               opts.URL,
		QueueName:         opts.QueueName,
		ConnectionOptions: opts.ConnectionOptions,
	})
}

// NewHandler creates a new WebSocket handler that upgrades HTTP connections to WebSocket
// and handles message routing through the provided broker.
func NewHandler(b broker.Broker, logger Logger, opts HandlerOptions) http.Handler {
	return server.New(b)
}

// Message is a convenience type alias for model.Message
type Message = model.Message

// NewEvent creates a new event message for the specified topic with the given body.
func NewEvent(topic string, body any) *Message {
	return model.NewEvent(topic, body)
}

// ScriptHandler returns an http.Handler that serves the embedded JavaScript client.
// This handler should be mounted at a URL path that is accessible to browser clients.
//
// Example:
//
//	mux.Handle("/wsmq/", http.StripPrefix("/wsmq/", websocketmq.ScriptHandler()))
func ScriptHandler() http.Handler {
	return assets.Handler()
}

// DevWatchOptions contains configuration options for the development file watcher.
type DevWatchOptions struct {
	// Paths is a list of file paths or directories to watch for changes.
	Paths []string

	// Extensions is a list of file extensions to watch for changes.
	// If empty, all files are watched.
	Extensions []string

	// IgnorePaths is a list of paths to ignore.
	IgnorePaths []string
}

// DefaultDevWatchOptions returns the default options for the development file watcher.
func DefaultDevWatchOptions() DevWatchOptions {
	return DevWatchOptions{
		Paths:      []string{"."},
		Extensions: []string{".html", ".css", ".js"},
		IgnorePaths: []string{
			"node_modules",
			".git",
			"vendor",
		},
	}
}

// StartDevWatcher starts a file watcher that publishes hot-reload events to the broker.
// This is intended for development use only and should not be used in production.
//
// Returns a function that can be called to stop the watcher.
func StartDevWatcher(ctx context.Context, b broker.Broker, logger Logger, opts DevWatchOptions) (func(), error) {
	watcher, err := devwatch.New(b, logger, devwatch.Options{
		Paths:       opts.Paths,
		Extensions:  opts.Extensions,
		IgnorePaths: opts.IgnorePaths,
	})
	if err != nil {
		return nil, err
	}

	if err := watcher.Start(ctx); err != nil {
		return nil, err
	}

	return func() {
		watcher.Stop()
	}, nil
}
