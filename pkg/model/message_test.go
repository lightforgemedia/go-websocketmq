package model

import (
	"testing"
	"time"
)

func TestRandomID(t *testing.T) {
	// Test that RandomID returns non-empty strings
	id1 := RandomID()
	if id1 == "" {
		t.Error("RandomID returned an empty string")
	}

	// Test that RandomID returns different values on subsequent calls
	id2 := RandomID()
	if id1 == id2 {
		t.Error("RandomID returned the same ID twice in a row")
	}
}

func TestNewEvent(t *testing.T) {
	// Test creating a new event message
	topic := "test.topic"
	body := map[string]string{"key": "value"}
	
	msg := NewEvent(topic, body)
	
	// Check that the message has the correct properties
	if msg.Header.MessageID == "" {
		t.Error("NewEvent: MessageID is empty")
	}
	if msg.Header.Type != KindEvent {
		t.Errorf("NewEvent: Type is %s, expected %s", msg.Header.Type, KindEvent)
	}
	if msg.Header.Topic != topic {
		t.Errorf("NewEvent: Topic is %s, expected %s", msg.Header.Topic, topic)
	}
	if msg.Header.Timestamp == 0 {
		t.Error("NewEvent: Timestamp is 0")
	}
	if msg.Header.CorrelationID != "" {
		t.Errorf("NewEvent: CorrelationID should be empty, got %s", msg.Header.CorrelationID)
	}
	
	// Check that the body was set correctly
	bodyMap, ok := msg.Body.(map[string]string)
	if !ok {
		t.Errorf("NewEvent: Body is not a map[string]string, got %T", msg.Body)
	} else if bodyMap["key"] != "value" {
		t.Errorf("NewEvent: Body has incorrect value, got %v", bodyMap)
	}
}

func TestNewRequest(t *testing.T) {
	// Test creating a new request message
	topic := "test.action"
	body := map[string]int{"id": 123}
	ttl := int64(5000) // 5 seconds
	
	msg := NewRequest(topic, body, ttl)
	
	// Check that the message has the correct properties
	if msg.Header.MessageID == "" {
		t.Error("NewRequest: MessageID is empty")
	}
	if msg.Header.Type != KindRequest {
		t.Errorf("NewRequest: Type is %s, expected %s", msg.Header.Type, KindRequest)
	}
	if msg.Header.Topic != topic {
		t.Errorf("NewRequest: Topic is %s, expected %s", msg.Header.Topic, topic)
	}
	if msg.Header.Timestamp == 0 {
		t.Error("NewRequest: Timestamp is 0")
	}
	if msg.Header.CorrelationID == "" {
		t.Error("NewRequest: CorrelationID is empty")
	}
	if msg.Header.TTL != ttl {
		t.Errorf("NewRequest: TTL is %d, expected %d", msg.Header.TTL, ttl)
	}
	
	// Check that the body was set correctly
	bodyMap, ok := msg.Body.(map[string]int)
	if !ok {
		t.Errorf("NewRequest: Body is not a map[string]int, got %T", msg.Body)
	} else if bodyMap["id"] != 123 {
		t.Errorf("NewRequest: Body has incorrect value, got %v", bodyMap)
	}
}

func TestNewResponse(t *testing.T) {
	// Create a request to respond to
	reqTopic := "test.action"
	reqMsg := NewRequest(reqTopic, nil, 5000)
	
	// Test creating a response message
	respBody := map[string]bool{"success": true}
	
	msg := NewResponse(reqMsg, respBody)
	
	// Check that the message has the correct properties
	if msg.Header.MessageID == "" {
		t.Error("NewResponse: MessageID is empty")
	}
	if msg.Header.Type != KindResponse {
		t.Errorf("NewResponse: Type is %s, expected %s", msg.Header.Type, KindResponse)
	}
	if msg.Header.Topic != reqMsg.Header.CorrelationID {
		t.Errorf("NewResponse: Topic is %s, expected %s (request's CorrelationID)", 
			msg.Header.Topic, reqMsg.Header.CorrelationID)
	}
	if msg.Header.Timestamp == 0 {
		t.Error("NewResponse: Timestamp is 0")
	}
	if msg.Header.CorrelationID != reqMsg.Header.CorrelationID {
		t.Errorf("NewResponse: CorrelationID is %s, expected %s", 
			msg.Header.CorrelationID, reqMsg.Header.CorrelationID)
	}
	
	// Check that the body was set correctly
	bodyMap, ok := msg.Body.(map[string]bool)
	if !ok {
		t.Errorf("NewResponse: Body is not a map[string]bool, got %T", msg.Body)
	} else if !bodyMap["success"] {
		t.Errorf("NewResponse: Body has incorrect value, got %v", bodyMap)
	}
}

func TestNewErrorMessage(t *testing.T) {
	// Create a request that will result in an error
	reqTopic := "test.action"
	reqMsg := NewRequest(reqTopic, nil, 5000)
	
	// Test creating an error message
	errBody := map[string]string{"error": "Something went wrong"}
	
	msg := NewErrorMessage(reqMsg, errBody)
	
	// Check that the message has the correct properties
	if msg.Header.MessageID == "" {
		t.Error("NewErrorMessage: MessageID is empty")
	}
	if msg.Header.Type != KindError {
		t.Errorf("NewErrorMessage: Type is %s, expected %s", msg.Header.Type, KindError)
	}
	if msg.Header.Topic != reqMsg.Header.CorrelationID {
		t.Errorf("NewErrorMessage: Topic is %s, expected %s (request's CorrelationID)", 
			msg.Header.Topic, reqMsg.Header.CorrelationID)
	}
	if msg.Header.Timestamp == 0 {
		t.Error("NewErrorMessage: Timestamp is 0")
	}
	if msg.Header.CorrelationID != reqMsg.Header.CorrelationID {
		t.Errorf("NewErrorMessage: CorrelationID is %s, expected %s", 
			msg.Header.CorrelationID, reqMsg.Header.CorrelationID)
	}
	
	// Check that the body was set correctly
	bodyMap, ok := msg.Body.(map[string]string)
	if !ok {
		t.Errorf("NewErrorMessage: Body is not a map[string]string, got %T", msg.Body)
	} else if bodyMap["error"] != "Something went wrong" {
		t.Errorf("NewErrorMessage: Body has incorrect value, got %v", bodyMap)
	}
}

func TestMessageTimestamps(t *testing.T) {
	// Test that timestamps are set correctly (within a reasonable range)
	beforeTime := time.Now().UnixMilli() - 10 // 10ms buffer
	
	msg := NewEvent("test.topic", nil)
	
	afterTime := time.Now().UnixMilli() + 10 // 10ms buffer
	
	if msg.Header.Timestamp < beforeTime || msg.Header.Timestamp > afterTime {
		t.Errorf("Message timestamp %d is outside the expected range (%d to %d)",
			msg.Header.Timestamp, beforeTime, afterTime)
	}
}
