// examples/js_client/main.go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
)

func main() {
	// Create a logger
	logHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(logHandler)
	slog.SetDefault(logger)

	// Create a broker using the new Options pattern
	// This demonstrates how to use the structured Options approach
	opts := broker.DefaultOptions()
	opts.Logger = logger
	opts.AcceptOptions = &websocket.AcceptOptions{OriginPatterns: []string{"localhost:*"}}
	opts.PingInterval = 15 * time.Second
	opts.ClientSendBuffer = 32
	
	// You can also mix Options with functional options if needed:
	// ergoBroker, err := broker.NewWithOptions(opts, broker.WithServerRequestTimeout(30*time.Second))
	
	ergoBroker, err := broker.NewWithOptions(opts)
	if err != nil {
		logger.Error("Failed to create broker", "error", err)
		os.Exit(1)
	}

	// Register a handler for the GetTime request
	err = ergoBroker.HandleClientRequest(shared_types.TopicGetTime,
		func(client broker.ClientHandle, req shared_types.GetTimeRequest) (shared_types.GetTimeResponse, error) {
			logger.Info("Server: Client requested time", "clientID", client.ID())
			return shared_types.GetTimeResponse{CurrentTime: time.Now().Format(time.RFC3339)}, nil
		},
	)
	if err != nil {
		logger.Error("Failed to register handler", "topic", shared_types.TopicGetTime, "error", err)
		os.Exit(1)
	}

	// Create a server mux
	mux := http.NewServeMux()

	// Register both the WebSocket handler and JavaScript client handler with default options
	ergoBroker.RegisterHandlersWithDefaults(mux)

	// Serve a simple HTML page that uses the JavaScript client
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `
<!DOCTYPE html>
<html>
<head>
    <title>WebSocketMQ Example</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 20px; }
        #log { background-color: #f0f0f0; padding: 10px; height: 300px; overflow-y: auto; }
        button { margin: 5px; padding: 5px 10px; }
    </style>
</head>
<body>
    <h1>WebSocketMQ Example</h1>
    <div>
        <button id="connectBtn">Connect</button>
        <button id="disconnectBtn">Disconnect</button>
        <button id="getTimeBtn">Get Time</button>
    </div>
    <h2>Log</h2>
    <div id="log"></div>

    <script src="/websocketmq.js"></script>
    <script>
        const log = document.getElementById('log');
        const connectBtn = document.getElementById('connectBtn');
        const disconnectBtn = document.getElementById('disconnectBtn');
        const getTimeBtn = document.getElementById('getTimeBtn');

        function appendLog(message) {
            const entry = document.createElement('div');
            entry.textContent = message;
            log.appendChild(entry);
            log.scrollTop = log.scrollHeight;
        }

        let client = null;

        connectBtn.addEventListener('click', () => {
            if (client) {
                appendLog('Already connected');
                return;
            }

            appendLog('Connecting...');
            client = new WebSocketMQ.Client({
                url: 'ws://' + window.location.host + '/wsmq',
                clientName: 'BrowserExample',
                clientType: 'browser',
                logger: {
                    debug: (msg) => appendLog('DEBUG: ' + msg),
                    info: (msg) => appendLog('INFO: ' + msg),
                    warn: (msg) => appendLog('WARN: ' + msg),
                    error: (msg) => appendLog('ERROR: ' + msg)
                }
            });

            client.onConnect(() => {
                appendLog('Connected with ID: ' + client.getID());
                connectBtn.disabled = true;
                disconnectBtn.disabled = false;
                getTimeBtn.disabled = false;
            });

            client.onDisconnect(() => {
                appendLog('Disconnected');
                connectBtn.disabled = false;
                disconnectBtn.disabled = true;
                getTimeBtn.disabled = true;
                client = null;
            });

            client.connect();
        });

        disconnectBtn.addEventListener('click', () => {
            if (!client) {
                appendLog('Not connected');
                return;
            }
            client.disconnect();
        });

        getTimeBtn.addEventListener('click', () => {
            if (!client) {
                appendLog('Not connected');
                return;
            }
            appendLog('Requesting time...');
            client.sendServerRequest('system:get_time', {})
                .then(response => {
                    appendLog('Server time: ' + response.currentTime);
                })
                .catch(err => {
                    appendLog('Error getting time: ' + err.message);
                });
        });

        // Initialize button states
        disconnectBtn.disabled = true;
        getTimeBtn.disabled = true;
    </script>
</body>
</html>
`)
	})

	// Create an HTTP server
	httpServer := &http.Server{
		Addr:         ":8090",
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Start the server
	logger.Info("Starting server", "address", httpServer.Addr)
	fmt.Printf("WebSocketMQ server running at http://localhost%s\n", httpServer.Addr)
	fmt.Printf("Open your browser to http://localhost%s to see the example\n", httpServer.Addr)

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	// Shutdown gracefully
	logger.Info("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err)
	}

	if err := ergoBroker.Shutdown(ctx); err != nil {
		logger.Error("Broker shutdown error", "error", err)
	}

	logger.Info("Server shutdown complete")
}
