# WebSocketMQ Library Onboarding Guide

## Introduction

WebSocketMQ is a Go library that provides a robust, bidirectional messaging system over WebSockets. It implements a publish-subscribe pattern with RPC capabilities, allowing for both client-to-server and server-to-client communication. This document will guide you through the core concepts, architecture, and usage patterns of the library.

## Architecture Overview

WebSocketMQ consists of several key components that work together to provide a complete messaging solution:

```
+----------------+                                  +----------------+
|                |                                  |                |
|     Client     |                                  |     Server     |
|                |                                  |                |
+-------+--------+                                  +--------+-------+
        |                                                    |
        |                WebSocket Connection                |
        |<-------------------------------------------------->|
        |                                                    |
        |                                                    |
        |                                                    |
+-------v--------+                                  +--------v-------+
|                |                                  |                |
|  Client-side   |                                  |   Broker      |
|  Handlers      |                                  |               |
|                |                                  |   +--------+  |
|  - Subscribe   |                                  |   |        |  |
|  - Publish     |                                  |   | Topics |  |
|  - Request     |                                  |   |        |  |
|                |                                  |   +--------+  |
|                |                                  |               |
|                |                                  |   +--------+  |
|                |                                  |   |        |  |
|                |                                  |   |Handlers|  |
|                |                                  |   |        |  |
|                |                                  |   +--------+  |
|                |                                  |               |
+----------------+                                  +----------------+
```

### Core Components

1. **Broker**: The central message routing system that manages subscriptions and message delivery
2. **Server Handler**: Manages WebSocket connections and translates between WebSocket messages and broker messages
3. **Client**: Connects to the server via WebSocket and provides an API for sending/receiving messages
4. **Message Model**: Defines the structure of messages exchanged between clients and the server

## Message Flow Patterns

WebSocketMQ supports several communication patterns:

### 1. Client Registration Flow

```
+----------------+                                  +----------------+
|                |                                  |                |
|     Client     |                                  |     Server     |
|                |                                  |                |
+-------+--------+                                  +--------+-------+
        |                                                    |
        |  1. Connect WebSocket                              |
        +--------------------------------------------------->|
        |                                                    |
        |  2. Send Registration Event                        |
        |     (_client.register)                             |
        +--------------------------------------------------->|
        |                                                    |
        |                     3. Process Registration        |
        |                        - Generate Client ID        |
        |                        - Store Connection          |
        |                                                    |
        |  4. Send Registration Acknowledgment               |
        |     (_internal.client.registered)                  |
        |<---------------------------------------------------+
        |                                                    |
        |  5. Store Client ID                                |
        |                                                    |
+-------v--------+                                  +--------v-------+
|                |                                  |                |
| Client Ready   |                                  | Server Ready   |
|                |                                  |                |
+----------------+                                  +----------------+
```

### 2. Client-to-Server RPC

```
+----------------+                                  +----------------+
|                |                                  |                |
|     Client     |                                  |     Server     |
|                |                                  |                |
+-------+--------+                                  +--------+-------+
        |                                                    |
        |  1. Send RPC Request                               |
        |     (topic, correlation_id)                        |
        +--------------------------------------------------->|
        |                                                    |
        |                     2. Process Request             |
        |                        - Find Handler              |
        |                        - Execute Handler           |
        |                        - Generate Response         |
        |                                                    |
        |  3. Send Response Directly to Client               |
        |     (correlation_id)                               |
        |<---------------------------------------------------+
        |                                                    |
        |  4. Process Response                               |
        |     - Match with Request                           |
        |     - Deliver to Waiting Handler                   |
        |                                                    |
+-------v--------+                                  +--------v-------+
|                |                                  |                |
| Request Complete|                                 |                |
|                |                                  |                |
+----------------+                                  +----------------+
```

### 3. Server-to-Client RPC

```
+----------------+                                  +----------------+
|                |                                  |                |
|     Client     |                                  |     Server     |
|                |                                  |                |
+-------+--------+                                  +--------+-------+
        |                                                    |
        |                     1. Initiate RPC Request        |
        |                        - Target Client ID          |
        |                        - Generate Correlation ID   |
        |                                                    |
        |  2. Send RPC Request to Specific Client            |
        |     (topic, correlation_id)                        |
        |<---------------------------------------------------+
        |                                                    |
        |  3. Process Request                                |
        |     - Find Handler                                 |
        |     - Execute Handler                              |
        |     - Generate Response                            |
        |                                                    |
        |  4. Send Response Back to Server                   |
        |     (correlation_id)                               |
        +--------------------------------------------------->|
        |                                                    |
        |                     5. Process Response            |
        |                        - Match with Request        |
        |                        - Deliver to Waiting Handler|
        |                                                    |
+-------v--------+                                  +--------v-------+
|                |                                  |                |
|                |                                  | Request Complete|
|                |                                  |                |
+----------------+                                  +----------------+
```

## Implementation Guide

### Server-Side Setup

1. **Create a Broker**:
   ```go
   logger := yourLoggerImplementation

   // Start with defaults and tweak only what you need
   brokerOpts := broker.DefaultOptions()
   brokerOpts.PingInterval = 15 * time.Second

   // Create the broker using the options struct
   b, err := broker.NewWithOptions(brokerOpts)
   if err != nil {
       log.Fatal(err)
   }
   ```

2. **Create a WebSocket Handler**:
   ```go
   // Create handler options
   handlerOpts := server.DefaultHandlerOptions()
   
   // Create the handler
   handler := server.NewHandler(b, logger, handlerOpts)
   ```

3. **Set Up HTTP Server**:
   ```go
   // Create HTTP server
   mux := http.NewServeMux()
   mux.Handle("/ws", handler)
   
   // Start server
   httpServer := &http.Server{
       Addr:    ":8080",
       Handler: mux,
   }
   go httpServer.ListenAndServe()
   ```

4. **Register Server-Side Handlers**:
   ```go
   // Register a handler for a specific topic
   b.Subscribe(context.Background(), "example.topic", func(ctx context.Context, msg *model.Message, clientID string) (*model.Message, error) {
       // Process the message
       // ...
       
       // Return a response (for RPC)
       return model.NewResponse(msg, responseData), nil
   })
   ```

5. **Implement Server-to-Client RPC**:
   ```go
   // Create a request message
   req := model.NewRequest("client.action", requestData, 5000) // 5000ms timeout
   
   // Send the request to a specific client
   resp, err := b.RequestToClient(ctx, clientID, req, 5000)
   if err != nil {
       // Handle error
   }
   
   // Process the response
   // ...
   ```

### Go Client Setup with Options

```go
clientOpts := client.DefaultOptions()
clientOpts.AutoReconnect = true

cli, err := client.ConnectWithOptions("ws://localhost:8080/wsmq", clientOpts)
if err != nil {
    log.Fatal(err)
}
```

### Client-Side Implementation

For client-side implementation, you'll typically use JavaScript in the browser. The library provides a JavaScript client that can be included in your web application.

1. **Connect to the Server**:
   ```javascript
   const client = new WebSocketMQ('ws://your-server/ws');
   
   client.onOpen = () => {
     console.log('Connected to server');
   };
   
   client.onError = (error) => {
     console.error('Connection error:', error);
   };
   
   client.onClose = () => {
     console.log('Connection closed');
   };
   
   client.connect();
   ```

2. **Register Client-Side Handlers**:
   ```javascript
   // Register a handler for a specific topic
   client.subscribe('client.action', async (message) => {
     // Process the message
     // ...
     
     // Return a response (for RPC)
     return { result: 'success', data: someData };
   });
   ```

3. **Send Client-to-Server RPC**:
   ```javascript
   try {
     // Send an RPC request to the server
     const response = await client.request('example.topic', { param: 'value' }, 5000);
     
     // Process the response
     console.log('Response:', response);
   } catch (error) {
     console.error('RPC error:', error);
   }
   ```

## Best Practices

### 1. Connection Management

- **Client Registration**: Always implement proper client registration to ensure clients receive a unique ID
- **Reconnection Logic**: Implement reconnection logic on the client side with exponential backoff
- **Graceful Shutdown**: Properly close connections when shutting down the server or client

### 2. Message Handling

- **Timeout Handling**: Always set appropriate timeouts for RPC requests
- **Error Handling**: Implement proper error handling for failed requests
- **Message Validation**: Validate message structure and content before processing

### 3. Performance Considerations

- **Message Size**: Keep message size small to minimize network overhead
- **Subscription Management**: Limit the number of subscriptions per client
- **Connection Pooling**: Consider connection pooling for high-traffic applications

### Typed Request Helpers

The Go library includes helpers to enforce compile-time safety when working with RPC.
Use `client.GenericRequest[T]` on the client and `broker.GenericClientRequest[T]`
on the server to automatically unmarshal responses:

```go
type TimeResp struct { CurrentTime string }

resp, err := client.GenericRequest[TimeResp](cli, ctx, "time.now")
if err != nil {
    log.Fatal(err)
}
fmt.Println(resp.CurrentTime)
```

## Advanced Topics

### 1. Authentication and Authorization

WebSocketMQ doesn't provide built-in authentication, but you can implement it by:

- Using HTTP authentication for the initial WebSocket connection
- Implementing a custom authentication handler in your server
- Validating client credentials during the registration process

### 2. Scaling

For high-traffic applications, consider:

- Using multiple broker instances behind a load balancer
- Implementing a distributed broker using a message queue like NATS
- Sharding clients across multiple servers based on client ID

### 3. Monitoring and Debugging

- Implement comprehensive logging for all message flows
- Set up metrics collection for connection counts, message rates, and error rates
- Use correlation IDs to trace requests through the system

## Troubleshooting

### Common Issues

1. **Connection Failures**:
   - Check network connectivity
   - Verify WebSocket endpoint URL
   - Check for firewall or proxy issues

2. **Message Delivery Issues**:
   - Verify topic names match between publisher and subscriber
   - Check for message size limits
   - Ensure correlation IDs are properly set for RPC

3. **Performance Problems**:
   - Monitor message rates and sizes
   - Check for memory leaks in long-lived connections
   - Optimize handler performance

## Example: Implementing Server-to-Client RPC

Here's a complete example of implementing server-to-client RPC:

### Server-Side Code

```go
package main

import (
    "context"
    "log"
    "net/http"
    "time"

    "github.com/lightforgemedia/go-websocketmq/pkg/broker"
    "github.com/lightforgemedia/go-websocketmq/pkg/broker/ps"
    "github.com/lightforgemedia/go-websocketmq/pkg/model"
    "github.com/lightforgemedia/go-websocketmq/pkg/server"
)

// Simple logger implementation
type Logger struct{}

func (l *Logger) Debug(msg string, args ...any) { log.Printf("DEBUG: "+msg, args...) }
func (l *Logger) Info(msg string, args ...any)  { log.Printf("INFO: "+msg, args...) }
func (l *Logger) Warn(msg string, args ...any)  { log.Printf("WARN: "+msg, args...) }
func (l *Logger) Error(msg string, args ...any) { log.Printf("ERROR: "+msg, args...) }

func main() {
    logger := &Logger{}
    
    // Create broker
    brokerOpts := broker.DefaultOptions()
    b := ps.New(logger, brokerOpts)
    
    // Create WebSocket handler
    handlerOpts := server.DefaultHandlerOptions()
    handler := server.NewHandler(b, logger, handlerOpts)
    
    // Set up HTTP server
    mux := http.NewServeMux()
    mux.Handle("/ws", handler)
    
    // Track connected clients
    connectedClients := make(map[string]bool)
    
    // Subscribe to client registration events
    b.Subscribe(context.Background(), broker.TopicClientRegistered, func(ctx context.Context, msg *model.Message, _ string) (*model.Message, error) {
        if bodyMap, ok := msg.Body.(map[string]interface{}); ok {
            if clientID, ok := bodyMap["brokerClientID"].(string); ok {
                logger.Info("Client registered: %s", clientID)
                connectedClients[clientID] = true
            }
        }
        return nil, nil
    })
    
    // Subscribe to client deregistration events
    b.Subscribe(context.Background(), broker.TopicClientDeregistered, func(ctx context.Context, msg *model.Message, _ string) (*model.Message, error) {
        if bodyMap, ok := msg.Body.(map[string]interface{}); ok {
            if clientID, ok := bodyMap["brokerClientID"].(string); ok {
                logger.Info("Client deregistered: %s", clientID)
                delete(connectedClients, clientID)
            }
        }
        return nil, nil
    })
    
    // Start a goroutine to periodically send RPC requests to clients
    go func() {
        for {
            time.Sleep(5 * time.Second)
            
            // Send RPC to all connected clients
            for clientID := range connectedClients {
                go func(cID string) {
                    // Create request with data to send to client
                    req := model.NewRequest("server.ping", map[string]interface{}{
                        "timestamp": time.Now().Unix(),
                        "message":   "Ping from server",
                    }, 5000)
                    
                    // Send the request to the client
                    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
                    defer cancel()
                    
                    resp, err := b.RequestToClient(ctx, cID, req, 5000)
                    if err != nil {
                        logger.Error("Failed to send ping to client %s: %v", cID, err)
                        return
                    }
                    
                    logger.Info("Received ping response from client %s: %+v", cID, resp.Body)
                }(clientID)
            }
        }
    }()
    
    // Start the server
    logger.Info("Starting server on :8080")
    if err := http.ListenAndServe(":8080", mux); err != nil {
        logger.Error("Server error: %v", err)
    }
}
```

### Client-Side Code (JavaScript)

```javascript
// Initialize the WebSocketMQ client
const client = new WebSocketMQ('ws://localhost:8080/ws');

client.onOpen = () => {
  console.log('Connected to server');
  
  // Register handlers after connection is established
  setupHandlers();
};

client.onError = (error) => {
  console.error('Connection error:', error);
};

client.onClose = () => {
  console.log('Connection closed');
};

function setupHandlers() {
  // Register a handler for server pings
  client.subscribe('server.ping', async (message) => {
    console.log('Received ping from server:', message);
    
    // Process the ping
    const timestamp = message.timestamp;
    const serverMessage = message.message;
    
    // Return a response
    return {
      clientTime: Date.now(),
      serverTime: timestamp,
      message: 'Pong from client',
      echo: serverMessage
    };
  });
  
  // You can also send requests to the server
  document.getElementById('sendButton').addEventListener('click', async () => {
    try {
      const message = document.getElementById('messageInput').value;
      
      const response = await client.request('example.echo', { message }, 5000);
      
      console.log('Server response:', response);
    } catch (error) {
      console.error('Request failed:', error);
    }
  });
}

// Connect to the server
client.connect();
```

## Conclusion

WebSocketMQ provides a robust foundation for building real-time, bidirectional communication between clients and servers. By following the patterns and practices outlined in this guide, you can implement reliable messaging systems for your applications.

The library's support for both client-to-server and server-to-client RPC makes it particularly well-suited for applications that require interactive, real-time communication, such as:

- Collaborative editing tools
- Real-time dashboards and monitoring
- Interactive web applications
- Chat and messaging systems
- Multiplayer games

As you become more familiar with WebSocketMQ, you'll discover additional patterns and optimizations that can be applied to your specific use cases.
