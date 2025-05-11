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
	mrand "math/rand" // Renamed to avoid conflict with crypto/rand
	"sync"
	"time"
)

var (
	pseudoRandSource mrand.Source
	pseudoRandLock   sync.Mutex // To protect pseudoRandSource initialization and use if needed
	prng             *mrand.Rand
)

func init() {
	// Initialize with a seed from crypto/rand for better randomness than just time.
	var seed int64
	if err := binary.Read(rand.Reader, binary.LittleEndian, &seed); err != nil {
		// Fallback to time-based seed if crypto/rand fails for some reason (highly unlikely)
		seed = time.Now().UnixNano()
	}
	pseudoRandSource = mrand.NewSource(seed)
	prng = mrand.New(pseudoRandSource) // Create a new pseudo-random generator
}

// Kind represents the type of message (event, request, response, error)
type Kind string

// Predefined message kinds
const (
	KindEvent    Kind = "event"
	KindRequest  Kind = "request"
	KindResponse Kind = "response"
	KindError    Kind = "error"
)

// MessageHeader contains metadata and routing information for a message.
// Headers include identifiers, timing information, and routing details.
type MessageHeader struct {
	// MessageID is a unique identifier for this specific message.
	MessageID string `json:"messageID"`

	// CorrelationID links related messages together, particularly
	// for request-response pairs where the response includes the
	// same correlation ID as the original request.
	CorrelationID string `json:"correlationID,omitempty"`

	// Type indicates the message purpose: "event", "request", "response", or "error".
	// Events are one-way notifications, while requests expect responses.
	// Use the Kind constants (KindEvent, KindRequest, etc.) for type safety.
	Type Kind `json:"type"`

	// Topic is the publish/subscribe channel for this message.
	// For RPC-style requests initiated by the server to a specific client,
	// this topic will often represent the "action name" or "procedure name".
	// For responses, this is typically set to the CorrelationID of the request.
	Topic string `json:"topic"`

	// Timestamp records when the message was created (milliseconds since epoch).
	Timestamp int64 `json:"timestamp"`

	// TTL (Time To Live) indicates how long a request should wait for a response
	// in milliseconds before timing out. Only used for request messages.
	TTL int64 `json:"ttl,omitempty"`

	// SourceBrokerClientID is an optional field used internally by the server
	// to identify the origin connection of a message received from a client.
	// It is NOT part of the JSON message exchanged with clients.
	SourceBrokerClientID string `json:"-"` // Ignored by JSON, for internal use only
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

// RandomID generates a unique identifier for messages using a thread-safe PRNG.
// This is exported for use by other packages that need to generate IDs.
func RandomID() string {
	pseudoRandLock.Lock()
	// Simple ID format. Consider UUID for higher collision resistance in large distributed systems.
	// For typical single-server or small cluster use, this should be sufficient.
	id := fmt.Sprintf("%d-%d", time.Now().UnixNano(), prng.Int63())
	pseudoRandLock.Unlock()
	return id
}


// NewEvent creates a new event message for the specified topic.
func NewEvent(topic string, body any) *Message {
	return &Message{
		Header: MessageHeader{
			MessageID: RandomID(),
			Type:      KindEvent,
			Topic:     topic,
			Timestamp: time.Now().UnixMilli(),
		},
		Body: body,
	}
}

// NewRequest creates a new request message with a correlation ID for responses.
// For server-to-client RPC, 'topic' will be the action/procedure name.
// A new CorrelationID is generated for each request.
func NewRequest(topic string, body any, timeoutMs int64) *Message {
	return &Message{
		Header: MessageHeader{
			MessageID:     RandomID(),
			CorrelationID: RandomID(), // Each request gets a new CorrelationID
			Type:          KindRequest,
			Topic:         topic, // For RPC, this is the action name the remote end is listening on
			Timestamp:     time.Now().UnixMilli(),
			TTL:           timeoutMs,
		},
		Body: body,
	}
}

// NewResponse creates a response message for a received request.
// The response Topic is set to the original request's CorrelationID.
// It also copies the SourceBrokerClientID from the request to aid direct routing if needed.
func NewResponse(req *Message, body any) *Message {
	if req == nil {
		// Handle nil request gracefully, though this shouldn't happen in normal flow
		return &Message{
			Header: MessageHeader{
				MessageID: RandomID(),
				Type:      KindResponse,
				Topic:     "error_nil_request_for_response", // Or some other indicator
				Timestamp: time.Now().UnixMilli(),
			},
			Body: body,
		}
	}
	resp := &Message{
		Header: MessageHeader{
			MessageID:     RandomID(),
			CorrelationID: req.Header.CorrelationID, // Link to the original request
			Type:          KindResponse,
			Topic:         req.Header.CorrelationID, // Responses are published to the CorrelationID "topic"
			Timestamp:     time.Now().UnixMilli(),
		},
		Body: body,
	}
	// Preserve the originating client ID if available, for potential direct routing by broker
	resp.Header.SourceBrokerClientID = req.Header.SourceBrokerClientID
	return resp
}

// NewErrorMessage creates a specialized response indicating an error, linked to an original request.
// It also copies the SourceBrokerClientID from the request.
func NewErrorMessage(req *Message, errorBody any) *Message {
	if req == nil {
		return &Message{
			Header: MessageHeader{
				MessageID: RandomID(),
				Type:      KindError,
				Topic:     "error_nil_request_for_error_response",
				Timestamp: time.Now().UnixMilli(),
			},
			Body: errorBody,
		}
	}
	errMsg := &Message{
		Header: MessageHeader{
			MessageID:     RandomID(),
			CorrelationID: req.Header.CorrelationID, // Link to the original request
			Type:          KindError,
			Topic:         req.Header.CorrelationID, // Error responses also published to CorrelationID "topic"
			Timestamp:     time.Now().UnixMilli(),
		},
		Body: errorBody,
	}
	// Preserve the originating client ID for direct error routing if needed
	errMsg.Header.SourceBrokerClientID = req.Header.SourceBrokerClientID
	return errMsg
}