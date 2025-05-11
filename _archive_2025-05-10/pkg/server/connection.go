// pkg/server/connection.go
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker" // Use broker from within the library
	"github.com/lightforgemedia/go-websocketmq/pkg/model"  // Use model from within the library
	"nhooyr.io/websocket"
)

// wsConnectionAdapter adapts a *websocket.Conn to the broker.ConnectionWriter interface.
// It handles message serialization and thread-safe writes to the WebSocket connection.
type wsConnectionAdapter struct {
	conn           *websocket.Conn
	brokerClientID string
	writeTimeout   time.Duration
	logger         broker.Logger
	writeMu        sync.Mutex // Protects writes to the websocket connection

	closedMu sync.Mutex // Protects access to `closed` flag
	closed   bool       // To prevent writes after Close() is called by broker or handler
}
type noOpLogger struct{}

func (l *noOpLogger) Debug(msg string, args ...any) {}
func (l *noOpLogger) Info(msg string, args ...any)  {}
func (l *noOpLogger) Warn(msg string, args ...any)  {}
func (l *noOpLogger) Error(msg string, args ...any) {}

// newWSConnectionAdapter creates a new adapter.
func newWSConnectionAdapter(conn *websocket.Conn, clientID string, writeTimeout time.Duration, logger broker.Logger) *wsConnectionAdapter {
	if logger == nil {
		// Fallback to a no-op logger if nil, though ideally it should always be provided.
		logger = &noOpLogger{}
		logger.Warn("wsConnectionAdapter: No logger provided, using no-op logger. This is not recommended.")
	}
	return &wsConnectionAdapter{
		conn:           conn,
		brokerClientID: clientID,
		writeTimeout:   writeTimeout,
		logger:         logger,
	}
}

// isAdapterMarkedClosed checks if the adapter has been marked as closed.
func (a *wsConnectionAdapter) isAdapterMarkedClosed() bool {
	a.closedMu.Lock()
	defer a.closedMu.Unlock()
	return a.closed
}

// WriteMessage sends a message to the WebSocket client.
// It ensures thread-safe writes and handles timeouts.
func (a *wsConnectionAdapter) WriteMessage(ctx context.Context, msg *model.Message) error {
	if a.isAdapterMarkedClosed() {
		a.logger.Warn("wsConnectionAdapter: Write attempt on already closed connection %s for msg topic %s", a.brokerClientID, msg.Header.Topic)
		return broker.ErrConnectionWrite
	}

	a.writeMu.Lock()
	defer a.writeMu.Unlock()

	// Double check after acquiring lock, in case Close was called concurrently
	if a.isAdapterMarkedClosed() {
		a.logger.Warn("wsConnectionAdapter: Write attempt on closed connection %s (checked after lock) for msg topic %s", a.brokerClientID, msg.Header.Topic)
		return broker.ErrConnectionWrite
	}

	data, err := json.Marshal(msg)
	if err != nil {
		a.logger.Error("wsConnectionAdapter: Failed to marshal message for client %s: %v. Message: %+v", a.brokerClientID, err, msg)
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	writeCtx := ctx
	var cancel context.CancelFunc
	if a.writeTimeout > 0 {
		writeCtx, cancel = context.WithTimeout(ctx, a.writeTimeout)
		defer cancel()
	}

	if err := a.conn.Write(writeCtx, websocket.MessageText, data); err != nil {
		// Check common error types to determine if it's a connection issue
		wsCloseErr := websocket.CloseError{}
		isCloseError := errors.As(err, &wsCloseErr) // Check if it's a websocket.CloseError

		// Context errors or specific WebSocket close statuses usually mean the connection is gone.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || isCloseError {
			a.logger.Warn("wsConnectionAdapter: Write to client %s failed (likely disconnected, timeout, or WebSocket close error): %v. CloseError Code (if any): %d", a.brokerClientID, err, wsCloseErr.Code)
			// Mark as closed internally to prevent further writes and signal to broker.
			a.markClosed()
			return broker.ErrConnectionWrite // Standard error for connection write issues
		}
		// For other types of errors, log and return a generic error
		a.logger.Error("wsConnectionAdapter: Failed to write message to client %s (unexpected net error): %v", a.brokerClientID, err)
		// It's possible this is also a fatal connection error, so mark closed.
		a.markClosed()
		return fmt.Errorf("failed to write message to websocket: %w", err) // Wrap original error
	}

	a.logger.Debug("wsConnectionAdapter: Sent message to client %s, topic %s, type %s, corrID %s",
		a.brokerClientID, msg.Header.Topic, msg.Header.Type, msg.Header.CorrelationID)
	return nil
}

// BrokerClientID returns the unique ID for this connection.
func (a *wsConnectionAdapter) BrokerClientID() string {
	return a.brokerClientID
}

// markClosed sets the internal closed flag.
func (a *wsConnectionAdapter) markClosed() {
	a.closedMu.Lock()
	if !a.closed {
		a.logger.Debug("wsConnectionAdapter: Marking connection %s as closed.", a.brokerClientID)
		a.closed = true
	}
	a.closedMu.Unlock()
}

// Close closes the underlying WebSocket connection and marks the adapter as closed.
// This method should be idempotent.
func (a *wsConnectionAdapter) Close() error {
	a.closedMu.Lock()
	if a.closed {
		a.closedMu.Unlock()
		a.logger.Debug("wsConnectionAdapter: Connection %s Close() called, but already marked as closed.", a.brokerClientID)
		return nil
	}
	// Mark as closed first to prevent races with WriteMessage
	a.closed = true
	a.closedMu.Unlock()

	a.logger.Info("wsConnectionAdapter: Closing connection for client %s (adapter Close() called)", a.brokerClientID)

	// Attempt to close the WebSocket connection with a normal status.
	// The error from conn.Close is often "status = StatusNormalClosure" or about underlying net.Conn.
	// It's generally safe to ignore some errors here if the intent is just to ensure it's closed.
	err := a.conn.Close(websocket.StatusNormalClosure, "server shutting down connection")
	if err != nil {
		// Log if error is not typical for a normal closure.
		wsCloseErr := websocket.CloseError{}
		if errors.As(err, &wsCloseErr) &&
			(wsCloseErr.Code == websocket.StatusNormalClosure || wsCloseErr.Code == websocket.StatusGoingAway) {
			// These are expected or acceptable close statuses.
			a.logger.Debug("wsConnectionAdapter: Normal close status %d for %s: %v", wsCloseErr.Code, a.brokerClientID, err)
		} else if strings.Contains(err.Error(), "already wrote close") ||
			strings.Contains(err.Error(), "connection reset by peer") ||
			strings.Contains(err.Error(), "broken pipe") ||
			strings.Contains(err.Error(), "use of closed network connection") {
			// Common errors indicating the connection was already closing or gone.
			a.logger.Debug("wsConnectionAdapter: Info during close for %s (likely already closed or underlying net conn issue): %v", a.brokerClientID, err)
		} else {
			// Potentially more significant error
			a.logger.Warn("wsConnectionAdapter: Error closing WebSocket for client %s: %v", a.brokerClientID, err)
			return err
		}
	}
	return nil
}
