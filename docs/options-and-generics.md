# Using Options Pattern and GenericClientRequest in WebSocketMQ

## Overview

WebSocketMQ provides two powerful features that make it easier to configure and use the library:

1. **Options Pattern**: A structured way to configure the broker with default values
2. **GenericClientRequest**: A type-safe helper for making client-to-client requests

## Options Pattern

The Options pattern provides a cleaner way to configure the broker with multiple settings at once.

### Basic Usage

```go
// Get default options
opts := broker.DefaultOptions()

// Customize the options
opts.Logger = slog.New(slog.NewTextHandler(os.Stdout, nil))
opts.PingInterval = 15 * time.Second
opts.ClientSendBuffer = 32
opts.AcceptOptions = &websocket.AcceptOptions{
    OriginPatterns: []string{"*"},
}

// Create broker with options
broker, err := broker.NewWithOptions(opts)
```

### Available Options

The `Options` struct includes:

- **Logger**: Structured logger for the broker
- **AcceptOptions**: WebSocket accept configuration
- **ClientSendBuffer**: Size of the send buffer for each client
- **WriteTimeout**: Timeout for writing messages to clients
- **ReadTimeout**: Timeout for reading messages from clients  
- **PingInterval**: Interval for sending ping messages
- **ServerRequestTimeout**: Default timeout for server-initiated requests

## GenericClientRequest

The `GenericClientRequest` function provides a type-safe way to make requests from the broker to clients.

### Basic Usage

```go
// Define your request and response types
type CalculateRequest struct {
    A int `json:"a"`
    B int `json:"b"`
}

type CalculateResponse struct {
    Result int `json:"result"`
}

// Make a type-safe request to a client
response, err := broker.GenericClientRequest[CalculateResponse](
    clientHandle,
    ctx,
    "calculate",
    CalculateRequest{A: 10, B: 20},
    5*time.Second,
)

if err != nil {
    log.Printf("Request failed: %v", err)
    return
}

fmt.Printf("Result: %d\n", response.Result)
```

### Proxy Requests Between Clients

GenericClientRequest is particularly useful for proxy requests:

```go
// Send a proxy request from one client to another
proxyReq := shared_types.ProxyRequest{
    TargetID: targetClientID,
    Topic:    "browser.navigate",
    Payload:  json.RawMessage(`{"url": "https://example.com"}`),
}

// Use GenericClientRequest with raw JSON response
response, err := broker.GenericClientRequest[json.RawMessage](
    controlClientHandle,
    ctx,
    "system:proxy",
    proxyReq,
    5*time.Second,
)
```

### Error Handling

GenericClientRequest provides clear error handling:

```go
response, err := broker.GenericClientRequest[MyResponse](
    clientHandle,
    ctx,
    "my.topic",
    myRequest,
    5*time.Second,
)

if err != nil {
    switch {
    case strings.Contains(err.Error(), "timeout"):
        // Handle timeout
    case strings.Contains(err.Error(), "not found"):
        // Handle missing handler
    default:
        // Handle other errors
    }
}
```

## Example: Browser Control with Options and GenericClientRequest

Here's a complete example showing both features in action:

```go
func main() {
    // Configure broker with Options pattern
    opts := broker.DefaultOptions()
    opts.Logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
        Level: slog.LevelDebug,
    }))
    opts.PingInterval = 15 * time.Second
    opts.AcceptOptions = &websocket.AcceptOptions{
        OriginPatterns: []string{"*"},
    }

    // Create broker
    b, err := broker.NewWithOptions(opts)
    if err != nil {
        log.Fatal(err)
    }

    // Register handlers
    b.HandleClientRequest("browser.control", handleBrowserControl)

    // ... setup HTTP server ...

    // Later, use GenericClientRequest for type-safe proxy requests
    var clientHandle broker.ClientHandle
    b.IterateClients(func(ch broker.ClientHandle) bool {
        if ch.ClientType() == "controller" {
            clientHandle = ch
            return false
        }
        return true
    })

    // Send command to browser through controller
    resp, err := broker.GenericClientRequest[BrowserResponse](
        clientHandle,
        context.Background(),
        "browser.navigate",
        BrowserNavigateRequest{URL: "https://example.com"},
        5*time.Second,
    )
    
    if err != nil {
        log.Printf("Navigation failed: %v", err)
        return
    }
    
    log.Printf("Navigation successful: %+v", resp)
}
```

## Benefits

### Options Pattern Benefits

1. **Cleaner Configuration**: Group related settings together
2. **Default Values**: Get sensible defaults with `DefaultOptions()`
3. **Validation**: Easier to validate settings before creating the broker
4. **Extensibility**: Add new options without breaking existing code

### GenericClientRequest Benefits

1. **Type Safety**: Compile-time type checking for requests and responses
2. **Less Boilerplate**: No manual JSON marshaling/unmarshaling
3. **Better Error Handling**: Clear error messages with context
4. **IDE Support**: Auto-completion and type hints

## Testing with Options and GenericClientRequest

Both features make testing cleaner and more maintainable:

```go
func TestBrowserProxyControl(t *testing.T) {
    // Setup broker with test configuration
    opts := broker.DefaultOptions()
    opts.Logger = testLogger
    opts.PingInterval = 100 * time.Millisecond // Faster for tests
    
    b, err := broker.NewWithOptions(opts)
    require.NoError(t, err)
    
    // ... setup test clients ...
    
    // Test proxy request
    req := ProxyRequest{
        TargetID: browserClient.ID,
        Topic:    "browser.alert",  
        Payload:  json.RawMessage(`{"message": "Test alert"}`),
    }
    
    resp, err := broker.GenericClientRequest[json.RawMessage](
        controlClient,
        ctx,
        "system:proxy",
        req,
        5*time.Second,
    )
    
    require.NoError(t, err)
    // ... assert response ...
}
```

## Migration Guide

The functional options pattern has been removed. Use the Options pattern exclusively:

```go
// Create broker with Options pattern
opts := broker.DefaultOptions()
opts.Logger = logger
opts.PingInterval = 15 * time.Second
opts.ClientSendBuffer = 32

broker, err := broker.NewWithOptions(opts)
```

For client requests:

```go
// Old way
var resp MyResponse
err := clientHandle.SendClientRequest(ctx, "topic", req, &resp, 5*time.Second)

// New way (with GenericClientRequest)
resp, err := broker.GenericClientRequest[MyResponse](
    clientHandle, ctx, "topic", req, 5*time.Second)
```

The Options pattern is now the only supported way to configure the broker.