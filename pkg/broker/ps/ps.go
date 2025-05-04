// pkg/broker/ps/ps.go
package ps

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/cskr/pubsub"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
)

type PubSubBroker struct{ bus *pubsub.PubSub }

func New(queueLen int) *PubSubBroker { return &PubSubBroker{bus: pubsub.New(queueLen)} }

func (b *PubSubBroker) Publish(ctx context.Context, m *model.Message) error {
	// Check if the context is canceled
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if m == nil {
		return errors.New("message cannot be nil")
	}

	raw, _ := json.Marshal(m)
	b.bus.Pub(raw, m.Header.Topic)
	return nil
}

func (b *PubSubBroker) Subscribe(ctx context.Context, topic string, fn broker.Handler) error {
	// Check if the context is canceled
	if ctx.Err() != nil {
		return ctx.Err()
	}

	ch := b.bus.Sub(topic)

	// Start a goroutine to handle messages
	go func() {
		// Ensure we unsubscribe when the context is done
		defer b.bus.Unsub(ch, topic)

		// Create a done channel from the context
		done := ctx.Done()

		for {
			select {
			case <-done:
				// Context is canceled, stop processing
				return
			case raw, ok := <-ch:
				if !ok {
					// Channel is closed
					return
				}

				var msg model.Message
				if err := json.Unmarshal(raw.([]byte), &msg); err != nil {
					// Skip invalid messages
					continue
				}

				// Create a new context for the handler
				handlerCtx, cancel := context.WithCancel(ctx)

				// Call the handler
				resp, err := fn(handlerCtx, &msg)
				cancel() // Clean up the handler context

				// If the handler returned a response, publish it
				if err == nil && resp != nil {
					// Use a background context for the response to ensure it's sent
					// even if the original context is canceled
					_ = b.Publish(context.Background(), resp)
				}
			}
		}
	}()

	return nil
}

func (b *PubSubBroker) Request(ctx context.Context, req *model.Message, timeoutMs int64) (*model.Message, error) {
	replySub := req.Header.CorrelationID
	respCh := b.bus.SubOnce(replySub)
	_ = b.Publish(ctx, req)
	select {
	case raw := <-respCh:
		var m model.Message
		_ = json.Unmarshal(raw.([]byte), &m)
		return &m, nil
	case <-time.After(time.Duration(timeoutMs) * time.Millisecond):
		return nil, context.DeadlineExceeded
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close closes the broker and cleans up resources.
func (b *PubSubBroker) Close() error {
	b.bus.Shutdown()
	return nil
}
