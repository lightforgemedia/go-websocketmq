// pkg/model/message_test.go
package model

import (
	"testing"
	"time"
)

func TestNewEvent(t *testing.T) {
	topic := "test.topic"
	body := map[string]interface{}{"key": "value"}
	
	msg := NewEvent(topic, body)
	
	if msg.Header.MessageID == "" {
		t.Error("MessageID should not be empty")
	}
	
	if msg.Header.Type != "event" {
		t.Errorf("Expected message type 'event', got '%s'", msg.Header.Type)
	}
	
	if msg.Header.Topic != topic {
		t.Errorf("Expected topic '%s', got '%s'", topic, msg.Header.Topic)
	}
	
	// Timestamp should be recent (within the last second)
	now := time.Now().UnixMilli()
	if now-msg.Header.Timestamp > 1000 {
		t.Errorf("Timestamp not recent enough: %d", msg.Header.Timestamp)
	}
	
	// Check body
	bodyMap, ok := msg.Body.(map[string]interface{})
	if !ok {
		t.Errorf("Expected body to be of type map[string]interface{}, got %T", msg.Body)
	}
	
	if value, ok := bodyMap["key"]; !ok || value != "value" {
		t.Errorf("Expected body to contain key 'key' with value 'value'")
	}
}

func TestNewRequest(t *testing.T) {
	topic := "test.request"
	body := map[string]interface{}{"action": "get"}
	timeoutMs := int64(5000)
	
	msg := NewRequest(topic, body, timeoutMs)
	
	if msg.Header.MessageID == "" {
		t.Error("MessageID should not be empty")
	}
	
	if msg.Header.CorrelationID == "" {
		t.Error("CorrelationID should not be empty")
	}
	
	if msg.Header.Type != "request" {
		t.Errorf("Expected message type 'request', got '%s'", msg.Header.Type)
	}
	
	if msg.Header.Topic != topic {
		t.Errorf("Expected topic '%s', got '%s'", topic, msg.Header.Topic)
	}
	
	if msg.Header.TTL != timeoutMs {
		t.Errorf("Expected TTL %d, got %d", timeoutMs, msg.Header.TTL)
	}
}

func TestNewResponse(t *testing.T) {
	// Create a request first
	reqTopic := "test.request"
	reqBody := map[string]interface{}{"action": "get"}
	req := NewRequest(reqTopic, reqBody, 5000)
	
	// Create a response to the request
	respBody := map[string]interface{}{"result": "success"}
	resp := NewResponse(req, respBody)
	
	if resp.Header.MessageID == "" {
		t.Error("MessageID should not be empty")
	}
	
	if resp.Header.CorrelationID != req.Header.CorrelationID {
		t.Errorf("Expected correlation ID '%s', got '%s'", req.Header.CorrelationID, resp.Header.CorrelationID)
	}
	
	if resp.Header.Type != "response" {
		t.Errorf("Expected message type 'response', got '%s'", resp.Header.Type)
	}
	
	if resp.Header.Topic != req.Header.CorrelationID {
		t.Errorf("Expected topic to be the request's correlation ID")
	}
	
	// Check body
	respMap, ok := resp.Body.(map[string]interface{})
	if !ok {
		t.Errorf("Expected body to be of type map[string]interface{}, got %T", resp.Body)
	}
	
	if value, ok := respMap["result"]; !ok || value != "success" {
		t.Errorf("Expected body to contain key 'result' with value 'success'")
	}
}