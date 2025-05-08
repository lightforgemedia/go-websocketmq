# WebSocketMQ Browser Control Design

## Overview

This document outlines the design for implementing browser control functionality in WebSocketMQ. The goal is to create an HTTP API that allows server-side code to control and interact with connected browser clients.

## Background

WebSocketMQ is a Go library that provides a message broker for WebSocket connections. It supports:

1. **Client-to-Server RPC**: Clients can send requests to the server and receive responses
2. **Server-to-Client RPC**: The server can send requests to specific clients and receive responses
3. **Broadcast Events**: The server can broadcast events to all connected clients
4. **Topic-Based Routing**: Messages are routed based on topics

The library has been tested with both Go clients and JavaScript clients, with comprehensive test coverage for both.

## Current Implementation

The current implementation includes:

### Server-Side Components

```go
// PubSubBroker is the main broker implementation
type PubSubBroker struct {
    // Manages client connections
    connections map[string]broker.Connection
    // Manages topic subscriptions
    subscriptions map[string]map[string]*subscription
    // ...other fields
}

// Connection represents a client connection
type wsConnectionAdapter struct {
    conn        *websocket.Conn
    clientID    string
    pageSession string
    // ...other fields
}

// Message represents a message in the system
type Message struct {
    Header struct {
        MessageID            string
        CorrelationID        string
        Type                 string // "request", "response", "event", "error"
        Topic                string
        Timestamp            int64
        TTL                  int64
        SourceBrokerClientID string
    }
    Body interface{}
}
```

### JavaScript Client

```javascript
class WebSocketMQClient {
    constructor(url, options = {}) {
        this.url = url;
        this.options = Object.assign({
            reconnectDelay: 1000,
            maxReconnectAttempts: 5,
            debug: false
        }, options);
        
        this.socket = null;
        this.connected = false;
        this.reconnectAttempts = 0;
        this.pageSessionID = this.generateID();
        this.brokerClientID = null;
        this.messageID = 0;
        this.pendingRequests = new Map();
        this.handlers = new Map();
        
        // Connect immediately
        this.connect();
    }
    
    // Register a handler for a specific topic
    registerHandler(topic, handler) {
        this.handlers.set(topic, handler);
    }
    
    // Send an RPC request to the server
    sendRPC(topic, body, timeoutMs = 5000) {
        // Implementation...
    }
    
    // Send an event to the server
    sendEvent(topic, body) {
        // Implementation...
    }
}
```

### Example of Server-to-Client RPC in Tests

```go
// Server sending RPC to client
resp, err := server.SendRPCToClient(context.Background(), clientID, "server.echo", map[string]string{
    "param": "server-param",
}, 5000)

// JavaScript client handling the request
client.registerHandler('server.echo', function(message) {
    log('Received server.echo request', message);
    // Return a response
    return {
        result: 'echo-' + (message.body.param || 'default')
    };
});
```

## Design Requirements for Browser Control

1. **Client Discovery**: Ability to find connected browsers by URL or unique identifier
2. **Consistent Client Identification**: Clients should maintain the same identifier across reconnections
3. **Command Execution**: Ability to send commands to browsers and receive responses
4. **HTTP API**: An HTTP interface for controlling browsers from external systems

## Detailed Design

### 1. Client Identification and Discovery

#### Client Registration with Enhanced Metadata

Clients will register with the server providing enhanced metadata:

```javascript
class WebSocketMQClient {
    constructor(url, options = {}) {
        // ...existing initialization
        
        // Get or create a persistent client ID
        this.clientIdentifier = this._getOrCreateClientIdentifier();
        this.pageSessionID = this.clientIdentifier;
    }
    
    _getOrCreateClientIdentifier() {
        // URL Parameter approach
        const urlParams = new URLSearchParams(window.location.search);
        let clientId = urlParams.get('clientId');
        
        if (!clientId) {
            // Generate a new ID
            clientId = Date.now() + '-' + Math.random().toString(36).substr(2, 9);
            
            // Add to URL without reloading
            urlParams.set('clientId', clientId);
            const newUrl = window.location.pathname + '?' + urlParams.toString() + window.location.hash;
            window.history.replaceState({}, '', newUrl);
        }
        
        return clientId;
    }
    
    _sendRegistration() {
        const message = {
            header: {
                messageID: this.generateID(),
                type: 'event',
                topic: '_client.register',
                timestamp: Date.now()
            },
            body: {
                pageSessionID: this.pageSessionID,
                pageUrl: window.location.href,
                clientIdentifier: this.clientIdentifier,
                connectedAt: Date.now(),
                userAgent: navigator.userAgent,
                screenSize: {
                    width: window.screen.width,
                    height: window.screen.height
                }
            }
        };
        
        this._sendMessage(message);
    }
}
```

#### Server-Side Client Registry

```go
// ClientInfo stores information about a connected client
type ClientInfo struct {
    ClientID        string
    PageURL         string
    ClientIdentifier string
    ConnectedAt     time.Time
    LastActivity    time.Time
    UserAgent       string
    ScreenSize      map[string]int
    Connection      broker.Connection
}

// ClientRegistry manages connected clients
type ClientRegistry struct {
    clients map[string]*ClientInfo
    mu      sync.RWMutex
}

// RegisterClient registers a new client
func (r *ClientRegistry) RegisterClient(clientID string, info *ClientInfo) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.clients[clientID] = info
}

// UnregisterClient removes a client
func (r *ClientRegistry) UnregisterClient(clientID string) {
    r.mu.Lock()
    defer r.mu.Unlock()
    delete(r.clients, clientID)
}

// FindClientsByURL finds clients by URL
func (r *ClientRegistry) FindClientsByURL(url string) []*ClientInfo {
    r.mu.RLock()
    defer r.mu.RUnlock()
    
    var result []*ClientInfo
    for _, client := range r.clients {
        if client.PageURL == url {
            result = append(result, client)
        }
    }
    
    // Sort by connection time (newest first)
    sort.Slice(result, func(i, j int) bool {
        return result[i].ConnectedAt.After(result[j].ConnectedAt)
    })
    
    return result
}

// FindClientByIdentifier finds a client by its unique identifier
func (r *ClientRegistry) FindClientByIdentifier(identifier string) *ClientInfo {
    r.mu.RLock()
    defer r.mu.RUnlock()
    
    for _, client := range r.clients {
        if client.ClientIdentifier == identifier {
            return client
        }
    }
    
    return nil
}
```

### 2. Client-Side Command Handlers

Clients will register handlers for browser control commands:

```javascript
// Register UI control handlers
client.registerHandler('ui.clickButton', function(message) {
    const buttonSelector = message.body.selector;
    const button = document.querySelector(buttonSelector);
    if (button) {
        button.click();
        return { success: true };
    }
    return { success: false, error: "Button not found" };
});

client.registerHandler('ui.navigate', function(message) {
    const url = message.body.url;
    window.location.href = url;
    return { success: true };
});

client.registerHandler('ui.getData', function(message) {
    // Extract data from the page
    const data = {
        title: document.title,
        url: window.location.href,
        // Other data extraction
    };
    return { success: true, data: data };
});

client.registerHandler('ui.setValue', function(message) {
    const selector = message.body.selector;
    const value = message.body.value;
    const element = document.querySelector(selector);
    if (element) {
        if (element.tagName === 'INPUT' || element.tagName === 'TEXTAREA' || element.tagName === 'SELECT') {
            element.value = value;
            // Trigger change event
            const event = new Event('change', { bubbles: true });
            element.dispatchEvent(event);
            return { success: true };
        }
        return { success: false, error: "Element is not an input" };
    }
    return { success: false, error: "Element not found" };
});
```

### 3. HTTP API for Browser Control

```go
// BrowserControlServer provides HTTP endpoints for browser control
type BrowserControlServer struct {
    broker        broker.Broker
    clientRegistry *ClientRegistry
}

// NewBrowserControlServer creates a new browser control server
func NewBrowserControlServer(broker broker.Broker, registry *ClientRegistry) *BrowserControlServer {
    return &BrowserControlServer{
        broker:        broker,
        clientRegistry: registry,
    }
}

// RegisterHandlers registers HTTP handlers
func (s *BrowserControlServer) RegisterHandlers(mux *http.ServeMux) {
    mux.HandleFunc("/api/browser-control", s.HandleBrowserControl)
    mux.HandleFunc("/api/list-clients", s.HandleListClients)
}

// HandleBrowserControl handles browser control requests
func (s *BrowserControlServer) HandleBrowserControl(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        return
    }
    
    var req struct {
        TargetURL      string                 `json:"targetUrl,omitempty"`
        TargetID       string                 `json:"targetId,omitempty"`
        Command        string                 `json:"command"`
        Params         map[string]interface{} `json:"params"`
        TimeoutMs      int64                  `json:"timeoutMs,omitempty"`
    }
    
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    
    // Default timeout
    if req.TimeoutMs == 0 {
        req.TimeoutMs = 5000
    }
    
    var targetClients []*ClientInfo
    
    // Find target clients
    if req.TargetID != "" {
        // Find by specific ID
        client := s.clientRegistry.FindClientByIdentifier(req.TargetID)
        if client != nil {
            targetClients = []*ClientInfo{client}
        }
    } else if req.TargetURL != "" {
        // Find by URL (potentially multiple clients)
        targetClients = s.clientRegistry.FindClientsByURL(req.TargetURL)
    } else {
        http.Error(w, "Must specify either targetUrl or targetId", http.StatusBadRequest)
        return
    }
    
    if len(targetClients) == 0 {
        http.Error(w, "No matching clients found", http.StatusNotFound)
        return
    }
    
    // Send command to each client
    results := make(map[string]interface{})
    for _, client := range targetClients {
        resp, err := s.broker.RequestToClient(r.Context(), client.ClientID, req.Command, req.Params, req.TimeoutMs)
        results[client.ClientID] = map[string]interface{}{
            "success": err == nil,
            "response": resp,
            "error": err,
            "clientInfo": map[string]interface{}{
                "url": client.PageURL,
                "identifier": client.ClientIdentifier,
                "connectedAt": client.ConnectedAt,
            },
        }
    }
    
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(results)
}

// HandleListClients lists all connected clients
func (s *BrowserControlServer) HandleListClients(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        return
    }
    
    // Get URL filter from query parameter
    urlFilter := r.URL.Query().Get("url")
    
    var clients []*ClientInfo
    if urlFilter != "" {
        clients = s.clientRegistry.FindClientsByURL(urlFilter)
    } else {
        clients = s.clientRegistry.GetAllClients()
    }
    
    // Convert to response format
    response := make([]map[string]interface{}, 0, len(clients))
    for _, client := range clients {
        response = append(response, map[string]interface{}{
            "clientId": client.ClientID,
            "identifier": client.ClientIdentifier,
            "url": client.PageURL,
            "connectedAt": client.ConnectedAt,
            "userAgent": client.UserAgent,
            "screenSize": client.ScreenSize,
        })
    }
    
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(response)
}
```

### 4. Integration with Existing Broker

```go
// Extend the PubSubBroker to maintain the client registry
func (b *PubSubBroker) handleClientRegistration(ctx context.Context, msg *model.Message, clientID string) (*model.Message, error) {
    // Extract client information
    var clientInfo ClientInfo
    clientInfo.ClientID = clientID
    
    if bodyMap, ok := msg.Body.(map[string]interface{}); ok {
        if pageURL, ok := bodyMap["pageUrl"].(string); ok {
            clientInfo.PageURL = pageURL
        }
        if clientIdentifier, ok := bodyMap["clientIdentifier"].(string); ok {
            clientInfo.ClientIdentifier = clientIdentifier
        }
        if connectedAt, ok := bodyMap["connectedAt"].(float64); ok {
            clientInfo.ConnectedAt = time.Unix(0, int64(connectedAt)*int64(time.Millisecond))
        }
        if userAgent, ok := bodyMap["userAgent"].(string); ok {
            clientInfo.UserAgent = userAgent
        }
        if screenSize, ok := bodyMap["screenSize"].(map[string]interface{}); ok {
            clientInfo.ScreenSize = make(map[string]int)
            if width, ok := screenSize["width"].(float64); ok {
                clientInfo.ScreenSize["width"] = int(width)
            }
            if height, ok := screenSize["height"].(float64); ok {
                clientInfo.ScreenSize["height"] = int(height)
            }
        }
    }
    
    // Get the connection
    conn, exists := b.GetConnection(clientID)
    if exists {
        clientInfo.Connection = conn
    }
    
    // Register the client
    b.clientRegistry.RegisterClient(clientID, &clientInfo)
    
    // Continue with normal registration process
    // ...
    
    return nil, nil
}

// Handle client disconnection
func (b *PubSubBroker) handleClientDisconnection(clientID string) {
    // Unregister the client
    b.clientRegistry.UnregisterClient(clientID)
    
    // Continue with normal disconnection process
    // ...
}
```

## Example Usage

### HTTP API Examples

#### Controlling a Specific Browser

```http
POST /api/browser-control HTTP/1.1
Content-Type: application/json

{
    "targetId": "1623456789-abc123def",
    "command": "ui.clickButton",
    "params": {
        "selector": "#submit-button"
    },
    "timeoutMs": 5000
}
```

#### Controlling All Browsers on a Specific Page

```http
POST /api/browser-control HTTP/1.1
Content-Type: application/json

{
    "targetUrl": "https://example.com/dashboard",
    "command": "ui.navigate",
    "params": {
        "url": "https://example.com/details"
    }
}
```

#### Listing Connected Clients

```http
GET /api/list-clients?url=https://example.com/dashboard HTTP/1.1
```

## Implementation Considerations

1. **Security**: Ensure that the HTTP API is properly secured to prevent unauthorized access.

2. **Error Handling**: Implement robust error handling for both client and server components.

3. **Timeouts**: Set appropriate timeouts for RPC requests to prevent hanging requests.

4. **Reconnection Logic**: Ensure that the client reconnection logic maintains the same client identifier.

5. **Testing**: Create comprehensive tests for the browser control functionality.

## Next Steps

1. Implement the client registry in the PubSubBroker
2. Extend the JavaScript client with enhanced registration
3. Implement the HTTP API for browser control
4. Create example applications demonstrating the functionality
5. Write comprehensive tests

## Conclusion

This design leverages the existing WebSocketMQ infrastructure to provide a powerful browser control capability. By using the URL parameter approach for client identification, we ensure consistent identification across reconnections while making the client identifier visible and shareable.

The HTTP API provides a simple interface for external systems to control connected browsers, making it easy to integrate with other applications.
