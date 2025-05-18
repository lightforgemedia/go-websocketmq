package broker

import "context"

// GenericClientRequest sends a typed request to a client and decodes the response into type T.
func GenericClientRequest[T any](ch ClientHandle, ctx context.Context, topic string, requestData interface{}, timeout time.Duration) (*T, error) {
	var resp T
	if err := ch.SendClientRequest(ctx, topic, requestData, &resp, timeout); err != nil {
		return nil, err
	}
	return &resp, nil
}
