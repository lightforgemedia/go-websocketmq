// ergosockets/broker/broker.go
package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/lightforgemedia/go-websocketmq/pkg/ergosockets"
)

const (
	defaultClientSendBuffer     = 16 // Refined: Added for slow client policy
	defaultWriteTimeout         = 10 * time.Second
	defaultReadTimeout          = 60 * time.Second // Should be > ping interval if pings enabled
	libraryDefaultPingInterval  = 30 * time.Second // Library's own default if user passes 0 to WithPingInterval
	defaultServerRequestTimeout = 10 * time.Second
)

type brokerConfig struct {
	logger               *slog.Logger // Refined: Logger interface
	acceptOptions        *websocket.AcceptOptions
	clientSendBuffer     int // Refined: For outgoing messages per client
	writeTimeout         time.Duration
	readTimeout          time.Duration
	pingInterval         time.Duration // 0 means use libraryDefaultPingInterval, <0 means disable
	serverRequestTimeout time.Duration
}

// Broker manages client connections and message routing.
type Broker struct {
	config brokerConfig

	clientsMu      sync.RWMutex
	managedClients map[string]*managedClient // clientID -> client

	requestHandlersMu sync.RWMutex
	requestHandlers   map[string]*ergosockets.HandlerWrapper // topic -> handler

	publishSubscribersMu sync.RWMutex
	publishSubscribers   map[string]map[*managedClient]struct{} // topic -> set of clients

	shutdownOnce sync.Once
	shutdownChan chan struct{}   // Closed when broker starts shutting down
	mainCtx      context.Context // Top-level context for the broker itself
	mainCancel   context.CancelFunc
}

// Option configures the Broker.
type Option func(*Broker)

// WithLogger sets a custom logging implementation.
func WithLogger(logger *slog.Logger) Option {
	return func(b *Broker) {
		if logger != nil {
			b.config.logger = logger
		}
	}
}

// WithAcceptOptions provides custom websocket.AcceptOptions.
func WithAcceptOptions(opts *websocket.AcceptOptions) Option {
	return func(b *Broker) {
		b.config.acceptOptions = opts
	}
}

// WithClientSendBuffer sets the buffer size for outgoing messages per client.
// Default is 16. Large buffers only delay, not prevent, issues with slow clients.
func WithClientSendBuffer(size int) Option {
	return func(b *Broker) {
		if size > 0 {
			b.config.clientSendBuffer = size
		}
	}
}

// WithPingInterval sets the server-initiated ping interval.
// interval < 0: Disables server pings.
// interval == 0: Uses the library's default ping interval (e.g., 30s).
// interval > 0: Uses the specified interval.
func WithPingInterval(interval time.Duration) Option {
	return func(b *Broker) {
		b.config.pingInterval = interval // Logic applied in New()
	}
}

// New creates a new Broker.
func New(opts ...Option) (*Broker, error) {
	mainCtx, mainCancel := context.WithCancel(context.Background())
	b := &Broker{
		config: brokerConfig{
			logger:               slog.Default(), // Discard by default
			clientSendBuffer:     defaultClientSendBuffer,
			writeTimeout:         defaultWriteTimeout,
			readTimeout:          defaultReadTimeout,
			pingInterval:         0, // Indicates "use default" initially
			serverRequestTimeout: defaultServerRequestTimeout,
		},
		managedClients:     make(map[string]*managedClient),
		requestHandlers:    make(map[string]*ergosockets.HandlerWrapper),
		publishSubscribers: make(map[string]map[*managedClient]struct{}),
		shutdownChan:       make(chan struct{}),
		mainCtx:            mainCtx,
		mainCancel:         mainCancel,
	}
	for _, opt := range opts {
		opt(b)
	}

	// Finalize ping interval logic
	if b.config.pingInterval == 0 { // User passed 0, means use library default
		b.config.pingInterval = libraryDefaultPingInterval
	} else if b.config.pingInterval < 0 { // User passed negative, means disable
		b.config.pingInterval = 0 // Set to 0 to effectively disable the ping loop
	}
	// If user passed > 0, it's already set.

	if b.config.acceptOptions == nil {
		b.config.acceptOptions = &websocket.AcceptOptions{} // Default allows all origins, no compression
	}
	b.config.logger.Info(fmt.Sprintf("Broker initialized. Ping interval: %v, Client send buffer: %d", b.config.pingInterval, b.config.clientSendBuffer))
	return b, nil
}

// UpgradeHandler returns an http.HandlerFunc to handle WebSocket upgrade requests.
func (b *Broker) UpgradeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-b.shutdownChan: // Or <-b.mainCtx.Done()
			http.Error(w, "server is shutting down", http.StatusServiceUnavailable)
			b.config.logger.Info("Broker: Rejected connection, server shutting down.")
			return
		default:
		}

		conn, err := websocket.Accept(w, r, b.config.acceptOptions)
		if err != nil {
			b.config.logger.Info(fmt.Sprintf("Broker: Failed to accept websocket connection: %v", err))
			return
		}

		clientID := ergosockets.GenerateID()
		// Client's context is derived from broker's main context
		clientCtx, clientCancel := context.WithCancel(b.mainCtx)

		mc := &managedClient{
			id:                    clientID,
			conn:                  conn,
			broker:                b,
			send:                  make(chan *ergosockets.Envelope, b.config.clientSendBuffer),
			ctx:                   clientCtx,
			cancel:                clientCancel,
			activeSubscriptions:   make(map[string]struct{}),
			pendingServerRequests: make(map[string]chan *ergosockets.Envelope),
			logger:                b.config.logger, // Pass logger to managedClient
		}

		b.addClient(mc)
		mc.logger.Info(fmt.Sprintf("Broker: Client %s connected", mc.id))

		go mc.writePump()
		go mc.readPump()
		if b.config.pingInterval > 0 { // Only start ping loop if interval is positive
			go mc.pingLoop()
		}
	}
}

func (b *Broker) addClient(mc *managedClient) {
	b.clientsMu.Lock()
	defer b.clientsMu.Unlock()
	b.managedClients[mc.id] = mc
}

func (b *Broker) removeClient(mc *managedClient) {
	mc.cancel() // Signal all client-specific goroutines to stop FIRST

	b.clientsMu.Lock()
	if _, exists := b.managedClients[mc.id]; !exists {
		b.clientsMu.Unlock() // Already removed
		return
	}
	delete(b.managedClients, mc.id)
	b.clientsMu.Unlock()

	b.publishSubscribersMu.Lock()
	mc.activeSubscriptionsMu.Lock() // Lock client's subs before iterating
	for topic := range mc.activeSubscriptions {
		if subs, ok := b.publishSubscribers[topic]; ok {
			delete(subs, mc)
			if len(subs) == 0 {
				delete(b.publishSubscribers, topic)
			}
		}
	}
	mc.activeSubscriptionsMu.Unlock()
	b.publishSubscribersMu.Unlock()

	// Close the connection after removing from maps and cancelling context
	// This ensures no new messages are queued to its send channel after this point.
	// StatusPolicyViolation might have been set by writePump if it was a slow client.
	// Otherwise, use StatusNormalClosure or StatusGoingAway.
	mc.conn.CloseRead(context.Background())
	// currentStatus := websocket.CloseStatus() // Check if already closed with a specific status
	// if currentStatus == -1 {                 // Not yet closed or unknown status
	// 	mc.conn.Close(websocket.StatusNormalClosure, "client removed")
	// }

	mc.logger.Info(fmt.Sprintf("Broker: Client %s disconnected and removed.", mc.id))
}

// OnRequest registers a handler for a specific request topic.
// handlerFunc must be of type: func(ClientHandle, ReqStruct) (RespStruct, error) or func(ClientHandle, ReqStruct) error
func (b *Broker) OnRequest(topic string, handlerFunc interface{}) error {
	hw, err := ergosockets.NewHandlerWrapper(handlerFunc)
	if err != nil {
		return fmt.Errorf("broker OnRequest topic '%s': %w", topic, err)
	}
	if hw.HandlerFunc.Type().NumIn() != 2 {
		return fmt.Errorf("broker OnRequest topic '%s': handler must have 2 input arguments (ClientHandle, RequestType), got %d", topic, hw.HandlerFunc.Type().NumIn())
	}

	b.requestHandlersMu.Lock()
	defer b.requestHandlersMu.Unlock()
	if _, exists := b.requestHandlers[topic]; exists {
		return fmt.Errorf("broker: handler already registered for topic '%s'", topic)
	}
	b.requestHandlers[topic] = hw
	b.config.logger.Info(fmt.Sprintf("Broker: Registered request handler for topic '%s'", topic))
	return nil
}

// Publish sends a message to all clients subscribed to the given topic.
func (b *Broker) Publish(ctx context.Context, topic string, payloadData interface{}) error {
	select {
	case <-b.mainCtx.Done(): // Use broker's main context for shutdown check
		return errors.New("broker is shutting down")
	default:
	}

	env, err := ergosockets.NewEnvelope("", ergosockets.TypePublish, topic, payloadData, nil)
	if err != nil {
		return fmt.Errorf("broker: failed to create publish envelope for topic '%s': %w", topic, err)
	}

	b.publishSubscribersMu.RLock()
	// Create a snapshot of subscribers to avoid holding lock during send attempts
	subscribersToNotify := make([]*managedClient, 0)
	if subs, ok := b.publishSubscribers[topic]; ok {
		for mc := range subs {
			subscribersToNotify = append(subscribersToNotify, mc)
		}
	}
	b.publishSubscribersMu.RUnlock()

	if len(subscribersToNotify) == 0 {
		// b.config.logger.Info(fmt.Sprintf("Broker: No subscribers for publish to topic '%s'", topic) // Can be noisy
		return nil
	}

	b.config.logger.Info(fmt.Sprintf("Broker: Publishing message on topic '%s' to %d subscribers", topic, len(subscribersToNotify)))
	for _, mc := range subscribersToNotify {
		// Non-blocking send attempt. If channel is full, it's handled by writePump's slow client policy.
		select {
		case mc.send <- env:
		case <-mc.ctx.Done(): // Client's own context
			b.config.logger.Info(fmt.Sprintf("Broker: Client %s context done while trying to publish to topic '%s'", mc.id, topic))
		default:
			// This path means mc.send is full. The writePump will detect this, close with StatusPolicyViolation,
			// and then removeClient will be called. So, we don't need to call removeClient directly here.
			// Just log that the message might be dropped for this specific client due to full buffer.
			b.config.logger.Info(fmt.Sprintf("Broker: Client %s send channel full for publish to topic '%s'. Message may be dropped if client doesn't recover quickly.", mc.id, topic))
		}
	}
	return nil
}

// GetClient retrieves a handle to a connected client by its ID.
func (b *Broker) GetClient(clientID string) (ClientHandle, error) {
	b.clientsMu.RLock()
	defer b.clientsMu.RUnlock()
	mc, ok := b.managedClients[clientID]
	if !ok {
		return nil, fmt.Errorf("client with ID '%s' not found", clientID)
	}
	return mc, nil
}

// Shutdown gracefully shuts down the broker.
func (b *Broker) Shutdown(ctx context.Context) error {
	b.shutdownOnce.Do(func() {
		b.config.logger.Info("Broker: Initiating shutdown...")
		close(b.shutdownChan) // Signal internal components that rely on this
		b.mainCancel()        // Cancel the broker's main context, which propagates to clients

		// Wait for all client goroutines (read/write/ping pumps) to finish.
		// This requires managedClients to have their own WaitGroup or similar.
		// For now, we'll rely on the context cancellation and a short wait.
		// A more robust shutdown would involve each managedClient signaling its completion.

		b.clientsMu.RLock()
		numClients := len(b.managedClients)
		b.clientsMu.RUnlock()
		b.config.logger.Info(fmt.Sprintf("Broker: Waiting for %d clients to disconnect...", numClients))

		// Simple wait loop, a more robust system would use a WaitGroup for clients.
		// Or check against b.mainCtx.Done() if clients are guaranteed to stop.
		// The client's mc.cancel() in removeClient should ensure their pumps stop.
		// The removeClient calls happen as client read/write pumps exit due to context cancellation.
	})

	// Wait for a specified period or until all clients are gone (simplified)
	timeout := time.NewTimer(5 * time.Second) // Max wait for clients to clear up
	defer timeout.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		b.clientsMu.RLock()
		remainingClients := len(b.managedClients)
		b.clientsMu.RUnlock()
		if remainingClients == 0 {
			b.config.logger.Info("Broker: All clients disconnected.")
			break
		}
		select {
		case <-timeout.C:
			b.config.logger.Info(fmt.Sprintf("Broker: Shutdown timed out waiting for %d clients.", remainingClients))
			return errors.New("broker shutdown timed out")
		case <-ticker.C:
			// continue waiting
		case <-ctx.Done(): // External shutdown context timed out
			b.config.logger.Info(fmt.Sprintf("Broker: External shutdown context timed out (%d clients remaining): %v", remainingClients, ctx.Err()))
			return ctx.Err()
		}
	}

	b.config.logger.Info("Broker: Shutdown complete.")
	return nil
}

// Context returns the broker's main context, which is cancelled on Shutdown.
func (b *Broker) Context() context.Context {
	return b.mainCtx
}

// --- managedClient (internal representation of a connected client) ---
type managedClient struct {
	id     string
	conn   *websocket.Conn
	broker *Broker
	send   chan *ergosockets.Envelope // Buffered channel for outgoing messages
	logger *slog.Logger

	ctx    context.Context    // Context for this client's lifetime, derived from broker.mainCtx
	cancel context.CancelFunc // Cancels this client's context

	activeSubscriptionsMu sync.Mutex
	activeSubscriptions   map[string]struct{}

	pendingServerRequestsMu sync.Mutex
	pendingServerRequests   map[string]chan *ergosockets.Envelope
}

// ID, Context, RemoteAddr, Request, Send methods for ClientHandle interface
func (mc *managedClient) ID() string               { return mc.id }
func (mc *managedClient) Context() context.Context { return mc.ctx }
func (mc *managedClient) Request(ctx context.Context, topic string, requestData interface{}, responsePayloadPtr interface{}, timeout time.Duration) error {
	select {
	case <-mc.ctx.Done():
		return fmt.Errorf("client %s disconnected: %w", mc.id, mc.ctx.Err())
	case <-mc.broker.mainCtx.Done(): // Use broker's main context for shutdown check
		return errors.New("broker is shutting down")
	default:
	}

	correlationID := ergosockets.GenerateID()
	reqEnv, err := ergosockets.NewEnvelope(correlationID, ergosockets.TypeRequest, topic, requestData, nil)
	if err != nil {
		return fmt.Errorf("failed to create request envelope for client %s: %w", mc.id, err)
	}

	respChan := make(chan *ergosockets.Envelope, 1)
	mc.pendingServerRequestsMu.Lock()
	mc.pendingServerRequests[correlationID] = respChan
	mc.pendingServerRequestsMu.Unlock()

	defer func() {
		mc.pendingServerRequestsMu.Lock()
		delete(mc.pendingServerRequests, correlationID)
		mc.pendingServerRequestsMu.Unlock()
		// Do not close respChan here, receiver might still be selecting on it if timeout occurred on send.
		// It will be garbage collected. Or, ensure it's drained if not used.
	}()

	// Send the request
	sendCtx, sendCancel := context.WithTimeout(ctx, mc.broker.config.writeTimeout) // Timeout for the send itself
	defer sendCancel()
	select {
	case mc.send <- reqEnv:
		mc.logger.Info(fmt.Sprintf("Broker: Sent request (ID: %s) on topic '%s' to client %s", correlationID, topic, mc.id))
	case <-mc.ctx.Done():
		return fmt.Errorf("client %s context done before sending request: %w", mc.id, mc.ctx.Err())
	case <-sendCtx.Done(): // Send timed out
		return fmt.Errorf("timeout sending request to client %s (ID: %s): %w", mc.id, correlationID, sendCtx.Err())
	case <-ctx.Done(): // Overall request context timed out/cancelled
		return fmt.Errorf("requesting context done before sending request to client %s (ID: %s): %w", mc.id, correlationID, ctx.Err())
	}

	// Wait for the response
	effectiveTimeout := mc.broker.config.serverRequestTimeout
	if timeout > 0 {
		effectiveTimeout = timeout
	}

	// The timer should be based on the parent context `ctx` for the whole operation.
	timer := time.NewTimer(effectiveTimeout)
	defer timer.Stop()

	select {
	case respEnv, ok := <-respChan:
		if !ok {
			return fmt.Errorf("response channel closed for request ID %s to client %s (client likely disconnected or internal error)", correlationID, mc.id)
		}
		if respEnv.Error != nil {
			return fmt.Errorf("client %s responded with error (code %d) for request ID %s: %s", mc.id, respEnv.Error.Code, correlationID, respEnv.Error.Message)
		}
		if responsePayloadPtr != nil { // Only decode if a non-nil pointer is provided
			if reflect.ValueOf(responsePayloadPtr).IsNil() {
				// Programmer error: passed a nil pointer for decoding.
				return fmt.Errorf("responsePayloadPtr cannot be nil for request ID %s from client %s", correlationID, mc.id)
			}
			if err := respEnv.DecodePayload(responsePayloadPtr); err != nil {
				return fmt.Errorf("failed to decode response payload from client %s for request ID %s: %w", mc.id, correlationID, err)
			}
		}
		mc.logger.Info(fmt.Sprintf("Broker: Received response (ID: %s) from client %s", correlationID, mc.id))
		return nil
	case <-timer.C:
		return fmt.Errorf("request to client %s (ID: %s) timed out after %v", mc.id, correlationID, effectiveTimeout)
	case <-mc.ctx.Done():
		return fmt.Errorf("client %s context done while waiting for response (ID: %s): %w", mc.id, correlationID, mc.ctx.Err())
	case <-ctx.Done(): // Overall request context timed out/cancelled
		return fmt.Errorf("requesting context done while waiting for response from client %s (ID: %s): %w", mc.id, correlationID, ctx.Err())
	}
}

func (mc *managedClient) Send(ctx context.Context, topic string, payloadData interface{}) error {
	select {
	case <-mc.ctx.Done():
		return fmt.Errorf("client %s disconnected: %w", mc.id, mc.ctx.Err())
	case <-mc.broker.mainCtx.Done():
		return errors.New("broker is shutting down")
	default:
	}

	env, err := ergosockets.NewEnvelope("", ergosockets.TypePublish, topic, payloadData, nil)
	if err != nil {
		return fmt.Errorf("failed to create send envelope for client %s: %w", mc.id, err)
	}

	sendCtx, sendCancel := context.WithTimeout(ctx, mc.broker.config.writeTimeout)
	defer sendCancel()
	select {
	case mc.send <- env:
		mc.logger.Info(fmt.Sprintf("Broker: Sent direct message on topic '%s' to client %s", topic, mc.id))
		return nil
	case <-mc.ctx.Done():
		return fmt.Errorf("client %s context done before sending direct message: %w", mc.id, mc.ctx.Err())
	case <-sendCtx.Done():
		return fmt.Errorf("timeout sending direct message to client %s: %w", mc.id, sendCtx.Err())
	case <-ctx.Done():
		return fmt.Errorf("sending context done before sending direct message to client %s: %w", mc.id, ctx.Err())
	}
}

func (mc *managedClient) readPump() {
	defer mc.broker.removeClient(mc) // Ensures cleanup on any exit

	cfg := mc.broker.config
	readDeadlineDuration := cfg.readTimeout
	if cfg.pingInterval > 0 { // If pings are enabled, base read deadline on ping interval
		readDeadlineDuration = cfg.pingInterval * 2
		if readDeadlineDuration < cfg.readTimeout { // But ensure it's not less than configured min read timeout
			readDeadlineDuration = cfg.readTimeout
		}
	}

	if readDeadlineDuration > 0 {
		mc.conn.SetReadLimit(1024 * 1024) // Max message size 1MB
		// _ = mc.conn.SetReadDeadline(ergosockets.TimeNow().Add(readDeadlineDuration))
		// mc.conn.SetPongHandler(func(string) error {
		// 	// mc.logger.Info(fmt.Sprintf("Broker: Pong received from client %s", mc.id)
		// 	if readDeadlineDuration > 0 {
		// 		_ = mc.conn.SetReadDeadline(ergosockets.TimeNow().Add(readDeadlineDuration))
		// 	}
		// 	return nil
		// })
	}

	for {
		select {
		case <-mc.ctx.Done(): // Check for client context cancellation first
			mc.logger.Info(fmt.Sprintf("Broker: Client %s readPump stopping due to context cancellation: %v", mc.id, mc.ctx.Err()))
			return
		default:
		}

		var env ergosockets.Envelope
		// Use a context for the Read operation that can be shorter than mc.ctx
		// For example, link it to the readDeadline if one is set, or just use mc.ctx
		readOpCtx := mc.ctx // For now, use client's main context for read op
		err := wsjson.Read(readOpCtx, mc.conn, &env)
		if err != nil {
			status := websocket.CloseStatus(err)
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
				status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway {
				mc.logger.Info(fmt.Sprintf("Broker: Client %s readPump closing gracefully: %v", mc.id, err))
			} else {
				mc.logger.Info(fmt.Sprintf("Broker: Client %s read error in readPump: %v (status: %d)", mc.id, err, status))
			}
			return // Exits loop, triggers defer removeClient
		}

		// if readDeadlineDuration > 0 { // Refresh read deadline on successful message read
		// 	_ = mc.conn.SetReadDeadline(ergosockets.TimeNow().Add(readDeadlineDuration))
		// }

		// Process envelope
		switch env.Type {
		case ergosockets.TypeRequest:
			go mc.handleClientRequest(&env) // Process in goroutine to not block readPump
		case ergosockets.TypeResponse, ergosockets.TypeError:
			mc.pendingServerRequestsMu.Lock()
			if ch, ok := mc.pendingServerRequests[env.ID]; ok {
				select {
				case ch <- &env: // Try to send, non-blocking
				default:
					mc.logger.Info(fmt.Sprintf("Broker: Response channel for ID %s (client %s) not ready (possibly timed out or already processed)", env.ID, mc.id))
				}
			} else {
				mc.logger.Info(fmt.Sprintf("Broker: Received unsolicited server-targeted response/error with ID %s from client %s", env.ID, mc.id))
			}
			mc.pendingServerRequestsMu.Unlock()
		case ergosockets.TypeSubscribeRequest:
			mc.handleSubscribeRequest(&env)
		case ergosockets.TypeUnsubscribeRequest:
			mc.handleUnsubscribeRequest(&env)
		default:
			mc.logger.Info(fmt.Sprintf("Broker: Client %s sent unknown envelope type: '%s'", mc.id, env.Type))
		}
	}
}

func (mc *managedClient) handleClientRequest(reqEnv *ergosockets.Envelope) {
	mc.broker.requestHandlersMu.RLock()
	handlerWrapper, ok := mc.broker.requestHandlers[reqEnv.Topic]
	mc.broker.requestHandlersMu.RUnlock()

	if !ok {
		mc.logger.Info(fmt.Sprintf("Broker: No handler for request topic '%s' from client %s", reqEnv.Topic, mc.id))
		errEnv, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeError, reqEnv.Topic, nil,
			&ergosockets.ErrorPayload{Code: http.StatusNotFound, Message: "No handler for topic: " + reqEnv.Topic})
		mc.trySend(errEnv)
		return
	}

	// Create a new instance of the request type
	var reqPayloadVal reflect.Value

	// Check if the request type is a pointer or a value type
	if handlerWrapper.ReqType.Kind() == reflect.Ptr {
		// If it's a pointer type, create a new instance of the pointed-to type
		reqPayloadVal = reflect.New(handlerWrapper.ReqType.Elem())
	} else {
		// If it's a value type, create a new instance of the type itself
		reqPayloadVal = reflect.New(handlerWrapper.ReqType)
	}

	// Unmarshal the payload into the new instance
	if reqEnv.Payload != nil && string(reqEnv.Payload) != "null" { // Handle null payload for empty structs
		if err := json.Unmarshal(reqEnv.Payload, reqPayloadVal.Interface()); err != nil {
			mc.logger.Info(fmt.Sprintf("Broker: Failed to unmarshal request payload for topic '%s' from client %s: %v. Payload: %s", reqEnv.Topic, mc.id, err, string(reqEnv.Payload)))
			errEnv, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeError, reqEnv.Topic, nil,
				&ergosockets.ErrorPayload{Code: http.StatusBadRequest, Message: "Invalid request payload: " + err.Error()})
			mc.trySend(errEnv)
			return
		}
	}

	// Prepare the input arguments for the handler function
	var inputs []reflect.Value

	// First argument is always the client handle
	inputs = append(inputs, reflect.ValueOf(mc))

	// Second argument is the request payload, which might need to be a value or a pointer
	if handlerWrapper.ReqType.Kind() == reflect.Ptr {
		// If handler expects a pointer, pass the pointer directly
		inputs = append(inputs, reqPayloadVal)
	} else {
		// If handler expects a value, dereference the pointer
		inputs = append(inputs, reqPayloadVal.Elem())
	}

	// Call the handler function
	results := handlerWrapper.HandlerFunc.Call(inputs)

	var errResult error
	if len(results) > 0 {
		if errVal, ok := results[len(results)-1].Interface().(error); ok {
			errResult = errVal
		}
	}

	if errResult != nil {
		mc.logger.Info(fmt.Sprintf("Broker: Handler for topic '%s' (client %s) returned error: %v", reqEnv.Topic, mc.id, errResult))
		errEnv, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeError, reqEnv.Topic, nil,
			&ergosockets.ErrorPayload{Code: http.StatusInternalServerError, Message: errResult.Error()}) // Consider mapping codes
		mc.trySend(errEnv)
		return
	}

	if handlerWrapper.RespType != nil {
		respPayload := results[0].Interface()
		respEnv, err := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeResponse, reqEnv.Topic, respPayload, nil)
		if err != nil {
			mc.logger.Info(fmt.Sprintf("Broker: Failed to create response envelope for topic '%s' (client %s): %v", reqEnv.Topic, mc.id, err))
			serverErrEnv, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeError, reqEnv.Topic, nil,
				&ergosockets.ErrorPayload{Code: http.StatusInternalServerError, Message: "Server error creating response"})
			mc.trySend(serverErrEnv)
			return
		}
		mc.trySend(respEnv)
	} else if reqEnv.ID != "" { // No response payload, but request had an ID, send simple ack
		ackEnv, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeResponse, reqEnv.Topic, nil, nil)
		mc.trySend(ackEnv)
	}
}

func (mc *managedClient) handleSubscribeRequest(env *ergosockets.Envelope) {
	topic := env.Topic
	if topic == "" {
		mc.logger.Info(fmt.Sprintf("Broker: Client %s sent subscribe request with empty topic", mc.id))
		errEnv, _ := ergosockets.NewEnvelope(env.ID, ergosockets.TypeError, "", nil, &ergosockets.ErrorPayload{Code: http.StatusBadRequest, Message: "Subscription topic cannot be empty"})
		mc.trySend(errEnv)
		return
	}

	mc.activeSubscriptionsMu.Lock()
	mc.activeSubscriptions[topic] = struct{}{}
	mc.activeSubscriptionsMu.Unlock()

	mc.broker.publishSubscribersMu.Lock()
	if _, ok := mc.broker.publishSubscribers[topic]; !ok {
		mc.broker.publishSubscribers[topic] = make(map[*managedClient]struct{})
	}
	mc.broker.publishSubscribers[topic][mc] = struct{}{}
	mc.broker.publishSubscribersMu.Unlock()

	mc.logger.Info(fmt.Sprintf("Broker: Client %s subscribed to topic '%s'", mc.id, topic))
	ackEnv, _ := ergosockets.NewEnvelope(env.ID, ergosockets.TypeSubscriptionAck, topic, map[string]string{"status": "subscribed", "topic": topic}, nil)
	mc.trySend(ackEnv)
}

func (mc *managedClient) handleUnsubscribeRequest(env *ergosockets.Envelope) {
	topic := env.Topic
	if topic == "" { // Should not happen if client validates
		mc.logger.Info(fmt.Sprintf("Broker: Client %s sent unsubscribe request with empty topic", mc.id))
		return
	}

	mc.activeSubscriptionsMu.Lock()
	delete(mc.activeSubscriptions, topic)
	mc.activeSubscriptionsMu.Unlock()

	mc.broker.publishSubscribersMu.Lock()
	if subs, ok := mc.broker.publishSubscribers[topic]; ok {
		delete(subs, mc)
		if len(subs) == 0 {
			delete(mc.broker.publishSubscribers, topic)
		}
	}
	mc.broker.publishSubscribersMu.Unlock()

	mc.logger.Info(fmt.Sprintf("Broker: Client %s unsubscribed from topic '%s'", mc.id, topic))
	// Optionally send ack for unsubscribe
	ackEnv, _ := ergosockets.NewEnvelope(env.ID, ergosockets.TypeSubscriptionAck, topic, map[string]string{"status": "unsubscribed", "topic": topic}, nil) // Re-use ack type
	mc.trySend(ackEnv)
}

// trySend attempts to send an envelope to the client's send channel without blocking indefinitely.
func (mc *managedClient) trySend(env *ergosockets.Envelope) {
	select {
	case mc.send <- env:
	case <-mc.ctx.Done():
		mc.logger.Info(fmt.Sprintf("Broker: Client %s context done, cannot send envelope type %s on topic %s", mc.id, env.Type, env.Topic))
	default: // Should only happen if send channel is full and writePump is also blocked/slow
		mc.logger.Info(fmt.Sprintf("Broker: Client %s send channel full when trying to send envelope type %s on topic %s. Message potentially dropped.", mc.id, env.Type, env.Topic))
		// This indicates a slow client; writePump should eventually close it.
	}
}

func (mc *managedClient) writePump() {
	defer func() {
		// This defer ensures that if writePump exits (e.g., due to error or context cancellation),
		// it triggers the full client removal process.
		// mc.broker.removeClient(mc) // removeClient is called by readPump's defer or if ping fails
		mc.logger.Info(fmt.Sprintf("Broker: Client %s writePump stopping.", mc.id))
	}()

	for {
		select {
		case message, ok := <-mc.send:
			if !ok { // send channel closed by broker.removeClient or broker.Shutdown
				mc.logger.Info(fmt.Sprintf("Broker: Client %s send channel closed, closing connection.", mc.id))
				mc.conn.Close(websocket.StatusNormalClosure, "send channel closed")
				return
			}

			writeCtx, cancel := context.WithTimeout(mc.ctx, mc.broker.config.writeTimeout)
			err := wsjson.Write(writeCtx, mc.conn, message)
			cancel() // Release resources associated with writeCtx

			if err != nil {
				mc.logger.Info(fmt.Sprintf("Broker: Client %s write error in writePump: %v. Closing connection.", mc.id, err))
				// A write error typically means the connection is bad.
				// Close the connection; readPump's defer will handle full removeClient.
				mc.conn.Close(websocket.CloseStatus(err), "write error") // Use status from error if available
				return
			}
		case <-mc.ctx.Done(): // Client's context cancelled
			mc.logger.Info(fmt.Sprintf("Broker: Client %s context cancelled, writePump stopping.", mc.id))
			// Connection might already be closed by removeClient or pingLoop.
			// If not, close it now.
			//NOT SURE IF THIS IS CORRECT
			mc.conn.CloseRead(context.Background())

			// if websocket.CloseStatus() == -1 {
			// 	mc.conn.Close(websocket.StatusGoingAway, "client context cancelled")
			// }
			return
		}
	}
}

func (mc *managedClient) pingLoop() {
	if mc.broker.config.pingInterval <= 0 { // Guard: only run if interval is positive
		return
	}
	ticker := time.NewTicker(mc.broker.config.pingInterval)
	defer ticker.Stop()
	mc.logger.Info(fmt.Sprintf("Broker: Client %s pingLoop started with interval %v", mc.id, mc.broker.config.pingInterval))

	for {
		select {
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(mc.ctx, mc.broker.config.pingInterval/2) // Timeout for ping op itself
			err := mc.conn.Ping(pingCtx)
			cancel()
			if err != nil {
				mc.logger.Info(fmt.Sprintf("Broker: Client %s ping failed: %v. Closing connection.", mc.id, err))
				// Ping failure means connection is likely dead. Close it.
				// removeClient will be called by readPump's defer when it detects the closure.
				mc.conn.Close(websocket.StatusPolicyViolation, "ping failure")
				return // Exit ping loop
			}
			// mc.logger.Info(fmt.Sprintf("Broker: Ping sent to client %s", mc.id)
		case <-mc.ctx.Done(): // Client's context cancelled
			mc.logger.Info(fmt.Sprintf("Broker: Client %s context cancelled, pingLoop stopping.", mc.id))
			return
		}
	}
}

// Test helper: IterateClients
func (b *Broker) IterateClients(f func(ClientHandle) bool) {
	b.clientsMu.RLock()
	snapshot := make([]ClientHandle, 0, len(b.managedClients))
	for _, client := range b.managedClients {
		snapshot = append(snapshot, client)
	}
	b.clientsMu.RUnlock()

	for _, client := range snapshot {
		if !f(client) {
			break
		}
	}
}
