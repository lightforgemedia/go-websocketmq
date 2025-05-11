// pkg/model/message_test.go
package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRandomID(t *testing.T) {
	id1 := RandomID()
	id2 := RandomID()

	assert.NotEmpty(t, id1, "RandomID should not return an empty string")
	assert.NotEmpty(t, id2, "RandomID should not return an empty string")
	assert.NotEqual(t, id1, id2, "Successive calls to RandomID should produce different IDs")
	assert.True(t, strings.Contains(id1, "-"), "RandomID should contain a hyphen")
}

func TestNewEvent(t *testing.T) {
	topic := "test.event.topic"
	bodyData := map[string]interface{}{"key": "value", "count": 123}
	startTime := time.Now().UnixMilli()

	event := NewEvent(topic, bodyData)
	endTime := time.Now().UnixMilli()

	require.NotNil(t, event, "NewEvent should not return nil")
	assert.NotEmpty(t, event.Header.MessageID, "Event MessageID should not be empty")
	assert.Empty(t, event.Header.CorrelationID, "Event CorrelationID should be empty")
	assert.Equal(t, KindEvent, event.Header.Type, "Event type should be KindEvent")
	assert.Equal(t, topic, event.Header.Topic, "Event topic mismatch")
	assert.GreaterOrEqual(t, event.Header.Timestamp, startTime, "Event timestamp too early")
	assert.LessOrEqual(t, event.Header.Timestamp, endTime, "Event timestamp too late")
	assert.Equal(t, bodyData, event.Body, "Event body mismatch")
	assert.Empty(t, event.Header.SourceBrokerClientID, "Event SourceBrokerClientID should be empty by default")
}

func TestNewRequest(t *testing.T) {
	topic := "test.request.topic"
	bodyData := struct{ User string }{User: "tester"}
	timeoutMs := int64(5000)
	startTime := time.Now().UnixMilli()

	request := NewRequest(topic, bodyData, timeoutMs)
	endTime := time.Now().UnixMilli()

	require.NotNil(t, request, "NewRequest should not return nil")
	assert.NotEmpty(t, request.Header.MessageID, "Request MessageID should not be empty")
	assert.NotEmpty(t, request.Header.CorrelationID, "Request CorrelationID should not be empty")
	assert.NotEqual(t, request.Header.MessageID, request.Header.CorrelationID, "MessageID and CorrelationID should be different for a new request")
	assert.Equal(t, KindRequest, request.Header.Type, "Request type should be KindRequest")
	assert.Equal(t, topic, request.Header.Topic, "Request topic mismatch")
	assert.GreaterOrEqual(t, request.Header.Timestamp, startTime, "Request timestamp too early")
	assert.LessOrEqual(t, request.Header.Timestamp, endTime, "Request timestamp too late")
	assert.Equal(t, timeoutMs, request.Header.TTL, "Request TTL mismatch")
	assert.Equal(t, bodyData, request.Body, "Request body mismatch")
	assert.Empty(t, request.Header.SourceBrokerClientID, "Request SourceBrokerClientID should be empty by default")
}

func TestNewResponse(t *testing.T) {
	// Create a mock request to respond to
	reqTopic := "original.request.topic"
	reqBody := map[string]int{"id": 1}
	reqTimeout := int64(1000)
	originalRequest := NewRequest(reqTopic, reqBody, reqTimeout)
	originalRequest.Header.SourceBrokerClientID = "client-123" // Simulate received from a client

	respBodyData := map[string]string{"status": "ok", "data": "processed"}
	startTime := time.Now().UnixMilli()

	response := NewResponse(originalRequest, respBodyData)
	endTime := time.Now().UnixMilli()

	require.NotNil(t, response, "NewResponse should not return nil")
	assert.NotEmpty(t, response.Header.MessageID, "Response MessageID should not be empty")
	assert.Equal(t, originalRequest.Header.CorrelationID, response.Header.CorrelationID, "Response CorrelationID should match request's")
	assert.Equal(t, KindResponse, response.Header.Type, "Response type should be KindResponse")
	assert.Equal(t, originalRequest.Header.CorrelationID, response.Header.Topic, "Response topic should be request's CorrelationID")
	assert.GreaterOrEqual(t, response.Header.Timestamp, startTime, "Response timestamp too early")
	assert.LessOrEqual(t, response.Header.Timestamp, endTime, "Response timestamp too late")
	assert.Equal(t, respBodyData, response.Body, "Response body mismatch")
	assert.Equal(t, originalRequest.Header.SourceBrokerClientID, response.Header.SourceBrokerClientID, "Response SourceBrokerClientID should be copied from request")

	// Test with nil request
	nilReqResponse := NewResponse(nil, respBodyData)
	require.NotNil(t, nilReqResponse)
	assert.Equal(t, KindResponse, nilReqResponse.Header.Type)
	assert.Equal(t, "error_nil_request_for_response", nilReqResponse.Header.Topic)
}

func TestNewErrorMessage(t *testing.T) {
	// Create a mock request
	reqTopic := "action.that.failed"
	reqBody := "input data"
	reqTimeout := int64(2000)
	originalRequest := NewRequest(reqTopic, reqBody, reqTimeout)
	originalRequest.Header.SourceBrokerClientID = "client-abc"

	errorDetails := map[string]interface{}{"code": 500, "reason": "internal server error"}
	startTime := time.Now().UnixMilli()

	errorMessage := NewErrorMessage(originalRequest, errorDetails)
	endTime := time.Now().UnixMilli()

	require.NotNil(t, errorMessage, "NewErrorMessage should not return nil")
	assert.NotEmpty(t, errorMessage.Header.MessageID, "Error MessageID should not be empty")
	assert.Equal(t, originalRequest.Header.CorrelationID, errorMessage.Header.CorrelationID, "Error CorrelationID should match request's")
	assert.Equal(t, KindError, errorMessage.Header.Type, "Error type should be KindError")
	assert.Equal(t, originalRequest.Header.CorrelationID, errorMessage.Header.Topic, "Error topic should be request's CorrelationID")
	assert.GreaterOrEqual(t, errorMessage.Header.Timestamp, startTime, "Error timestamp too early")
	assert.LessOrEqual(t, errorMessage.Header.Timestamp, endTime, "Error timestamp too late")
	assert.Equal(t, errorDetails, errorMessage.Body, "Error body mismatch")
	assert.Equal(t, originalRequest.Header.SourceBrokerClientID, errorMessage.Header.SourceBrokerClientID, "Error SourceBrokerClientID should be copied from request")

	// Test with nil request
	nilReqError := NewErrorMessage(nil, errorDetails)
	require.NotNil(t, nilReqError)
	assert.Equal(t, KindError, nilReqError.Header.Type)
	assert.Equal(t, "error_nil_request_for_error_response", nilReqError.Header.Topic)
}

func TestMessageSerialization(t *testing.T) {
	event := NewEvent("serialize.test", map[string]bool{"active": true})
	event.Header.SourceBrokerClientID = "should-not-be-serialized" // Internal field

	jsonData, err := json.Marshal(event)
	require.NoError(t, err, "JSON marshaling failed")

	jsonString := string(jsonData)
	assert.NotContains(t, jsonString, "sourceBrokerClientID", "SourceBrokerClientID should not be in JSON output")
	assert.Contains(t, jsonString, event.Header.MessageID, "MessageID missing from JSON")
	assert.Contains(t, jsonString, `"type":"event"`, "Type missing or incorrect in JSON")
	assert.Contains(t, jsonString, `"topic":"serialize.test"`, "Topic missing or incorrect in JSON")
	assert.Contains(t, jsonString, `"active":true`, "Body content missing or incorrect in JSON")

	var unmarshaledEvent Message
	err = json.Unmarshal(jsonData, &unmarshaledEvent)
	require.NoError(t, err, "JSON unmarshaling failed")

	assert.Equal(t, event.Header.MessageID, unmarshaledEvent.Header.MessageID)
	assert.Equal(t, event.Header.Type, unmarshaledEvent.Header.Type)
	assert.Equal(t, event.Header.Topic, unmarshaledEvent.Header.Topic)
	// Body will be map[string]interface{} after unmarshal
	if bodyMap, ok := unmarshaledEvent.Body.(map[string]interface{}); ok {
		assert.Equal(t, true, bodyMap["active"])
	} else {
		t.Errorf("Unmarshaled body is not a map[string]interface{}: %T", unmarshaledEvent.Body)
	}
	// SourceBrokerClientID is not serialized, so it won't be present on unmarshal
	assert.Empty(t, unmarshaledEvent.Header.SourceBrokerClientID)
}
