// Package hotreload provides hot reload functionality for web applications.
package hotreload

import (
	"context"
	"fmt"
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
		return fmt.Errorf("failed to start file watcher: %w", err)
	}

	// Set up broker request handler for client errors.
	// The JavaScript client will send a request to this topic when reporting errors.
	err := hr.broker.HandleClientRequest(TopicClientError, func(client broker.ClientHandle, payload map[string]interface{}) error {
		hr.handleClientError(client.ID(), payload)
		// No response payload needed for error reporting, just acknowledge receipt
		return nil
	})
	if err != nil {
		hr.logger.Error("Failed to register client error handler with broker", "error", err)
		return fmt.Errorf("failed to setup client error handler: %w", err)
	}

	// Set up broker request handler for client ready notifications
	err = hr.broker.HandleClientRequest(TopicHotReloadReady, func(client broker.ClientHandle, payload map[string]interface{}) error {
		hr.handleClientReady(client.ID(), payload)
		return nil
	})
	if err != nil {
		hr.logger.Error("Failed to register client ready handler with broker", "error", err)
		return fmt.Errorf("failed to setup client ready handler: %w", err)
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

// ForceReload triggers an immediate reload for all clients that are marked ready.
// This is primarily for testing purposes.
func (hr *HotReload) ForceReload() {
	hr.logger.Info("Force reload requested")
	hr.triggerReload()
}

// SetClientReady marks a client as ready for hot reload.
// This is primarily for testing purposes.
func (hr *HotReload) SetClientReady(clientID string, clientURL string) {
	hr.handleClientReady(clientID, map[string]interface{}{
		"url": clientURL,
		"userAgent": "TestUserAgent",
	})
}

// GetClientCount returns the number of clients that are ready for hot reload.
// This is primarily for testing purposes.
func (hr *HotReload) GetClientCount() int {
	hr.clientsMu.RLock()
	defer hr.clientsMu.RUnlock()
	
	ready := 0
	for _, client := range hr.clients {
		if client.status == "ready" {
			ready++
		}
	}
	return ready
}

// handleFileChange is called when a file changes
func (hr *HotReload) handleFileChange(file string) {
	hr.logger.Info("File changed, triggering hot reload", "file", file)

	// Notify all connected clients
	hr.triggerReload()
}

// triggerReload sends a reload command to all connected clients
func (hr *HotReload) triggerReload() {
	// Get all connected clients that are ready for hot reload
	var clientIDs []string
	hr.clientsMu.RLock()
	for id, info := range hr.clients {
		if info.status == "ready" {
			clientIDs = append(clientIDs, id)
		}
	}
	hr.clientsMu.RUnlock()
	
	if len(clientIDs) == 0 {
		hr.logger.Info("No ready clients to send hot reload command to")
		return
	}

	hr.logger.Info("Sending reload command", "client_count", len(clientIDs))
	
	// Create a reload payload with timestamp
	reloadPayload := map[string]interface{}{
		"timestamp": time.Now().Unix(),
	}
	
	// Use broker.Publish to send the message to all subscribed clients in one go
	ctx := context.Background()
	err := hr.broker.Publish(ctx, TopicHotReload, reloadPayload)
	if err != nil {
		hr.logger.Error("Failed to publish hot reload command", "error", err)
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
