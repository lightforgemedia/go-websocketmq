# Browser Control Client: A WebSocket-Based Browser Remote Control Tool

This guide introduces Browser Control Client, a powerful library for remote browser control and automation. The library enables real-time, bidirectional communication between a server and browsers, making it ideal for testing, demonstrations, and synchronizing multiple browser instances.

## Table of Contents

- [Installation](#installation)
- [Core Concepts](#core-concepts)
- [Server-Side API](#server-side-api)
  - [Server Setup](#server-setup)
  - [Client Management](#client-management)
  - [Remote Commands](#remote-commands)
- [Client-Side API](#client-side-api)
  - [Browser Client](#browser-client)
  - [Connection Management](#connection-management)
  - [Command Handling](#command-handling)
- [Communication Patterns](#communication-patterns)
- [Examples](#examples)
  - [Synchronized Demo](#synchronized-demo)
  - [Remote Testing](#remote-testing)
  - [Live Presentation](#live-presentation)
- [Advanced Features](#advanced-features)

## Installation

```bash
go get github.com/example/browser-control-client
```

## Core Concepts

Browser Control Client is built around these core concepts:

- **Controller**: Central server that sends commands to connected browsers
- **Browser Client**: JavaScript client running in the browser that responds to commands
- **Commands**: Messages with specific actions for browser clients to execute
- **Events**: Messages sent from browsers back to the controller

## Server-Side API

### Server Setup

```go
import (
    "github.com/example/browser-control-client/controller"
    "net/http"
)

// Create a new controller
ctrl, err := controller.New(
    controller.WithLogger(logger),
    controller.WithAllowedOrigins([]string{"localhost:*", "example.com"}),
)
if err != nil {
    log.Fatalf("Failed to create controller: %v", err)
}

// Register command handlers
ctrl.HandleClientEvent("browser:ready", func(client controller.ClientHandle, data map[string]interface{}) {
    log.Printf("Browser ready: %s (%s)", client.ID(), client.UserAgent())
})

// Set up HTTP server
mux := http.NewServeMux()
ctrl.RegisterHandlers(mux) // Registers WebSocket and client assets

// Start server
http.ListenAndServe(":8080", mux)
```

### Client Management

The controller maintains an active list of connected browsers. You can interact with specific clients or broadcast to all:

```go
// Broadcast to all clients
ctrl.Broadcast("navigation:goto", map[string]interface{}{
    "url": "https://example.com/dashboard",
})

// Send command to specific client
clientID := "client-123"
client, err := ctrl.GetClient(clientID)
if err == nil {
    client.SendCommand("ui:highlight", map[string]interface{}{
        "selector": "#login-button",
        "duration": 3000,
    })
}

// Get client information
ctrl.IterateClients(func(client controller.ClientHandle) bool {
    fmt.Printf("Client: %s, Type: %s, URL: %s\n", 
               client.ID(), client.BrowserType(), client.CurrentURL())
    return true // continue iteration
})
```

### Remote Commands

The controller can send various commands to browser clients:

```go
// Navigation commands
client.SendCommand("navigation:goto", map[string]interface{}{
    "url": "https://example.com/products",
})

// Interaction commands
client.SendCommand("dom:click", map[string]interface{}{
    "selector": "#submit-button",
})

client.SendCommand("dom:type", map[string]interface{}{
    "selector": "#search-input",
    "text": "query text",
})

// UI manipulation
client.SendCommand("ui:highlight", map[string]interface{}{
    "selector": ".important-item",
    "color": "rgba(255, 0, 0, 0.3)",
    "duration": 2000,
})

// JavaScript execution
client.SendCommand("script:execute", map[string]interface{}{
    "code": "document.querySelector('.result').textContent = 'Modified by controller';",
})

// Screenshot capture
response, err := client.RequestData("capture:screenshot", map[string]interface{}{
    "selector": "#chart-container", // Optional, full page if omitted
    "format": "png",
})
if err == nil {
    // response contains base64-encoded image data
    saveScreenshot(response["data"].(string))
}
```

## Client-Side API

### Browser Client

```html
<!-- Include the client library -->
<script src="/browser-control.js"></script>

<script>
    // Initialize the client
    const client = new BrowserControl.Client({
        // Optional custom settings
        reconnect: true,
        commandPrefix: "ctrl:",
        debugMode: false
    });
    
    // Connect to the controller
    client.connect();
</script>
```

### Connection Management

```javascript
// Connection status events
client.onConnect(() => {
    console.log("Connected to controller");
    document.getElementById("status").textContent = "Connected";
});

client.onDisconnect(() => {
    console.log("Disconnected from controller");
    document.getElementById("status").textContent = "Disconnected";
});

// Manually connect/disconnect
client.connect();
client.disconnect();
```

### Command Handling

You can customize how the browser client responds to commands:

```javascript
// Override default command handlers
client.handleCommand("dom:click", async (data) => {
    console.log(`Custom click handler for ${data.selector}`);
    // Custom implementation
    const element = document.querySelector(data.selector);
    if (element) {
        element.classList.add("highlight-before-click");
        await new Promise(resolve => setTimeout(resolve, 300));
        element.click();
        element.classList.remove("highlight-before-click");
    }
    return { success: true };
});

// Send events to the controller
client.sendEvent("user:interaction", {
    type: "click",
    element: "#signup-button",
    timestamp: Date.now()
});

// Request data from controller
client.requestData("session:info")
    .then(data => {
        console.log("Session info:", data);
        displaySessionInfo(data);
    })
    .catch(err => {
        console.error("Failed to get session info:", err);
    });
```

## Communication Patterns

Browser Control Client supports two main communication patterns:

### Command/Response

The controller sends a command to one or more browsers and receives a success/failure response:

```go
// Server side
response, err := client.SendCommand("dom:fill", map[string]interface{}{
    "selector": "input[name='email']",
    "value": "test@example.com",
})
if err != nil {
    log.Printf("Command failed: %v", err)
} else {
    log.Printf("Command succeeded: %v", response)
}

// Client side (automatic)
client.handleCommand("dom:fill", (data) => {
    const elem = document.querySelector(data.selector);
    if (!elem) return { success: false, error: "Element not found" };
    
    elem.value = data.value;
    return { success: true };
});
```

### Event Broadcasting

Browsers can broadcast events that the controller and other browsers can listen for:

```javascript
// Client side
client.sendEvent("form:submitted", {
    formId: "signup",
    fields: {
        email: "user@example.com",
        plan: "premium"
    }
});

// Server side
ctrl.HandleClientEvent("form:submitted", (client, data) => {
    log.Printf("Form submitted by client %s: %v", client.ID(), data)
    
    // Optionally broadcast to other clients
    ctrl.BroadcastExcept(client.ID(), "notification:new", {
        message: "New user signed up!",
        level: "info"
    });
});
```

## Examples

### Synchronized Demo

Control multiple browser instances for a synchronized product demo:

```go
// Server code
func productsDemo(ctrl *controller.Controller) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // Render the demo template
        renderTemplate(w, "demo.html")
        
        // When a new page loads, automatically start the demo after 2 seconds
        ctrl.HandleClientEvent("browser:pageload", func(client controller.ClientHandle, data map[string]interface{}) {
            time.Sleep(2 * time.Second)
            startDemo(ctrl, client)
        })
    }
}

func startDemo(ctrl *controller.Controller, client controller.ClientHandle) {
    // Show product features sequentially
    client.SendCommand("navigation:goto", map[string]interface{}{
        "url": "/products/feature1",
    })
    
    time.Sleep(5 * time.Second)
    
    client.SendCommand("ui:highlight", map[string]interface{}{
        "selector": ".key-benefit",
        "duration": 3000,
    })
    
    // Continue with demo sequence...
}
```

### Remote Testing

Implement remote browser testing across multiple devices:

```go
// Server code
func runTestSuite(ctrl *controller.Controller) {
    // Get all connected browser clients
    var clients []controller.ClientHandle
    ctrl.IterateClients(func(client controller.ClientHandle) bool {
        clients = append(clients, client)
        return true
    })
    
    // Run tests across all browsers
    for _, client := range clients {
        testLogin(client)
        testProductSearch(client)
        testCheckout(client)
    }
}

func testLogin(client controller.ClientHandle) {
    // Navigate to login page
    client.SendCommand("navigation:goto", map[string]interface{}{
        "url": "/login",
    })
    
    // Wait for page to load
    time.Sleep(1 * time.Second)
    
    // Fill login form
    client.SendCommand("dom:fill", map[string]interface{}{
        "selector": "#email",
        "value": "test@example.com",
    })
    
    client.SendCommand("dom:fill", map[string]interface{}{
        "selector": "#password",
        "value": "password123",
    })
    
    // Click login button
    client.SendCommand("dom:click", map[string]interface{}{
        "selector": "#login-button",
    })
    
    // Verify successful login
    resp, err := client.RequestData("dom:check", map[string]interface{}{
        "selector": ".welcome-message",
        "exists": true,
    })
    
    if err != nil || !resp["success"].(bool) {
        log.Printf("Login test failed for client %s", client.ID())
    } else {
        log.Printf("Login test passed for client %s", client.ID())
    }
}
```

### Live Presentation

Create a tool for synchronizing browsers during a presentation:

```javascript
// Presenter client
const presenterClient = new BrowserControl.Client({
    role: "presenter"
});

// When presenter changes slides, notify the server
document.querySelector(".slide-container").addEventListener("slide-change", (e) => {
    presenterClient.sendEvent("presentation:slide-change", {
        slideIndex: e.detail.slideIndex,
        slideId: e.detail.slideId
    });
});

// Audience client
const audienceClient = new BrowserControl.Client({
    role: "audience",
    followPresenter: true
});

// Automatically sync with presenter's actions
audienceClient.handleCommand("presentation:slide-change", (data) => {
    document.querySelector(".slide-container").goToSlide(data.slideIndex);
    return { success: true };
});
```

## Advanced Features

### Browser Fingerprinting

Identify and categorize connected browsers based on their capabilities:

```go
// Server side
ctrl.HandleClientEvent("browser:ready", func(client controller.ClientHandle, data map[string]interface{}) {
    // Log browser capabilities
    log.Printf("Browser connected: %s", client.UserAgent())
    log.Printf("Features: %v", data["features"])
    
    // Tag clients by capabilities
    if supportsWebGL(data) {
        client.AddTag("webgl")
    }
    
    if supportsTouchEvents(data) {
        client.AddTag("touch")
    }
})

// Later, select clients by capability
ctrl.GetClientsByTag("webgl", func(client controller.ClientHandle) {
    client.SendCommand("render:3d-demo", {...})
})
```

### DOM Synchronization

Keep DOM state synchronized across multiple browsers:

```go
// Server side - track and broadcast DOM changes
ctrl.HandleClientEvent("dom:changed", func(client controller.ClientHandle, data map[string]interface{}) {
    // Broadcast the change to all clients except the sender
    ctrl.BroadcastExcept(client.ID(), "dom:apply-change", data)
})

// Client side
// Send DOM changes to server
const observer = new MutationObserver((mutations) => {
    const changes = processMutations(mutations);
    if (changes.length > 0) {
        client.sendEvent("dom:changed", { changes });
    }
});

// Apply DOM changes from server
client.handleCommand("dom:apply-change", (data) => {
    applyDOMChanges(data.changes);
    return { success: true };
});
```

### Session Recording

Record and replay user sessions:

```go
// Server side - start recording
func startRecording(client controller.ClientHandle) {
    session := newSession(client.ID())
    
    // Listen for events
    client.OnEvent(func(topic string, data map[string]interface{}) {
        session.recordEvent(topic, data, time.Now())
    })
    
    // Take periodic screenshots
    go func() {
        ticker := time.NewTicker(5 * time.Second)
        defer ticker.Stop()
        
        for {
            select {
            case <-ticker.C:
                resp, err := client.RequestData("capture:screenshot", nil)
                if err == nil {
                    session.addScreenshot(resp["data"].(string), time.Now())
                }
            case <-session.done:
                return
            }
        }
    }()
}

// Server side - replay recording
func replaySession(session *Session, client controller.ClientHandle) {
    // Navigate to starting URL
    client.SendCommand("navigation:goto", map[string]interface{}{
        "url": session.initialURL,
    })
    
    // Replay events in sequence with original timing
    for _, event := range session.events {
        time.Sleep(event.timing)
        client.SendCommand("replay:event", event.data)
    }
}
```

### Performance Monitoring

Collect and analyze performance metrics from connected browsers:

```go
// Server side
ctrl.HandleClientEvent("metrics:performance", func(client controller.ClientHandle, data map[string]interface{}) {
    // Store metrics
    db.StoreMetrics(client.ID(), data)
    
    // Analyze for problems
    if isPerformanceDegraded(data) {
        alertOps("Performance issue detected", client, data)
    }
})

// Client side
// Send periodic performance reports
setInterval(() => {
    const metrics = {
        timing: window.performance.timing.toJSON(),
        memory: window.performance.memory, // Chrome only
        fps: calculateFPS(),
        resources: getResourceMetrics()
    };
    
    client.sendEvent("metrics:performance", metrics);
}, 30000);
```

This comprehensive guide should help you get started with Browser Control Client, a powerful tool for remote browser control and synchronization.