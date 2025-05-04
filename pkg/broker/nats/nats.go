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

	// Use a mutex to ensure only one subscription is created per topic
	b.subsLock.Lock()
	defer b.subsLock.Unlock()

	// Check if we already have a subscription for this topic
	if sub, exists := b.subs[topic]; exists {
		// If the subscription exists but is invalid, remove it
		if sub == nil || sub.IsValid() == false {
			delete(b.subs, topic)
		} else {
			return nil // Already subscribed with a valid subscription
		}
	}

	// Create a new context for this subscription
	subCtx, cancel := context.WithCancel(ctx)

	// Subscribe to the topic with a queue group
	sub, err := b.conn.QueueSubscribe(topic, b.queueName, func(msg *nats.Msg) {
		// Check if the context is still valid
		select {
		case <-subCtx.Done():
			return // Context is canceled, don't process the message
		default:
		}

		// Unmarshal the message
		var m model.Message
		if err := json.Unmarshal(msg.Data, &m); err != nil {
			// Log error and return
			fmt.Printf("Error unmarshaling message: %v\n", err)
			return
		}

		// Create a context for this handler call
		handlerCtx, handlerCancel := context.WithCancel(subCtx)
		defer handlerCancel()

		// Call the handler
		resp, err := handler(handlerCtx, &m)
		if err != nil {
			// Log error and return
			fmt.Printf("Error handling message: %v\n", err)
			return
		}

		// If this is a request and the handler returned a response, publish the response
		if m.Header.Type == "request" && m.Header.CorrelationID != "" && resp != nil {
			// Set the response topic to the correlation ID
			resp.Header.Topic = m.Header.CorrelationID

			// Publish the response using a background context to ensure it's sent
			// even if the original context is canceled
			if err := b.Publish(context.Background(), resp); err != nil {
				fmt.Printf("Error publishing response: %v\n", err)
			}
		}
	})

	if err != nil {
		cancel() // Clean up the context if subscription fails
		return fmt.Errorf("failed to subscribe to topic: %w", err)
	}

	// Store the subscription
	b.subs[topic] = sub

	// Start a goroutine to clean up the subscription when the context is done
	go func() {
		<-ctx.Done()
		b.subsLock.Lock()
		if sub, exists := b.subs[topic]; exists && sub != nil {
			sub.Unsubscribe()
			delete(b.subs, topic)
		}
		b.subsLock.Unlock()
		cancel() // Clean up the subscription context
	}()

	return nil
}

// Request sends a request message and waits for a response.
func (b *NATSBroker) Request(ctx context.Context, req *model.Message, timeoutMs int64) (*model.Message, error) {
	if req == nil {
		return nil, errors.New("request message cannot be nil")
	}

	// Check if context is canceled
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Use NATS built-in request-reply pattern
	// This avoids the need to manage subscriptions manually
	msg := nats.NewMsg(req.Header.Topic)

	// Marshal the request message
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	msg.Data = data

	// Set timeout
	timeout := time.Duration(timeoutMs) * time.Millisecond

	// Create a context with timeout
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Send the request and wait for a response
	resp, err := b.conn.RequestMsgWithContext(reqCtx, msg)
	if err != nil {
		if err == nats.ErrTimeout {
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	// Unmarshal the response
	var respMsg model.Message
	if err := json.Unmarshal(resp.Data, &respMsg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &respMsg, nil
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
