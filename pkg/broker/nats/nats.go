// Package nats provides a NATS implementation of the broker.Broker interface.
package nats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"github.com/nats-io/nats.go"
)

// NATSBroker is a broker implementation that uses NATS as the backend.
type NATSBroker struct {
	conn      *nats.Conn
	subs      map[string]*nats.Subscription
	subsLock  sync.RWMutex
	respSubs  map[string]chan *model.Message
	respLock  sync.RWMutex
	queueName string
}

// Options contains configuration options for the NATS broker.
type Options struct {
	// URL is the NATS server URL.
	URL string

	// QueueName is the name of the queue group to use for subscriptions.
	// If empty, a unique queue name will be generated.
	QueueName string

	// ConnectionOptions are additional options for the NATS connection.
	ConnectionOptions []nats.Option
}

// New creates a new NATS broker.
func New(opts Options) (*NATSBroker, error) {
	// Set default URL if not provided
	if opts.URL == "" {
		opts.URL = nats.DefaultURL
	}

	// Set default queue name if not provided
	if opts.QueueName == "" {
		opts.QueueName = fmt.Sprintf("websocketmq-%d", time.Now().UnixNano())
	}

	// Connect to NATS
	conn, err := nats.Connect(opts.URL, opts.ConnectionOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	return &NATSBroker{
		conn:      conn,
		subs:      make(map[string]*nats.Subscription),
		respSubs:  make(map[string]chan *model.Message),
		queueName: opts.QueueName,
	}, nil
}

// Publish publishes a message to the specified topic.
func (b *NATSBroker) Publish(ctx context.Context, m *model.Message) error {
	if m == nil {
		return errors.New("message cannot be nil")
	}

	// Check if context is canceled
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Marshal the message to JSON
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	// Publish the message to NATS
	return b.conn.Publish(m.Header.Topic, data)
}

// Subscribe subscribes to the specified topic and calls the handler function when a message is received.
func (b *NATSBroker) Subscribe(ctx context.Context, topic string, handler broker.Handler) error {
	// Check if context is canceled
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Check if we already have a subscription for this topic
	b.subsLock.RLock()
	_, exists := b.subs[topic]
	b.subsLock.RUnlock()

	if exists {
		return nil // Already subscribed
	}

	// Subscribe to the topic with a queue group
	sub, err := b.conn.QueueSubscribe(topic, b.queueName, func(msg *nats.Msg) {
		// Unmarshal the message
		var m model.Message
		if err := json.Unmarshal(msg.Data, &m); err != nil {
			// Log error and return
			fmt.Printf("Error unmarshaling message: %v\n", err)
			return
		}

		// Call the handler
		resp, err := handler(ctx, &m)
		if err != nil {
			// Log error and return
			fmt.Printf("Error handling message: %v\n", err)
			return
		}

		// If this is a request and the handler returned a response, publish the response
		if m.Header.Type == "request" && m.Header.CorrelationID != "" && resp != nil {
			// Set the response topic to the correlation ID
			resp.Header.Topic = m.Header.CorrelationID

			// Publish the response
			if err := b.Publish(ctx, resp); err != nil {
				fmt.Printf("Error publishing response: %v\n", err)
			}
		}
	})

	if err != nil {
		return fmt.Errorf("failed to subscribe to topic: %w", err)
	}

	// Store the subscription
	b.subsLock.Lock()
	b.subs[topic] = sub
	b.subsLock.Unlock()

	return nil
}

// Request sends a request message and waits for a response.
func (b *NATSBroker) Request(ctx context.Context, req *model.Message, timeoutMs int64) (*model.Message, error) {
	if req == nil {
		return nil, errors.New("request message cannot be nil")
	}

	// Create a channel to receive the response
	respChan := make(chan *model.Message, 1)

	// Store the response channel
	b.respLock.Lock()
	b.respSubs[req.Header.CorrelationID] = respChan
	b.respLock.Unlock()

	// Clean up when done
	defer func() {
		b.respLock.Lock()
		delete(b.respSubs, req.Header.CorrelationID)
		b.respLock.Unlock()
		close(respChan)
	}()

	// Subscribe to the response topic (correlation ID)
	sub, err := b.conn.Subscribe(req.Header.CorrelationID, func(msg *nats.Msg) {
		// Unmarshal the message
		var m model.Message
		if err := json.Unmarshal(msg.Data, &m); err != nil {
			// Log error and return
			fmt.Printf("Error unmarshaling response: %v\n", err)
			return
		}

		// Send the response to the channel
		respChan <- &m
	})

	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to response topic: %w", err)
	}
	defer sub.Unsubscribe()

	// Publish the request
	if err := b.Publish(ctx, req); err != nil {
		return nil, fmt.Errorf("failed to publish request: %w", err)
	}

	// Wait for the response or timeout
	timeout := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
	defer timeout.Stop()

	select {
	case resp := <-respChan:
		return resp, nil
	case <-timeout.C:
		return nil, context.DeadlineExceeded
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close closes the NATS connection and cleans up resources.
func (b *NATSBroker) Close() error {
	// Unsubscribe from all topics
	b.subsLock.Lock()
	for _, sub := range b.subs {
		sub.Unsubscribe()
	}
	b.subs = make(map[string]*nats.Subscription)
	b.subsLock.Unlock()

	// Close the NATS connection
	b.conn.Close()

	return nil
}
