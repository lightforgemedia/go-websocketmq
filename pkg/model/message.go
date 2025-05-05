// Package model defines the core message types and structures used in WebSocketMQ.
//
// This package provides the Message and MessageHeader types that form the foundation
// of the messaging system, along with factory functions for creating different
// types of messages (events, requests, and responses).
package model

import (
	"time"
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
	
	// Type indicates the message purpose: "event", "request", or "response".
	// Events are one-way notifications, while requests expect responses.
	Type string `json:"type"`
	
	// Topic is the publish/subscribe channel for this message.
	// Subscribers to this topic will receive the message.
	Topic string `json:"topic"`
	
	// Timestamp records when the message was created (milliseconds since epoch).
	Timestamp int64 `json:"timestamp"`
	
	// TTL (Time To Live) indicates how long a request should wait for a response
	// in milliseconds before timing out. Only used for request messages.
	TTL int64 `json:"ttl,omitempty"`
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
//
// Events are one-way, fire-and-forget messages that don't expect a response.
// They are used for broadcasting information to all subscribers of a topic.
//
// Parameters:
//   - topic: The topic to publish the event to
//   - body: The message payload, which can be any JSON-serializable value
//
// Returns a new Message configured as an event.
//
// Example:
//
//	eventMsg := model.NewEvent("user.login", map[string]interface{}{
//	    "username": "johndoe",
//	    "timestamp": time.Now().Unix(),
//	})
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
//
// Requests expect a response from a handler subscribed to the specified topic.
// The correlation ID is used to match responses back to the original request.
//
// Parameters:
//   - topic: The topic to send the request to
//   - body: The request payload, which can be any JSON-serializable value
//   - timeoutMs: Timeout in milliseconds for waiting for a response
//
// Returns a new Message configured as a request.
//
// Example:
//
//	reqMsg := model.NewRequest("calculate.sum", []int{1, 2, 3}, 5000)
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

// NewResponse creates a response message for a received request.
//
// Responses are sent back to the originator of a request. The correlation ID
// from the request is used to match the response back to the waiting request handler.
//
// Parameters:
//   - req: The original request message
//   - body: The response payload, which can be any JSON-serializable value
//
// Returns a new Message configured as a response to the given request.
//
// Example:
//
//	func handleSumRequest(ctx context.Context, m *model.Message) (*model.Message, error) {
//	    numbers, ok := m.Body.([]interface{})
//	    if !ok {
//	        return nil, fmt.Errorf("expected array of numbers")
//	    }
//	    
//	    sum := 0.0
//	    for _, n := range numbers {
//	        if num, ok := n.(float64); ok {
//	            sum += num
//	        }
//	    }
//	    
//	    return model.NewResponse(m, sum), nil
//	}
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

// randomID generates a unique identifier for messages.
// Currently implemented as a formatted timestamp for simplicity and zero dependencies.
// TODO: replace with UUID v4 in the future.
func randomID() string {
	return time.Now().Format("150405.000000")
}