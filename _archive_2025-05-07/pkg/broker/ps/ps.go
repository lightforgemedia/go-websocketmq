// pkg/broker/ps/ps.go
package ps

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/cskr/pubsub"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
)

// PubSubBroker implements the broker.Broker interface using cskr/pubsub
type PubSubBroker struct {
	bus    *pubsub.PubSub
	logger broker.Logger
	opts   broker.Options
	mu     sync.RWMutex
	subs   map[string]chan interface{}
}

// New creates a new PubSubBroker with the given options
func New(logger broker.Logger, opts broker.Options) *PubSubBroker {
	if logger == nil {
		panic("logger must not be nil")
	}
	
	return &PubSubBroker{
		bus:    pubsub.New(opts.QueueLength),
		logger: logger,
		opts:   opts,
		subs:   make(map[string]chan interface{}),
	}
}

// Publish sends a message to the specified topic
func (b *PubSubBroker) Publish(_ context.Context, m *model.Message) error {
	if m == nil {
		return fmt.Errorf("cannot publish nil message")
	}
	
	raw, err := json.Marshal(m)
	if err != nil {
		b.logger.Error("Failed to marshal message: %v", err)
		return err
	}
	
	b.logger.Debug("Publishing to %s: %s", m.Header.Topic, string(raw))
	b.bus.Pub(raw, m.Header.Topic)
	return nil
}

// Subscribe registers a handler for a specific topic
func (b *PubSubBroker) Subscribe(ctx context.Context, topic string, fn broker.Handler) error {
	if topic == "" {
		return fmt.Errorf("topic cannot be empty")
	}
	
	if fn == nil {
		return fmt.Errorf("handler function cannot be nil")
	}
	
	b.logger.Info("Subscribing to topic: %s", topic)
	
	b.mu.Lock()
	ch := b.bus.Sub(topic)
	b.subs[topic] = ch
	b.mu.Unlock()
	
	go func() {
		for {
			select {
			case <-ctx.Done():
				b.mu.Lock()
				b.bus.Unsub(ch, topic)
				delete(b.subs, topic)
				b.mu.Unlock()
				b.logger.Debug("Unsubscribed from topic due to context cancellation: %s", topic)
				return
				
			case raw, ok := <-ch:
				if !ok {
					b.logger.Debug("Channel closed for topic: %s", topic)
					return
				}
				
				var msg model.Message
				if err := json.Unmarshal(raw.([]byte), &msg); err != nil {
					b.logger.Error("Failed to unmarshal message: %v", err)
					continue
				}
				
				b.logger.Debug("Received message on %s: %v", topic, msg.Body)
				
				go func(msg model.Message) {
					resp, err := fn(ctx, &msg)
					if err != nil {
						b.logger.Error("Handler error for topic %s: %v", topic, err)
						return
					}
					
					if resp != nil {
						if err := b.Publish(ctx, resp); err != nil {
							b.logger.Error("Failed to publish response: %v", err)
						}
					}
				}(msg)
			}
		}
	}()
	
	return nil
}

// Request sends a request and waits for a response with the given timeout
func (b *PubSubBroker) Request(ctx context.Context, req *model.Message, timeoutMs int64) (*model.Message, error) {
	if req == nil {
		return nil, fmt.Errorf("request cannot be nil")
	}
	
	if req.Header.CorrelationID == "" {
		return nil, fmt.Errorf("correlation ID is required for requests")
	}
	
	replyCh := b.bus.SubOnce(req.Header.CorrelationID)
	
	if err := b.Publish(ctx, req); err != nil {
		return nil, fmt.Errorf("failed to publish request: %w", err)
	}
	
	b.logger.Debug("Sent request to %s with correlation ID %s", req.Header.Topic, req.Header.CorrelationID)
	
	timeout := time.After(time.Duration(timeoutMs) * time.Millisecond)
	
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
		
	case <-timeout:
		return nil, context.DeadlineExceeded
		
	case raw, ok := <-replyCh:
		if !ok {
			return nil, fmt.Errorf("reply channel closed unexpectedly")
		}
		
		var resp model.Message
		if err := json.Unmarshal(raw.([]byte), &resp); err != nil {
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}
		
		b.logger.Debug("Received response for correlation ID %s", req.Header.CorrelationID)
		return &resp, nil
	}
}

// Close terminates the broker and cleans up resources
func (b *PubSubBroker) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	for topic, ch := range b.subs {
		b.bus.Unsub(ch, topic)
	}
	
	b.subs = make(map[string]chan interface{})
	b.logger.Info("Broker closed and all subscriptions removed")
	return nil
}