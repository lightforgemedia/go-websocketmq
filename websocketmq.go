// websocketmq.go
package websocketmq

import (
	"context"
	"net/http"

	"github.com/lightforgemedia/go-websocketmq/assets"
	"github.com/lightforgemedia/go-websocketmq/internal/devwatch"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"github.com/lightforgemedia/go-websocketmq/pkg/server"
)

//go:generate go run ./internal/buildjs/main.go

// Re-export types and functions from subpackages
type (
	Message        = model.Message
	MessageHeader  = model.MessageHeader
	Broker         = broker.Broker
	Logger         = broker.Logger
	Handler        = server.Handler
	HandlerOptions = server.HandlerOptions
	BrokerOptions  = broker.Options
	WatchOptions   = devwatch.WatchOptions
)

// NewEvent creates a new event message
func NewEvent(topic string, body any) *Message {
	return model.NewEvent(topic, body)
}

// NewRequest creates a new request message
func NewRequest(topic string, body any, timeoutMs int64) *Message {
	return model.NewRequest(topic, body, timeoutMs)
}

// NewResponse creates a new response message
func NewResponse(req *Message, body any) *Message {
	return model.NewResponse(req, body)
}

// DefaultHandlerOptions returns default options for the WebSocket handler
func DefaultHandlerOptions() HandlerOptions {
	return server.DefaultHandlerOptions()
}

// DefaultBrokerOptions returns default options for the broker
func DefaultBrokerOptions() BrokerOptions {
	return broker.DefaultOptions()
}

// DefaultDevWatchOptions returns default options for the development watcher
func DefaultDevWatchOptions() WatchOptions {
	return devwatch.DefaultWatchOptions()
}

// NewPubSubBroker creates a new in-memory broker using pubsub
func NewPubSubBroker(logger Logger, opts BrokerOptions) Broker {
	return ps.New(logger, opts)
}

// NewHandler creates a new WebSocket handler
func NewHandler(b Broker, logger Logger, opts HandlerOptions) *Handler {
	return server.NewHandler(b, logger, opts)
}

// ScriptHandler returns an HTTP handler for serving the JavaScript client
func ScriptHandler() http.Handler {
	return assets.ScriptHandler()
}

// GetClientScript returns the JavaScript client as a byte slice
func GetClientScript(minified bool) ([]byte, error) {
	return assets.GetClientScript(minified)
}

// StartDevWatcher starts a file watcher that publishes hot-reload events
func StartDevWatcher(ctx context.Context, b Broker, logger Logger, opts WatchOptions) (func() error, error) {
	return devwatch.StartWatcher(ctx, b, logger, opts)
}