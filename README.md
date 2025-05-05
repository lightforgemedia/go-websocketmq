# WebSocketMQ (Go)

Lightweight, embeddable **WebSocket message‑queue** for Go web apps.

* **Single static binary** – pure‑Go back‑end, no external broker required.
* **Pluggable broker** – default in‑memory fan‑out powered by [`cskr/pubsub`]. Replace with NATS later via the `Broker` interface.
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
    logger.Info("Server starting on :3000")
    log.Fatal(http.ListenAndServe(":3000", mux))
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
    url: 'ws://localhost:3000/ws',
    devMode: true
});
```

## Development Server

For quick development, use the built-in dev server:

```bash
go run ./cmd/devserver
```

This starts a server on port 3000 with hot-reload enabled.

## Examples

See the `examples/` directory for complete examples:

- `examples/simple`: A simple example showing basic usage of WebSocketMQ with the in-memory broker.

## License

MIT

[`cskr/pubsub`]: https://github.com/cskr/pubsub