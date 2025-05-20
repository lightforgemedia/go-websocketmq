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

# Running tests
go test ./pkg/...                      # Run all tests in pkg directory
go test ./pkg/broker/...               # Test just the broker package
go test -v ./pkg/broker/...            # Verbose test output
go test -run TestBrokerPublishSubscribe ./pkg/broker/... # Run specific test
go test -race ./pkg/...                # Run tests with race detection

# Testing with browser integration
go test -v ./pkg/browser_client/browser_tests/...
go test -v ./pkg/hotreload/browser_tests/...

# Building the project
go build ./cmd/...                     # Build all commands

# Linting
# The project uses Go's standard formatting
go fmt ./...                           # Format code
go vet ./...                           # Check for suspicious code
```

## Architecture

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

4. **Shared Types** (`pkg/shared_types/`): Common message types shared between client and server.

5. **Testing Utilities** (`pkg/testutil/`): Helper functions for testing the library.

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

## Configuration

The library uses the Options pattern for configuration. Functional options were removed in favor of structured Options objects:

```go
// Server configuration
opts := broker.DefaultOptions()
opts.Logger = myLogger
opts.PingInterval = 15 * time.Second
b, err := broker.NewWithOptions(opts)

// Client configuration
clientOpts := client.DefaultOptions()
clientOpts.AutoReconnect = true
clientOpts.ReconnectDelayMin = 1 * time.Second
cli, err := client.ConnectWithOptions("ws://localhost:8080/ws", clientOpts)
```

## Important Notes

1. The project uses Go generics for type-safe requests and responses.

2. Browser control features are available for remote browser automation.

3. Integration tests use real WebSockets and JavaScript clients.

4. Any server must properly handle CORS with `AcceptOptions.OriginPatterns`.

5. When making changes, follow the existing Options pattern, not the removed functional options approach.

6. Broker automatically handles client disconnection and cleanup.

7. The library has reconnection capabilities for clients with exponential backoff.

8. Browser client is available via a JavaScript asset served by the broker.