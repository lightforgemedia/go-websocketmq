# WebSocketMQ (Go)

Lightweight, embeddable **WebSocket message‑queue** for Go web apps.

* **Single static binary** – pure‑Go back‑end, no external broker required.
* **Pluggable broker** – default in‑memory fan‑out powered by [`cskr/pubsub`].  Replace with NATS/Redis later via the `Broker` interface.
* **Tiny JS client** – `< 10 kB` min‑gzip, universal `<script>` drop‑in.
* **Dev hot‑reload** – built‑in file watcher publishes `_dev.hotreload`, causing connected browsers to reload and funnel JS errors back to the Go log.

```bash
# quick demo
go run ./examples/simple
```

## Features

- **Application Messaging:** Topic-based publish/subscribe, request/response patterns (client-server & server-client).
- **Developer Experience:** Optional built-in hot-reloading for web development (auto-refresh on file changes, JS error reporting back to the server).
- **Extensibility:** A pluggable broker interface allowing future replacement of the default in-memory broker with systems like NATS without altering the core WebSocket handling or client code.
- **Security:** Configurable message size limits and origin restrictions for production use.

## Installation

```bash
go get github.com/lightforgemedia/go-websocketmq
```

## Quick Start

### Server-side (Go)

```go
package main

import (
    "context"
    "log"
    "net/http"

    "github.com/lightforgemedia/go-websocketmq"
)

// Simple logger implementation
type Logger struct{}

func (l *Logger) Debug(msg string, args ...any) { log.Printf("DEBUG: "+msg, args...) }
func (l *Logger) Info(msg string, args ...any)  { log.Printf("INFO: "+msg, args...) }
func (l *Logger) Warn(msg string, args ...any)  { log.Printf("WARN: "+msg, args...) }
func (l *Logger) Error(msg string, args ...any) { log.Printf("ERROR: "+msg, args...) }

func main() {
    logger := &Logger{}
    
    // Create a broker
    brokerOpts := websocketmq.DefaultBrokerOptions()
    broker := websocketmq.NewPubSubBroker(logger, brokerOpts)
    
    // Create a WebSocket handler with security options
    handlerOpts := websocketmq.DefaultHandlerOptions()
    handlerOpts.MaxMessageSize = 1024 * 1024 // 1MB limit
    handlerOpts.AllowedOrigins = []string{"https://example.com"} // Restrict to specific origins
    handler := websocketmq.NewHandler(broker, logger, handlerOpts)
    
    // Set up HTTP routes
    mux := http.NewServeMux()
    mux.Handle("/ws", handler)
    mux.Handle("/wsmq/", http.StripPrefix("/wsmq/", websocketmq.ScriptHandler()))
    mux.Handle("/", http.FileServer(http.Dir("static")))
    
    // Subscribe to a topic
    broker.Subscribe(context.Background(), "user.login", func(ctx context.Context, m *websocketmq.Message) (*websocketmq.Message, error) {
        logger.Info("User logged in: %v", m.Body)
        return nil, nil
    })
    
    // Start the server
    logger.Info("Server starting on :8080")
    log.Fatal(http.ListenAndServe(":8080", mux))
}
```

### Client-side (JavaScript)

```html
<!DOCTYPE html>
<html>
<head>
    <title>WebSocketMQ Example</title>
</head>
<body>
    <h1>WebSocketMQ Example</h1>
    
    <script src="/wsmq/websocketmq.min.js"></script>
    <script>
        // Initialize client
        const client = new WebSocketMQ.Client({
            url: `ws://${window.location.host}/ws`,
            reconnect: true,
            devMode: true // Enable for hot-reload and error reporting
        });
        
        client.onConnect(() => {
            console.log('Connected to server');
            
            // Publish a message
            client.publish('user.login', { username: 'test' });
            
            // Subscribe to a topic
            client.subscribe('server.tick', (body) => {
                console.log('Received tick:', body);
            });
            
            // Make a request
            client.request('server.echo', { message: 'Hello, server!' }, 5000)
                .then(response => {
                    console.log('Server response:', response);
                })
                .catch(err => {
                    console.error('Request failed:', err);
                });
        });
        
        client.onDisconnect(() => {
            console.log('Disconnected from server');
        });
        
        client.onError((err) => {
            console.error('WebSocket error:', err);
        });
        
        // Connect to the server
        client.connect();
    </script>
</body>
</html>
```

## API Reference

### Go API

#### Broker

```go
// Create an in-memory broker
brokerOpts := websocketmq.DefaultBrokerOptions()
broker := websocketmq.NewPubSubBroker(logger, brokerOpts)

// Create a NATS broker
natsOpts := websocketmq.DefaultNATSBrokerOptions()
broker, err := websocketmq.NewNATSBroker(logger, natsOpts)

// Publish a message
msg := websocketmq.NewEvent("topic.name", map[string]any{
    "key": "value",
})
broker.Publish(context.Background(), msg)

// Subscribe to a topic
broker.Subscribe(context.Background(), "topic.name", func(ctx context.Context, m *websocketmq.Message) (*websocketmq.Message, error) {
    // Handle message
    return nil, nil
})

// Make a request
response, err := broker.Request(context.Background(), requestMsg, 5000)
```

#### WebSocket Handler

```go
// Create a WebSocket handler with security options
handlerOpts := websocketmq.DefaultHandlerOptions()
handlerOpts.MaxMessageSize = 1024 * 1024 // 1MB limit
handlerOpts.AllowedOrigins = []string{"https://example.com"} // Restrict to specific origins
handler := websocketmq.NewHandler(broker, logger, handlerOpts)

// Mount the handler
mux.Handle("/ws", handler)
```

#### JavaScript Client Handler

```go
// Serve the JavaScript client
mux.Handle("/wsmq/", http.StripPrefix("/wsmq/", websocketmq.ScriptHandler()))
```

#### Development Watcher

```go
// Start development watcher
watchOpts := websocketmq.DefaultDevWatchOptions()
stopWatcher, err := websocketmq.StartDevWatcher(context.Background(), broker, logger, watchOpts)
if err != nil {
    // Handle error
}
defer stopWatcher()
```

### JavaScript API

#### Client

```javascript
// Create a client
const client = new WebSocketMQ.Client({
    url: 'ws://localhost:8080/ws',
    reconnect: true,
    reconnectInterval: 1000,
    maxReconnectInterval: 30000,
    reconnectMultiplier: 1.5,
    devMode: false
});

// Connect
client.connect();

// Disconnect
client.disconnect();

// Event handlers
client.onConnect(callback);
client.onDisconnect(callback);
client.onError(callback);
```

#### Messaging

```javascript
// Publish a message
client.publish('topic.name', { key: 'value' });

// Subscribe to a topic
const unsubscribe = client.subscribe('topic.name', (body, message) => {
    // Handle message
});

// Unsubscribe
unsubscribe();

// Make a request
client.request('topic.name', { key: 'value' }, 5000)
    .then(response => {
        // Handle response
    })
    .catch(err => {
        // Handle error
    });
```

## Development Mode

WebSocketMQ includes built-in development features to improve the developer experience:

1. **Hot Reload**: When files are changed, the watcher publishes a message to the `_dev.hotreload` topic, causing connected browsers to reload.

2. **JavaScript Error Reporting**: In development mode, the JavaScript client catches errors and unhandled promise rejections and reports them back to the server via the `_dev.js-error` topic.

To enable development mode:

1. Server-side:
```go
watchOpts := websocketmq.DefaultDevWatchOptions()
stopWatcher, err := websocketmq.StartDevWatcher(context.Background(), broker, logger, watchOpts)
```

2. Client-side:
```javascript
const client = new WebSocketMQ.Client({
    url: 'ws://localhost:8080/ws',
    devMode: true
});
```

## Broker Implementations

WebSocketMQ supports multiple broker implementations:

### In-Memory Broker

The default in-memory broker uses the `cskr/pubsub` package for message routing. It's suitable for single-server deployments and doesn't require any external dependencies.

```go
brokerOpts := websocketmq.DefaultBrokerOptions()
broker := websocketmq.NewPubSubBroker(logger, brokerOpts)
```

### NATS Broker

The NATS broker uses the NATS messaging system for message routing. It's suitable for distributed deployments and requires a running NATS server.

```go
natsOpts := websocketmq.DefaultNATSBrokerOptions()
natsOpts.URL = "nats://localhost:4222" // NATS server URL
broker, err := websocketmq.NewNATSBroker(logger, natsOpts)
```

## Examples

See the `examples/` directory for complete examples:

- `examples/simple`: A simple example showing basic usage of WebSocketMQ with the in-memory broker.
- `examples/nats`: An example showing how to use WebSocketMQ with a NATS broker.

### Running the NATS Example

To run the NATS example, you need to have a NATS server running. You can start a NATS server using Docker:

```bash
docker run -d --name nats-server -p 4222:4222 -p 8222:8222 -p 6222:6222 nats
```

Then run the example:

```bash
go run ./examples/nats
```

## License

MIT
