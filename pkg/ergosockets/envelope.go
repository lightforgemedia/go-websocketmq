// ergosockets/envelope.go
package ergosockets

import (
	"encoding/json"
	"fmt"
)

// ErrorPayload defines the structure for errors within an Envelope.
type ErrorPayload struct {
	Code    int    `json:"code,omitempty"`    // Application-specific or HTTP-like status code
	Message string `json:"message,omitempty"` // Human-readable error message
}

// Envelope is the standard message structure for ErgoSockets communication.
type Envelope struct {
	ID      string          `json:"id,omitempty"`      // Unique identifier for request-response correlation
	Type    string          `json:"type"`              // e.g., "request", "response", "publish", "error", "subscribe_request", "unsubscribe_request"
	Topic   string          `json:"topic,omitempty"`   // Subject/channel for the message
	Payload json.RawMessage `json:"payload,omitempty"` // Application-specific data. `null` if no payload.
	Error   *ErrorPayload   `json:"error,omitempty"`   // Error details if this envelope represents an error
}

// Constants for Envelope Type
const (
	TypeRequest           = "request"
	TypeResponse          = "response"
	TypePublish           = "publish"
	TypeError             = "error" // Used when an operation results in an error, often in response to a request.
	TypeSubscribeRequest  = "subscribe_request"   // Client wants to subscribe
	TypeUnsubscribeRequest= "unsubscribe_request" // Client wants to unsubscribe
	TypeSubscriptionAck   = "subscription_ack"    // Server acknowledges subscription (can also carry error if sub failed)
)

// NewEnvelope creates a basic envelope.
// For requests without payload, pass nil for payloadData. wsjson will marshal nil as JSON `null`.
func NewEnvelope(id, typ, topic string, payloadData interface{}, errPayload *ErrorPayload) (*Envelope, error) {
	var payloadBytes json.RawMessage
	var err error
	// Only marshal if payloadData is not nil. If it's nil, payloadBytes remains nil,
	// which json.Marshal will correctly serialize as `null` for the Envelope.Payload field.
	if payloadData != nil {
		payloadBytes, err = json.Marshal(payloadData)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal payload for envelope: %w", err)
		}
	}
	return &Envelope{
		ID:      id,
		Type:    typ,
		Topic:   topic,
		Payload: payloadBytes, // Will be nil if payloadData was nil, leading to JSON `null`
		Error:   errPayload,
	}, nil
}

// DecodePayload unmarshals the Envelope's Payload into the provided value (must be a pointer).
func (e *Envelope) DecodePayload(v interface{}) error {
	if e.Payload == nil || string(e.Payload) == "null" {
		// If payload is null, and v is a pointer to a struct, unmarshalling will typically zero it.
		// If v is e.g. *interface{}, it might remain nil.
		// This behavior is generally fine. If a payload is strictly required,
		// the handler should check if the decoded value is its zero value.
		// For empty request structs, this is the desired behavior.
		return nil
	}
	return json.Unmarshal(e.Payload, v)
}