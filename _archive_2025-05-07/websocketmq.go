// Package websocketmq provides a lightweight, embeddable WebSocket message queue for Go web applications.
//
// WebSocketMQ enables real-time communication between server and browser clients through WebSockets
// with publish/subscribe and request/response messaging patterns. It includes:
//   - An in-memory message broker (powered by github.com/cskr/pubsub)
//   - A WebSocket server handler (using nhooyr.io/websocket)
//   - An embedded JavaScript client (~10 KB minified)
//   - Development tools with hot-reload capability
//
// # Basic Usage
//
// Server-side:
//
//	// Create a broker
//	broker := websocketmq.NewPubSubBroker(logger, websocketmq.DefaultBrokerOptions())
//
//	// Create a WebSocket handler
//	handler := websocketmq.NewHandler(broker, logger, websocketmq.DefaultHandlerOptions())
//
//	// Set up HTTP routes
//	mux := http.NewServeMux()
//	mux.Handle("/ws", handler)
//	mux.Handle("/wsmq/", http.StripPrefix("/wsmq/", websocketmq.ScriptHandler()))
//
//	// Subscribe to a topic
//	broker.Subscribe(ctx, "client.message", func(ctx context.Context, m *websocketmq.Message) (*websocketmq.Message, error) {
//	    fmt.Printf("Received message: %v\n", m.Body)
//	    return nil, nil
//	})
//
//	// Start the server
//	log.Fatal(http.ListenAndServe(":3000", mux))
//
// Client-side:
//
//	<script src="/wsmq/websocketmq.min.js"></script>
//	<script>
//	    const client = new WebSocketMQ.Client({
//	        url: `ws://${window.location.host}/ws`,
//	        reconnect: true
//	    });
//
//	    client.onConnect(() => {
//	        // Publish a message
//	        client.publish("client.message", { text: "Hello, server!" });
//
//	        // Subscribe to a topic
//	        client.subscribe("server.message", (body) => {
//	            console.log("Received message:", body);
//	        });
//
//	        // Make a request
//	        client.sendServerRequest("server.echo", { text: "Echo this" }, 5000)
//	            .then(response => console.log("Response:", response))
//	            .catch(err => console.error("Request failed:", err));
//	    });
//
//	    client.connect();
//	</script>
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
	// Message represents a message that can be sent between clients and servers.
	// It contains a header with routing information and a body with the message payload.
	Message = model.Message

	// MessageHeader contains routing and metadata information for a message.
	// This includes the message ID, correlation ID, message type, topic, and timestamp.
	MessageHeader = model.MessageHeader

	// Broker defines the interface for message routing and distribution.
	// It provides methods for publishing messages, subscribing to topics, and
	// making request-response style calls.
	Broker = broker.Broker

	// Logger defines the interface for logging messages from the WebSocketMQ system.
	// Implement this interface to integrate with your application's logging system.
	Logger = broker.Logger

	// Handler is the WebSocket connection handler that processes incoming WebSocket
	// connections and messages.
	Handler = server.Handler

	// HandlerOptions configures the behavior of the WebSocket handler.
	// This includes security settings like maximum message size and allowed origins.
	HandlerOptions = server.HandlerOptions

	// BrokerOptions configures the behavior of the message broker.
	// This includes settings like queue lengths and topic capacity.
	BrokerOptions = broker.Options

	// WatchOptions configures the behavior of the development file watcher.
	// This includes settings for file patterns to watch and directories to exclude.
	WatchOptions = devwatch.WatchOptions
)

// NewEvent creates a new event message for one-way communication.
//
// Events are fire-and-forget messages that don't expect a response. They are
// used for broadcasting information to subscribers on a specific topic.
//
// Parameters:
//   - topic: The topic to publish the message to
//   - body: The message payload, which can be any JSON-serializable value
//
// Returns a new Message configured as an event.
//
// Example:
//
//	// Create and publish an event
//	eventMsg := websocketmq.NewEvent("user.login", map[string]interface{}{
//	    "username": "johndoe",
//	    "timestamp": time.Now().Unix(),
//	})
//	broker.Publish(ctx, eventMsg)
func NewEvent(topic string, body any) *Message {
	return model.NewEvent(topic, body)
}

// NewRequest creates a new request message for request-response communication.
//
// Requests expect a response from a handler subscribed to the specified topic.
// The timeout parameter controls how long to wait for a response before giving up.
//
// Parameters:
//   - topic: The topic to send the request to
//   - body: The request payload, which can be any JSON-serializable value
//   - timeoutMs: Timeout in milliseconds for waiting for a response
//
// Returns a new Message configured as a request.
//
// Example:
//
//	// Create and send a request, waiting for response
//	reqMsg := websocketmq.NewRequest("calculate.sum", []int{1, 2, 3}, 5000)
//	resp, err := broker.Request(ctx, reqMsg, 5000)
//	if err != nil {
//	    log.Printf("Request failed: %v", err)
//	    return
//	}
//	fmt.Printf("Sum: %v\n", resp.Body)
func NewRequest(topic string, body any, timeoutMs int64) *Message {
	return model.NewRequest(topic, body, timeoutMs)
}

// NewResponse creates a new response message for a received request.
//
// Responses are sent back to the originator of a request. The correlation ID
// from the request is used to match the response to the waiting request handler.
//
// Parameters:
//   - req: The original request message
//   - body: The response payload, which can be any JSON-serializable value
//
// Returns a new Message configured as a response to the given request.
//
// Example:
//
//	// Subscribe to a topic and handle requests
//	broker.Subscribe(ctx, "calculate.sum", func(ctx context.Context, m *websocketmq.Message) (*websocketmq.Message, error) {
//	    // Extract numbers from the request
//	    numbers, ok := m.Body.([]interface{})
//	    if !ok {
//	        return nil, fmt.Errorf("expected array of numbers")
//	    }
//
//	    // Calculate sum
//	    sum := 0.0
//	    for _, n := range numbers {
//	        if num, ok := n.(float64); ok {
//	            sum += num
//	        }
//	    }
//
//	    // Return response
//	    return websocketmq.NewResponse(m, sum), nil
//	})
func NewResponse(req *Message, body any) *Message {
	return model.NewResponse(req, body)
}

// DefaultHandlerOptions returns default options for the WebSocket handler.
//
// The default options include:
//   - MaxMessageSize: 1MB (1048576 bytes)
//   - AllowedOrigins: All origins allowed ("*")
//   - ReadTimeout: 5 seconds
//   - WriteTimeout: 5 seconds
//   - PingInterval: 30 seconds
//
// Example:
//
//	// Start with default options and customize
//	opts := websocketmq.DefaultHandlerOptions()
//	opts.MaxMessageSize = 512 * 1024  // 512KB
//	opts.AllowedOrigins = []string{"https://example.com"}
//	handler := websocketmq.NewHandler(broker, logger, opts)
func DefaultHandlerOptions() HandlerOptions {
	return server.DefaultHandlerOptions()
}

// DefaultBrokerOptions returns default options for the broker.
//
// The default options include:
//   - QueueLength: 100 messages per subscription
//
// Example:
//
//	// Start with default options and customize
//	opts := websocketmq.DefaultBrokerOptions()
//	opts.QueueLength = 500  // Increase queue capacity
//	broker := websocketmq.NewPubSubBroker(logger, opts)
func DefaultBrokerOptions() BrokerOptions {
	return broker.DefaultOptions()
}

// DefaultDevWatchOptions returns default options for the development watcher.
//
// The default options include file patterns to watch for changes and directories
// to exclude from watching.
//
// Example:
//
//	// Start with default options and customize
//	opts := websocketmq.DefaultDevWatchOptions()
//	opts.Patterns = append(opts.Patterns, "*.css")  // Also watch CSS files
//	stopWatcher, err := websocketmq.StartDevWatcher(ctx, broker, logger, opts)
func DefaultDevWatchOptions() WatchOptions {
	return devwatch.DefaultWatchOptions()
}

// NewPubSubBroker creates a new in-memory broker using the cskr/pubsub library.
//
// This is the default broker implementation that stores and routes messages in memory.
// It's suitable for single-server deployments or development.
//
// Parameters:
//   - logger: A logger implementing the Logger interface
//   - opts: Options to configure the broker behavior
//
// Returns a new Broker instance.
//
// Example:
//
//	// Create a simple logger implementation
//	type AppLogger struct{}
//	func (l *AppLogger) Debug(msg string, args ...any) { log.Printf("DEBUG: "+msg, args...) }
//	func (l *AppLogger) Info(msg string, args ...any)  { log.Printf("INFO: "+msg, args...) }
//	func (l *AppLogger) Warn(msg string, args ...any)  { log.Printf("WARN: "+msg, args...) }
//	func (l *AppLogger) Error(msg string, args ...any) { log.Printf("ERROR: "+msg, args...) }
//
//	// Create a broker with default options
//	logger := &AppLogger{}
//	broker := websocketmq.NewPubSubBroker(logger, websocketmq.DefaultBrokerOptions())
func NewPubSubBroker(logger Logger, opts BrokerOptions) Broker {
	return ps.New(logger, opts)
}

// NewHandler creates a new WebSocket handler for processing WebSocket connections.
//
// The handler implements http.Handler and can be directly mounted in an HTTP server.
// It manages WebSocket connections, authentication, and message handling.
//
// Parameters:
//   - b: A broker for message routing
//   - logger: A logger implementing the Logger interface
//   - opts: Options to configure the handler behavior
//
// Returns a new Handler instance.
//
// Example:
//
//	// Create a WebSocket handler with custom options
//	handlerOpts := websocketmq.DefaultHandlerOptions()
//	handlerOpts.MaxMessageSize = 512 * 1024  // Limit message size to 512KB
//	handlerOpts.AllowedOrigins = []string{"https://example.com"}  // Restrict to specific origin
//
//	handler := websocketmq.NewHandler(broker, logger, handlerOpts)
//
//	// Mount in an HTTP server
//	mux := http.NewServeMux()
//	mux.Handle("/ws", handler)
func NewHandler(b Broker, logger Logger, opts HandlerOptions) *Handler {
	return server.NewHandler(b, logger, opts)
}

// ScriptHandler returns an HTTP handler for serving the JavaScript client.
//
// This handler serves the embedded JavaScript client files (both unminified and minified)
// and handles content-type and caching headers appropriately.
//
// Returns an http.Handler that can be mounted in an HTTP server.
//
// Example:
//
//	// Mount the script handler at the /wsmq/ path
//	mux := http.NewServeMux()
//	mux.Handle("/wsmq/", http.StripPrefix("/wsmq/", websocketmq.ScriptHandler()))
//
//	// The client can then include the script with:
//	// <script src="/wsmq/websocketmq.min.js"></script>
func ScriptHandler() http.Handler {
	return assets.ScriptHandler()
}

// GetClientScript returns the JavaScript client as a byte slice.
//
// This function can be used if you need to serve the JavaScript client
// manually or include it in another asset pipeline.
//
// Parameters:
//   - minified: If true, returns the minified version; otherwise, returns the unminified version
//
// Returns the JavaScript client as a byte slice and any error encountered.
//
// Example:
//
//	// Get the minified JavaScript client
//	script, err := websocketmq.GetClientScript(true)
//	if err != nil {
//	    log.Printf("Failed to get client script: %v", err)
//	    return
//	}
//
//	// Use the script bytes as needed
//	ioutil.WriteFile("public/js/websocketmq.min.js", script, 0644)
func GetClientScript(minified bool) ([]byte, error) {
	return assets.GetClientScript(minified)
}

// StartDevWatcher starts a file watcher that publishes hot-reload events.
//
// The watcher monitors file changes and publishes events to the "_dev.hotreload" topic.
// Browsers connected with devMode enabled will automatically reload when these events
// are received.
//
// Parameters:
//   - ctx: A context for cancellation
//   - b: A broker for publishing events
//   - logger: A logger implementing the Logger interface
//   - opts: Options to configure the watcher behavior
//
// Returns a function to stop the watcher and any error encountered.
//
// Example:
//
//	// Start the development watcher with default options
//	stopWatcher, err := websocketmq.StartDevWatcher(context.Background(), broker, logger, websocketmq.DefaultDevWatchOptions())
//	if err != nil {
//	    log.Printf("Failed to start watcher: %v", err)
//	    return
//	}
//
//	// Stop the watcher when shutting down
//	defer stopWatcher()
func StartDevWatcher(ctx context.Context, b Broker, logger Logger, opts WatchOptions) (func() error, error) {
	return devwatch.StartWatcher(ctx, b, logger, opts)
}
