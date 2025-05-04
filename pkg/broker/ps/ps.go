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

func (b *PubSubBroker) Publish(_ context.Context, m *model.Message) error {
	if m == nil {
		return errors.New("message cannot be nil")
	}
	raw, _ := json.Marshal(m)
	b.bus.Pub(raw, m.Header.Topic)
	return nil
}

func (b *PubSubBroker) Subscribe(_ context.Context, topic string, fn broker.Handler) error {
	ch := b.bus.Sub(topic)
	go func() {
		for raw := range ch {
			var msg model.Message
			_ = json.Unmarshal(raw.([]byte), &msg)
			if resp, err := fn(context.Background(), &msg); err == nil && resp != nil {
				_ = b.Publish(context.Background(), resp) // response back on its correlationID topic
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
