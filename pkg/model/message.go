// pkg/model/message.go
package model

import (
	"time"
)

// MessageHeader contains metadata for a message
type MessageHeader struct {
	MessageID     string `json:"messageID"`
	CorrelationID string `json:"correlationID,omitempty"`
	Type          string `json:"type"`
	Topic         string `json:"topic"`
	Timestamp     int64  `json:"timestamp"`
	TTL           int64  `json:"ttl,omitempty"`
}

// Message represents the structure of all messages in the system
type Message struct {
	Header MessageHeader `json:"header"`
	Body   any           `json:"body"`
}

// NewEvent creates a new event message for the specified topic
func NewEvent(topic string, body any) *Message {
	return &Message{
		Header: MessageHeader{
			MessageID: randomID(),
			Type:      "event",
			Topic:     topic,
			Timestamp: time.Now().UnixMilli(),
		},
		Body: body,
	}
}

// NewRequest creates a new request message with a correlation ID for responses
func NewRequest(topic string, body any, timeoutMs int64) *Message {
	correlationID := randomID()
	return &Message{
		Header: MessageHeader{
			MessageID:     randomID(),
			CorrelationID: correlationID,
			Type:          "request",
			Topic:         topic,
			Timestamp:     time.Now().UnixMilli(),
			TTL:           timeoutMs,
		},
		Body: body,
	}
}

// NewResponse creates a response message for a request
func NewResponse(req *Message, body any) *Message {
	return &Message{
		Header: MessageHeader{
			MessageID:     randomID(),
			CorrelationID: req.Header.CorrelationID,
			Type:          "response",
			Topic:         req.Header.CorrelationID, // Publish to the correlation ID topic
			Timestamp:     time.Now().UnixMilli(),
		},
		Body: body,
	}
}

// TODO: replace with UUID v4 – keep zero‑dep for now.
func randomID() string {
	return time.Now().Format("150405.000000")
}