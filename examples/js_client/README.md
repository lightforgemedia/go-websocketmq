# WebSocketMQ JavaScript Client Example

This example demonstrates how to use the WebSocketMQ JavaScript client with a Go server.

## Features

- Automatically serves the JavaScript client from the broker
- Browser can connect to the WebSocket server using the JavaScript client
- Client is searchable by both its URL and a unique ID
- URL is updated with the client ID parameter for persistence across refreshes
- Simple example of requesting the current time from the server

## Running the Example

1. Make sure you have Go installed
2. Run the example:

```bash
go run examples/js_client/main.go
```

3. Open your browser to http://localhost:8090
4. Click the "Connect" button to establish a WebSocket connection
5. Click the "Get Time" button to request the current time from the server

## How It Works

The example demonstrates:

1. Creating a WebSocketMQ broker
2. Registering a request handler for the "system:get_time" topic
3. Setting up an HTTP server with:
   - WebSocket endpoint at `/wsmq`
   - JavaScript client served at `/websocketmq.js`
   - Simple HTML page at the root path
4. Using the JavaScript client in the browser to:
   - Connect to the WebSocket server
   - Send requests to the server
   - Handle responses from the server

## JavaScript Client Features

The JavaScript client automatically:

- Connects to the WebSocket server
- Registers with the server using a unique ID
- Updates the URL with the client ID parameter
- Handles reconnection if the connection is lost
- Provides a simple API for sending requests and receiving responses

## Code Explanation

The key parts of the example are:

```go
// Register both the WebSocket handler and JavaScript client handler with default options
ergoBroker.RegisterHandlersWithDefaults(mux)
```

This sets up the WebSocket endpoint at the default path (`/wsmq`) and serves the JavaScript client at the default path (`/websocketmq.js`).

In the browser, the JavaScript client is used like this:

```javascript
// Create a new client
client = new WebSocketMQ.Client({
    url: 'ws://' + window.location.host + '/wsmq',
    clientName: 'BrowserExample',
    clientType: 'browser'
});

// Connect to the server
client.connect();

// Send a request
client.request('system:get_time', {})
    .then(response => {
        console.log('Server time:', response.currentTime);
    });
```

## Customization

You can customize both the WebSocket endpoint and JavaScript client handler by using `RegisterHandlers` with custom options:

```go
// Create custom options
options := broker.DefaultWebSocketMQHandlerOptions()
options.WebSocketPath = "/api/websocket"

// Customize JavaScript client options
jsOptions := browser_client.DefaultClientScriptOptions()
jsOptions.Path = "/js/websocketmq.js"
jsOptions.UseMinified = true
jsOptions.CacheMaxAge = 7200 // 2 hours
options.JavaScriptClientOptions = &jsOptions

// Register handlers with custom options
ergoBroker.RegisterHandlers(mux, options)
```
