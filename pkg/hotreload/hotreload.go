// Package hotreload provides hot reload functionality for web applications.
package hotreload

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/filewatcher"
)

// Constants for topics
const (
	TopicHotReload      = "system:hot_reload"
	TopicClientError    = "system:client_error"
	TopicHotReloadReady = "system:hotreload_ready"
)

// HotReload coordinates file watching and browser reloading
type HotReload struct {
	broker    *broker.Broker
	watcher   *filewatcher.FileWatcher
	logger    *slog.Logger
	clients   map[string]*clientInfo
	clientsMu sync.RWMutex
	options   Options
}

// clientInfo stores information about a connected client
type clientInfo struct {
	id       string
	errors   []clientError
	status   string
	url      string
	lastSeen time.Time
}

// clientError represents a JavaScript error from a client
type clientError struct {
	message   string
	filename  string
	lineno    int
	colno     int
	stack     string
	timestamp string
}

// New creates a new HotReload service
func New(opts ...Option) (*HotReload, error) {
	// Create with default options
	hr := &HotReload{
		clients: make(map[string]*clientInfo),
		logger:  slog.New(slog.NewTextHandler(os.Stderr, nil)),
		options: DefaultOptions(),
	}

	// Apply options
	for _, opt := range opts {
		opt(hr)
	}

	// Validate required fields
	if hr.broker == nil {
		return nil, ErrNoBroker
	}

	if hr.watcher == nil {
		return nil, ErrNoFileWatcher
	}

	return hr, nil
}

// Start starts the hot reload service
func (hr *HotReload) Start() error {
	// Set up file watcher callback
	hr.watcher.AddCallback(hr.handleFileChange)

	// Start the file watcher
	if err := hr.watcher.Start(); err != nil {
		return err
	}

	// Set up broker request handler for client errors
	err := hr.broker.HandleClientRequest(TopicClientError, func(client broker.ClientHandle, payload map[string]interface{}) error {
		hr.handleClientError(client.ID(), payload)
		return nil
	})
	if err != nil {
		return err
	}

	// Also set up a subscription to handle published client errors
	// This is needed because the JavaScript client uses publish instead of request
	hr.broker.IterateClients(func(client broker.ClientHandle) bool {
		// Subscribe to the client error topic
		client.Send(context.Background(), TopicClientError, nil)
		return true
	})

	// Set up broker request handler for client ready notifications
	err = hr.broker.HandleClientRequest(TopicHotReloadReady, func(client broker.ClientHandle, payload map[string]interface{}) error {
		hr.handleClientReady(client.ID(), payload)
		return nil
	})
	if err != nil {
		return err
	}

	hr.logger.Info("Hot reload service started")
	return nil
}

// Stop stops the hot reload service
func (hr *HotReload) Stop() error {
	// Stop the file watcher
	if err := hr.watcher.Stop(); err != nil {
		return err
	}

	hr.logger.Info("Hot reload service stopped")
	return nil
}

// handleFileChange is called when a file changes
func (hr *HotReload) handleFileChange(file string) {
	hr.logger.Info("File changed, triggering hot reload", "file", file)

	// Notify all connected clients
	hr.triggerReload()
}

// triggerReload sends a reload command to all connected clients
func (hr *HotReload) triggerReload() {
	// Get all connected clients
	var clientIDs []string
	hr.broker.IterateClients(func(client broker.ClientHandle) bool {
		clientIDs = append(clientIDs, client.ID())
		return true
	})

	// Send reload command to each client
	ctx := context.Background()
	for _, id := range clientIDs {
		hr.logger.Info("Sending reload command to client", "client_id", id)

		// Find the client handle
		var clientHandle broker.ClientHandle
		hr.broker.IterateClients(func(ch broker.ClientHandle) bool {
			if ch.ID() == id {
				clientHandle = ch
				return false // Stop iteration
			}
			return true // Continue iteration
		})

		if clientHandle != nil {
			// Send reload command
			err := clientHandle.Send(ctx, TopicHotReload, struct{}{})
			if err != nil {
				hr.logger.Error("Failed to send reload command", "client_id", id, "error", err)
			}
		}
	}
}

// handleClientError handles client error reports
func (hr *HotReload) handleClientError(clientID string, payload map[string]interface{}) {
	hr.logger.Info("Received client error", "client_id", clientID, "error", payload)

	// Call custom error handler if provided
	if hr.options.ErrorHandler != nil {
		hr.options.ErrorHandler(clientID, payload)
	}

	// Update client info
	hr.clientsMu.Lock()
	defer hr.clientsMu.Unlock()

	client, ok := hr.clients[clientID]
	if !ok {
		client = &clientInfo{
			id:       clientID,
			errors:   []clientError{},
			status:   "connected",
			lastSeen: time.Now(),
		}
		hr.clients[clientID] = client
	}

	// Add the error
	client.errors = append(client.errors, clientError{
		message:   getStringOrDefault(payload, "message", "Unknown error"),
		filename:  getStringOrDefault(payload, "filename", ""),
		lineno:    getIntOrDefault(payload, "lineno", 0),
		colno:     getIntOrDefault(payload, "colno", 0),
		stack:     getStringOrDefault(payload, "stack", ""),
		timestamp: getStringOrDefault(payload, "timestamp", time.Now().Format(time.RFC3339)),
	})

	// Limit the number of errors stored
	if len(client.errors) > hr.options.MaxErrorsPerClient {
		client.errors = client.errors[len(client.errors)-hr.options.MaxErrorsPerClient:]
	}
}

// handleClientReady handles client ready notifications
func (hr *HotReload) handleClientReady(clientID string, payload map[string]interface{}) {
	hr.logger.Info("Client ready", "client_id", clientID, "payload", payload)

	// Update client info
	hr.clientsMu.Lock()
	defer hr.clientsMu.Unlock()

	client, ok := hr.clients[clientID]
	if !ok {
		client = &clientInfo{
			id:       clientID,
			errors:   []clientError{},
			status:   "ready",
			lastSeen: time.Now(),
		}
		hr.clients[clientID] = client
	} else {
		client.status = "ready"
		client.lastSeen = time.Now()
	}

	// Update URL if provided
	if url, ok := payload["url"].(string); ok {
		client.url = url
	}
}

// Helper functions for type conversion
func getStringOrDefault(m map[string]interface{}, key, defaultValue string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return defaultValue
}

func getIntOrDefault(m map[string]interface{}, key string, defaultValue int) int {
	if val, ok := m[key].(float64); ok {
		return int(val)
	}
	return defaultValue
}
