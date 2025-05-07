// pkg/model/message.go
// Package model defines the core message types and structures used in WebSocketMQ.
//
// This package provides the Message and MessageHeader types that form the foundation
// of the messaging system, along with factory functions for creating different
// types of messages (events, requests, and responses).
package model

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	mrand "math/rand"
	"sync"
	"time"
)

var (
	pseudoRand *mrand.Rand
	once       sync.Once
)

// For randomID to avoid dependency on math/rand's global state and ensure some randomness.
// Not cryptographically secure, just for unique IDs.
func initRandom() {
	var seed int64
	err := binary.Read(rand.Reader, binary.LittleEndian, &seed)
	if err != nil {
		// Fallback to time-based seed if crypto/rand fails
		seed = time.Now().UnixNano()
	}
	pseudoRand = mrand.New(mrand.NewSource(seed))
}

func init() {
	once.Do(initRandom)
}

// MessageHeader contains metadata and routing information for a message.
// Headers include identifiers, timing information, and routing details.
type MessageHeader struct {
	// MessageID is a unique identifier for this specific message.
	MessageID string `json:"messageID"`

	// CorrelationID links related messages together, particularly
	// for request-response pairs where the response includes the
	// same correlation ID as the original request.
	CorrelationID string `json:"correlationID,omitempty"`

	// Type indicates the message purpose: "event", "request", or "response".
	// Events are one-way notifications, while requests expect responses.
	Type string `json:"type"`

	// Topic is the publish/subscribe channel for this message.
	// For RPC-style requests initiated by the server to a specific client,
	// this topic will often represent the "action name" or "procedure name".
	Topic string `json:"topic"`

	// Timestamp records when the message was created (milliseconds since epoch).
	Timestamp int64 `json:"timestamp"`

	// TTL (Time To Live) indicates how long a request should wait for a response
	// in milliseconds before timing out. Only used for request messages.
	TTL int64 `json:"ttl,omitempty"`

	// SourceBrokerClientID is an optional field that can be used internally by the server
	// to identify the origin connection of a message received from a client.
	// It is typically not set by clients or for server-to-client messages.
	SourceBrokerClientID string `json:"-"` // Ignored by JSON, for internal use
}

// Message is the core data structure that flows through the WebSocketMQ system.
// Each message contains a header with routing information and a body with
// the actual payload data.
type Message struct {
	// Header contains metadata and routing information for the message.
	Header MessageHeader `json:"header"`

	// Body contains the actual message payload, which can be any JSON-serializable value.
	Body any `json:"body"`
}

// NewEvent creates a new event message for the specified topic.
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

// NewRequest creates a new request message with a correlation ID for responses.
// For server-to-client RPC, 'topic' will be the action/procedure name.
func NewRequest(topic string, body any, timeoutMs int64) *Message {
	correlationID := randomID()
	return &Message{
		Header: MessageHeader{
			MessageID:     randomID(),
			CorrelationID: correlationID,
			Type:          "request",
			Topic:         topic, // For RPC, this is the action name
			Timestamp:     time.Now().UnixMilli(),
			TTL:           timeoutMs,
		},
		Body: body,
	}
}

// NewResponse creates a response message for a received request.
// The response Topic is set to the original request's CorrelationID.
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

// NewErrorMessage creates a specialized response indicating an error.
func NewErrorMessage(req *Message, errorBody any) *Message {
	return &Message{
		Header: MessageHeader{
			MessageID:     randomID(),
			CorrelationID: req.Header.CorrelationID,
			Type:          "error", // Specific type for errors
			Topic:         req.Header.CorrelationID,
			Timestamp:     time.Now().UnixMilli(),
		},
		Body: errorBody,
	}
}

// randomID generates a unique identifier for messages.
func randomID() string {
	// Simple ID, consider UUID for production robustness if collisions are a concern.
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), pseudoRand.Int63())
}
