// pkg/server/connection.go
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"nhooyr.io/websocket"
)

// wsConnectionAdapter adapts a *websocket.Conn to the broker.ConnectionWriter interface.
type wsConnectionAdapter struct {
	conn           *websocket.Conn
	brokerClientID string
	writeTimeout   time.Duration
	logger         broker.Logger
	mu             sync.Mutex // Protects writes to the websocket connection
}

// newWSConnectionAdapter creates a new adapter.
func newWSConnectionAdapter(conn *websocket.Conn, clientID string, writeTimeout time.Duration, logger broker.Logger) *wsConnectionAdapter {
	return &wsConnectionAdapter{
		conn:           conn,
		brokerClientID: clientID,
		writeTimeout:   writeTimeout,
		logger:         logger,
	}
}

// WriteMessage sends a message to the WebSocket client.
func (a *wsConnectionAdapter) WriteMessage(ctx context.Context, msg *model.Message) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	data, err := json.Marshal(msg)
	if err != nil {
		a.logger.Error("wsConnectionAdapter: Failed to marshal message for client %s: %v", a.brokerClientID, err)
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	writeCtx, cancel := context.WithTimeout(ctx, a.writeTimeout)
	defer cancel()

	if err := a.conn.Write(writeCtx, websocket.MessageText, data); err != nil {
		// Check if the error is due to context cancellation (e.g. client disconnected)
		// or a genuine write error.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || websocket.CloseStatus(err) != -1 {
			a.logger.Warn("wsConnectionAdapter: Write to client %s failed (likely disconnected or timeout): %v", a.brokerClientID, err)
			return broker.ErrConnectionWrite // Indicate a connection issue
		}
		a.logger.Error("wsConnectionAdapter: Failed to write message to client %s: %v", a.brokerClientID, err)
		return fmt.Errorf("failed to write message: %w", err)
	}
	a.logger.Debug("wsConnectionAdapter: Sent message to client %s, topic %s, type %s", a.brokerClientID, msg.Header.Topic, msg.Header.Type)
	return nil
}

// BrokerClientID returns the unique ID for this connection.
func (a *wsConnectionAdapter) BrokerClientID() string {
	return a.brokerClientID
}

// Close closes the underlying WebSocket connection.
func (a *wsConnectionAdapter) Close() error {
	a.logger.Debug("wsConnectionAdapter: Closing connection for client %s", a.brokerClientID)
	return a.conn.Close(websocket.StatusNormalClosure, "connection closed by adapter")
}
