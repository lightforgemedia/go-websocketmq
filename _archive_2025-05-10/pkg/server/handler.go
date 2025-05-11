// pkg/server/handler.go
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker" // Use broker from within the library
	"github.com/lightforgemedia/go-websocketmq/pkg/model"  // Use model from within the library
	"nhooyr.io/websocket"
)

// HandlerOptions configures the behavior of the WebSocket handler.
type HandlerOptions struct {
	MaxMessageSize int64
	AllowedOrigins []string // If nil or empty, all origins are allowed by default by nhooyr.io/websocket
	WriteTimeout   time.Duration
	// ClientRegisterTopic is the topic clients send messages to for session registration.
	ClientRegisterTopic string
	// ClientRegisteredAckTopic is the topic on which the server sends registration acknowledgment (containing BrokerClientID).
	ClientRegisteredAckTopic string
}

// DefaultHandlerOptions returns default configuration.
func DefaultHandlerOptions() HandlerOptions {
	return HandlerOptions{
		MaxMessageSize:           1024 * 1024,    // 1MB
		AllowedOrigins:           nil,            // Default: allow all (suitable for local dev, review for prod)
		WriteTimeout:             10 * time.Second,
		ClientRegisterTopic:      "_client.register", // Default topic name for client registration messages
		ClientRegisteredAckTopic: "_internal.client.registered", // Default topic for server to ACK registration
	}
}

// Handler implements http.Handler for WebSocket connections.
// It manages the lifecycle of individual client connections and routes messages
// between the client and the broker.
type Handler struct {
	broker broker.Broker
	logger broker.Logger
	opts   HandlerOptions
}

// NewHandler creates a new WebSocket handler.
func NewHandler(b broker.Broker, logger broker.Logger, opts HandlerOptions) *Handler {
	if logger == nil {
		panic("logger must not be nil")
	}
	if b == nil {
		panic("broker must not be nil")
	}
	if opts.ClientRegisterTopic == "" {
		opts.ClientRegisterTopic = DefaultHandlerOptions().ClientRegisterTopic
		logger.Warn("ClientRegisterTopic not set in HandlerOptions, using default: %s", opts.ClientRegisterTopic)
	}
	if opts.ClientRegisteredAckTopic == "" {
		opts.ClientRegisteredAckTopic = DefaultHandlerOptions().ClientRegisteredAckTopic
		logger.Warn("ClientRegisteredAckTopic not set in HandlerOptions, using default: %s", opts.ClientRegisteredAckTopic)
	}


	if opts.AllowedOrigins == nil {
		logger.Warn("WebSocket Handler initialized with no specific AllowedOrigins (allowing all by default from nhooyr.io/websocket). Ensure this is intentional for your environment.")
	}

	return &Handler{
		broker: b,
		logger: logger,
		opts:   opts,
	}
}

// ServeHTTP handles WebSocket upgrade requests and manages the connection lifecycle.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var acceptOptions *websocket.AcceptOptions
	if len(h.opts.AllowedOrigins) > 0 {
		acceptOptions = &websocket.AcceptOptions{OriginPatterns: h.opts.AllowedOrigins}
	}
	// If h.opts.AllowedOrigins is nil or empty, websocket.Accept defaults to allowing all origins.

	conn, err := websocket.Accept(w, r, acceptOptions)
	if err != nil {
		// nhooyr.io/websocket handles writing the HTTP error response for common cases like bad origin.
		h.logger.Error("Failed to accept WebSocket connection from %s: %v", r.RemoteAddr, err)
		return
	}
	// This top-level defer is a final safety net.
	// The primary close is handled by the defer within the main logic block of this function.
	defer func() {
		// Attempt to close with a generic server error if not already closed.
		// The error from conn.Close() is often informational here.
		_ = conn.Close(websocket.StatusInternalError, "server handler unexpected exit")
		h.logger.Debug("ServeHTTP top-level defer: Ensured WebSocket connection %s is closed.", r.RemoteAddr)
	}()

	if h.opts.MaxMessageSize > 0 {
		conn.SetReadLimit(h.opts.MaxMessageSize)
	}

	brokerClientID := fmt.Sprintf("wsconn-%s", model.RandomID())
	h.logger.Info("WebSocket client connected: %s, assigned BrokerClientID: %s", r.RemoteAddr, brokerClientID)

	connAdapter := newWSConnectionAdapter(conn, brokerClientID, h.opts.WriteTimeout, h.logger)

	if err := h.broker.RegisterConnection(connAdapter); err != nil {
		h.logger.Error("Failed to register connection with broker for BrokerClientID %s: %v", brokerClientID, err)
		conn.Close(websocket.StatusInternalError, "broker registration failed")
		return
	}

	// This defer handles the normal lifecycle closure and deregistration.
	defer func() {
		h.logger.Info("WebSocket client processing ending for: %s, BrokerClientID: %s. Deregistering.", r.RemoteAddr, brokerClientID)
		if errDereg := h.broker.DeregisterConnection(brokerClientID); errDereg != nil {
			// Log error, but DeregisterConnection should be robust to being called multiple times
			// or if the connection is already gone.
			h.logger.Error("Error during explicit deregistration of BrokerClientID %s from broker: %v", brokerClientID, errDereg)
		}
		// The adapter's Close method should be called by DeregisterConnection.
		// If not, or as a safeguard, connAdapter.Close() could be called here,
		// but it should be idempotent.
		// Forcing another close on `conn` itself might be redundant if adapter.Close() did its job.
		// Let's rely on DeregisterConnection to handle adapter.Close().
	}()

	// Use request's context for the lifetime of this connection handling.
	// If the client disconnects or server shuts down, this context will be cancelled.
	h.handleMessages(r.Context(), connAdapter) // Removed brokerClientID as it's in adapter
}

// handleMessages reads and processes messages from a single WebSocket connection.
func (h *Handler) handleMessages(ctx context.Context, adapter *wsConnectionAdapter) {
	brokerClientID := adapter.BrokerClientID() // Get from adapter
	conn := adapter.conn                       // Underlying websocket.Conn from adapter
	
	for {
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			status := websocket.CloseStatus(err)
			// Normal closures or context cancellation
			if status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway ||
			   errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				h.logger.Info("WebSocket connection %s closed or context done: %v (status: %d)", brokerClientID, err, status)
			} else if strings.Contains(err.Error(), "EOF") || strings.Contains(err.Error(), "reset by peer") || strings.Contains(err.Error(), "broken pipe") { 
				// Common for abrupt client-side closures
				h.logger.Info("WebSocket connection %s likely closed abruptly: %v", brokerClientID, err)
			} else if status == websocket.StatusMessageTooBig {
				h.logger.Warn("WebSocket connection %s sent message exceeding size limit (MaxMessageSize: %d): %v", brokerClientID, h.opts.MaxMessageSize, err)
				// Connection is closed by the library in this case.
			} else {
				// Other, potentially unexpected errors
				h.logger.Warn("WebSocket connection %s read error: %v (status: %d)", brokerClientID, err, status)
			}
			// Any read error implies the connection is no longer usable.
			return // Exit loop, defer in ServeHTTP will handle deregistration.
		}

		if msgType != websocket.MessageText {
			h.logger.Warn("Received non-text message from client %s, ignoring.", brokerClientID)
			continue
		}

		var msg model.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			h.logger.Error("Failed to unmarshal message from client %s: %v. Data: %s", brokerClientID, err, string(data))
			// Consider sending a specific error message to the client if the protocol allows/requires.
			// For now, log and continue, assuming client might send other valid messages.
			// If this is a critical parsing failure, could close connection.
			// Example: adapter.WriteMessage(ctx, model.NewEvent("client.error", map[string]string{"error":"malformed JSON"}))
			continue
		}

		// Enrich message with source BrokerClientID for broker handlers
		msg.Header.SourceBrokerClientID = brokerClientID
		h.logger.Debug("Received message from client %s: Type=%s, Topic=%s, CorrID=%s",
			brokerClientID, msg.Header.Type, msg.Header.Topic, msg.Header.CorrelationID)

		// Handle client registration as a special event type
		if msg.Header.Type == model.KindEvent && msg.Header.Topic == h.opts.ClientRegisterTopic {
			h.handleClientRegistration(ctx, &msg, adapter)
			continue // Registration handled, proceed to next message
		}
		
		// For other messages, publish to the broker.
		// Use a timeout for the publish operation to prevent indefinite blocking.
		// Link it to the incoming request's context for cancellation propagation.
		publishCtx, publishCancel := context.WithTimeout(ctx, 10*time.Second) 

		if err := h.broker.Publish(publishCtx, &msg); err != nil {
			h.logger.Error("Failed to publish message from client %s to broker: %v. Message: %+v", brokerClientID, err, msg)
			// If this was a request from client, try to send an error response back
			if msg.Header.Type == model.KindRequest && msg.Header.CorrelationID != "" {
				errMsg := model.NewErrorMessage(&msg, map[string]string{"error": "server failed to process request via broker"})
				// Use the original context `ctx` for writing back, as `publishCtx` might have timed out.
				if writeErr := adapter.WriteMessage(ctx, errMsg); writeErr != nil {
					h.logger.Error("Failed to send broker processing error back to client %s: %v", brokerClientID, writeErr)
				}
			}
		}
		publishCancel() // Release resources of publishCtx
	}
}

// handleClientRegistration processes the client's registration message.
func (h *Handler) handleClientRegistration(ctx context.Context, msg *model.Message, adapter *wsConnectionAdapter) {
	brokerClientID := adapter.BrokerClientID()
	bodyMap, ok := msg.Body.(map[string]interface{}) // JS clients typically send JSON objects
	if !ok {
		h.logger.Error("Invalid registration message body from client %s: not a map. Body: %+v", brokerClientID, msg.Body)
		// Optionally send error back to client
		// Example: adapter.WriteMessage(ctx, model.NewEvent("client.error", map[string]string{"error":"invalid registration format"}))
		return
	}
	pageSessionID, psOk := bodyMap["pageSessionID"].(string)
	if !psOk || pageSessionID == "" {
		h.logger.Error("Invalid registration message from client %s: missing or empty pageSessionID. Body: %+v", brokerClientID, bodyMap)
		return
	}

	h.logger.Info("Processing registration for PageSessionID: %s by BrokerClientID: %s", pageSessionID, brokerClientID)

	// Publish internal event for SessionManager or other interested components.
	// This event signals that a WebSocket connection (BrokerClientID) is associated with a PageSessionID.
	registrationNotification := model.NewEvent(broker.TopicClientRegistered, map[string]string{
		"pageSessionID":  pageSessionID,
		"brokerClientID": brokerClientID,
	})
	// Use a background context or a short-lived context for this internal, non-blocking publish.
	internalPubCtx, cancelInternalPub := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelInternalPub()
	if err := h.broker.Publish(internalPubCtx, registrationNotification); err != nil {
		h.logger.Error("Failed to publish client registration notification event for %s (page %s): %v", brokerClientID, pageSessionID, err)
	} else {
		h.logger.Info("Client %s (page %s) registration notification event published to internal broker topic.", brokerClientID, pageSessionID)
	}

	// Send an acknowledgment event back to the client on the configured ACK topic.
	// This ACK confirms registration and provides the client with its BrokerClientID.
	ackMsg := model.NewEvent(h.opts.ClientRegisteredAckTopic, map[string]string{
		"brokerClientID": brokerClientID,
		"pageSessionID":  pageSessionID, // Echo back for client's confirmation
		"status":         "registered",
	})
	// Use the original connection context `ctx` for writing the ACK.
	if err := adapter.WriteMessage(ctx, ackMsg); err != nil {
		h.logger.Error("Failed to send registration ACK to client %s (page %s): %v", brokerClientID, pageSessionID, err)
	} else {
		h.logger.Info("Sent registration ACK with BrokerClientID %s to client %s (page %s) on topic %s",
			brokerClientID, brokerClientID, pageSessionID, h.opts.ClientRegisteredAckTopic)
	}
}

// Close is not part of http.Handler but can be called during graceful server shutdown.
// Connection cleanup is primarily handled by context cancellation in ServeHTTP/handleMessages.
func (h *Handler) Close() error {
	h.logger.Info("WebSocketMQ Handler Close() called. Active connections will be closed by their respective goroutines or broker shutdown.")
	// The broker's Close() method is responsible for broader system shutdown including its resources.
	// This handler doesn't manage a list of active connections itself; that's the broker's job.
	return nil
}