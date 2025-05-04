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

type Handler struct {
	Broker broker.Broker
}

func New(b broker.Broker) *Handler { return &Handler{Broker: b} }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	ctx := r.Context()
	go h.reader(ctx, ws)
}

func (h *Handler) reader(ctx context.Context, ws *websocket.Conn) {
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var m model.Message
		if json.Unmarshal(data, &m) != nil {
			continue
		}

		// basic routing: publish & if reply expected pipe back on correlationID
		if m.Header.Type == "request" {
			go func(req model.Message) {
				resp, err := h.Broker.Request(ctx, &req, 5000)
				if err != nil || resp == nil {
					return
				}
				raw, _ := json.Marshal(resp)
				_ = ws.Write(ctx, websocket.MessageText, raw)
			}(m)
		} else if m.Header.Type == "subscribe" {
			// Handle subscription messages
			go func(sub model.Message) {
				// Subscribe to the topic
				err := h.Broker.Subscribe(ctx, sub.Header.Topic, func(ctx context.Context, msg *model.Message) (*model.Message, error) {
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
			_ = h.Broker.Publish(ctx, &m)
		}
	}
}
