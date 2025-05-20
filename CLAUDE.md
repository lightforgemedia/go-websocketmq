# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

go-websocketmq is a WebSocket-based messaging library implementing the publisher/subscriber pattern with additional RPC capabilities. It enables bidirectional messaging between server and clients (browsers, Go apps) with strong typing, connection management, and request-response patterns.

## Build and Test Commands

```bash
# Running the server example (creates a WebSocket server on port 8081)
go run cmd/server/main.go

# Running the client example (connects to the server)
go run cmd/client/main.go

# Running the JavaScript client example (creates WebSocket server with JS client)
go run examples/js_client/main.go

# Running the browser control example
go run examples/browser_control/main.go  # Start the broker server
go run examples/browser_control/cmd/control/main.go list  # List connected clients
go run examples/browser_control/cmd/control/main.go navigate <client-id> https://example.com  # Control browser

# Running tests
go test ./pkg/...                      # Run all tests in pkg directory
go test ./pkg/broker/...               # Test just the broker package
go test -v ./pkg/broker/...            # Verbose test output
go test -run TestBrokerPublishSubscribe ./pkg/broker/... # Run specific test
go test -race ./pkg/...                # Run tests with race detection

# Testing with browser integration
go test -v ./pkg/browser_client/browser_tests/...  # Browser client tests
go test -v ./pkg/hotreload/browser_tests/...       # Hot reload tests

# Building the project
go build ./cmd/...                     # Build all commands

# Linting
go fmt ./...                           # Format code
go vet ./...                           # Check for suspicious code
```

## Architecture

### Core Concepts

1. **Broker**: Central message broker that handles client connections, subscriptions, and message routing.
2. **Client**: Connects to the broker, can publish messages, subscribe to topics, and make requests.
3. **Topics**: Messages are routed based on topics (string identifiers).
4. **RPC**: Request-response pattern for both client-to-server and server-to-client communication.
5. **Proxy Requests**: Special topic that allows clients to communicate with each other through the broker.

### Key Components

1. **Broker** (`pkg/broker/`): Central component managing connections, messages, and request handling.
   - Handles WebSocket connections
   - Routes messages between clients
   - Manages topic subscriptions
   - Processes client requests

2. **Client** (`pkg/client/`): Go client for connecting to the broker.
   - Manages WebSocket connections
   - Provides typed request-response APIs
   - Handles reconnection and message handling

3. **Browser Client** (`pkg/browser_client/`): Embeddable JavaScript client for browsers.
   - Connects to WebSocket server
   - Provides publish/subscribe and request APIs
   - Handles automatic reconnection

4. **Hot Reload** (`pkg/hotreload/`): Development utility for automatic browser reloading.
   - Monitors file changes
   - Notifies connected browsers to reload
   - Includes JavaScript client integration

5. **Browser Control** (`examples/browser_control/`): Remote browser automation via WebSocketMQ.
   - Allows commanding browser clients from a control client
   - Supports navigation, alerts, and JavaScript execution
   - Uses the proxy request pattern

6. **Shared Types** (`pkg/shared_types/`): Common message types shared between client and server.

7. **Testing Utilities** (`pkg/testutil/`): Helper functions for testing the library.
   - Mock servers and clients
   - Rod integration for browser testing

### Communication Patterns

1. **Publish/Subscribe**: Clients subscribe to topics and receive messages published to those topics.
   - Server: `broker.Publish(ctx, topic, payload)`
   - Client: `client.Subscribe(topic, handler)`

2. **Request/Response**: Client sends request to server and receives typed response.
   - Client: `client.GenericRequest[ResponseType](cli, ctx, topic, requestPayload)`
   - Server: `broker.HandleClientRequest(topic, handlerFunc)`

3. **Server-to-Client Requests**: Server initiates requests to specific clients.
   - Server: `clientHandle.SendClientRequest(ctx, topic, request, &response, timeout)`
   - Client: `client.HandleServerRequest(topic, handlerFunc)`

4. **Proxy Requests**: Clients can send requests to other clients via broker.
   - Uses system:proxy topic with target client ID
   - Implemented in `GenericClientRequest`

## Configuration

The library uses the Options pattern for configuration (functional options were removed):

```go
// Server configuration
opts := broker.DefaultOptions()
opts.Logger = myLogger
opts.PingInterval = 15 * time.Second
opts.AcceptOptions = &websocket.AcceptOptions{OriginPatterns: []string{"localhost:*"}}
b, err := broker.NewWithOptions(opts)

// Client configuration
clientOpts := client.DefaultOptions()
clientOpts.AutoReconnect = true
clientOpts.ReconnectDelayMin = 1 * time.Second
clientOpts.ClientName = "MyService"
cli, err := client.ConnectWithOptions("ws://localhost:8081/ws", clientOpts)
```

### JavaScript Client Configuration

```javascript
// Create a client
const client = new WebSocketMQ.Client({
    url: 'ws://' + window.location.host + '/wsmq',
    clientName: 'BrowserApp',
    clientType: 'browser',
    autoReconnect: true
});

// Connect to the server
client.connect();
```

## Implementation Guidelines

1. **Use Options Pattern**: Always use the structured Options pattern for configuration, not functional options.

2. **Type Safety with Generics**: Use the generic request methods for type-safe RPC:
   ```go
   // Define response type
   type TimeResponse struct {
       CurrentTime string `json:"currentTime"`
   }
   
   // Make type-safe request
   resp, err := client.GenericRequest[TimeResponse](cli, ctx, "system:get_time", struct{}{})
   ```

3. **CORS Configuration**: Always set CORS origin patterns when creating a broker:
   ```go
   opts.AcceptOptions = &websocket.AcceptOptions{OriginPatterns: []string{"localhost:*"}}
   ```

4. **Request Timeouts**: Set appropriate timeouts for RPC requests to prevent hanging requests.
   ```go
   opts.ServerRequestTimeout = 10 * time.Second  // Default timeout for server-to-client requests
   ```

5. **Error Handling**: Always check for errors and handle them appropriately in both client and server code.

## Hot Reload Feature

The project includes a hot reload feature for development, which monitors file changes and automatically reloads connected browsers:

```go
// Create file watcher
fw, err := filewatcher.New(
    filewatcher.WithLogger(logger),
    filewatcher.WithDirs([]string{"./static"}),
    filewatcher.WithPatterns([]string{"*.html", "*.js", "*.css"}),
)

// Create hot reload service
hr, err := hotreload.New(
    hotreload.WithLogger(logger),
    hotreload.WithBroker(b),
    hotreload.WithFileWatcher(fw),
)

// Start the hot reload service
err := hr.Start()
defer hr.Stop()

// Register hot reload handlers
hr.RegisterHandlers(mux)
```

## Browser Control Feature

The project includes browser control capabilities that allow remote control of browser clients:

```go
// On control client:
resp, err := client.GenericRequest[proxyResponse](cli, ctx, "system:proxy", proxyRequest{
    TargetID: clientID,
    Topic:    "browser.navigate",
    Payload:  map[string]string{"url": targetURL},
})

// On browser client (JavaScript):
client.handleRequest('browser.navigate', function(request) {
    window.location.href = request.url;
    return { success: true };
});
```

## Testing with Real Browsers

The project includes browser integration tests using the Rod library. These tests require:

1. A working Chrome installation
2. Network access for WebSocket connections
3. Proper CORS configuration in test servers

```go
// Example browser test
func TestBrowserPublishSubscribe(t *testing.T) {
    srv := testutil.NewBrokerServer(t)
    defer srv.Close()
    
    page := testutil.NewRodPage(t)
    defer page.Close()
    
    // Navigate to test page
    err := page.Navigate(srv.URL)
    require.NoError(t, err)
    
    // Wait for client connection
    // Execute tests using JavaScript
    // Verify results
}
```

## Important Notes

1. The project uses Go generics for type-safe requests and responses.

2. Browser control features are available for remote browser automation.

3. Integration tests use real WebSockets and JavaScript clients.

4. Any server must properly handle CORS with `AcceptOptions.OriginPatterns`.

5. When making changes, follow the Options pattern, not functional options.

6. Broker automatically handles client disconnection and cleanup.

7. The library has reconnection capabilities for clients with exponential backoff.

8. Browser client is available via a JavaScript asset served by the broker.