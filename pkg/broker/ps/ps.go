// pkg/broker/ps/ps.go
package ps

import (
	"context"
	"encoding/json"
	"errors"
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

	// For managing direct client connections
	connections      sync.Map // map[brokerClientID]broker.ConnectionWriter
	subscriptions    sync.Map // map[topicString]map[subID]broker.MessageHandler (server-side handlers)
	nextSubID        int64
	subIDMutex       sync.Mutex
	pendingRequests  sync.Map // map[correlationID]chan *model.Message
	closed           bool
	closedMutex      sync.Mutex
	shutdownComplete chan struct{}
}

type subscription struct {
	id      int64
	handler broker.MessageHandler
	ctx     context.Context // context for this specific subscription
	cancel  context.CancelFunc
}

// New creates a new PubSubBroker.
func New(logger broker.Logger, opts broker.Options) *PubSubBroker {
	if logger == nil {
		panic("logger must not be nil")
	}
	if opts.DefaultRequestTimeout == 0 {
		opts.DefaultRequestTimeout = 10 * time.Second
	}

	b := &PubSubBroker{
		bus:              pubsub.New(opts.QueueLength),
		logger:           logger,
		opts:             opts,
		shutdownComplete: make(chan struct{}),
	}
	logger.Info("PubSubBroker initialized")
	return b
}

func (b *PubSubBroker) isClosed() bool {
	b.closedMutex.Lock()
	defer b.closedMutex.Unlock()
	return b.closed
}

// Publish sends a message to subscribers.
func (b *PubSubBroker) Publish(ctx context.Context, msg *model.Message) error {
	if b.isClosed() {
		return broker.ErrBrokerClosed
	}
	if msg == nil {
		return broker.ErrInvalidMessage
	}

	// Marshal message to raw bytes for pubsub library
	// We need to be careful here: the pubsub library takes interface{},
	// but our handlers expect *model.Message.
	// To avoid double marshaling/unmarshaling, we can pass the *model.Message directly
	// if all subscribers are Go handlers. If we mix with raw byte subscribers (e.g. other systems),
	// then marshaling here is safer.
	// For now, assume we pass *model.Message directly.

	b.logger.Debug("Publishing message: Topic=%s, Type=%s, CorrID=%s, SrcClientID=%s",
		msg.Header.Topic, msg.Header.Type, msg.Header.CorrelationID, msg.Header.SourceBrokerClientID)

	// Dispatch to server-side handlers
	b.dispatchToHandlers(ctx, msg)

	// If this message is a response, it's typically targeted via its Topic (which is a CorrelationID)
	// and doesn't need to go to general topic subscribers or other clients unless explicitly designed.
	// However, if it's a general event, it might also need to be fanned out to connected clients
	// that are subscribed to this topic. This part is complex if clients subscribe to arbitrary topics.

	// For RPC, RequestToClient handles direct sending.
	// For general Pub/Sub to clients, clients would need to tell the server what they are subscribed to,
	// and the server (or broker) would need to manage these client-topic subscriptions.
	// This implementation currently focuses on server-side handlers and direct client RPC.

	// The cskr/pubsub library is used for internal signaling (e.g., responses to correlationIDs).
	// We are not using its Sub/Pub for general topic dispatch to handlers in this iteration,
	// instead, we manage subscriptions manually in b.subscriptions for more control over handler signatures.
	// Let's use the bus for correlation ID responses.
	if msg.Header.Type == "response" || msg.Header.Type == "error" {
		if msg.Header.CorrelationID != "" {
			// Marshal for the bus, as SubOnce expects raw data
			rawData, err := json.Marshal(msg)
			if err != nil {
				b.logger.Error("Failed to marshal response/error message for bus: %v", err)
				return fmt.Errorf("marshal response/error: %w", err)
			}
			b.bus.Pub(rawData, msg.Header.CorrelationID) // Topic is CorrelationID for responses
		}
	}


	return nil
}

func (b *PubSubBroker) dispatchToHandlers(ctx context.Context, msg *model.Message) {
	subsForTopic, ok := b.subscriptions.Load(msg.Header.Topic)
	if !ok {
		return
	}
	
	handlersMap, ok := subsForTopic.(*sync.Map) // map[subID]*subscription
	if !ok {
		b.logger.Error("internal error: subscription map for topic %s is not *sync.Map", msg.Header.Topic)
		return
	}

	handlersMap.Range(func(key, value interface{}) bool {
		sub, ok := value.(*subscription)
		if !ok {
			b.logger.Error("internal error: subscription value is not *subscription for topic %s", msg.Header.Topic)
			return true // continue iteration
		}

		// Check if subscription context is done
		select {
		case <-sub.ctx.Done():
			b.logger.Debug("Subscription context done for topic %s, subID %d. Skipping handler.", msg.Header.Topic, sub.id)
			// Optionally remove the subscription here if it's meant to be auto-cleaned on context done.
			// b.removeSubscription(msg.Header.Topic, sub.id)
			return true // continue iteration
		default:
			// continue to execute handler
		}
		
		// Execute handler in a new goroutine to prevent blocking the publish loop.
		go func(s *subscription, m *model.Message) {
			b.logger.Debug("Dispatching to handler for topic %s, subID %d", m.Header.Topic, s.id)
			responseMsg, err := s.handler(s.ctx, m, m.Header.SourceBrokerClientID) // Pass enriched SourceBrokerClientID
			if err != nil {
				b.logger.Error("Handler for topic %s (subID %d) returned error: %v", m.Header.Topic, s.id, err)
				// Optionally, if original message was a request, send an error response
				if m.Header.Type == "request" && m.Header.CorrelationID != "" {
					errMsg := model.NewErrorMessage(m, map[string]string{"error": fmt.Sprintf("handler error: %v", err)})
					if errPub := b.Publish(ctx, errMsg); errPub != nil {
						b.logger.Error("Failed to publish error message from handler error: %v", errPub)
					}
				}
				return
			}

			if responseMsg != nil {
				// Ensure the response is correctly correlated if the original was a request
				if m.Header.Type == "request" && m.Header.CorrelationID != "" {
					if responseMsg.Header.CorrelationID == "" {
						responseMsg.Header.CorrelationID = m.Header.CorrelationID
					}
					if responseMsg.Header.Topic == "" || responseMsg.Header.Topic == m.Header.Topic { // If topic is same as request or empty
						responseMsg.Header.Topic = m.Header.CorrelationID // Response topic is the correlation ID
					}
				}
				if err := b.Publish(s.ctx, responseMsg); err != nil { // Use subscription context for publishing response
					b.logger.Error("Failed to publish response from handler for topic %s (subID %d): %v", m.Header.Topic, s.id, err)
				}
			}
		}(sub, msg)
		return true // continue iteration
	})
}


// Subscribe registers a server-side handler for a topic.
func (b *PubSubBroker) Subscribe(ctx context.Context, topic string, handler broker.MessageHandler) error {
	if b.isClosed() {
		return broker.ErrBrokerClosed
	}
	if topic == "" {
		return errors.New("topic cannot be empty")
	}
	if handler == nil {
		return errors.New("handler cannot be nil")
	}

	b.subIDMutex.Lock()
	subID := b.nextSubID
	b.nextSubID++
	b.subIDMutex.Unlock()

	subCtx, subCancel := context.WithCancel(ctx) // Create a new context for this subscription
	newSub := &subscription{
		id:      subID,
		handler: handler,
		ctx:     subCtx,
		cancel:  subCancel,
	}

	actualMap, _ := b.subscriptions.LoadOrStore(topic, &sync.Map{})
	topicSubsMap := actualMap.(*sync.Map)
	topicSubsMap.Store(subID, newSub)

	b.logger.Info("Subscribed handler (ID %d) to topic: %s", subID, topic)

	// Goroutine to clean up subscription when its context is done
	go func() {
		<-subCtx.Done()
		b.removeSubscription(topic, subID)
		b.logger.Info("Subscription (ID %d) for topic %s cleaned up due to context cancellation.", subID, topic)
	}()

	return nil
}

func (b *PubSubBroker) removeSubscription(topic string, subID int64) {
	subsForTopic, ok := b.subscriptions.Load(topic)
	if !ok {
		return
	}
	topicSubsMap := subsForTopic.(*sync.Map)
	subValue, loaded := topicSubsMap.LoadAndDelete(subID)
	if loaded {
		if sub, ok := subValue.(*subscription); ok {
			sub.cancel() // Ensure cancel is called if not already
		}
		// If map becomes empty, consider removing it from b.subscriptions
		// This requires careful synchronization if other goroutines are adding to it.
		// For simplicity, we might leave empty maps.
	}
}


// Request sends a request and waits for a response (server-to-server or server-to-handler).
func (b *PubSubBroker) Request(ctx context.Context, req *model.Message, timeoutMs int64) (*model.Message, error) {
	if b.isClosed() {
		return nil, broker.ErrBrokerClosed
	}
	if req == nil || req.Header.CorrelationID == "" {
		return nil, broker.ErrInvalidMessage // CorrelationID is essential
	}
	if req.Header.Type != "request" {
		req.Header.Type = "request" // Ensure it's marked as a request
	}

	timeout := time.Duration(timeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = b.opts.DefaultRequestTimeout
	}

	// Use cskr/pubsub's SubOnce for request-response on CorrelationID
	replyCh := b.bus.SubOnce(req.Header.CorrelationID)
	defer b.bus.Unsub(replyCh, req.Header.CorrelationID) // Ensure unsubscription

	if err := b.Publish(ctx, req); err != nil { // Publish will dispatch to handlers
		return nil, fmt.Errorf("failed to publish request: %w", err)
	}
	b.logger.Debug("Request published, waiting for response on CorrID: %s, Topic: %s", req.Header.CorrelationID, req.Header.Topic)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(timeout):
		return nil, broker.ErrRequestTimeout
	case rawData, ok := <-replyCh:
		if !ok {
			return nil, errors.New("reply channel closed unexpectedly")
		}
		dataBytes, ok := rawData.([]byte)
		if !ok {
			return nil, errors.New("received non-byte data on reply channel")
		}
		var resp model.Message
		if err := json.Unmarshal(dataBytes, &resp); err != nil {
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}
		b.logger.Debug("Response received for CorrID: %s", req.Header.CorrelationID)
		return &resp, nil
	}
}

// RequestToClient sends a request to a specific client and waits for a response.
func (b *PubSubBroker) RequestToClient(ctx context.Context, brokerClientID string, req *model.Message, timeoutMs int64) (*model.Message, error) {
	if b.isClosed() {
		return nil, broker.ErrBrokerClosed
	}
	if req == nil {
		return nil, broker.ErrInvalidMessage
	}
	if req.Header.Type != "request" {
		req.Header.Type = "request" // Ensure it's a request
	}
	if req.Header.CorrelationID == "" {
		req.Header.CorrelationID = model.Message{}.Header.MessageID // Generate new if empty
	}


	connVal, ok := b.connections.Load(brokerClientID)
	if !ok {
		b.logger.Warn("RequestToClient: Client %s not found", brokerClientID)
		return nil, broker.ErrClientNotFound
	}
	conn, ok := connVal.(broker.ConnectionWriter)
	if !ok {
		b.logger.Error("RequestToClient: Connection for client %s is of wrong type", brokerClientID)
		return nil, broker.ErrClientNotFound // Or a different internal error
	}

	timeout := time.Duration(timeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = b.opts.DefaultRequestTimeout
	}

	replyCh := b.bus.SubOnce(req.Header.CorrelationID)
	defer b.bus.Unsub(replyCh, req.Header.CorrelationID)

	b.logger.Debug("RequestToClient: Sending to %s, Topic: %s, CorrID: %s", brokerClientID, req.Header.Topic, req.Header.CorrelationID)
	if err := conn.WriteMessage(ctx, req); err != nil {
		b.logger.Error("RequestToClient: Failed to write message to client %s: %v", brokerClientID, err)
		if errors.Is(err, broker.ErrConnectionWrite) { // If adapter signaled connection issue
			b.DeregisterConnection(brokerClientID) // Proactively deregister
		}
		return nil, broker.ErrConnectionWrite
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(timeout):
		b.logger.Warn("RequestToClient: Timeout waiting for response from %s on CorrID %s", brokerClientID, req.Header.CorrelationID)
		return nil, broker.ErrRequestTimeout
	case rawData, ok := <-replyCh:
		if !ok {
			b.logger.Error("RequestToClient: Reply channel for %s (CorrID %s) closed unexpectedly", brokerClientID, req.Header.CorrelationID)
			return nil, errors.New("reply channel closed")
		}
		dataBytes, ok := rawData.([]byte)
		if !ok {
			return nil, errors.New("received non-byte data on reply channel for client request")
		}
		var resp model.Message
		if err := json.Unmarshal(dataBytes, &resp); err != nil {
			b.logger.Error("RequestToClient: Failed to unmarshal response from %s (CorrID %s): %v", brokerClientID, req.Header.CorrelationID, err)
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}
		b.logger.Debug("RequestToClient: Response received from %s for CorrID %s", brokerClientID, req.Header.CorrelationID)
		return &resp, nil
	}
}

// RegisterConnection stores a new client connection.
func (b *PubSubBroker) RegisterConnection(conn broker.ConnectionWriter) error {
	if b.isClosed() {
		return broker.ErrBrokerClosed
	}
	if conn == nil || conn.BrokerClientID() == "" {
		return errors.New("invalid connection or empty BrokerClientID")
	}
	b.connections.Store(conn.BrokerClientID(), conn)
	b.logger.Info("Connection registered: BrokerClientID %s", conn.BrokerClientID())
	return nil
}

// DeregisterConnection removes a client connection.
func (b *PubSubBroker) DeregisterConnection(brokerClientID string) error {
	if brokerClientID == "" {
		return errors.New("BrokerClientID cannot be empty")
	}
	connVal, loaded := b.connections.LoadAndDelete(brokerClientID)
	if loaded {
		b.logger.Info("Connection deregistered: BrokerClientID %s", brokerClientID)
		// Publish an internal event that this client has disconnected
		deregisteredEvent := model.NewEvent(broker.TopicClientDeregistered, map[string]string{
			"brokerClientID": brokerClientID,
		})
		// Use a background context as this is an internal notification.
		if err := b.Publish(context.Background(), deregisteredEvent); err != nil {
			b.logger.Error("Failed to publish client deregistration event for %s: %v", brokerClientID, err)
		}
		// Optionally close the connection if it's not already closed by the server handler
		if conn, ok := connVal.(broker.ConnectionWriter); ok {
			 go conn.Close() // Close in a goroutine to avoid blocking
		}

	} else {
		b.logger.Warn("Attempted to deregister non-existent or already deregistered client: %s", brokerClientID)
	}
	return nil
}

// GetConnection (utility for server.Handler, not part of broker.Broker interface)
// This is a helper that might be used by server.Handler if it needs to send an error
// back to a client when broker.Publish fails for a client-initiated request.
// It's a bit of a layering concern, so use with caution.
func (b *PubSubBroker) GetConnection(brokerClientID string) (broker.ConnectionWriter, bool) {
	connVal, ok := b.connections.Load(brokerClientID)
	if !ok {
		return nil, false
	}
	conn, ok := connVal.(broker.ConnectionWriter)
	return conn, ok
}


// Close shuts down the broker.
func (b *PubSubBroker) Close() error {
	b.closedMutex.Lock()
	if b.closed {
		b.closedMutex.Unlock()
		return broker.ErrBrokerClosed
	}
	b.closed = true
	b.closedMutex.Unlock()

	b.logger.Info("PubSubBroker closing...")

	// Close all client connections
	b.connections.Range(func(key, value interface{}) bool {
		brokerClientID := key.(string)
		conn := value.(broker.ConnectionWriter)
		b.logger.Debug("Closing connection for client %s during broker shutdown", brokerClientID)
		conn.Close() // This should ideally trigger DeregisterConnection flow too
		b.connections.Delete(brokerClientID) // Ensure removal
		return true
	})
	b.logger.Info("All client connections instructed to close.")
	
	// Cancel all server-side subscriptions
	b.subscriptions.Range(func(topicKey, topicValue interface{}) bool {
		topicSubsMap, ok := topicValue.(*sync.Map)
		if !ok { return true }
		topicSubsMap.Range(func(subKey, subValue interface{}) bool {
			if sub, ok := subValue.(*subscription); ok {
				sub.cancel() // This will trigger cleanup goroutine for each subscription
			}
			return true
		})
		return true
	})
	b.logger.Info("All server-side subscriptions cancelled.")

	// Shutdown the cskr/pubsub bus
	// The library doesn't have an explicit Close/Shutdown. We rely on Unsub.
	// Outstanding SubOnce calls will eventually timeout or their channels will close.

	// Wait for a brief period for goroutines to finish
	// This is a heuristic. A more robust way would involve wait groups for active handlers.
	time.Sleep(100 * time.Millisecond) 

	close(b.shutdownComplete) // Signal that shutdown is complete
	b.logger.Info("PubSubBroker closed.")
	return nil
}

// Wait for shutdown to complete (for testing or graceful shutdown)
func (b *PubSubBroker) WaitForShutdown() {
	<-b.shutdownComplete
}