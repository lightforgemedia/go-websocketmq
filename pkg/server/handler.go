// pkg/server/handler.go
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"nhooyr.io/websocket"
)

// Options contains configuration options for the WebSocket handler.
type Options struct {
	// MaxMessageSize is the maximum size of a message in bytes.
	// Default: 1MB
	MaxMessageSize int64

	// AllowedOrigins is a list of allowed origins for WebSocket connections.
	// If empty, all origins are allowed (not recommended for production).
	AllowedOrigins []string
}

// DefaultOptions returns the default options for the WebSocket handler.
func DefaultOptions() Options {
	return Options{
		MaxMessageSize: 1024 * 1024, // 1MB
		AllowedOrigins: []string{},  // Empty means all origins allowed
	}
}

// Handler handles WebSocket connections and routes messages through the broker.
type Handler struct {
	Broker  broker.Broker
	Options Options
}

// New creates a new WebSocket handler with the given broker and options.
func New(b broker.Broker, opts ...Options) *Handler {
	h := &Handler{
		Broker:  b,
		Options: DefaultOptions(),
	}

	// Apply options if provided
	if len(opts) > 0 {
		h.Options = opts[0]
	}

	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Check origin if allowed origins are specified
	if len(h.Options.AllowedOrigins) > 0 {
		origin := r.Header.Get("Origin")
		allowed := false

		// Check if the origin is allowed
		for _, allowedOrigin := range h.Options.AllowedOrigins {
			if origin == allowedOrigin {
				allowed = true
				break
			}
		}

		// If not allowed, return 403 Forbidden
		if !allowed {
			w.WriteHeader(http.StatusForbidden)
			return
		}
	}

	// Accept the WebSocket connection
	acceptOptions := &websocket.AcceptOptions{
		// Only skip verification if no allowed origins are specified
		InsecureSkipVerify: len(h.Options.AllowedOrigins) == 0,
		// Set the maximum message size
		CompressionMode: websocket.CompressionDisabled,
	}

	ws, err := websocket.Accept(w, r, acceptOptions)
	if err != nil {
		return
	}

	// Set the maximum message size
	if h.Options.MaxMessageSize > 0 {
		ws.SetReadLimit(h.Options.MaxMessageSize)
	}

	// Create a context that's canceled when the connection is closed
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Handle the connection directly in this goroutine
	// This ensures that the WebSocket connection is properly managed
	h.handleConnection(ctx, ws)
}

func (h *Handler) handleConnection(ctx context.Context, ws *websocket.Conn) {
	// Create a map to track subscriptions for this connection
	subscriptions := make(map[string]context.CancelFunc)

	// Ensure all subscriptions are canceled when the reader exits
	defer func() {
		for _, cancel := range subscriptions {
			cancel()
		}
	}()

	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			// Check for context cancellation or connection closure
			if ctx.Err() != nil || websocket.CloseStatus(err) != -1 {
				return
			}
			// Log other errors and continue
			continue
		}

		var m model.Message
		if json.Unmarshal(data, &m) != nil {
			continue
		}

		// basic routing: publish & if reply expected pipe back on correlationID
		if m.Header.Type == "request" {
			go func(req model.Message) {
				// Create a context with timeout for the request
				reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()

				resp, err := h.Broker.Request(reqCtx, &req, 5000)
				if err != nil || resp == nil {
					return
				}

				// Check if the context is still valid
				if ctx.Err() != nil {
					return
				}

				raw, _ := json.Marshal(resp)
				_ = ws.Write(ctx, websocket.MessageText, raw)
			}(m)
		} else if m.Header.Type == "subscribe" {
			// Handle subscription messages
			go func(sub model.Message) {
				// Create a context for this subscription
				subCtx, cancel := context.WithCancel(ctx)

				// Store the cancel function to clean up later
				subscriptions[sub.Header.Topic] = cancel

				// Subscribe to the topic
				err := h.Broker.Subscribe(subCtx, sub.Header.Topic, func(msgCtx context.Context, msg *model.Message) (*model.Message, error) {
					// Check if the context is still valid
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}

					// Forward the message to the WebSocket client
					raw, _ := json.Marshal(msg)
					_ = ws.Write(ctx, websocket.MessageText, raw)
					return nil, nil
				})

				// Send a response to acknowledge the subscription
				resp := &model.Message{
					Header: model.MessageHeader{
						MessageID:     "resp-" + sub.Header.MessageID,
						CorrelationID: sub.Header.MessageID,
						Type:          "response",
						Topic:         sub.Header.Topic,
						Timestamp:     time.Now().UnixMilli(),
					},
					Body: map[string]any{
						"success": err == nil,
					},
				}
				raw, _ := json.Marshal(resp)
				_ = ws.Write(ctx, websocket.MessageText, raw)
			}(m)
		} else {
			// Check if the context is still valid
			if ctx.Err() != nil {
				return
			}

			_ = h.Broker.Publish(ctx, &m)
		}
	}
}
