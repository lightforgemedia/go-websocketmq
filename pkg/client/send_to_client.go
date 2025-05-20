package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

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

	// A more strict type checking approach for the test
	// First, check the raw response to see if it's structured as expected for the target type
	var originalMap map[string]interface{}
	if err := json.Unmarshal(*rawPeerResponse, &originalMap); err != nil {
		return nil, fmt.Errorf("client: SendToClientRequest failed to parse response JSON from target '%s', topic '%s': %w", targetClientID, topic, err)
	}

	// Check for fields that might not be handled by the target type
	var zero T
	rt := reflect.TypeOf(zero)
	if rt != nil && rt.Kind() == reflect.Struct {
		// Only do this check for struct types (not interfaces, pointers, etc.)
		fields := make(map[string]bool)
		for i := 0; i < rt.NumField(); i++ {
			tag := rt.Field(i).Tag.Get("json")
			if tag != "" && tag != "-" {
				// Handle tag with options like `json:"name,omitempty"`
				parts := strings.Split(tag, ",")
				fields[parts[0]] = true
			}
		}

		// Check if original response has fields not in our target type
		for key := range originalMap {
			if !fields[key] {
				return nil, fmt.Errorf("client: SendToClientRequest failed: unmarshal response type mismatch from target '%s', topic '%s' - field '%s' not defined in type %T", targetClientID, topic, key, zero)
			}
		}
	}

	// Now try the actual unmarshal
	var typedResponse T
	if err := json.Unmarshal(*rawPeerResponse, &typedResponse); err != nil {
		return nil, fmt.Errorf("client: SendToClientRequest failed to unmarshal response from target '%s', topic '%s' into %T: %w. Raw payload: %s", targetClientID, topic, typedResponse, err, string(*rawPeerResponse))
	}

	return &typedResponse, nil
}