// pkg/server/handler.go
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"nhooyr.io/websocket"
)

// HandlerOptions configures the WebSocket handler
type HandlerOptions struct {
	MaxMessageSize int64        // Maximum message size in bytes
	AllowedOrigins []string     // List of allowed origins, empty means any origin
	WriteTimeout   time.Duration // Timeout for write operations
	ReadTimeout    time.Duration // Timeout for read operations
}

// DefaultHandlerOptions returns the default options for the handler
func DefaultHandlerOptions() HandlerOptions {
	return HandlerOptions{
		MaxMessageSize: 1024 * 1024, // 1MB
		AllowedOrigins: nil,         // Any origin
		WriteTimeout:   10 * time.Second,
		ReadTimeout:    60 * time.Second,
	}
}

// Handler implements http.Handler for WebSocket connections
type Handler struct {
	broker broker.Broker
	logger broker.Logger
	opts   HandlerOptions

	// Track active connections
	mu    sync.RWMutex
	conns map[*websocket.Conn]bool
}

// NewHandler creates a new WebSocket handler
func NewHandler(b broker.Broker, logger broker.Logger, opts HandlerOptions) *Handler {
	if logger == nil {
		panic("logger must not be nil")
	}
	if b == nil {
		panic("broker must not be nil")
	}

	return &Handler{
		broker: b,
		logger: logger,
		opts:   opts,
		conns:  make(map[*websocket.Conn]bool),
	}
}

// ServeHTTP implements http.Handler
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Configure WebSocket accept options
	acceptOptions := &websocket.AcceptOptions{
		OriginPatterns: h.opts.AllowedOrigins,
	}

	// Accept the WebSocket connection
	conn, err := websocket.Accept(w, r, acceptOptions)
	if err != nil {
		h.logger.Error("Failed to accept WebSocket connection: %v", err)
		return
	}

	if h.opts.MaxMessageSize > 0 {
		conn.SetReadLimit(h.opts.MaxMessageSize)
	}

	h.mu.Lock()
	h.conns[conn] = true
	h.mu.Unlock()

	// Start the reader goroutine
	ctx, cancel := context.WithCancel(r.Context())
	defer func() {
		cancel()
		h.mu.Lock()
		delete(h.conns, conn)
		h.mu.Unlock()
		_ = conn.Close(websocket.StatusNormalClosure, "handler closing")
	}()

	h.logger.Info("WebSocket client connected: %s", r.RemoteAddr)
	
	// Handle messages in a loop
	h.handleMessages(ctx, conn)
	
	h.logger.Info("WebSocket client disconnected: %s", r.RemoteAddr)
}

// handleMessages reads and processes messages from the WebSocket connection
func (h *Handler) handleMessages(ctx context.Context, conn *websocket.Conn) {
	// Generate a unique client ID for this connection
	clientID := fmt.Sprintf("client-%d", time.Now().UnixNano())
	
	// Subscribe to direct messages for this client
	h.broker.Subscribe(ctx, clientID, func(ctx context.Context, m *model.Message) (*model.Message, error) {
		return nil, h.sendMessageToClient(ctx, conn, m)
	})
	
	for {
		// Read the next message
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				h.logger.Debug("WebSocket closed normally")
			} else {
				h.logger.Error("WebSocket read error: %v", err)
			}
			return
		}
		
		// We only handle text messages (JSON)
		if msgType != websocket.MessageText {
			h.logger.Warn("Received non-text message from client, ignoring")
			continue
		}
		
		// Parse the message
		var msg model.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			h.logger.Error("Failed to unmarshal message: %v", err)
			continue
		}
		
		// Handle based on message type
		switch msg.Header.Type {
		case "event":
			// Just publish events
			if err := h.broker.Publish(ctx, &msg); err != nil {
				h.logger.Error("Failed to publish message: %v", err)
			}
			
		case "request":
			// For requests, use the broker's Request method and send the response back
			go func(msg model.Message) {
				resp, err := h.broker.Request(ctx, &msg, msg.Header.TTL)
				if err != nil {
					h.logger.Error("Request failed: %v", err)
					// Send error response
					errorMsg := model.NewResponse(&msg, map[string]interface{}{
						"error": err.Error(),
					})
					errorMsg.Header.Type = "error"
					if err := h.sendMessageToClient(ctx, conn, errorMsg); err != nil {
						h.logger.Error("Failed to send error response: %v", err)
					}
					return
				}
				
				// Send response back to client
				if resp != nil {
					if err := h.sendMessageToClient(ctx, conn, resp); err != nil {
						h.logger.Error("Failed to send response: %v", err)
					}
				}
			}(msg)
			
		case "subscribe":
			// Handle subscribe messages
			topic, ok := msg.Body.(string)
			if !ok {
				h.logger.Error("Invalid subscribe message, body is not a string topic")
				continue
			}
			
			// Subscribe to the topic and forward messages to this client
			h.broker.Subscribe(ctx, topic, func(ctx context.Context, m *model.Message) (*model.Message, error) {
				return nil, h.sendMessageToClient(ctx, conn, m)
			})
			
			h.logger.Debug("Client subscribed to topic: %s", topic)
			
		case "unsubscribe":
			// Unsubscribe is handled implicitly by context cancellation
			// This is a simplified implementation - a more complete one would track subscriptions per client
			h.logger.Debug("Unsubscribe not implemented in this version")
			
		default:
			h.logger.Warn("Unknown message type: %s", msg.Header.Type)
		}
	}
}

// sendMessageToClient sends a message to a specific WebSocket client
func (h *Handler) sendMessageToClient(ctx context.Context, conn *websocket.Conn, msg *model.Message) error {
	// Marshal the message to JSON
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}
	
	// Create a context with timeout for the write
	writeCtx, cancel := context.WithTimeout(ctx, h.opts.WriteTimeout)
	defer cancel()
	
	// Write the message to the WebSocket
	if err := conn.Write(writeCtx, websocket.MessageText, data); err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}
	
	return nil
}

// Close closes all WebSocket connections
func (h *Handler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	for conn := range h.conns {
		_ = conn.Close(websocket.StatusGoingAway, "server shutting down")
		delete(h.conns, conn)
	}
	
	return nil
}