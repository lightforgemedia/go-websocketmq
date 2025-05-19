// Package broker provides the core WebSocketMQ broker functionality.
package broker

import (
	"context"
	"time"
)

// GenericClientRequest sends a typed request to a client and automatically decodes the response into type T.
// This is a type-safe wrapper around ClientHandle.SendClientRequest that eliminates the need for manual type casting.
//
// Parameters:
//   - ch: The client handle to send the request to
//   - ctx: Context for cancellation and timeout
//   - topic: The topic string for the request
//   - requestData: The request payload (will be JSON-marshaled)
//   - timeout: Request timeout duration (0 uses broker default)
//
// Returns:
//   - *T: Pointer to the decoded response of type T
//   - error: Error if the request fails or response cannot be decoded
//
// Example:
//     type CalculateResponse struct {
//         Result int `json:"result"`
//     }
//     
//     resp, err := GenericClientRequest[CalculateResponse](
//         clientHandle,
//         context.Background(),
//         "calculate",
//         map[string]int{"a": 5, "b": 3},
//         5*time.Second,
//     )
//     if err != nil {
//         log.Printf("Request failed: %v", err)
//         return
//     }
//     fmt.Printf("Result: %d\n", resp.Result)
func GenericClientRequest[T any](ch ClientHandle, ctx context.Context, topic string, requestData interface{}, timeout time.Duration) (*T, error) {
	var resp T
	if err := ch.SendClientRequest(ctx, topic, requestData, &resp, timeout); err != nil {
		return nil, err
	}
	return &resp, nil
}
