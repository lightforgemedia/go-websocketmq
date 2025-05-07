// pkg/server/handler.go
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"nhooyr.io/websocket"
)

// HandlerOptions configures the behavior of the WebSocket handler.
type HandlerOptions struct {
	MaxMessageSize int64
	AllowedOrigins []string
	WriteTimeout   time.Duration
	ReadTimeout    time.Duration // Note: nhooyr.io/websocket handles read timeouts differently; this is more for the write adapter.
	PingInterval   time.Duration // Note: nhooyr.io/websocket has its own keepalive. This can be for application-level pings if needed.
	// ClientRegisterTopic is the topic clients send messages to for session registration.
	ClientRegisterTopic string
}

// DefaultHandlerOptions returns default configuration.
func DefaultHandlerOptions() HandlerOptions {
	return HandlerOptions{
		MaxMessageSize:      1024 * 1024, // 1MB
		AllowedOrigins:      nil,
		WriteTimeout:        10 * time.Second,
		ReadTimeout:         60 * time.Second, // Used for initial read, then library manages
		PingInterval:        30 * time.Second,
		ClientRegisterTopic: "_client.register", // Default topic for client registration
	}
}

// Handler implements http.Handler for WebSocket connections.
type Handler struct {
	broker broker.Broker
	logger broker.Logger
	opts   HandlerOptions
	// activeBrokerClientIDs is used to prevent duplicate registration if a client sends multiple register messages
	// This is a simple mechanism; a more robust one might involve a per-connection state.
	activeBrokerClientIDs sync.Map 
}

// NewHandler creates a new WebSocket handler.
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
	}
}

// ServeHTTP handles WebSocket upgrade and connection lifecycle.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	acceptOptions := &websocket.AcceptOptions{
		OriginPatterns: h.opts.AllowedOrigins,
		// Subprotocols:     []string{"wsmq"}, // Optional: if you define a subprotocol
	}

	conn, err := websocket.Accept(w, r, acceptOptions)
	if err != nil {
		h.logger.Error("Failed to accept WebSocket connection: %v", err)
		return
	}
	defer conn.Close(websocket.StatusInternalError, "internal server error during handler lifecycle")

	if h.opts.MaxMessageSize > 0 {
		conn.SetReadLimit(h.opts.MaxMessageSize)
	}

	// Generate a unique ID for this server-side representation of the client connection
	brokerClientID := fmt.Sprintf("wsconn-%s", model.Message{}.Header.MessageID) // Leverage existing randomID via empty message
	
	h.logger.Info("WebSocket client connected: %s, assigned BrokerClientID: %s", r.RemoteAddr, brokerClientID)

	connAdapter := newWSConnectionAdapter(conn, brokerClientID, h.opts.WriteTimeout, h.logger)

	if err := h.broker.RegisterConnection(connAdapter); err != nil {
		h.logger.Error("Failed to register connection with broker for BrokerClientID %s: %v", brokerClientID, err)
		conn.Close(websocket.StatusInternalError, "failed to register connection")
		return
	}
	h.activeBrokerClientIDs.Store(brokerClientID, true) // Mark as active

	defer func() {
		if err := h.broker.DeregisterConnection(brokerClientID); err != nil {
			h.logger.Error("Error deregistering BrokerClientID %s: %v", brokerClientID, err)
		}
		h.activeBrokerClientIDs.Delete(brokerClientID)
		h.logger.Info("WebSocket client disconnected: %s, BrokerClientID: %s", r.RemoteAddr, brokerClientID)
	}()

	ctx := r.Context() // Use request context for connection lifetime
	h.handleMessages(ctx, conn, brokerClientID)
}

// handleMessages reads and processes messages from a single WebSocket connection.
func (h *Handler) handleMessages(ctx context.Context, conn *websocket.Conn, brokerClientID string) {
	for {
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
				websocket.CloseStatus(err) == websocket.StatusGoingAway ||
				errors.Is(err, context.Canceled) {
				h.logger.Debug("WebSocket connection %s closed normally or context canceled: %v", brokerClientID, err)
			} else {
				h.logger.Warn("WebSocket connection %s read error: %v", brokerClientID, err)
			}
			return // Exit loop on read error or closure
		}

		if msgType != websocket.MessageText {
			h.logger.Warn("Received non-text message from client %s, ignoring", brokerClientID)
			continue
		}

		var msg model.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			h.logger.Error("Failed to unmarshal message from client %s: %v. Data: %s", brokerClientID, err, string(data))
			// Optionally send an error back to the client if the message was a request
			// For now, just log and continue.
			continue
		}

		// Enrich message with source BrokerClientID for broker handlers
		msg.Header.SourceBrokerClientID = brokerClientID
		h.logger.Debug("Received message from client %s: Type=%s, Topic=%s, CorrID=%s",
			brokerClientID, msg.Header.Type, msg.Header.Topic, msg.Header.CorrelationID)

		// Handle different message types
		switch msg.Header.Type {
		case "event":
			// Publish event to the broker
			if err := h.broker.Publish(ctx, &msg); err != nil {
				h.logger.Error("Failed to publish event from client %s: %v", brokerClientID, err)
			}
		case "request":
			// This is a client-initiated request to a server-side handler.
			// The server-side handler is subscribed via broker.Subscribe().
			// The response from that handler will be sent back via its return value.
			go func(requestMsg model.Message) {
				// The topic of the request message is what the server-side handler is subscribed to.
				// The broker's Subscribe mechanism will route this.
				// We expect the broker's subscribed handler to potentially return a response.
				// The broker itself doesn't directly "respond" here; its subscribed handler does.
				// The PubSubBroker's Subscribe method already handles calling the handler
				// and publishing its response if one is returned.
				// So, we just publish it as if it's an event, but its "request" type
				// and CorrelationID signal its intent.
				// The actual request-response matching for client-to-server requests happens
				// if a server-side handler is subscribed to `requestMsg.Header.Topic`
				// and returns a response message. That response message will then be published
				// by the broker to `requestMsg.Header.CorrelationID`, which the client is listening on.

				// We simply publish this request to the broker.
				// If a handler is subscribed to msg.Header.Topic, it will process it.
				// If that handler returns a response, the PubSubBroker's Subscribe logic
				// will publish that response to msg.Header.CorrelationID.
				// The client JS library is responsible for listening on that CorrelationID.
				if err := h.broker.Publish(ctx, &requestMsg); err != nil {
					h.logger.Error("Failed to publish client request %s to broker: %v", brokerClientID, err)
					// Attempt to send an error response directly back if publishing fails
					errMsg := model.NewErrorMessage(&requestMsg, map[string]string{"error": "server failed to process request"})
					if connAdapter, ok := h.broker.(interface{ GetConnection(string) (broker.ConnectionWriter, bool) }); ok {
						if clientConn, found := connAdapter.GetConnection(brokerClientID); found {
							clientConn.WriteMessage(ctx, errMsg)
						}
					}
				}
			}(msg)

		case "response", "error":
			// This is a response from the client to a server-initiated request.
			// The message's Topic should be the CorrelationID of the original server request.
			// Publish it so the waiting RequestToClient call can pick it up.
			if msg.Header.CorrelationID == "" {
				h.logger.Warn("Received client %s %s message without CorrelationID: %+v", brokerClientID, msg.Header.Type, msg)
				continue
			}
			// The topic of a response message *is* its correlation ID.
			// The broker.RequestToClient is subscribed to this.
			if err := h.broker.Publish(ctx, &msg); err != nil {
				h.logger.Error("Failed to publish client %s %s to broker: %v", brokerClientID, msg.Header.Type, err)
			}

		case "subscribe", "unsubscribe":
			// For RPC, clients subscribe to "actionName" topics locally.
			// The server uses RequestToClient with msg.Topic = "actionName".
			// If general pub/sub from server to client is needed where clients
			// explicitly subscribe/unsubscribe to arbitrary topics, this logic would be needed.
			// For now, we assume RPC is primary, so these are logged but not processed server-side.
			h.logger.Info("Client %s sent '%s' message for topic '%s'. For RPC, clients subscribe locally to action names. This message type is not processed by server for RPC.",
				brokerClientID, msg.Header.Type, msg.Body)
			// If you want to support general client-driven subscriptions:
			// topicToManage, ok := msg.Body.(string)
			// if !ok { /* handle error */ }
			// if msg.Header.Type == "subscribe" {
			//    h.broker.SubscribeClientToTopic(brokerClientID, topicToManage) // Needs new broker method
			// } else {
			//    h.broker.UnsubscribeClientFromTopic(brokerClientID, topicToManage) // Needs new broker method
			// }

		default:
			// Handle client registration messages
			if msg.Header.Topic == h.opts.ClientRegisterTopic {
				bodyMap, ok := msg.Body.(map[string]interface{})
				if !ok {
					h.logger.Error("Invalid registration message body from client %s: not a map. Body: %+v", brokerClientID, msg.Body)
					continue
				}
				pageSessionID, ok := bodyMap["pageSessionID"].(string)
				if !ok || pageSessionID == "" {
					h.logger.Error("Invalid registration message from client %s: missing or empty pageSessionID. Body: %+v", brokerClientID, bodyMap)
					continue
				}

				// Publish an internal event for the SessionManager to pick up
				registrationEvent := model.NewEvent(broker.TopicClientRegistered, map[string]string{
					"pageSessionID":  pageSessionID,
					"brokerClientID": brokerClientID,
				})
				if err := h.broker.Publish(context.Background(), registrationEvent); err != nil { // Use background context for internal event
					h.logger.Error("Failed to publish client registration event for %s (page %s): %v", brokerClientID, pageSessionID, err)
				} else {
					h.logger.Info("Client %s registered with PageSessionID %s. Registration event published.", brokerClientID, pageSessionID)
				}
			} else {
				h.logger.Warn("Unknown message type '%s' from client %s. Topic: '%s'", msg.Header.Type, brokerClientID, msg.Header.Topic)
			}
		}
	}
}

// Close is not strictly needed for http.Handler, but can be useful for graceful shutdown.
// However, the primary cleanup is done via DeregisterConnection in the defer of ServeHTTP.
func (h *Handler) Close() error {
	h.logger.Info("WebSocketMQ Handler Close called. Active connections will be closed by their respective ServeHTTP goroutines.")
	// Note: Closing active connections here would be complex due to race conditions.
	// Rely on context cancellation and defer in ServeHTTP for individual connection cleanup.
	return nil
}