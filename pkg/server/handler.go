// pkg/server/handler.go
package server

import (
    "context"
    "encoding/json"
    "net/http"

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
    if err != nil { return }
    ctx := r.Context()
    go h.reader(ctx, ws)
}

func (h *Handler) reader(ctx context.Context, ws *websocket.Conn) {
    for {
        _, data, err := ws.Read(ctx)
        if err != nil { return }
        var m model.Message
        if json.Unmarshal(data, &m) != nil { continue }

        // basic routing: publish & if reply expected pipe back on correlationID
        if m.Header.Type == "request" {
            go func(req model.Message) {
                resp, err := h.Broker.Request(ctx, &req, 5000)
                if err != nil || resp == nil { return }
                raw, _ := json.Marshal(resp)
                _ = ws.Write(ctx, websocket.MessageText, raw)
            }(m)
        } else {
            _ = h.Broker.Publish(ctx, &m)
        }
    }
}
