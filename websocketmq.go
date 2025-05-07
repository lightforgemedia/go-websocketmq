// websocketmq.go
package websocketmq

import (
	"context"
	"net/http"
	"time"

	"github.com/lightforgemedia/go-websocketmq/assets"
	"github.com/lightforgemedia/go-websocketmq/internal/devwatch"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"github.com/lightforgemedia/go-websocketmq/pkg/server"
)

//go:generate go run ./internal/buildjs/main.go

// Re-export core types
type (
	Message        = model.Message
	MessageHeader  = model.MessageHeader
	Broker         = broker.Broker
	Logger         = broker.Logger
	MessageHandler = broker.MessageHandler // New: Expose the server-side handler type
	ConnectionWriter = broker.ConnectionWriter // New: Expose connection writer interface
	WebSocketHandler = server.Handler // Renamed from Handler for clarity
	HandlerOptions = server.HandlerOptions
	BrokerOptions  = broker.Options
	WatchOptions   = devwatch.WatchOptions
)

// Re-export error types
var (
	ErrClientNotFound  = broker.ErrClientNotFound
	ErrRequestTimeout  = broker.ErrRequestTimeout
	ErrConnectionWrite = broker.ErrConnectionWrite
	ErrBrokerClosed    = broker.ErrBrokerClosed
	ErrInvalidMessage  = broker.ErrInvalidMessage
)

// Re-export constants for internal broker events (application might want to listen)
const (
	TopicClientRegistered   = broker.TopicClientRegistered
	TopicClientDeregistered = broker.TopicClientDeregistered
)


// NewEvent creates a new event message.
func NewEvent(topic string, body any) *model.Message {
	return model.NewEvent(topic, body)
}

// NewRequest creates a new request message.
// For server-to-client RPC, 'topic' will be the action/procedure name.
func NewRequest(topic string, body any, timeoutMs int64) *model.Message {
	return model.NewRequest(topic, body, timeoutMs)
}

// NewResponse creates a new response message for a received request.
func NewResponse(req *model.Message, body any) *model.Message {
	return model.NewResponse(req, body)
}

// NewErrorMessage creates a new error response message.
func NewErrorMessage(req *model.Message, errorBody any) *model.Message {
	return model.NewErrorMessage(req, errorBody)
}


// DefaultHandlerOptions returns default options for the WebSocket handler.
func DefaultHandlerOptions() server.HandlerOptions { // Return concrete type
	return server.DefaultHandlerOptions()
}

// DefaultBrokerOptions returns default options for the broker.
func DefaultBrokerOptions() broker.Options { // Return concrete type
	return broker.DefaultOptions()
}

// DefaultDevWatchOptions returns default options for the development watcher.
func DefaultDevWatchOptions() devwatch.WatchOptions { // Return concrete type
	return devwatch.DefaultWatchOptions()
}

// NewPubSubBroker creates a new in-memory broker.
func NewPubSubBroker(logger broker.Logger, opts broker.Options) broker.Broker { // Return interface type
	return ps.New(logger, opts)
}

// NewWebSocketHandler creates a new WebSocket connection handler.
func NewWebSocketHandler(b broker.Broker, logger broker.Logger, opts server.HandlerOptions) *server.Handler { // Return concrete type
	return server.NewHandler(b, logger, opts)
}

// ScriptHandler serves the embedded JavaScript client.
func ScriptHandler() http.Handler {
	return assets.ScriptHandler()
}

// GetClientScript returns the JavaScript client as a byte slice.
func GetClientScript(minified bool) ([]byte, error) {
	return assets.GetClientScript(minified)
}

// StartDevWatcher starts a file watcher for hot-reload events.
func StartDevWatcher(ctx context.Context, b broker.Broker, logger broker.Logger, opts devwatch.WatchOptions) (func() error, error) {
	return devwatch.StartWatcher(ctx, b, logger, opts)
}