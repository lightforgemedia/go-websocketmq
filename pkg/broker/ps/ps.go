// pkg/broker/ps/ps.go
package ps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cskr/pubsub" // Using this for internal request-response correlation
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
)

// PubSubBroker implements the broker.Broker interface.
// It uses a custom subscription management for server-side handlers
// and cskr/pubsub for correlating replies to requests.
type PubSubBroker struct {
	internalBus *pubsub.PubSub // For request-reply correlation (CorrelationID -> reply message)
	logger      broker.Logger
	opts        broker.Options

	connections   sync.Map // map[brokerClientID]broker.ConnectionWriter
	subscriptions sync.Map // map[topicString]*topicSubscriptions
	nextSubID     int64    // Access protected by subIDMutex
	subIDMutex    sync.Mutex

	closed           bool
	closedMutex      sync.Mutex
	shutdownWG       sync.WaitGroup // To wait for active handlers and cleanup goroutines
	shutdownComplete chan struct{}  // Closed when broker shutdown is fully complete
}

// topicSubscriptions holds all subscriptions for a single topic.
type topicSubscriptions struct {
	mu   sync.RWMutex
	subs map[int64]*subscription // map[subscriptionID]*subscription
}

// subscription represents a single server-side handler subscription.
type subscription struct {
	id      int64
	handler broker.MessageHandler
	ctx     context.Context // Context tied to this subscription's lifecycle
	cancel  context.CancelFunc
}

// New creates a new PubSubBroker.
func New(logger broker.Logger, opts broker.Options) *PubSubBroker {
	if logger == nil {
		// In a real library, you might provide a default no-op logger or return an error.
		// For this exercise, panic is acceptable to highlight missing dependency.
		panic("logger must not be nil")
	}
	if opts.DefaultRequestTimeout <= 0 {
		opts.DefaultRequestTimeout = 10 * time.Second // Ensure a sane default
	}
	if opts.QueueLength <= 0 {
		opts.QueueLength = 256 // Default for cskr/pubsub
	}

	b := &PubSubBroker{
		internalBus:      pubsub.New(opts.QueueLength),
		logger:           logger,
		opts:             opts,
		shutdownComplete: make(chan struct{}),
	}
	logger.Info("PubSubBroker initialized.")
	return b
}

func (b *PubSubBroker) isClosed() bool {
	b.closedMutex.Lock()
	defer b.closedMutex.Unlock()
	return b.closed
}

// Publish sends a message.
// - If it's a Response or Error, it's routed via internalBus using CorrelationID.
// - It's also dispatched to any server-side handlers subscribed to its Topic.
func (b *PubSubBroker) Publish(ctx context.Context, msg *model.Message) error {
	if b.isClosed() {
		return broker.ErrBrokerClosed
	}
	if msg == nil {
		return broker.ErrInvalidMessage
	}

	b.logger.Debug("Publishing message: Topic=%s, Type=%s, CorrID=%s, SrcClientID=%s",
		msg.Header.Topic, msg.Header.Type, msg.Header.CorrelationID, msg.Header.SourceBrokerClientID)

	// 1. Route responses/errors to internal bus for request-reply matching
	if msg.Header.Type == model.KindResponse || msg.Header.Type == model.KindError {
		if msg.Header.CorrelationID != "" {
			rawData, err := json.Marshal(msg) // Marshal for the bus
			if err != nil {
				b.logger.Error("Failed to marshal response/error message for internal bus: %v", err)
				return fmt.Errorf("marshal response/error for bus: %w", err)
			}
			// Pub on internalBus uses CorrelationID as the "topic" for SubOnce
			b.internalBus.Pub(rawData, msg.Header.CorrelationID)
			b.logger.Debug("Published response/error for CorrID %s to internal bus", msg.Header.CorrelationID)
		}
	}

	// 2. Dispatch to server-side handlers subscribed to this message's Topic
	b.dispatchToTopicHandlers(ctx, msg)

	return nil
}

// dispatchToTopicHandlers finds and executes server-side handlers for the message's topic.
func (b *PubSubBroker) dispatchToTopicHandlers(ctx context.Context, msg *model.Message) {
	val, ok := b.subscriptions.Load(msg.Header.Topic)
	if !ok {
		b.logger.Debug("No server-side subscriptions for topic: %s", msg.Header.Topic)
		return
	}
	topicSubs, ok := val.(*topicSubscriptions)
	if !ok {
		b.logger.Error("Internal error: subscription map for topic %s is not *topicSubscriptions", msg.Header.Topic)
		return
	}

	topicSubs.mu.RLock()
	currentHandlers := make([]*subscription, 0, len(topicSubs.subs))
	for _, sub := range topicSubs.subs {
		currentHandlers = append(currentHandlers, sub)
	}
	topicSubs.mu.RUnlock()

	if len(currentHandlers) == 0 {
		b.logger.Debug("No active server-side handlers for topic: %s (snapshot empty)", msg.Header.Topic)
		return
	}

	for _, sub := range currentHandlers {
		select {
		case <-sub.ctx.Done():
			b.logger.Debug("Subscription context done for topic %s, subID %d. Skipping handler.", msg.Header.Topic, sub.id)
			continue // Cleanup is handled by the goroutine in Subscribe
		default:
			// Proceed to execute handler
		}

		b.shutdownWG.Add(1)
		go func(s *subscription, m *model.Message, dispatchCtx context.Context) {
			defer b.shutdownWG.Done()

			// Use the subscription's context for the handler execution
			handlerCtx := s.ctx
			// If the dispatchCtx (original Publish context) has a shorter deadline, respect that too.
			// This is a bit complex; for now, primarily rely on subscription context.
			// A more advanced context merge could be done if needed.

			b.logger.Debug("Dispatching to handler for topic %s (subID %d)", m.Header.Topic, s.id)
			responseMsg, err := s.handler(handlerCtx, m, m.Header.SourceBrokerClientID)
			if err != nil {
				b.logger.Error("Handler for topic %s (subID %d) returned error: %v", m.Header.Topic, s.id, err)
				if m.Header.Type == model.KindRequest && m.Header.CorrelationID != "" {
					errMsg := model.NewErrorMessage(m, map[string]string{"error": fmt.Sprintf("handler error: %v", err)})
					// Use dispatchCtx for publishing this error, as handlerCtx might be done.
					if errPub := b.Publish(dispatchCtx, errMsg); errPub != nil {
						b.logger.Error("Failed to publish error message from handler error: %v", errPub)
					}
				}
				return
			}

			if responseMsg != nil {
				// Ensure response is correctly correlated if the original was a request
				if m.Header.Type == model.KindRequest && m.Header.CorrelationID != "" {
					responseMsg.Header.CorrelationID = m.Header.CorrelationID
					responseMsg.Header.Topic = m.Header.CorrelationID // Response topic is the correlation ID
				}

				// If original message came from a client, and this is a response/error for it,
				// try to send directly back.
				if m.Header.SourceBrokerClientID != "" &&
					(responseMsg.Header.Type == model.KindResponse || responseMsg.Header.Type == model.KindError) &&
					responseMsg.Header.CorrelationID == m.Header.CorrelationID {
					
					if conn, connOk := b.GetConnection(m.Header.SourceBrokerClientID); connOk {
						b.logger.Debug("Sending response directly to client %s for CorrID %s (Topic: %s)",
							m.Header.SourceBrokerClientID, responseMsg.Header.CorrelationID, responseMsg.Header.Topic)
						// Use handlerCtx (subscription's context) for writing back to client
						if writeErr := conn.WriteMessage(handlerCtx, responseMsg); writeErr != nil {
							b.logger.Error("Failed to write response directly to client %s: %v. Publishing to bus as fallback.",
								m.Header.SourceBrokerClientID, writeErr)
							if errPub := b.Publish(dispatchCtx, responseMsg); errPub != nil {
								b.logger.Error("Failed to publish response after direct write failed: %v", errPub)
							}
						}
					} else { // Client might have disconnected between receiving request and sending response
						b.logger.Warn("Client %s not found for direct response, publishing to bus for CorrID %s",
							m.Header.SourceBrokerClientID, responseMsg.Header.CorrelationID)
						if errPub := b.Publish(dispatchCtx, responseMsg); errPub != nil {
							b.logger.Error("Failed to publish response when client not found: %v", errPub)
						}
					}
				} else { // Not a direct response to a client request, or no source client. General publish.
					if errPub := b.Publish(dispatchCtx, responseMsg); errPub != nil {
						b.logger.Error("Failed to publish general response from handler for topic %s (subID %d): %v",
							m.Header.Topic, s.id, errPub)
					}
				}
			}
		}(sub, msg, ctx) // Pass original Publish context for further publishes if needed
	}
}

// Subscribe registers a server-side handler for a topic.
// The subscription remains active until the provided context `ctx` is cancelled.
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

	// Create a new context for this specific subscription, derived from the input context.
	// When the input `ctx` is done, `subCtx` will also be done.
	subCtx, subCancel := context.WithCancel(ctx)
	newSub := &subscription{
		id:      subID,
		handler: handler,
		ctx:     subCtx,
		cancel:  subCancel, // Store cancel to call it explicitly if needed (e.g. during broker Close)
	}

	val, _ := b.subscriptions.LoadOrStore(topic, &topicSubscriptions{subs: make(map[int64]*subscription)})
	topicSubs := val.(*topicSubscriptions)

	topicSubs.mu.Lock()
	topicSubs.subs[subID] = newSub
	topicSubs.mu.Unlock()

	b.logger.Info("Subscribed handler (ID %d) to topic: %s", subID, topic)

	b.shutdownWG.Add(1)
	go func() {
		defer b.shutdownWG.Done()
		<-subCtx.Done() // Wait for the subscription-specific context to be cancelled
		b.removeSubscription(topic, subID)
		b.logger.Info("Subscription (ID %d) for topic %s context done: %v. Cleaned up.", subID, topic, subCtx.Err())
	}()

	return nil
}

func (b *PubSubBroker) removeSubscription(topic string, subID int64) {
	val, ok := b.subscriptions.Load(topic)
	if !ok {
		return
	}
	topicSubs, ok := val.(*topicSubscriptions)
	if !ok {
		b.logger.Error("Internal error: subscription map for topic %s is not *topicSubscriptions during remove", topic)
		return
	}

	topicSubs.mu.Lock()
	sub, exists := topicSubs.subs[subID]
	if exists {
		delete(topicSubs.subs, subID)
		// Ensure cancel is called if it wasn't the source of this removal path
		// (though it usually is via the goroutine in Subscribe).
		if sub.cancel != nil {
			sub.cancel()
		}
	}
	// Consider removing the topic from b.subscriptions if topicSubs.subs is empty.
	// This needs careful locking if done. For simplicity, allow empty maps.
	// if len(topicSubs.subs) == 0 {
	//    b.subscriptions.Delete(topic)
	// }
	topicSubs.mu.Unlock()

	if exists {
		b.logger.Debug("Removed subscription (ID %d) for topic: %s", subID, topic)
	}
}

// Request sends a request to a server-side handler and waits for a response.
func (b *PubSubBroker) Request(ctx context.Context, req *model.Message, timeoutMs int64) (*model.Message, error) {
	if b.isClosed() {
		return nil, broker.ErrBrokerClosed
	}
	if req == nil || req.Header.Topic == "" { // Topic is where the handler is subscribed
		return nil, broker.ErrInvalidMessage
	}
	if req.Header.Type != model.KindRequest {
		req.Header.Type = model.KindRequest
	}
	if req.Header.CorrelationID == "" {
		req.Header.CorrelationID = model.RandomID()
	}

	timeout := time.Duration(timeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = b.opts.DefaultRequestTimeout
	}
	// Create a new context for this request with its own timeout.
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	replyCh := b.internalBus.SubOnce(req.Header.CorrelationID)
	defer b.internalBus.Unsub(replyCh, req.Header.CorrelationID)

	// Publish the request. It will be picked up by dispatchToTopicHandlers.
	// Use requestCtx for publishing so it respects the timeout if publishing blocks.
	if err := b.Publish(requestCtx, req); err != nil {
		return nil, fmt.Errorf("failed to publish request for topic %s: %w", req.Header.Topic, err)
	}
	b.logger.Debug("Request (CorrID: %s, Topic: %s) published, waiting for response.", req.Header.CorrelationID, req.Header.Topic)

	select {
	case <-requestCtx.Done():
		err := requestCtx.Err()
		b.logger.Warn("Request (CorrID: %s, Topic: %s) context done: %v", req.Header.CorrelationID, req.Header.Topic, err)
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, broker.ErrRequestTimeout
		}
		return nil, err
	case rawData, ok := <-replyCh:
		if !ok {
			// This can happen if the broker is closing down while request is in flight.
			b.logger.Warn("Reply channel closed unexpectedly for CorrID: %s, Topic: %s", req.Header.CorrelationID, req.Header.Topic)
			return nil, errors.New("reply channel closed for request")
		}
		dataBytes, dataOk := rawData.([]byte)
		if !dataOk {
			return nil, fmt.Errorf("received non-byte data on reply channel for CorrID: %s", req.Header.CorrelationID)
		}
		var resp model.Message
		if err := json.Unmarshal(dataBytes, &resp); err != nil {
			return nil, fmt.Errorf("failed to unmarshal response for CorrID %s: %w", req.Header.CorrelationID, err)
		}
		b.logger.Debug("Response received for CorrID: %s, Topic: %s", req.Header.CorrelationID, req.Header.Topic)
		return &resp, nil
	}
}

// RequestToClient sends a request to a specific client and waits for a response.
func (b *PubSubBroker) RequestToClient(ctx context.Context, brokerClientID string, req *model.Message, timeoutMs int64) (*model.Message, error) {
	if b.isClosed() {
		return nil, broker.ErrBrokerClosed
	}
	if req == nil || brokerClientID == "" {
		return nil, broker.ErrInvalidMessage
	}
	if req.Header.Type != model.KindRequest {
		req.Header.Type = model.KindRequest
	}
	if req.Header.CorrelationID == "" {
		req.Header.CorrelationID = model.RandomID()
	}

	conn, ok := b.GetConnection(brokerClientID)
	if !ok {
		b.logger.Warn("RequestToClient: Client %s not found", brokerClientID)
		return nil, broker.ErrClientNotFound
	}

	timeout := time.Duration(timeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = b.opts.DefaultRequestTimeout
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	replyCh := b.internalBus.SubOnce(req.Header.CorrelationID)
	defer b.internalBus.Unsub(replyCh, req.Header.CorrelationID)

	b.logger.Debug("RequestToClient: Sending to %s, Topic: %s, CorrID: %s", brokerClientID, req.Header.Topic, req.Header.CorrelationID)
	// Use requestCtx for writing to client, so write respects overall timeout.
	if err := conn.WriteMessage(requestCtx, req); err != nil {
		b.logger.Error("RequestToClient: Failed to write message to client %s: %v", brokerClientID, err)
		// If WriteMessage returns ErrConnectionWrite, it means the adapter detected a persistent issue.
		// The server.Handler's read loop should also detect this and trigger DeregisterConnection.
		return nil, broker.ErrConnectionWrite // Propagate specific error
	}

	select {
	case <-requestCtx.Done():
		err := requestCtx.Err()
		b.logger.Warn("RequestToClient: Context done for client %s, CorrID %s: %v", brokerClientID, req.Header.CorrelationID, err)
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, broker.ErrRequestTimeout
		}
		return nil, err
	case rawData, ok := <-replyCh:
		if !ok {
			b.logger.Warn("RequestToClient: Reply channel for %s (CorrID %s) closed unexpectedly", brokerClientID, req.Header.CorrelationID)
			return nil, errors.New("reply channel closed for client " + brokerClientID)
		}
		dataBytes, dataOk := rawData.([]byte)
		if !dataOk {
			return nil, fmt.Errorf("received non-byte data on reply channel for client request to %s", brokerClientID)
		}
		var resp model.Message
		if err := json.Unmarshal(dataBytes, &resp); err != nil {
			b.logger.Error("RequestToClient: Failed to unmarshal response from %s (CorrID %s): %v", brokerClientID, req.Header.CorrelationID, err)
			return nil, fmt.Errorf("failed to unmarshal response from client %s: %w", brokerClientID, err)
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
		return errors.New("invalid connection or empty BrokerClientID for registration")
	}
	b.connections.Store(conn.BrokerClientID(), conn)
	b.logger.Info("Connection registered: BrokerClientID %s", conn.BrokerClientID())
	return nil
}

// DeregisterConnection removes a client connection.
func (b *PubSubBroker) DeregisterConnection(brokerClientID string) error {
	if brokerClientID == "" {
		return errors.New("BrokerClientID cannot be empty for deregistration")
	}
	connVal, loaded := b.connections.LoadAndDelete(brokerClientID)
	if loaded {
		b.logger.Info("Connection deregistered: BrokerClientID %s", brokerClientID)

		// Publish an internal event that this client has disconnected.
		// This should happen even if the broker is closing, to allow SessionManager to clean up.
		deregisteredEvent := model.NewEvent(broker.TopicClientDeregistered, map[string]string{
			"brokerClientID": brokerClientID,
		})
		// Use a background context for this internal notification.
		// If broker is already closed, Publish will return ErrBrokerClosed, which is fine to ignore here.
		if err := b.Publish(context.Background(), deregisteredEvent); err != nil && !errors.Is(err, broker.ErrBrokerClosed) {
			b.logger.Error("Failed to publish client deregistration event for %s: %v", brokerClientID, err)
		}

		if conn, ok := connVal.(broker.ConnectionWriter); ok {
			// Close the connection. Do this in a goroutine to avoid blocking the caller
			// (e.g., server.Handler's defer). The Close method should be idempotent.
			b.shutdownWG.Add(1) // Track this cleanup goroutine
			go func(c broker.ConnectionWriter) {
				defer b.shutdownWG.Done()
				if err := c.Close(); err != nil {
					b.logger.Debug("Error closing connection %s during deregister (might be already closed): %v", c.BrokerClientID(), err)
				}
			}(conn)
		}
	} else {
		b.logger.Warn("Attempted to deregister non-existent or already deregistered client: %s", brokerClientID)
	}
	return nil
}

// GetConnection retrieves a connection writer by broker client ID.
func (b *PubSubBroker) GetConnection(brokerClientID string) (broker.ConnectionWriter, bool) {
	if b.isClosed() { // Don't return connections if broker is closing/closed
		return nil, false
	}
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
		b.logger.Info("PubSubBroker Close called, but already closed.")
		return broker.ErrBrokerClosed
	}
	b.closed = true
	b.closedMutex.Unlock()

	b.logger.Info("PubSubBroker closing...")

	// 1. Signal all server-side subscriptions to cancel.
	// Their cleanup goroutines (started in Subscribe) will handle removal from maps.
	b.subscriptions.Range(func(topicKey, topicValue interface{}) bool {
		topicSubs, ok := topicValue.(*topicSubscriptions)
		if !ok {
			return true // continue
		}
		topicSubs.mu.RLock() // RLock to iterate over a snapshot
		subsToCancel := make([]*subscription, 0, len(topicSubs.subs))
		for _, sub := range topicSubs.subs {
			subsToCancel = append(subsToCancel, sub)
		}
		topicSubs.mu.RUnlock()

		for _, sub := range subsToCancel {
			if sub.cancel != nil {
				sub.cancel() // This triggers the cleanup goroutine for each subscription
			}
		}
		return true
	})
	b.logger.Info("All server-side subscriptions signalled to cancel.")

	// 2. Close all client connections.
	// DeregisterConnection will be called by server.Handler normally, but we ensure closure here.
	b.connections.Range(func(key, value interface{}) bool {
		brokerClientID := key.(string)
		conn := value.(broker.ConnectionWriter)
		b.logger.Debug("Closing connection for client %s during broker shutdown.", brokerClientID)
		
		// Add to WaitGroup before starting goroutine
		b.shutdownWG.Add(1)
		go func(c broker.ConnectionWriter, id string) {
			defer b.shutdownWG.Done()
			if err := c.Close(); err != nil {
				b.logger.Debug("Error closing client connection %s on broker shutdown (may be already closed): %v", id, err)
			}
		}(conn, brokerClientID)
		b.connections.Delete(brokerClientID) // Remove from map immediately
		return true
	})
	b.logger.Info("All client connections instructed to close.")

	// 3. Wait for active handlers, subscription cleanup goroutines, and connection close goroutines.
	waitDone := make(chan struct{})
	go func() {
		b.shutdownWG.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		b.logger.Info("All active handlers, subscription cleanups, and connection closures completed.")
	case <-time.After(10 * time.Second): // Max wait time for graceful shutdown activities
		b.logger.Warn("Timeout waiting for all handlers/cleanups/closures to complete during broker shutdown.")
	}
	
	// 4. Shutdown internal pubsub bus (cskr/pubsub doesn't have an explicit Close).
	// Active SubOnce calls will either complete, timeout, or their channels will be unblocked
	// as related operations (like client connection closures) occur.
	// The internalBus itself doesn't hold persistent resources needing explicit closing beyond unsubscription.
	b.internalBus.Shutdown() // cskr/pubsub v1.2.0 added Shutdown
	b.logger.Info("Internal cskr/pubsub bus shutdown.")


	close(b.shutdownComplete) // Signal that shutdown process is fully complete
	b.logger.Info("PubSubBroker successfully closed.")
	return nil
}

// WaitForShutdown blocks until the broker's Close method has completed its cleanup.
func (b *PubSubBroker) WaitForShutdown() {
	if b.isClosed() { // If already closed, shutdownComplete might be closed too.
		<-b.shutdownComplete
	} else {
		// If not closed yet, this call might block indefinitely.
		// It's intended to be called *after* Close() has been initiated.
		b.logger.Warn("WaitForShutdown called before Close; may block. Call Close() first.")
		<-b.shutdownComplete
	}
}