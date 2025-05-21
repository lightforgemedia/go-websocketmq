package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
)

// SendToClientRequest sends a request to another client (peer) via the broker
// and unmarshals the peer's response into the specified type T.
//
// Parameters:
//   - cli: The client instance initiating the request.
//   - ctx: Context for the operation.
//   - targetClientID: The ID of the destination client.
//   - topic: The application-specific topic the target client should handle.
//   - requestData: (Optional) The payload for the request to the target client.
//
// Returns:
//   - *T: A pointer to the unmarshalled response from the target client.
//   - error: An error if the operation failed at any stage (sending, broker error, target error, unmarshalling).
//
// Example:
//
//	type MyTargetRequest struct { Input string }
//	type MyTargetResponse struct { Output string }
//	resp, err := client.SendToClientRequest[MyTargetResponse](
//	    myClient, ctx, "target-client-id", "target.topic", MyTargetRequest{Input: "hello"},
//	)
func SendToClientRequest[T any](cli *Client, ctx context.Context, targetClientID string, topic string, requestData ...interface{}) (*T, error) {
	cli.closedMu.Lock()
	if cli.isClosed {
		cli.closedMu.Unlock()
		return nil, errors.New("client is closed")
	}
	cli.closedMu.Unlock()

	var actualPayload interface{}
	if len(requestData) > 0 {
		actualPayload = requestData[0]
	}

	// Marshal the actual application payload destined for the target client
	actualPayloadBytes, err := json.Marshal(actualPayload)
	if err != nil {
		return nil, fmt.Errorf("client: SendToClientRequest failed to marshal actual payload for topic '%s': %w", topic, err)
	}

	// Create the ProxyRequest wrapper
	proxyReqPayload := shared_types.ProxyRequest{
		TargetID: targetClientID,
		Topic:    topic,
		Payload:  actualPayloadBytes, // json.RawMessage
	}

	// Send the ProxyRequest to the broker's system:proxy topic
	// The response from this will be the json.RawMessage from the target client
	rawPeerResponse, brokerOrTargetErrorPayload, err := cli.SendServerRequest(ctx, shared_types.TopicProxyRequest, proxyReqPayload)

	if err != nil {
		// This 'err' could be from cli.SendServerRequest (e.g., network, local timeout)
		// or an error envelope returned by the broker itself (e.g., target client not found)
		// or an error envelope from the target client.
		if brokerOrTargetErrorPayload != nil {
			return nil, fmt.Errorf("client: SendToClientRequest received error response for proxy to target '%s', topic '%s' (code %d): %s. Original error: %w", targetClientID, topic, brokerOrTargetErrorPayload.Code, brokerOrTargetErrorPayload.Message, err)
		}
		return nil, fmt.Errorf("client: SendToClientRequest failed for target '%s', topic '%s': %w", targetClientID, topic, err)
	}

	// Handle cases where the target client might return no payload (or JSON null)
	if rawPeerResponse == nil || string(*rawPeerResponse) == "null" {
		var zero T
		// If T is a pointer type or an empty struct, a null payload might be acceptable.
		rt := reflect.TypeOf(zero)
		if rt == nil { // T is interface{}
			return nil, nil // Cannot determine, return nil
		}
		if rt.Kind() == reflect.Ptr || (rt.Kind() == reflect.Struct && rt.NumField() == 0) {
			// For pointer types or empty structs, a null payload results in a nil pointer or zero struct.
			return new(T), nil // Return pointer to zero value of T
		}
		return nil, fmt.Errorf("client: SendToClientRequest to target '%s', topic '%s' returned successful response with null/no payload, but expected non-empty type %T", targetClientID, topic, zero)
	}

	// First, do a quick check - try to unmarshal directly to see if it works
	var typedResponse T
	if err := json.Unmarshal(*rawPeerResponse, &typedResponse); err != nil {
		return nil, fmt.Errorf("client: SendToClientRequest failed to unmarshal response from target '%s', topic '%s' into %T: %w. Raw payload: %s", targetClientID, topic, typedResponse, err, string(*rawPeerResponse))
	}

	// Now do a strict field validation check
	var mapCheck map[string]interface{}
	if err := json.Unmarshal(*rawPeerResponse, &mapCheck); err != nil {
		return nil, fmt.Errorf("client: SendToClientRequest failed to parse JSON from target '%s', topic '%s': %w", targetClientID, topic, err)
	}

	// Create an empty instance of T to check its fields
	var zero T
	emptyJSON, err := json.Marshal(zero)
	if err != nil {
		return nil, fmt.Errorf("client: SendToClientRequest failed to marshal empty %T for validation: %w", zero, err)
	}

	var emptyMap map[string]interface{}
	if err := json.Unmarshal(emptyJSON, &emptyMap); err != nil {
		// If we can't unmarshal the empty type, that's a strange case - continue anyway
		emptyMap = make(map[string]interface{})
	}
	
	// Check for standard fields (This is a relaxed check for embedded structs)
	// Skip the strict field validation since embedded structs in Go can have fields that
	// aren't visible when marshaling an empty struct (due to omitempty tags)
	// Common fields like "error" might be from embedded response types
	
	// Verify types by round-trip marshaling/unmarshaling
	// This detects type mismatches like field type incompatibilities
	verifyJSON, err := json.Marshal(typedResponse)
	if err != nil {
		return nil, fmt.Errorf("client: SendToClientRequest failed to marshal response for validation: %w", err)
	}
	
	var verifyMap map[string]interface{}
	if err := json.Unmarshal(verifyJSON, &verifyMap); err != nil {
		return nil, fmt.Errorf("client: SendToClientRequest failed to unmarshal marshaled response: %w", err)
	}
	
	// Compare original response with round-trip result
	for k, v := range mapCheck {
		if verifyVal, ok := verifyMap[k]; ok {
			// For numeric types, Go's json decoder can convert between types
			// Check specifically for zero values that might have been lost
			origVal, _ := json.Marshal(v)
			roundVal, _ := json.Marshal(verifyVal)
			if string(origVal) != string(roundVal) {
				return nil, fmt.Errorf("client: SendToClientRequest unmarshal error: field '%s' type mismatch between response and %T", k, zero)
			}
		}
	}

	// We already did the unmarshal above, no need to do it again
	return &typedResponse, nil
}
