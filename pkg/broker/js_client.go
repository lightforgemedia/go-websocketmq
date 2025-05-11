// pkg/broker/js_client.go
package broker

import (
	"net/http"

	"github.com/lightforgemedia/go-websocketmq/pkg/browser_client"
)

// WebSocketMQHandlerOptions configures the WebSocketMQ HTTP handlers
type WebSocketMQHandlerOptions struct {
	// WebSocketPath is the URL path where the WebSocket endpoint will be served
	// Default: "/wsmq"
	WebSocketPath string

	// JavaScriptClientOptions configures the JavaScript client handler
	// If nil, default options will be used
	JavaScriptClientOptions *browser_client.ClientScriptOptions
}

// DefaultWebSocketMQHandlerOptions returns the default options for the WebSocketMQ HTTP handlers
func DefaultWebSocketMQHandlerOptions() WebSocketMQHandlerOptions {
	return WebSocketMQHandlerOptions{
		WebSocketPath:           "/wsmq",
		JavaScriptClientOptions: nil, // Will use defaults
	}
}

// RegisterHandlers registers both the WebSocket handler and JavaScript client handler
// with the provided ServeMux. This is the recommended way to set up WebSocketMQ
// in your HTTP server.
//
// The WebSocket endpoint will be served at the specified path (default: "/wsmq").
// The JavaScript client will be served at the path specified in the options
// (default: "/websocketmq.js").
//
// Example:
//
//	mux := http.NewServeMux()
//	broker.RegisterHandlers(mux, broker.DefaultWebSocketMQHandlerOptions())
//
// Or with custom options:
//
//	options := broker.DefaultWebSocketMQHandlerOptions()
//	options.WebSocketPath = "/api/websocket"
//	jsOptions := browser_client.DefaultClientScriptOptions()
//	jsOptions.Path = "/js/websocketmq.js"
//	options.JavaScriptClientOptions = &jsOptions
//	broker.RegisterHandlers(mux, options)
func (b *Broker) RegisterHandlers(mux *http.ServeMux, options WebSocketMQHandlerOptions) {
	// Register WebSocket handler
	mux.Handle(options.WebSocketPath, b.UpgradeHandler())
	b.config.logger.Info("Broker: Registered WebSocket handler at " + options.WebSocketPath)

	// Register JavaScript client handler
	jsOptions := browser_client.DefaultClientScriptOptions()
	if options.JavaScriptClientOptions != nil {
		jsOptions = *options.JavaScriptClientOptions
	}
	browser_client.RegisterHandler(mux, jsOptions)
	b.config.logger.Info("Broker: Registered JavaScript client handler at " + jsOptions.Path)
}

// RegisterHandlersWithDefaults registers both the WebSocket handler and JavaScript client handler
// with the provided ServeMux using default options.
//
// This is a convenience function that uses the default options for both handlers.
// The WebSocket endpoint will be served at "/wsmq".
// The JavaScript client will be served at "/websocketmq.js".
//
// Example:
//
//	mux := http.NewServeMux()
//	broker.RegisterHandlersWithDefaults(mux)
func (b *Broker) RegisterHandlersWithDefaults(mux *http.ServeMux) {
	b.RegisterHandlers(mux, DefaultWebSocketMQHandlerOptions())
}
