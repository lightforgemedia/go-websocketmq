// ergosockets/broker/client_handle.go
package broker

import (
	"context"
	"time"
)

// ClientHandle is an interface representing a client connection from the server's perspective.
// It's passed to server-side request handlers.
type ClientHandle interface {
	ID() string               // Unique server-assigned ID of the client (source of truth).
	ClientID() string         // Client-provided ID (for reference only).
	Name() string             // Human-readable name for the client.
	ClientType() string       // Type of client (e.g., "browser", "app", "service").
	ClientURL() string        // URL of the client (for browser connections).
	Context() context.Context // Context associated with this client's connection.

	// SendClientRequest sends a request to this specific client and waits for a response.
	// The responsePayloadPtr argument should be a pointer to a struct where the response will be unmarshalled.
	// Timeout <= 0 means use broker's default serverRequestTimeout.
	SendClientRequest(ctx context.Context, topic string, requestData interface{}, responsePayloadPtr interface{}, timeout time.Duration) error

	// Send publishes a message directly to this client on a specific topic without expecting a direct response.
	Send(ctx context.Context, topic string, payloadData interface{}) error
}
