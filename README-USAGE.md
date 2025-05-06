# Using WebSocketMQ in a Separate Go Project

This guide explains how to use WebSocketMQ from another Go project located in a sibling directory on the same computer.

## Project Setup

Assume you have the following directory structure:

```
/Users/username/PROJECTS/
  ├── main/
  │   └── go-websocketmq/      # The WebSocketMQ library
  └── your-project/            # Your new project that will use WebSocketMQ
```

## Step 1: Initialize Your Project

Create your new project directory and initialize it:

```bash
mkdir -p /Users/username/PROJECTS/your-project
cd /Users/username/PROJECTS/your-project
go mod init github.com/yourusername/your-project
```

## Step 2: Set Up Local Module Replacement

Since WebSocketMQ is in a sibling directory, use Go's module replacement feature to reference it locally. This allows you to use the local version during development.

Add this to your `go.mod` file:

```
module github.com/yourusername/your-project

go 1.21  # Use your Go version

require (
    github.com/lightforgemedia/go-websocketmq v0.0.0-unpublished
)

replace github.com/lightforgemedia/go-websocketmq => ../main/go-websocketmq
```

## Step 3: Create a Minimal Example

### Server Implementation (main.go)

Create a `main.go` file with the following content:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lightforgemedia/go-websocketmq"
)

// Simple logger implementation
type AppLogger struct{}

func (l *AppLogger) Debug(msg string, args ...any) { log.Printf("DEBUG: "+msg, args...) }
func (l *AppLogger) Info(msg string, args ...any)  { log.Printf("INFO: "+msg, args...) }
func (l *AppLogger) Warn(msg string, args ...any)  { log.Printf("WARN: "+msg, args...) }
func (l *AppLogger) Error(msg string, args ...any) { log.Printf("ERROR: "+msg, args...) }

func main() {
	// Create a logger
	logger := &AppLogger{}
	logger.Info("Starting WebSocketMQ example server")

	// Create a broker with default options
	brokerOpts := websocketmq.DefaultBrokerOptions()
	broker := websocketmq.NewPubSubBroker(logger, brokerOpts)

	// Create a WebSocket handler
	handlerOpts := websocketmq.DefaultHandlerOptions()
	wsHandler := websocketmq.NewHandler(broker, logger, handlerOpts)

	// Create a server mux
	mux := http.NewServeMux()

	// Mount WebSocket handler
	mux.Handle("/ws", wsHandler)

	// Mount JavaScript client
	mux.Handle("/wsmq/", http.StripPrefix("/wsmq/", websocketmq.ScriptHandler()))

	// Mount static file server
	mux.Handle("/", http.FileServer(http.Dir("./static")))

	// Subscribe to client messages
	broker.Subscribe(context.Background(), "client.hello", func(ctx context.Context, m *websocketmq.Message) (*websocketmq.Message, error) {
		logger.Info("Received client hello: %v", m.Body)
		return nil, nil
	})

	// Handle echo requests
	broker.Subscribe(context.Background(), "server.echo", func(ctx context.Context, m *websocketmq.Message) (*websocketmq.Message, error) {
		logger.Info("Echo request: %v", m.Body)
		return websocketmq.NewResponse(m, m.Body), nil
	})

	// Start a ticker to send periodic messages
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		count := 0
		for range ticker.C {
			count++
			msg := websocketmq.NewEvent("server.tick", map[string]interface{}{
				"time":  time.Now().Format(time.RFC3339),
				"count": count,
			})
			if err := broker.Publish(context.Background(), msg); err != nil {
				logger.Error("Failed to publish tick: %v", err)
			} else {
				logger.Debug("Published tick #%d", count)
			}
		}
	}()

	// Create HTTP server
	server := &http.Server{
		Addr:    ":9000",
		Handler: mux,
	}

	// Handle graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		logger.Info("Shutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			logger.Error("HTTP server shutdown error: %v", err)
		}

		if err := broker.Close(); err != nil {
			logger.Error("Broker close error: %v", err)
		}

		if err := wsHandler.Close(); err != nil {
			logger.Error("WebSocket handler close error: %v", err)
		}
	}()

	// Start the server
	logger.Info("Server listening on http://localhost:9000")
	fmt.Println("Open your browser to http://localhost:9000")
	fmt.Println("Press Ctrl+C to stop")

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		logger.Error("HTTP server error: %v", err)
	}
}
```

### HTML Client (static/index.html)

Create a `static` directory and a minimal HTML client:

```bash
mkdir -p static
```

Create `static/index.html`:

```html
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>WebSocketMQ Example</title>
    <style>
        body {
            font-family: Arial, sans-serif;
            max-width: 800px;
            margin: 0 auto;
            padding: 20px;
            line-height: 1.6;
        }
        .card {
            border: 1px solid #ddd;
            border-radius: 8px;
            padding: 15px;
            margin-bottom: 20px;
            background-color: #f9f9f9;
        }
        button {
            background-color: #4CAF50;
            border: none;
            color: white;
            padding: 8px 16px;
            text-align: center;
            text-decoration: none;
            display: inline-block;
            font-size: 14px;
            margin: 4px 2px;
            cursor: pointer;
            border-radius: 4px;
        }
        input, textarea {
            width: 100%;
            padding: 8px;
            margin-bottom: 10px;
            border: 1px solid #ddd;
            border-radius: 4px;
        }
        pre {
            background-color: #f5f5f5;
            padding: 10px;
            border-radius: 4px;
            overflow-x: auto;
        }
        .status {
            padding: 5px 10px;
            border-radius: 4px;
            display: inline-block;
            margin-left: 10px;
        }
        .connected {
            background-color: #4CAF50;
            color: white;
        }
        .disconnected {
            background-color: #f44336;
            color: white;
        }
    </style>
</head>
<body>
    <h1>WebSocketMQ Example
        <span id="status" class="status disconnected">Disconnected</span>
    </h1>

    <div class="card">
        <h2>Publish Event</h2>
        <input type="text" id="pubTopic" placeholder="Topic" value="client.hello">
        <textarea id="pubMessage" rows="3" placeholder="Message (JSON)">{
  "text": "Hello from browser!",
  "time": "2023-01-01T12:00:00Z"
}</textarea>
        <button id="publishBtn">Publish</button>
    </div>

    <div class="card">
        <h2>Subscribe to Topic</h2>
        <input type="text" id="subTopic" placeholder="Topic" value="server.tick">
        <button id="subscribeBtn">Subscribe</button>
        <div id="subscriptions"></div>
    </div>

    <div class="card">
        <h2>Make Request</h2>
        <input type="text" id="reqTopic" placeholder="Topic" value="server.echo">
        <textarea id="reqMessage" rows="3" placeholder="Message (JSON)">{
  "text": "Echo this message!",
  "timestamp": 1234567890
}</textarea>
        <button id="requestBtn">Send Request</button>
        <h3>Response:</h3>
        <pre id="response"></pre>
    </div>

    <div class="card">
        <h2>Messages Received</h2>
        <button id="clearBtn">Clear</button>
        <pre id="messages"></pre>
    </div>

    <script src="/wsmq/websocketmq.min.js"></script>
    <script>
        // Initialize client
        const client = new WebSocketMQ.Client({
            url: `ws://${window.location.host}/ws`,
            reconnect: true,
            devMode: true
        });

        // DOM elements
        const statusEl = document.getElementById('status');
        const messagesEl = document.getElementById('messages');
        const responseEl = document.getElementById('response');
        const subscriptionsEl = document.getElementById('subscriptions');

        // Event handlers
        document.getElementById('publishBtn').addEventListener('click', () => {
            const topic = document.getElementById('pubTopic').value;
            const messageStr = document.getElementById('pubMessage').value;
            
            try {
                const message = JSON.parse(messageStr);
                client.publish(topic, message);
                logMessage(`Published to ${topic}:`, message);
            } catch (err) {
                logMessage('Error publishing:', err.message);
            }
        });

        document.getElementById('subscribeBtn').addEventListener('click', () => {
            const topic = document.getElementById('subTopic').value;
            
            // Create subscription element
            const subEl = document.createElement('div');
            subEl.innerHTML = `
                <p>Subscribed to: ${topic} 
                <button class="unsub-btn">Unsubscribe</button></p>
            `;
            subscriptionsEl.appendChild(subEl);
            
            // Subscribe to topic
            const unsubscribe = client.subscribe(topic, (body, message) => {
                logMessage(`Received on ${topic}:`, body);
            });
            
            // Handle unsubscribe
            subEl.querySelector('.unsub-btn').addEventListener('click', () => {
                unsubscribe();
                subscriptionsEl.removeChild(subEl);
            });
            
            logMessage(`Subscribed to ${topic}`);
        });

        document.getElementById('requestBtn').addEventListener('click', () => {
            const topic = document.getElementById('reqTopic').value;
            const messageStr = document.getElementById('reqMessage').value;
            
            try {
                const message = JSON.parse(messageStr);
                logMessage(`Sending request to ${topic}:`, message);
                
                client.request(topic, message, 5000)
                    .then(response => {
                        responseEl.textContent = JSON.stringify(response, null, 2);
                        logMessage('Received response:', response);
                    })
                    .catch(err => {
                        responseEl.textContent = `Error: ${err.message}`;
                        logMessage('Request error:', err.message);
                    });
            } catch (err) {
                logMessage('Error sending request:', err.message);
            }
        });

        document.getElementById('clearBtn').addEventListener('click', () => {
            messagesEl.textContent = '';
        });

        // Connection events
        client.onConnect(() => {
            statusEl.textContent = 'Connected';
            statusEl.className = 'status connected';
            logMessage('Connected to server');
        });

        client.onDisconnect(() => {
            statusEl.textContent = 'Disconnected';
            statusEl.className = 'status disconnected';
            logMessage('Disconnected from server');
        });

        client.onError((err) => {
            logMessage('Error:', err.message);
        });

        // Helper function to log messages
        function logMessage(title, data) {
            const timestamp = new Date().toISOString();
            const message = document.createElement('div');
            
            if (data) {
                message.textContent = `[${timestamp}] ${title} ${JSON.stringify(data)}`;
            } else {
                message.textContent = `[${timestamp}] ${title}`;
            }
            
            messagesEl.prepend(message);
        }

        // Connect to the server
        client.connect();
    </script>
</body>
</html>
```

## Step 4: Build and Run

Now build and run your project:

```bash
cd /Users/username/PROJECTS/your-project
go mod tidy  # Get dependencies
go build
./your-project  # Run the server
```

Open your browser to http://localhost:9000 to see the WebSocketMQ client in action.

## Step 5: Using with Production Dependencies

When you're ready to publish your project or use it in production, you'll need to reference the published WebSocketMQ package rather than the local version. You can either:

1. Use the published version on GitHub:
   ```
   go get github.com/lightforgemedia/go-websocketmq
   ```

2. Or update your `go.mod` file to remove the replace directive and specify a version:
   ```
   module github.com/yourusername/your-project

   go 1.21

   require (
       github.com/lightforgemedia/go-websocketmq v0.1.0  // Or whatever version is published
   )
   ```

## Understanding the Example

The minimal example demonstrates key features of WebSocketMQ:

1. **Server-side**:
   - Creating a broker to handle message routing
   - Setting up a WebSocket handler to manage client connections
   - Subscribing to topics to handle client messages
   - Implementing a request-response pattern with the echo service
   - Publishing periodic messages with the ticker

2. **Client-side**:
   - Connecting to the WebSocket server
   - Publishing messages to topics
   - Subscribing to topics to receive messages
   - Making requests and handling responses
   - Connection management (auto-reconnect)

This example can be used as a starting point for your own WebSocketMQ-based applications.