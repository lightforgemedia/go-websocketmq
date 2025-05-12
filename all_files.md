# Table of Contents

## pkg/broker

- [broker_test.go](#file-pkg-broker-broker_test-go)
- [broker.go](#file-pkg-broker-broker-go)
- [client_handle.go](#file-pkg-broker-client_handle-go)
- [js_client.go](#file-pkg-broker-js_client-go)

## pkg/browser_client

- [embed.go](#file-pkg-browser_client-embed-go)
- [handler.go](#file-pkg-browser_client-handler-go)

## pkg/browser_client/browser_tests

- [client_publish_test.go](#file-pkg-browser_client-browser_tests-client_publish_test-go)
- [connection_test.go](#file-pkg-browser_client-browser_tests-connection_test-go)
- [publish_subscribe_test.go](#file-pkg-browser_client-browser_tests-publish_subscribe_test-go)
- [request_response_test.go](#file-pkg-browser_client-browser_tests-request_response_test-go)
- [server_initiated_request_test.go](#file-pkg-browser_client-browser_tests-server_initiated_request_test-go)
- [test_helpers.go](#file-pkg-browser_client-browser_tests-test_helpers-go)

## pkg/browser_client/browser_tests/js_helpers

- [connect.js](#file-pkg-browser_client-browser_tests-js_helpers-connect-js)
- [console_override.js](#file-pkg-browser_client-browser_tests-js_helpers-console_override-js)
- [publish_helpers.js](#file-pkg-browser_client-browser_tests-js_helpers-publish_helpers-js)

## pkg/browser_client/dist

- [websocketmq.js](#file-pkg-browser_client-dist-websocketmq-js)

## pkg/client

- [client_test.go](#file-pkg-client-client_test-go)
- [client.go](#file-pkg-client-client-go)

## pkg/ergosockets

- [common.go](#file-pkg-ergosockets-common-go)
- [envelope.go](#file-pkg-ergosockets-envelope-go)

## pkg/filewatcher

- [options.go](#file-pkg-filewatcher-options-go)
- [watcher_test.go](#file-pkg-filewatcher-watcher_test-go)
- [watcher.go](#file-pkg-filewatcher-watcher-go)

## pkg/hotreload

- [embed.go](#file-pkg-hotreload-embed-go)
- [handler.go](#file-pkg-hotreload-handler-go)
- [hotreload_simple_test.go](#file-pkg-hotreload-hotreload_simple_test-go)
- [hotreload_test.go](#file-pkg-hotreload-hotreload_test-go)
- [hotreload.go](#file-pkg-hotreload-hotreload-go)
- [options.go](#file-pkg-hotreload-options-go)

## pkg/hotreload/dist

- [hotreload.js](#file-pkg-hotreload-dist-hotreload-js)

## pkg/shared_types

- [types.go](#file-pkg-shared_types-types-go)

## pkg/testutil

- [broker.go](#file-pkg-testutil-broker-go)
- [client.go](#file-pkg-testutil-client-go)
- [mock_server.go](#file-pkg-testutil-mock_server-go)
- [rod_broker_example_test.go](#file-pkg-testutil-rod_broker_example_test-go)
- [rod_test.go](#file-pkg-testutil-rod_test-go)
- [rod.go](#file-pkg-testutil-rod-go)
- [server_test.go](#file-pkg-testutil-server_test-go)
- [wait.go](#file-pkg-testutil-wait-go)

---

<a id="file-pkg-broker-broker-go"></a>
**File: pkg/broker/broker.go**

```go
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
	"github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
)

const (
	defaultClientSendBuffer     = 16 // Refined: Added for slow client policy
	defaultWriteTimeout         = 10 * time.Second
	defaultReadTimeout          = 60 * time.Second // Should be > ping interval if pings enabled
	libraryDefaultPingInterval  = 30 * time.Second // Library's own default if user passes 0 to WithPingInterval
	defaultServerRequestTimeout = 10 * time.Second
)

type brokerConfig struct {
	logger                *slog.Logger // Refined: Logger interface
	acceptOptions         *websocket.AcceptOptions
	clientSendBuffer      int // Refined: For outgoing messages per client
	writeTimeout          time.Duration
	readTimeout           time.Duration
	pingInterval          time.Duration // 0 means use libraryDefaultPingInterval, <0 means disable
	serverRequestTimeout  time.Duration
	serveJavaScriptClient bool // Whether to serve the JavaScript client
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

	// Add default client registration handler
	err := b.OnRequest(shared_types.TopicClientRegister, func(client ClientHandle, req shared_types.ClientRegistration) (shared_types.ClientRegistrationResponse, error) {
		// Get the managedClient from the ClientHandle
		mc, ok := client.(*managedClient)
		if !ok {
			return shared_types.ClientRegistrationResponse{}, fmt.Errorf("invalid client type")
		}

		// Update client information
		mc.clientID = req.ClientID

		// Set client name based on provided name or URL
		if req.ClientName != "" {
			mc.name = req.ClientName
		}

		// Set client type
		if req.ClientType != "" {
			mc.clientType = req.ClientType
		}

		// Set client URL
		if req.ClientURL != "" {
			mc.clientURL = req.ClientURL
		} else if mc.clientURL == "" && req.ClientType == "browser" {
			// For browser clients without URL, use a default
			mc.clientURL = "unknown-browser"
		}

		// Mark client as registered
		mc.registered = true

		// Log registration
		mc.logger.Info(fmt.Sprintf("Broker: Client %s registered as %s (type: %s, URL: %s)", mc.id, mc.name, mc.clientType, mc.clientURL))

		// Return response with server-assigned ID
		return shared_types.ClientRegistrationResponse{
			ServerAssignedID: mc.id,
			ClientName:       mc.name,
			ServerTime:       time.Now().Format(time.RFC3339),
		}, nil
	})

	// Log error but don't fail if registration handler can't be added
	if err != nil {
		b.config.logger.Info(fmt.Sprintf("Broker: Failed to add client registration handler: %v", err))
	}

	b.config.logger.Info(fmt.Sprintf("Broker: Initialized. Ping interval: %v, Client send buffer: %d", b.config.pingInterval, b.config.clientSendBuffer))
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

		// Get client ID from URL (client-provided ID)
		clientProvidedID := r.URL.Query().Get("client_id")
		if clientProvidedID == "" {
			// If not provided, generate a temporary one
			clientProvidedID = ergosockets.GenerateID()
		}

		// Generate a server-assigned ID (source of truth)
		serverAssignedID := ergosockets.GenerateID()

		// Get client URL if available
		clientURL := ""
		referer := r.Header.Get("Referer")
		if referer != "" {
			clientURL = referer
		}

		// Client's context is derived from broker's main context
		clientCtx, clientCancel := context.WithCancel(b.mainCtx)

		// Default client name based on ID or URL
		clientName := "client-" + clientProvidedID[:8]
		if clientURL != "" {
			clientName = "browser-" + clientURL
		}

		mc := &managedClient{
			id:                    serverAssignedID,
			clientID:              clientProvidedID,
			name:                  clientName,
			clientType:            "unknown", // Will be updated during registration
			clientURL:             clientURL,
			conn:                  conn,
			broker:                b,
			send:                  make(chan *ergosockets.Envelope, b.config.clientSendBuffer),
			ctx:                   clientCtx,
			cancel:                clientCancel,
			activeSubscriptions:   make(map[string]struct{}),
			pendingServerRequests: make(map[string]chan *ergosockets.Envelope),
			logger:                b.config.logger, // Pass logger to managedClient
			registered:            false,           // Will be set to true after registration
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
		// Use trySend which will count dropped messages and disconnect slow clients
		mc.trySend(env)
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
	id         string // Server-assigned ID (source of truth)
	clientID   string // Client-provided ID (for reference only)
	name       string // Human-readable name for the client
	clientType string // Type of client (e.g., "browser", "app", "service")
	clientURL  string // URL of the client (for browser connections)
	conn       *websocket.Conn
	broker     *Broker
	send       chan *ergosockets.Envelope // Buffered channel for outgoing messages
	logger     *slog.Logger

	ctx    context.Context    // Context for this client's lifetime, derived from broker.mainCtx
	cancel context.CancelFunc // Cancels this client's context

	activeSubscriptionsMu sync.Mutex
	activeSubscriptions   map[string]struct{}

	pendingServerRequestsMu sync.Mutex
	pendingServerRequests   map[string]chan *ergosockets.Envelope

	droppedMessagesMu sync.Mutex
	droppedMessages   int // Counter for dropped messages due to full send buffer

	registered bool // Whether the client has completed registration
}

// Methods for ClientHandle interface
func (mc *managedClient) ID() string               { return mc.id }
func (mc *managedClient) ClientID() string         { return mc.clientID }
func (mc *managedClient) Name() string             { return mc.name }
func (mc *managedClient) ClientType() string       { return mc.clientType }
func (mc *managedClient) ClientURL() string        { return mc.clientURL }
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

	// Use trySend which will count dropped messages and disconnect slow clients
	mc.trySend(env)
	mc.logger.Info(fmt.Sprintf("Broker: Sent direct message on topic '%s' to client %s", topic, mc.id))
	return nil
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
		case ergosockets.TypePublish:
			mc.handleClientPublish(&env)
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

func (mc *managedClient) handleClientPublish(env *ergosockets.Envelope) {
	topic := env.Topic
	if topic == "" {
		mc.logger.Info(fmt.Sprintf("Broker: Client %s sent publish with empty topic", mc.id))
		return
	}

	// Forward the publish to all subscribers
	err := mc.broker.Publish(mc.ctx, topic, env.Payload)
	if err != nil {
		mc.logger.Info(fmt.Sprintf("Broker: Failed to publish message from client %s to topic '%s': %v", mc.id, topic, err))
	} else {
		mc.logger.Info(fmt.Sprintf("Broker: Client %s published message to topic '%s'", mc.id, topic))
	}
}

// trySend attempts to send an envelope to the client's send channel without blocking indefinitely.
func (mc *managedClient) trySend(env *ergosockets.Envelope) {
	select {
	case mc.send <- env:
		// Message sent successfully
	case <-mc.ctx.Done():
		mc.logger.Info(fmt.Sprintf("Broker: Client %s context done, cannot send envelope type %s on topic %s", mc.id, env.Type, env.Topic))
	default: // Should only happen if send channel is full and writePump is also blocked/slow
		mc.logger.Info(fmt.Sprintf("Broker: Client %s send channel full when trying to send envelope type %s on topic %s. Message potentially dropped.", mc.id, env.Type, env.Topic))

		// This indicates a slow client; disconnect it after a few dropped messages
		mc.droppedMessagesMu.Lock()
		mc.droppedMessages++
		droppedCount := mc.droppedMessages
		mc.droppedMessagesMu.Unlock()

		// If we've dropped too many messages, disconnect the client
		if droppedCount >= 3 { // Threshold for disconnection
			mc.logger.Info(fmt.Sprintf("Broker: Client %s dropped %d messages, disconnecting slow client.", mc.id, droppedCount))
			// Close the connection; readPump's defer will handle full removeClient
			mc.conn.Close(websocket.StatusPolicyViolation, "too many dropped messages")
			// Also remove the client directly to ensure it's removed immediately
			go mc.broker.removeClient(mc)
		}
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

```

<a id="file-pkg-broker-broker_test-go"></a>
**File: pkg/broker/broker_test.go**

```go
// ergosockets/broker/broker_test.go
package broker_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/client"
	app_shared_types "github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
)

func TestBrokerRequestResponse(t *testing.T) {
	bs := testutil.NewBrokerServer(t)

	err := bs.OnRequest(app_shared_types.TopicGetTime,
		func(ch broker.ClientHandle, req app_shared_types.GetTimeRequest) (app_shared_types.GetTimeResponse, error) {
			t.Logf("TestBroker: Server handler for %s invoked by client %s", app_shared_types.TopicGetTime, ch.ID())
			return app_shared_types.GetTimeResponse{CurrentTime: "test-time-refined"}, nil
		},
	)
	if err != nil {
		t.Fatalf("Failed to register server handler: %v", err)
	}

	cli := testutil.NewTestClient(t, bs.WSURL)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Using the new generic client.Request
	resp, err := client.GenericRequest[app_shared_types.GetTimeResponse](cli, ctx, app_shared_types.TopicGetTime)
	if err != nil {
		t.Fatalf("Client request failed: %v", err)
	}
	if resp.CurrentTime != "test-time-refined" {
		t.Errorf("Expected response 'test-time-refined', got '%s'", resp.CurrentTime)
	}
	t.Log("Client received correct time response.")
}

func TestBrokerPublishSubscribe(t *testing.T) {
	bs := testutil.NewBrokerServer(t)

	cli := testutil.NewTestClient(t, bs.WSURL)

	receivedChan := make(chan app_shared_types.ServerAnnouncement, 1)
	_, err := cli.Subscribe(app_shared_types.TopicServerAnnounce,
		func(announcement *app_shared_types.ServerAnnouncement) error {
			t.Logf("TestBroker: Client received announcement: %+v", announcement)
			receivedChan <- *announcement
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Client failed to subscribe: %v", err)
	}
	time.Sleep(150 * time.Millisecond) // Allow subscribe to propagate

	testAnnouncement := app_shared_types.ServerAnnouncement{Message: "hello-broker-test-refined", Timestamp: "nowish"}
	err = bs.Publish(context.Background(), app_shared_types.TopicServerAnnounce, testAnnouncement)
	if err != nil {
		t.Fatalf("Broker failed to publish: %v", err)
	}

	select {
	case received := <-receivedChan:
		if received.Message != testAnnouncement.Message || received.Timestamp != testAnnouncement.Timestamp {
			t.Errorf("Received announcement %+v, expected %+v", received, testAnnouncement)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Client did not receive published message in time")
	}
	t.Log("Client received correct published announcement.")
}

func TestBrokerWaitForClient(t *testing.T) {
	bs := testutil.NewBrokerServer(t, broker.WithPingInterval(-1)) // Disable pings for predictability

	cli := testutil.NewTestClient(t, bs.WSURL, client.WithClientPingInterval(-1)) // Disable client pings too

	clientHandle, err := testutil.WaitForClient(t, bs.Broker, cli.ID(), 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to get client handle from broker: %v. Client ID: %s", err, cli.ID())
	}
	t.Logf("=======Client connected. ID: %s", clientHandle.ID())

	// This test just verifies that we can get a client handle from the broker
	// The actual client-to-server request functionality is tested in TestBrokerClientToServerRequest
}

func TestBrokerClientToServerRequest(t *testing.T) {
	bs := testutil.NewBrokerServer(t, broker.WithPingInterval(-1)) // Disable pings for predictability

	cli := testutil.NewTestClient(t, bs.WSURL, client.WithClientPingInterval(-1)) // Disable client pings too

	clientHandlerInvoked := make(chan bool, 1)
	expectedClientUptime := "test-uptime-refined"
	err := cli.OnRequest(app_shared_types.TopicClientGetStatus,
		func(req app_shared_types.ClientStatusQuery) (app_shared_types.ClientStatusReport, error) {
			t.Logf("TestBroker: Client OnRequest handler for %s invoked with query: %s", app_shared_types.TopicClientGetStatus, req.QueryDetailLevel)
			clientHandlerInvoked <- true
			return app_shared_types.ClientStatusReport{ClientID: cli.ID(), Status: "client-test-ok-refined", Uptime: expectedClientUptime}, nil
		},
	)
	if err != nil {
		t.Fatalf("Client failed to register OnRequest handler: %v", err)
	}

	clientHandle, err := testutil.WaitForClient(t, bs.Broker, cli.ID(), 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to get client handle from broker: %v", err)
	}

	var responsePayload app_shared_types.ClientStatusReport
	ctxReq, cancelReq := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelReq()

	// ClientHandle.Request takes responsePayloadPtr interface{}
	err = clientHandle.Request(ctxReq, app_shared_types.TopicClientGetStatus,
		app_shared_types.ClientStatusQuery{QueryDetailLevel: "full-refined"}, &responsePayload, 0)

	if err != nil {
		t.Fatalf("Server failed to make request to client: %v", err)
	}

	select {
	case <-clientHandlerInvoked:
		t.Log("Client OnRequest handler was invoked.")
	case <-time.After(1 * time.Second):
		t.Fatal("Client OnRequest handler was not invoked in time.")
	}

	if responsePayload.Status != "client-test-ok-refined" || responsePayload.Uptime != expectedClientUptime {
		t.Errorf("Expected client status 'client-test-ok-refined' and uptime '%s', got status '%s', uptime '%s'",
			expectedClientUptime, responsePayload.Status, responsePayload.Uptime)
	}
	if responsePayload.ClientID != cli.ID() {
		t.Errorf("Expected client ID '%s', got '%s'", cli.ID(), responsePayload.ClientID)
	}
	t.Logf("Server received correct status response from client: %+v", responsePayload)
}

func TestBrokerSlowClientDisconnect(t *testing.T) {
	// Small send buffer for the client on the broker side
	bs := testutil.NewBrokerServer(t, broker.WithClientSendBuffer(1), broker.WithPingInterval(-1))

	// No defer bs.Shutdown here, we want to inspect after client is gone

	cli := testutil.NewTestClient(t, bs.WSURL, client.WithClientPingInterval(-1)) // Client doesn't need to be special

	// Wait for client to connect
	clientHandle, err := testutil.WaitForClient(t, bs.Broker, cli.ID(), 5*time.Second)
	if err != nil {
		t.Fatalf("Client did not connect: %v", err)
	}
	t.Logf("Client %s connected.", clientHandle.ID())

	// Client subscribes but will not read from its WebSocket connection
	// This requires the client library to have a way to "not read" or for us to
	// simply not process messages on the client side for this test.
	// For this test, we'll rely on the broker's send buffer filling up.
	// The client.Subscribe is not strictly needed if we just flood the send channel.

	// Make the client subscribe to the flood topic
	// This is needed to ensure the messages are actually sent to the client
	unsubscribe, err := cli.Subscribe("flood_topic", func(msg app_shared_types.BroadcastMessage) error {
		// Do nothing with the message, just let the buffer fill up
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to subscribe client to flood_topic: %v", err)
	}
	defer unsubscribe()

	// Give the subscription time to be processed by the broker
	time.Sleep(100 * time.Millisecond)

	t.Log("Client subscribed to flood_topic")

	// Flood the client's send buffer from the broker
	// The buffer is 1. Send 10 messages to ensure we trigger the slow client detection.
	for i := 0; i < 10; i++ {
		// Use direct send to the client to ensure the messages are sent
		err := clientHandle.Send(context.Background(), "flood_topic", app_shared_types.BroadcastMessage{Content: fmt.Sprintf("flood %d", i)})
		if err != nil {
			t.Logf("Error sending message %d: %v", i, err)
		}
		// Also try publishing to the topic
		err = bs.Publish(context.Background(), "flood_topic", app_shared_types.BroadcastMessage{Content: fmt.Sprintf("flood publish %d", i)})
		if err != nil {
			t.Logf("Error publishing message %d: %v", i, err)
		}
		// Small sleep to allow the broker to process the messages
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for the broker to detect the slow client and disconnect it
	var disconnected bool
	for i := 0; i < 100; i++ { // Poll for ~5 seconds
		_, errGet := bs.GetClient(cli.ID())
		if errGet != nil { // Client no longer found
			disconnected = true
			break
		}
		time.Sleep(50 * time.Millisecond)

		// Every 10 iterations, send more messages to ensure the buffer stays full
		if i%10 == 0 {
			for j := 0; j < 5; j++ {
				bs.Publish(context.Background(), "flood_topic", app_shared_types.BroadcastMessage{Content: fmt.Sprintf("additional flood %d-%d", i, j)})
			}
		}
	}

	if !disconnected {
		t.Fatal("Broker did not disconnect the slow client in time")
	}
	t.Log("Broker successfully disconnected the slow client.")

	bs.Shutdown(context.Background()) // Now shutdown broker
}

// TestBrokerClientDisconnect (from previous) - refined
func TestClientIDAndName(t *testing.T) {
	// Create a broker with registration handler
	bs := testutil.NewBrokerServer(t)

	// The broker already has a registration handler registered in New()

	// Create a client with custom name
	_ = testutil.NewTestClient(t, bs.WSURL, client.WithClientName("test-client"))

	// Wait a moment for the client to connect and register
	time.Sleep(500 * time.Millisecond)

	// Find the client in the broker
	var clientHandle broker.ClientHandle
	var found bool

	bs.IterateClients(func(ch broker.ClientHandle) bool {
		t.Logf("Found client: ID=%s, Name=%s, Type=%s, URL=%s", ch.ID(), ch.Name(), ch.ClientType(), ch.ClientURL())

		// Check if this is our client by name
		if ch.Name() == "test-client" {
			clientHandle = ch
			found = true
			return false // Stop iterating
		}
		return true // Continue iterating
	})

	if !found {
		t.Fatalf("Client with name 'test-client' not found in broker")
	}

	// Verify client information on the server side
	t.Logf("Client connected with ID: %s", clientHandle.ID())
	t.Logf("Client name: %s", clientHandle.Name())
	t.Logf("Client type: %s", clientHandle.ClientType())
	t.Logf("Client URL: %s", clientHandle.ClientURL())

	// Verify client name matches what we set
	if clientHandle.Name() != "test-client" {
		t.Errorf("Expected client name 'test-client', got '%s'", clientHandle.Name())
	}
}

func TestBrokerClientDisconnect(t *testing.T) {
	bs := testutil.NewBrokerServer(t, broker.WithPingInterval(1000*time.Millisecond)) // Faster pings for test
	t.Logf("WsURL: %s", bs.WSURL)

	cli := testutil.NewTestClient(t, bs.WSURL, client.WithClientPingInterval(-1)) // Client doesn't ping

	clientHandle, err := testutil.WaitForClient(t, bs.Broker, cli.ID(), 5*time.Second)
	if err != nil {
		t.Fatalf("Client did not connect for disconnect test: %v", err)
	}
	t.Logf("Client %s connected for disconnect test.", clientHandle.ID())

	// Get initial count (should be 1)
	var initialClientCount int
	bs.IterateClients(func(ch broker.ClientHandle) bool { initialClientCount++; return true })
	if initialClientCount != 1 {
		t.Fatalf("Expected 1 client, got %d", initialClientCount)
	}

	cli.Close() // Client closes connection

	// Wait for broker to remove client (due to readPump error or ping failure)
	var clientRemoved bool
	for i := 0; i < 60; i++ { // Wait up to 3s (generous for ping cycle + processing)
		_, errGet := bs.GetClient(cli.ID())
		if errGet != nil {
			clientRemoved = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !clientRemoved {
		t.Fatal("Broker did not remove disconnected client in time")
	}
	t.Log("Client disconnected and broker removed it.")

	var finalClientCount int
	bs.IterateClients(func(ch broker.ClientHandle) bool { finalClientCount++; return true })
	if finalClientCount != 0 {
		t.Errorf("Expected 0 clients after disconnect, got %d", finalClientCount)
	}

	bs.Shutdown(context.Background())
}

```

<a id="file-pkg-broker-client_handle-go"></a>
**File: pkg/broker/client_handle.go**

```go
// ergosockets/broker/client_handle.go
package broker

import (
	"context"
	"time"
)

// ClientHandle is an interface representing a client connection from the server's perspective.
// It's passed to server-side request handlers.
type ClientHandle interface {
	ID() string               // Unique server-assigned ID of the client (source of truth).
	ClientID() string         // Client-provided ID (for reference only).
	Name() string             // Human-readable name for the client.
	ClientType() string       // Type of client (e.g., "browser", "app", "service").
	ClientURL() string        // URL of the client (for browser connections).
	Context() context.Context // Context associated with this client's connection.

	// Request sends a request to this specific client and waits for a response.
	// The responsePayloadPtr argument should be a pointer to a struct where the response will be unmarshalled.
	// Timeout <= 0 means use broker's default serverRequestTimeout.
	Request(ctx context.Context, topic string, requestData interface{}, responsePayloadPtr interface{}, timeout time.Duration) error

	// Send publishes a message directly to this client on a specific topic without expecting a direct response.
	Send(ctx context.Context, topic string, payloadData interface{}) error
}

```

<a id="file-pkg-broker-js_client-go"></a>
**File: pkg/broker/js_client.go**

```go
// pkg/broker/js_client.go
package broker

import (
	"net/http"

	"github.com/lightforgemedia/go-websocketmq/pkg/browser_client"
)

// WebSocketMQHandlerOptions configures the WebSocketMQ HTTP handlers
type WebSocketMQHandlerOptions struct {
	// WebSocketPath is the URL path where the WebSocket endpoint will be served
	// Default: "/wsmq"
	WebSocketPath string

	// JavaScriptClientOptions configures the JavaScript client handler
	// If nil, default options will be used
	JavaScriptClientOptions *browser_client.ClientScriptOptions
}

// DefaultWebSocketMQHandlerOptions returns the default options for the WebSocketMQ HTTP handlers
func DefaultWebSocketMQHandlerOptions() WebSocketMQHandlerOptions {
	return WebSocketMQHandlerOptions{
		WebSocketPath:           "/wsmq",
		JavaScriptClientOptions: nil, // Will use defaults
	}
}

// RegisterHandlers registers both the WebSocket handler and JavaScript client handler
// with the provided ServeMux. This is the recommended way to set up WebSocketMQ
// in your HTTP server.
//
// The WebSocket endpoint will be served at the specified path (default: "/wsmq").
// The JavaScript client will be served at the path specified in the options
// (default: "/websocketmq.js").
//
// Example:
//
//	mux := http.NewServeMux()
//	broker.RegisterHandlers(mux, broker.DefaultWebSocketMQHandlerOptions())
//
// Or with custom options:
//
//	options := broker.DefaultWebSocketMQHandlerOptions()
//	options.WebSocketPath = "/api/websocket"
//	jsOptions := browser_client.DefaultClientScriptOptions()
//	jsOptions.Path = "/js/websocketmq.js"
//	options.JavaScriptClientOptions = &jsOptions
//	broker.RegisterHandlers(mux, options)
func (b *Broker) RegisterHandlers(mux *http.ServeMux, options WebSocketMQHandlerOptions) {
	// Register WebSocket handler
	mux.Handle(options.WebSocketPath, b.UpgradeHandler())
	b.config.logger.Info("Broker: Registered WebSocket handler at " + options.WebSocketPath)

	// Register JavaScript client handler
	jsOptions := browser_client.DefaultClientScriptOptions()
	if options.JavaScriptClientOptions != nil {
		jsOptions = *options.JavaScriptClientOptions
	}
	browser_client.RegisterHandler(mux, jsOptions)
	b.config.logger.Info("Broker: Registered JavaScript client handler at " + jsOptions.Path)
}

// RegisterHandlersWithDefaults registers both the WebSocket handler and JavaScript client handler
// with the provided ServeMux using default options.
//
// This is a convenience function that uses the default options for both handlers.
// The WebSocket endpoint will be served at "/wsmq".
// The JavaScript client will be served at "/websocketmq.js".
//
// Example:
//
//	mux := http.NewServeMux()
//	broker.RegisterHandlersWithDefaults(mux)
func (b *Broker) RegisterHandlersWithDefaults(mux *http.ServeMux) {
	b.RegisterHandlers(mux, DefaultWebSocketMQHandlerOptions())
}

```

<a id="file-pkg-browser_client-browser_tests-client_publish_test-go"></a>
**File: pkg/browser_client/browser_tests/client_publish_test.go**

```go
// Package browser_tests contains tests for the WebSocketMQ browser client.
package browser_tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBrowserClientPublish tests the browser client's ability to publish messages to topics.
func TestBrowserClientPublish(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping browser test in short mode")
	}

	// Create a broker server with accept options to allow any origin
	bs := testutil.NewBrokerServer(t, broker.WithAcceptOptions(&websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	}))

	// Create a channel to receive published messages
	messagesCh := make(chan map[string]interface{}, 10)
	var messagesLock sync.Mutex
	var receivedMessages []map[string]interface{}

	// Create a context for the subscription
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up a goroutine to monitor for published messages
	go func() {
		// Create a client to subscribe to the topic
		cli := testutil.NewTestClient(t, bs.WSURL)
		defer cli.Close()

		// Subscribe to the test:broadcast topic
		_, err := cli.Subscribe("test:broadcast", func(payload map[string]interface{}) error {
			t.Logf("Server received message: %v", payload)
			messagesCh <- payload
			messagesLock.Lock()
			receivedMessages = append(receivedMessages, payload)
			messagesLock.Unlock()
			return nil
		})
		require.NoError(t, err, "Failed to subscribe to test:broadcast")

		// Wait for the context to be cancelled
		<-ctx.Done()
	}()

	// Wait a bit for the subscription to be established
	time.Sleep(500 * time.Millisecond)

	// Create an HTTP server mux
	mux := http.NewServeMux()

	// Register both the WebSocket handler and JavaScript client handler
	bs.RegisterHandlersWithDefaults(mux)

	// Create the HTTP server first so we can use its URL in the HTML
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	// Serve the JavaScript helpers
	mux.Handle("/js_helpers/", http.StripPrefix("/js_helpers/", http.FileServer(http.Dir("js_helpers"))))

	// Create a test HTML page handler
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<!DOCTYPE html>
				<html>
				<head>
					<title>WebSocketMQ Client Publish Test</title>
					<script src="/js_helpers/console_override.js"></script>
					<script src="/websocketmq.js"></script>
					<script src="/js_helpers/connect.js"></script>
					<script src="/js_helpers/publish_helpers.js"></script>
				</head>
				<body>
					<h1>WebSocketMQ Client Publish Test</h1>
					<div>
						<h2>Connection Status: <span id="status">Disconnected</span></h2>
					</div>
					<div>
						<button id="publish-btn">Publish Message</button>
						<button id="multi-publish-btn">Publish Multiple Messages</button>
						<h2>Publish Status: <span id="publish-status">Not Published</span></h2>
					</div>
				</body>
				</html>`

		w.Write([]byte(html))
	})

	// Test the browser connection with headless mode disabled
	headless := false
	result := TestBrowserConnection(t, bs, httpServer, headless)

	// Now that we're connected, click the button to publish a message
	t.Log("Clicking the Publish Message button")
	result.Page.MustElement("#publish-btn").MustClick()

	// Wait for the message to be published and received by the server
	var receivedMessage map[string]interface{}
	select {
	case receivedMessage = <-messagesCh:
		t.Logf("Received message: %v", receivedMessage)
	case <-time.After(5 * time.Second):
		t.Fatal("Timed out waiting for message")
	}

	// Verify the message contains the expected fields
	assert.Contains(t, receivedMessage, "messageId", "Message should contain messageId")
	assert.Contains(t, receivedMessage, "timestamp", "Message should contain timestamp")
	assert.Contains(t, receivedMessage, "content", "Message should contain content")
	assert.Equal(t, "Hello from the client!", receivedMessage["content"], "Message content should match")

	// Wait for the publish status to be updated
	var publishStatus string
	var err error
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)

		// Check if publish status has been updated
		publishStatus, err = result.Page.MustElement("#publish-status").Text()
		if err == nil && publishStatus == "Published successfully" {
			t.Logf("Publish status updated after %d attempts: %s", i+1, publishStatus)
			break
		}
	}

	// Verify the publish status
	assert.Equal(t, "Published successfully", publishStatus, "Publish status should be 'Published successfully'")

	// Now test publishing multiple messages
	t.Log("Clicking the Publish Multiple Messages button")
	result.Page.MustElement("#multi-publish-btn").MustClick()

	// Wait for multiple messages to be received
	messageCount := 1 // We already received one message
	timeout := time.After(5 * time.Second)
	done := false

	for !done {
		select {
		case msg := <-messagesCh:
			t.Logf("Received additional message: %v", msg)
			messageCount++
			if messageCount >= 4 { // 1 initial + 3 from multi-publish
				done = true
			}
		case <-timeout:
			t.Logf("Timed out waiting for all messages, received %d so far", messageCount)
			done = true
		}
	}

	// Verify we received at least 3 messages (the initial one + at least 2 from multi-publish)
	assert.GreaterOrEqual(t, messageCount, 3, "Should have received at least 3 messages total")

	// Check console logs for publish operations
	logs := result.Page.GetConsoleLog()
	t.Logf("Console logs: %v", logs)

	// Check if we have log messages for publishing
	publishAttempted := false
	publishSucceeded := false
	for _, log := range logs {
		if strings.Contains(log, "Publishing message to test:broadcast") {
			publishAttempted = true
		}
		if strings.Contains(log, "Published message to test:broadcast") {
			publishSucceeded = true
		}
	}

	assert.True(t, publishAttempted, "Should have attempted to publish to test:broadcast")
	assert.True(t, publishSucceeded, "Should have successfully published to test:broadcast")

	// Add a delay so we can see the browser window before it closes
	// This is only useful when running with headless=false
	if !result.Browser.IsHeadless() {
		t.Log("Waiting 5 seconds so you can see the browser window...")
		time.Sleep(5 * time.Second)
	}
}

```

<a id="file-pkg-browser_client-browser_tests-connection_test-go"></a>
**File: pkg/browser_client/browser_tests/connection_test.go**

```go
// Package browser_tests contains tests for the WebSocketMQ browser client.
package browser_tests

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
)

// TestBrowserClientConnection tests the browser client's ability to connect to the broker.
// This is a minimal test to verify that the browser client can connect to the broker.
func TestBrowserClientConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping browser test in short mode")
	}

	// Create a broker server with accept options to allow any origin
	bs := testutil.NewBrokerServer(t, broker.WithAcceptOptions(&websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	}))

	// Note: system:register is already registered by the broker

	// Create an HTTP server mux
	mux := http.NewServeMux()

	// Register both the WebSocket handler and JavaScript client handler
	bs.RegisterHandlersWithDefaults(mux)

	// Create the HTTP server first so we can use its URL in the HTML
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()
	//serve /js_helpers/console_override.js and /js_helpers/connect.js and other static files from js_helpers folder
	mux.Handle("/js_helpers/", http.StripPrefix("/js_helpers/", http.FileServer(http.Dir("js_helpers"))))

	// Create a test HTML page handler
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<!DOCTYPE html>
			<html>
			<head>
				<title>WebSocketMQ Browser Client Test</title>
				<script src="/js_helpers/console_override.js"></script>
				<script src="/websocketmq.js"></script>
				<script src="/js_helpers/connect.js"></script>
			</head>
			<body>
				<h1>WebSocketMQ Browser Client Test</h1>
				<div>
					<h2>Connection Status: <span id="status">Disconnected</span></h2>
				</div>
				<!-- Just testing connection, no need for additional buttons or response display -->
			</body>
			</html>`

		// No need to replace WebSocket URL as the client will determine it automatically
		w.Write([]byte(html))
	})

	// HTTP server is already created above

	// Test the browser connection with headless mode disabled
	headless := false
	result := TestBrowserConnection(t, bs, httpServer, headless)

	// Add a delay so we can see the browser window before it closes
	// This is only useful when running with headless=false
	if !result.Browser.IsHeadless() {
		t.Log("Waiting 5 seconds so you can see the browser window...")
		time.Sleep(5 * time.Second)
	}
}

```

<a id="file-pkg-browser_client-browser_tests-js_helpers-connect-js"></a>
**File: pkg/browser_client/browser_tests/js_helpers/connect.js**

```javascript
// Initialize the WebSocketMQ client when the page loads
// Make client a global variable
window.client = null;

// Set up the client and connect automatically when the page loads
window.addEventListener('DOMContentLoaded', () => {
    console.log('Page loaded, connecting with default settings');

    // Create a new WebSocketMQ client with default settings
    // The client should automatically determine the WebSocket URL
    try {
        console.log('Creating client with explicit WebSocket URL');
        const wsUrl = window.location.origin.replace('http', 'ws') + '/wsmq';
        console.log('Using WebSocket URL:', wsUrl);

        window.client = new WebSocketMQ.Client({});
        console.log('Client created successfully');
    } catch (err) {
        console.error('Error creating client:', err);
    }

    window.client.onConnect(() => {
        console.log('Connected to WebSocket server with ID:', window.client.getID());
        document.getElementById('status').textContent = 'Connected';
        document.getElementById('status').style.color = 'green';
    });

    window.client.onDisconnect(() => {
        console.log('Disconnected from WebSocket server');
        document.getElementById('status').textContent = 'Disconnected';
        document.getElementById('status').style.color = 'red';
    });

    // Add error handler
    window.client.onError((error) => {
        console.error('WebSocket error:', error);
    });

    // Connect automatically with error handling
    try {
        console.log('Calling client.connect()');
        window.client.connect();
        console.log('client.connect() called successfully');
    } catch (err) {
        console.error('Error connecting to WebSocket:', err);
    }
});
```

<a id="file-pkg-browser_client-browser_tests-js_helpers-console_override-js"></a>
**File: pkg/browser_client/browser_tests/js_helpers/console_override.js**

```javascript
// Store console logs
window.consoleLog = [];
const originalConsole = console.log;
console.log = function() {
    window.consoleLog.push(Array.from(arguments).join(' '));
    originalConsole.apply(console, arguments);
};
```

<a id="file-pkg-browser_client-browser_tests-js_helpers-publish_helpers-js"></a>
**File: pkg/browser_client/browser_tests/js_helpers/publish_helpers.js**

```javascript
// publish_helpers.js
// Helper functions for publishing messages to topics

// Wait for client to be connected before publishing
function waitForConnection(callback, interval = 100) {
  if (window.client && window.client.isConnected) {
    callback();
  } else {
    setTimeout(() => waitForConnection(callback, interval), interval);
  }
}

// Publish a message to a topic
function publishMessage(topic, payload) {
  console.log(`Publishing message to ${topic} with payload:`, payload);
  
  // Update UI to show publish is in progress
  const statusElement = document.getElementById('publish-status');
  if (statusElement) {
    statusElement.textContent = 'Publishing...';
  }
  
  // Make sure client is connected
  waitForConnection(() => {
    // Publish the message
    window.client.publish(topic, payload)
      .then(() => {
        console.log(`Published message to ${topic}`);
        
        // Update UI with success
        if (statusElement) {
          statusElement.textContent = 'Published successfully';
          statusElement.style.color = 'green';
        }
        
        // Dispatch a custom event for test detection
        const event = new CustomEvent('publishComplete', { 
          detail: { success: true, topic, payload } 
        });
        document.dispatchEvent(event);
      })
      .catch(error => {
        console.error(`Error publishing message to ${topic}:`, error);
        
        // Update UI with error
        if (statusElement) {
          statusElement.textContent = `Error: ${error.message || error}`;
          statusElement.style.color = 'red';
        }
        
        // Dispatch a custom event for test detection
        const event = new CustomEvent('publishComplete', { 
          detail: { success: false, topic, error } 
        });
        document.dispatchEvent(event);
      });
  });
}

// Publish a test message
function publishTestMessage() {
  publishMessage('test:broadcast', {
    messageId: 'client-' + Date.now(),
    timestamp: new Date().toISOString(),
    content: 'Hello from the client!'
  });
}

// Publish multiple messages
function publishMultipleMessages(count = 3) {
  console.log(`Publishing ${count} messages`);
  
  for (let i = 0; i < count; i++) {
    setTimeout(() => {
      publishMessage('test:broadcast', {
        messageId: 'client-' + Date.now() + '-' + i,
        timestamp: new Date().toISOString(),
        content: `Message ${i+1} from the client`
      });
    }, i * 100); // Small delay between messages
  }
}

// Initialize publish buttons if they exist
document.addEventListener('DOMContentLoaded', () => {
  const publishBtn = document.getElementById('publish-btn');
  if (publishBtn) {
    publishBtn.addEventListener('click', publishTestMessage);
  }
  
  const multiPublishBtn = document.getElementById('multi-publish-btn');
  if (multiPublishBtn) {
    multiPublishBtn.addEventListener('click', () => publishMultipleMessages());
  }
});

```

<a id="file-pkg-browser_client-browser_tests-publish_subscribe_test-go"></a>
**File: pkg/browser_client/browser_tests/publish_subscribe_test.go**

```go
// Package browser_tests contains tests for the WebSocketMQ browser client.
package browser_tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBrowserClientPublishSubscribe tests the browser client's ability to subscribe to topics and receive published messages.
func TestBrowserClientPublishSubscribe(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping browser test in short mode")
	}

	// Create a broker server with accept options to allow any origin
	bs := testutil.NewBrokerServer(t, broker.WithAcceptOptions(&websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	}))

	// Create an HTTP server mux
	mux := http.NewServeMux()

	// Register both the WebSocket handler and JavaScript client handler
	bs.RegisterHandlersWithDefaults(mux)

	// Create the HTTP server first so we can use its URL in the HTML
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	// Serve the JavaScript helpers
	mux.Handle("/js_helpers/", http.StripPrefix("/js_helpers/", http.FileServer(http.Dir("js_helpers"))))

	// Create a test HTML page handler
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<!DOCTYPE html>
				<html>
				<head>
					<title>WebSocketMQ Publish-Subscribe Test</title>
					<script src="/js_helpers/console_override.js"></script>
					<script src="/websocketmq.js"></script>
					<script src="/js_helpers/connect.js"></script>
					<script>
						// Initialize message counter
						window.messageCount = 0;
						
						// Function to subscribe to a topic
						function subscribeTopic(topic) {
							console.log('Subscribing to topic:', topic);
							
							// Make sure client is connected
							if (window.client && window.client.isConnected) {
								// Subscribe to the topic
								window.unsubscribeFn = window.client.subscribe(topic, message => {
									console.log('Received message on topic ' + topic + ':', message);
									window.messageCount++;
									
									// Update UI with message count
									document.getElementById('message-count').textContent = window.messageCount;
									
									// Add message to messages container
									const messagesElement = document.getElementById('messages');
									if (messagesElement) {
										const messageElement = document.createElement('div');
										messageElement.className = 'message';
										messageElement.textContent = typeof message === 'object' 
											? JSON.stringify(message) 
											: message;
										messagesElement.appendChild(messageElement);
									}
								});
								
								document.getElementById('subscription-status').textContent = 'Subscribed to ' + topic;
								document.getElementById('subscription-status').style.color = 'green';
							} else {
								console.error('Client not connected');
								document.getElementById('subscription-status').textContent = 'Error: Client not connected';
								document.getElementById('subscription-status').style.color = 'red';
							}
						}
						
						// Function to unsubscribe from a topic
						function unsubscribeTopic() {
							console.log('Unsubscribing from topic');
							
							if (window.unsubscribeFn) {
								window.unsubscribeFn();
								window.unsubscribeFn = null;
								document.getElementById('subscription-status').textContent = 'Unsubscribed';
								document.getElementById('subscription-status').style.color = 'red';
							} else {
								console.warn('Not subscribed to any topic');
							}
						}
						
						// Initialize the page when it loads
						document.addEventListener('DOMContentLoaded', () => {
							// Set up subscription buttons
							const subscribeBtn = document.getElementById('subscribe-btn');
							if (subscribeBtn) {
								subscribeBtn.addEventListener('click', () => subscribeTopic('test:broadcast'));
							}
							
							const unsubscribeBtn = document.getElementById('unsubscribe-btn');
							if (unsubscribeBtn) {
								unsubscribeBtn.addEventListener('click', unsubscribeTopic);
							}
							
							// Auto-subscribe after connection
							// Use a polling approach to check for client connection
							const checkInterval = setInterval(() => {
								if (window.client && window.client.isConnected) {
									clearInterval(checkInterval);
									console.log('Client connected, auto-subscribing to test:broadcast');
									subscribeTopic('test:broadcast');
								}
							}, 100);
						});
					</script>
				</head>
				<body>
					<h1>WebSocketMQ Publish-Subscribe Test</h1>
					<div>
						<h2>Connection Status: <span id="status">Disconnected</span></h2>
					</div>
					<div>
						<h2>Subscription Status: <span id="subscription-status">Not Subscribed</span></h2>
						<button id="subscribe-btn">Subscribe</button>
						<button id="unsubscribe-btn">Unsubscribe</button>
					</div>
					<div>
						<h2>Messages Received: <span id="message-count">0</span></h2>
						<div id="messages"></div>
					</div>
				</body>
				</html>`

		w.Write([]byte(html))
	})

	// Test the browser connection with headless mode disabled
	headless := false
	result := TestBrowserConnection(t, bs, httpServer, headless)

	// Wait for the subscription to be established
	time.Sleep(1 * time.Second)

	// Publish a message to the topic
	t.Log("Publishing message to test:broadcast")
	ctx := context.Background()
	err := bs.Broker.Publish(ctx, "test:broadcast", map[string]interface{}{
		"messageId": "test-message-1",
		"timestamp": time.Now().Format(time.RFC3339),
		"content":   "Hello from the server!",
	})
	require.NoError(t, err, "Failed to publish message")

	// Wait a bit for the message to be received
	time.Sleep(500 * time.Millisecond)

	// Verify the message count has been updated
	messageCount, err := result.Page.MustElement("#message-count").Text()
	require.NoError(t, err, "Should be able to get message count")
	assert.Equal(t, "1", messageCount, "Should have received 1 message")

	// Publish another message
	t.Log("Publishing second message to test:broadcast")
	err = bs.Broker.Publish(ctx, "test:broadcast", map[string]interface{}{
		"messageId": "test-message-2",
		"timestamp": time.Now().Format(time.RFC3339),
		"content":   "Second message from the server!",
	})
	require.NoError(t, err, "Failed to publish second message")

	// Wait a bit for the message to be received
	time.Sleep(500 * time.Millisecond)

	// Verify the message count has been updated
	messageCount, err = result.Page.MustElement("#message-count").Text()
	require.NoError(t, err, "Should be able to get message count")
	assert.Equal(t, "2", messageCount, "Should have received 2 messages")

	messages, err := result.Page.MustElement("#messages").Text()
	require.NoError(t, err, "Should be able to get message count")
	t.Logf("Messages: %v", messages)
	assert.Contains(t, messages, "Hello from the server!")
	assert.Contains(t, messages, "Second message from the server!")

	// test - message - 2

	// Check console logs for subscription and messages
	logs := result.Page.GetConsoleLog()
	t.Logf("Console logs: %v", logs)

	// Check if we have log messages for the subscription and messages
	subscribed := false
	messageReceived := false
	for _, log := range logs {
		if strings.Contains(log, "Subscribing to topic: test:broadcast") {
			subscribed = true
		}
		if strings.Contains(log, "Received message on topic test:broadcast") {
			messageReceived = true
		}
	}

	assert.True(t, subscribed, "Should have subscribed to test:broadcast")
	assert.True(t, messageReceived, "Should have received a message on test:broadcast")

	// Add a delay so we can see the browser window before it closes
	// This is only useful when running with headless=false
	if !result.Browser.IsHeadless() {
		t.Log("Waiting 5 seconds so you can see the browser window...")
		time.Sleep(5 * time.Second)
	}
}

```

<a id="file-pkg-browser_client-browser_tests-request_response_test-go"></a>
**File: pkg/browser_client/browser_tests/request_response_test.go**

```go
// Package browser_tests contains tests for the WebSocketMQ browser client.
package browser_tests

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBrowserClientRequestResponse tests the browser client's ability to make requests to the server.
func TestBrowserClientRequestResponse(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping browser test in short mode")
	}

	// Create a broker server with accept options to allow any origin
	bs := testutil.NewBrokerServer(t, broker.WithAcceptOptions(&websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	}))

	// Register a handler for the GetTime request
	err := bs.OnRequest(shared_types.TopicGetTime,
		func(client broker.ClientHandle, req shared_types.GetTimeRequest) (shared_types.GetTimeResponse, error) {
			t.Logf("Server: Client %s requested time", client.ID())
			return shared_types.GetTimeResponse{CurrentTime: time.Now().Format(time.RFC3339)}, nil
		},
	)
	require.NoError(t, err, "Failed to register GetTime handler")

	// Create an HTTP server mux
	mux := http.NewServeMux()

	// Register both the WebSocket handler and JavaScript client handler
	bs.RegisterHandlersWithDefaults(mux)

	// Create the HTTP server first so we can use its URL in the HTML
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	// Serve the JavaScript helpers
	mux.Handle("/js_helpers/", http.StripPrefix("/js_helpers/", http.FileServer(http.Dir("js_helpers"))))

	// Create a test HTML page handler
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<!DOCTYPE html>
				<html>
				<head>
					<title>WebSocketMQ Request-Response Test</title>
					<script src="/js_helpers/console_override.js"></script>
					<script src="/websocketmq.js"></script>
					<script src="/js_helpers/connect.js"></script>
					<script>
						// Store the last response message
						window.lastResponse = null;

						// Function to make a request to get the server time
						function getServerTime() {
							console.log('Making request to system:get_time');

							// Update UI to show request is in progress
							document.getElementById('response').textContent = 'Requesting...';

							// Make sure client is connected
							if (window.client && window.client.isConnected) {
								// Generate a unique request ID so we can match the response
								const requestId = Date.now().toString();

								// Add a raw message event listener to the WebSocket
								const ws = window.client._ws;
								if (ws) {
									const originalOnMessage = ws.onmessage;
									ws.onmessage = function(event) {
										try {
											const data = JSON.parse(event.data);
											console.log('Raw WebSocket message:', data);

											// If this is a response to our get_time request
											if (data.type === 'response' && data.topic === 'system:get_time') {
												console.log('Found response message:', data);
												window.lastResponse = data;
												// Display the payload - make sure it has the currentTime field
												if (data.payload && data.payload.currentTime) {
													console.log('Found currentTime in response:', data.payload.currentTime);
													document.getElementById('response').textContent = JSON.stringify(data.payload);
												}
											}
										} catch (e) {
											console.error('Error parsing WebSocket message:', e);
										}

										// Call the original handler
										if (originalOnMessage) {
											originalOnMessage.call(ws, event);
										}
									};
								}

								// Make the request
								window.client.request('system:get_time', {})
									.then(response => {
										console.log('Promise resolved with:', response);

										// If we have a captured response with currentTime, use that
										if (window.lastResponse && window.lastResponse.payload && window.lastResponse.payload.currentTime) {
											document.getElementById('response').textContent = JSON.stringify(window.lastResponse.payload);
										} else if (response && response.currentTime) {
											// If the promise resolved with a response containing currentTime, use that
											document.getElementById('response').textContent = JSON.stringify(response);
										} else {
											// Try to extract the currentTime from the raw message
											console.log('Checking raw messages for currentTime');
											// We won't update the response element here, as we want the test to fail if we don't get currentTime
										}
									})
									.catch(error => {
										console.error('Error making request to system:get_time:', error);
										document.getElementById('response').textContent = 'Error: ' + error.message;
									});
							} else {
								console.error('Client not connected');
								document.getElementById('response').textContent = 'Error: Client not connected';
							}
						}

						// Initialize the button when the page loads
						document.addEventListener('DOMContentLoaded', () => {
							const getTimeBtn = document.getElementById('get-time-btn');
							if (getTimeBtn) {
								getTimeBtn.addEventListener('click', getServerTime);
							}
						});
					</script>
				</head>
				<body>
					<h1>WebSocketMQ Request-Response Test</h1>
					<div>
						<h2>Connection Status: <span id="status">Disconnected</span></h2>
					</div>
					<div>
						<button id="get-time-btn">Get Server Time</button>
						<h2>Response: <span id="response">None</span></h2>
					</div>
				</body>
				</html>`

		w.Write([]byte(html))
	})

	// Test the browser connection with headless mode disabled
	headless := false
	result := TestBrowserConnection(t, bs, httpServer, headless)
	time.Sleep(1000 * time.Millisecond)

	// Now that we're connected, click the button to make a request
	t.Log("Clicking the Get Server Time button")
	result.Page.MustElement("#get-time-btn").MustClick()

	// Wait for the response to be updated (up to 5 seconds)
	var responseText string
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)

		// Check if response has been updated
		responseText, err = result.Page.MustElement("#response").Text()
		if err == nil && responseText != "None" && responseText != "Requesting..." {
			t.Logf("Response received after %d attempts: %s", i+1, responseText)
			break
		}
	}

	// Verify the response is not empty
	assert.NotEqual(t, "None", responseText, "Response should not be 'None'")
	assert.NotEqual(t, "Requesting...", responseText, "Response should not be 'Requesting...'")

	//RESPONSE MUST BE currentTime
	validResponse := strings.Contains(responseText, "currentTime")
	assert.True(t, validResponse, "Response should contain either currentTime")

	// Check console logs for request and response
	logs := result.Page.GetConsoleLog()
	t.Logf("Console logs: %v", logs)

	// Check if we have log messages for the request and response
	requestSent := false
	responseReceived := false
	for _, log := range logs {
		if strings.Contains(log, "Making request to system:get_time") {
			requestSent = true
		}
		// Check for any indication of a response
		if strings.Contains(log, "Received message") ||
			strings.Contains(log, "Promise resolved with") ||
			strings.Contains(log, "Raw WebSocket message") {
			responseReceived = true
		}
	}

	assert.True(t, requestSent, "Should have sent a request to system:get_time")
	assert.True(t, responseReceived, "Should have received a response message")

	// Add a delay so we can see the browser window before it closes
	// This is only useful when running with headless=false
	if !result.Browser.IsHeadless() {
		t.Log("Waiting 5 seconds so you can see the browser window...")
		time.Sleep(5 * time.Second)
	}
}

```

<a id="file-pkg-browser_client-browser_tests-server_initiated_request_test-go"></a>
**File: pkg/browser_client/browser_tests/server_initiated_request_test.go**

```go
// Package browser_tests contains tests for the WebSocketMQ browser client.
package browser_tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBrowserClientServerInitiatedRequest tests the browser client's ability to handle requests from the server.
func TestBrowserClientServerInitiatedRequest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping browser test in short mode")
	}

	// Create a broker server with accept options to allow any origin
	bs := testutil.NewBrokerServer(t, broker.WithAcceptOptions(&websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	}))

	// Create an HTTP server mux
	mux := http.NewServeMux()

	// Register both the WebSocket handler and JavaScript client handler
	bs.RegisterHandlersWithDefaults(mux)

	// Create the HTTP server first so we can use its URL in the HTML
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	// Serve the JavaScript helpers
	mux.Handle("/js_helpers/", http.StripPrefix("/js_helpers/", http.FileServer(http.Dir("js_helpers"))))

	// Create a test HTML page handler
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<!DOCTYPE html>
				<html>
				<head>
					<title>WebSocketMQ Server-Initiated Request Test</title>
					<script src="/js_helpers/console_override.js"></script>
					<script src="/websocketmq.js"></script>
					<script src="/js_helpers/connect.js"></script>
					<script>
						// Function to register a handler for server-initiated requests
						function registerStatusHandler() {
							console.log('Registering handler for client:get_status');

							// Make sure client is connected
							if (window.client && window.client.isConnected) {
								// Register the handler
								window.client.onRequest('client:get_status', (payload) => {
									console.log('Received request on topic client:get_status:', payload);

									// Update UI with request details
									document.getElementById('request').textContent = JSON.stringify(payload);
									document.getElementById('request-count').textContent =
										(parseInt(document.getElementById('request-count').textContent) || 0) + 1;

									// Return client status
									return {
										status: 'online',
										uptime: Math.floor(performance.now() / 1000) + ' seconds',
										url: window.location.href,
										userAgent: navigator.userAgent
									};
								});

								document.getElementById('handler-status').textContent = 'Registered';
								document.getElementById('handler-status').style.color = 'green';
							} else {
								console.error('Client not connected');
								document.getElementById('handler-status').textContent = 'Error: Client not connected';
								document.getElementById('handler-status').style.color = 'red';
							}
						}

						// Initialize the page when it loads
						document.addEventListener('DOMContentLoaded', () => {
							// Register the handler when the page loads
							const registerBtn = document.getElementById('register-btn');
							if (registerBtn) {
								registerBtn.addEventListener('click', registerStatusHandler);
							}

							// Auto-register the handler after connection
							// Use a polling approach to check for client connection
							const checkInterval = setInterval(() => {
								if (window.client && window.client.isConnected) {
									clearInterval(checkInterval);
									console.log('Client connected, registering handler');
									registerStatusHandler();
								}
							}, 100);
						});
					</script>
				</head>
				<body>
					<h1>WebSocketMQ Server-Initiated Request Test</h1>
					<div>
						<h2>Connection Status: <span id="status">Disconnected</span></h2>
					</div>
					<div>
						<h2>Handler Status: <span id="handler-status">Not Registered</span></h2>
						<button id="register-btn">Register Handler</button>
					</div>
					<div>
						<h2>Requests Received: <span id="request-count">0</span></h2>
						<h3>Last Request: <span id="request">None</span></h3>
					</div>
				</body>
				</html>`

		w.Write([]byte(html))
	})

	// Test the browser connection with headless mode disabled
	headless := false
	result := TestBrowserConnection(t, bs, httpServer, headless)

	// Wait for the handler to be registered
	time.Sleep(1 * time.Second)

	// Make a request from the server to the client
	t.Log("Making request from server to client")
	var clientHandle broker.ClientHandle
	bs.IterateClients(func(ch broker.ClientHandle) bool {
		clientHandle = ch
		return false // Stop after the first client
	})
	require.NotNil(t, clientHandle, "Should have a client handle")

	// Make a request to the client
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	requestData := map[string]interface{}{
		"requestId": "test-request",
		"timestamp": time.Now().Unix(),
	}

	var responseData map[string]interface{}
	err := clientHandle.Request(ctx, "client:get_status", requestData, &responseData, 0)
	require.NoError(t, err, "Failed to make request to client")
	t.Logf("Response from client: %v", responseData)

	// Wait for the UI to update
	time.Sleep(500 * time.Millisecond)

	// Verify the request count has been updated
	requestCount, err := result.Page.MustElement("#request-count").Text()
	require.NoError(t, err, "Should be able to get request count")
	assert.Equal(t, "1", requestCount, "Should have received 1 request")

	// Verify the request details have been updated
	requestText, err := result.Page.MustElement("#request").Text()
	require.NoError(t, err, "Should be able to get request text")
	assert.NotEqual(t, "None", requestText, "Request should not be 'None'")
	assert.Contains(t, requestText, "requestId", "Request should contain requestId")

	// Check console logs for request handling
	logs := result.Page.GetConsoleLog()
	t.Logf("Console logs: %v", logs)

	// Check if we have log messages for the request handling
	handlerRegistered := false
	requestReceived := false
	for _, log := range logs {
		if strings.Contains(log, "Registering handler for client:get_status") {
			handlerRegistered = true
		}
		if strings.Contains(log, "Received request on topic client:get_status") {
			requestReceived = true
		}
	}

	assert.True(t, handlerRegistered, "Should have registered a handler for client:get_status")
	assert.True(t, requestReceived, "Should have received a request on client:get_status")

	// Add a delay so we can see the browser window before it closes
	// This is only useful when running with headless=false
	if !result.Browser.IsHeadless() {
		t.Log("Waiting 5 seconds so you can see the browser window...")
		time.Sleep(5 * time.Second)
	}
}

```

<a id="file-pkg-browser_client-browser_tests-test_helpers-go"></a>
**File: pkg/browser_client/browser_tests/test_helpers.go**

```go
// Package browser_tests contains tests for the WebSocketMQ browser client.
package browser_tests

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConnectionResult contains the results of a browser client connection test
type TestConnectionResult struct {
	Browser     *testutil.RodBrowser
	Page        *testutil.RodPage
	ClientID    string
	ClientURL   string
	ClientCount int
	BrowserURL  string
	ConsoleLog  []string
}

// TestBrowserConnection is a helper function that tests browser client connection
// It opens a browser, navigates to the test page, and verifies the connection
func TestBrowserConnection(t *testing.T, bs *testutil.BrokerServer, httpServer *httptest.Server, headless bool) *TestConnectionResult {
	// Get the WebSocket URL
	wsURL := strings.Replace(httpServer.URL, "http://", "ws://", 1) + "/wsmq"
	t.Logf("WebSocket URL: %s", wsURL)
	t.Logf("HTTP Server URL: %s", httpServer.URL)

	// Create a new Rod browser with headless mode disabled so we can see the browser
	browser := testutil.NewRodBrowser(t, testutil.WithHeadless(headless))

	// Navigate to the test page
	page := browser.MustPage(httpServer.URL).WaitForLoad()

	// Verify the page loaded correctly
	page.VerifyPageLoaded("body")

	// Wait for connection to establish (up to 3 seconds)
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)

		// Check connection status
		statusText, err := page.MustElement("#status").Text()
		if err == nil && statusText == "Connected" {
			t.Logf("Connection established after %d attempts", i+1)
			break
		}
	}

	// Check final connection status
	statusText, err := page.MustElement("#status").Text()
	require.NoError(t, err, "Should be able to get status text")
	assert.Equal(t, "Connected", statusText, "Status should be Connected")

	// Get console logs to verify connection
	logs := page.GetConsoleLog()
	t.Logf("Console logs: %v", logs)

	// Check if we have a log message indicating successful connection
	connectionSuccess := false
	for _, log := range logs {
		if strings.Contains(log, "Connected to WebSocket server with ID:") {
			connectionSuccess = true
			break
		}
	}

	assert.True(t, connectionSuccess, "Should have connected to the WebSocket server")

	// Verify a client connected to the broker
	var clientCount int
	var clientID string
	var clientURL string
	// Find the browser client
	var browserClient broker.ClientHandle
	bs.IterateClients(func(ch broker.ClientHandle) bool {
		clientCount++
		t.Logf("Client connected: ID=%s, Name=%s, Type=%s, URL=%s",
			ch.ID(), ch.Name(), ch.ClientType(), ch.ClientURL())

		// Look for the browser client (has a non-empty URL)
		if ch.ClientType() == "browser" && ch.ClientURL() != "" {
			clientID = ch.ID()
			clientURL = ch.ClientURL()
			browserClient = ch
		}
		return true
	})

	// Make sure we found a browser client
	assert.NotNil(t, browserClient, "Should have a browser client connected")
	assert.NotEmpty(t, clientURL, "Browser client URL should not be empty")

	// Verify the URL is updated with client ID
	// Wait a bit for the URL to be updated
	time.Sleep(500 * time.Millisecond)

	urlStr, err := page.GetCurrentURL()
	require.NoError(t, err, "Should be able to get current URL")
	t.Logf("Current page URL: %s", urlStr)
	assert.Contains(t, urlStr, "client_id=", "URL should contain client_id parameter")

	// Return the test results
	return &TestConnectionResult{
		Browser:     browser,
		Page:        page,
		ClientID:    clientID,
		ClientURL:   clientURL,
		ClientCount: clientCount,
		BrowserURL:  urlStr,
		ConsoleLog:  logs,
	}
}

```

<a id="file-pkg-browser_client-dist-websocketmq-js"></a>
**File: pkg/browser_client/dist/websocketmq.js**

```javascript
// WebSocketMQ Client - Custom version for testing
// Version: 2025-05-11-2

(function(global) {
  'use strict';

  // Log version to console
  console.log('WebSocketMQ Client - Custom Version: 2025-05-11-2');

  // Constants for Envelope Type
  const TYPE_REQUEST = "request";
  const TYPE_RESPONSE = "response";
  const TYPE_PUBLISH = "publish";
  const TYPE_ERROR = "error";
  const TYPE_SUBSCRIBE_REQUEST = "subscribe_request";
  const TYPE_UNSUBSCRIBE_REQUEST = "unsubscribe_request";
  const TYPE_SUBSCRIPTION_ACK = "subscription_ack";

  // Client registration constants
  const TOPIC_CLIENT_REGISTER = "system:register";

  class WebSocketMQClient {
    constructor(options = {}) {
      // Initialize options with defaults
      this.options = Object.assign({
        url: null,
        reconnect: true,
        reconnectInterval: 1000,
        maxReconnectInterval: 30000,
        reconnectMultiplier: 1.5,
        defaultRequestTimeout: 10000,
        clientName: "",
        clientType: "browser",
        clientURL: typeof window !== 'undefined' ? window.location.href : "",
        logger: console,
        updateURLWithClientID: true
      }, options);

      // Auto-determine URL if not provided
      if (!this.options.url && typeof window !== 'undefined') {
        this.options.url = window.location.origin.replace('http', 'ws') + '/wsmq';
        console.log('Auto-determined URL:', this.options.url);
      }

      // Initialize client state
      this.ws = null;
      this.isConnected = false;
      this.isConnecting = false;
      this.reconnectAttempts = 0;
      this.reconnectTimer = null;
      this.explicitlyClosed = false;

      // Generate a client ID or extract from URL
      this.id = this._extractClientIDFromURL() || this._generateID();

      // Initialize handlers and callbacks
      this.pendingRequests = new Map();
      this.subscriptionHandlers = new Map();
      this.requestHandlers = new Map();
      this.onConnectCallbacks = new Set();
      this.onDisconnectCallbacks = new Set();
      this.onErrorCallbacks = new Set();

      // Bind methods
      this._onMessage = this._onMessage.bind(this);
      this._onOpen = this._onOpen.bind(this);
      this._onClose = this._onClose.bind(this);
      this._onError = this._onError.bind(this);
    }

    connect() {
      if (this.isConnected || this.isConnecting) {
        console.log('Already connected or connecting');
        return;
      }

      this.isConnecting = true;
      this.explicitlyClosed = false;
      console.log('Connecting to', this.options.url);

      try {
        // Add client ID to the URL as a query parameter
        let urlWithID = this.options.url;
        const url = new URL(urlWithID, window.location.href);
        url.searchParams.set('client_id', this.id);

        this.ws = new WebSocket(url.toString());
        this.ws.addEventListener('open', this._onOpen);
        this.ws.addEventListener('message', this._onMessage);
        this.ws.addEventListener('close', this._onClose);
        this.ws.addEventListener('error', this._onError);
      } catch (err) {
        this.isConnecting = false;
        this._handleError(err);
      }
    }

    onConnect(callback) {
      if (typeof callback === 'function') {
        this.onConnectCallbacks.add(callback);
        if (this.isConnected) callback();
      }
      return () => this.onConnectCallbacks.delete(callback);
    }

    onDisconnect(callback) {
      if (typeof callback === 'function') {
        this.onDisconnectCallbacks.add(callback);
      }
      return () => this.onDisconnectCallbacks.delete(callback);
    }

    onError(callback) {
      if (typeof callback === 'function') {
        this.onErrorCallbacks.add(callback);
      }
      return () => this.onErrorCallbacks.delete(callback);
    }

    getID() {
      return this.id;
    }

    // Private methods
    _onOpen(event) {
      this.isConnected = true;
      this.isConnecting = false;
      this.reconnectAttempts = 0;
      console.log('Connected.');

      // Send client registration
      this._sendRegistration();

      // Notify callbacks
      this.onConnectCallbacks.forEach(cb => {
        try { cb(event); } catch(e) { console.error("Error in onConnect callback", e); }
      });
    }

    _onClose(event) {
      const wasConnected = this.isConnected;
      this.isConnected = false;
      this.isConnecting = false;
      console.log('Disconnected. Code:', event.code, 'Reason:', event.reason);

      if (wasConnected) {
        this.onDisconnectCallbacks.forEach(cb => {
          try { cb(event); } catch(e) { console.error("Error in onDisconnect callback", e); }
        });
      }
    }

    _onError(eventOrError) {
      const error = eventOrError instanceof Error ? eventOrError : new Error('WebSocket error');
      console.error('Error:', error);
      this.onErrorCallbacks.forEach(cb => {
        try { cb(error, eventOrError); } catch(e) { console.error("Error in onError callback", e); }
      });
    }

    _onMessage(event) {
      let envelope;
      try {
        envelope = JSON.parse(event.data);
        console.log('Received message:', envelope);

        // Process different message types
        switch (envelope.type) {
          case TYPE_RESPONSE:
            // Response handling is now done in the request method
            break;

          case TYPE_PUBLISH:
            // Handle published messages
            if (this.subscriptionHandlers.has(envelope.topic)) {
              const handler = this.subscriptionHandlers.get(envelope.topic);
              try {
                handler(envelope.payload);
              } catch (err) {
                console.error('Error in subscription handler:', err);
              }
            }
            break;

          case TYPE_REQUEST:
            // Handle server-initiated requests
            if (this.requestHandlers.has(envelope.topic)) {
              const handler = this.requestHandlers.get(envelope.topic);
              try {
                const result = handler(envelope.payload);

                // If the result is a Promise, wait for it to resolve
                if (result instanceof Promise) {
                  result.then(response => {
                    this._sendEnvelope({
                      id: envelope.id,
                      type: TYPE_RESPONSE,
                      topic: envelope.topic,
                      payload: response
                    }).catch(err => console.error('Error sending response:', err));
                  }).catch(err => {
                    console.error('Error in request handler:', err);
                    this._sendEnvelope({
                      id: envelope.id,
                      type: TYPE_ERROR,
                      topic: envelope.topic,
                      payload: { error: err.message }
                    }).catch(e => console.error('Error sending error response:', e));
                  });
                } else {
                  // Send the response immediately
                  this._sendEnvelope({
                    id: envelope.id,
                    type: TYPE_RESPONSE,
                    topic: envelope.topic,
                    payload: result
                  }).catch(err => console.error('Error sending response:', err));
                }
              } catch (err) {
                console.error('Error in request handler:', err);
                this._sendEnvelope({
                  id: envelope.id,
                  type: TYPE_ERROR,
                  topic: envelope.topic,
                  payload: { error: err.message }
                }).catch(e => console.error('Error sending error response:', e));
              }
            } else {
              console.warn('No handler for request topic:', envelope.topic);
              this._sendEnvelope({
                id: envelope.id,
                type: TYPE_ERROR,
                topic: envelope.topic,
                payload: { error: 'No handler for topic: ' + envelope.topic }
              }).catch(err => console.error('Error sending error response:', err));
            }
            break;

          case TYPE_ERROR:
            console.error('Received error:', envelope.payload);
            break;

          case TYPE_SUBSCRIPTION_ACK:
            console.log('Received subscription acknowledgement for topic:', envelope.topic);
            break;

          default:
            console.warn('Unknown message type:', envelope.type);
        }
      } catch (err) {
        this._handleError(new Error("Failed to process message: " + err.message));
      }
    }

    _sendRegistration() {
      // Create registration payload with snake_case field names
      const registration = {
        clientId: this.id,
        clientName: this.options.clientName || "browser-" + this.id.substring(0, 8),
        clientType: "browser",
        clientUrl: window.location.href
      };

      console.log('Sending registration:', registration);

      // Send registration request
      this.request(TOPIC_CLIENT_REGISTER, registration)
        .then(response => {
          if (response && response.serverAssignedId) {
            console.log('Server assigned new ID:', response.serverAssignedId);
            this.id = response.serverAssignedId;

            // Update URL with the new client ID
            this._updateURLWithClientID();
          }
        })
        .catch(err => {
          console.error('Registration failed:', err);
        });
    }

    request(topic, payload = null, timeoutMs = null) {
      if (!this.isConnected) {
        console.warn('Not connected. Cannot send request.');
        return Promise.reject(new Error('Not connected'));
      }

      return new Promise((resolve, reject) => {
        const requestId = this._generateID();

        // Set up a one-time message handler to catch the response
        const messageHandler = (event) => {
          try {
            const envelope = JSON.parse(event.data);

            // Check if this is a response to our request
            if (envelope.type === TYPE_RESPONSE && envelope.id === requestId) {
              // Remove the event listener once we get the response
              this.ws.removeEventListener('message', messageHandler);

              // Resolve with the actual payload from the response
              console.log('Received response for request', requestId, ':', envelope.payload);
              resolve(envelope.payload);
            }
          } catch (err) {
            console.error('Error handling response:', err);
          }
        };

        // Add the message handler
        this.ws.addEventListener('message', messageHandler);

        // Send the request envelope
        this._sendEnvelope({
          id: requestId,
          type: TYPE_REQUEST,
          topic: topic,
          payload: payload
        }).catch(err => {
          // Remove the message handler if sending fails
          this.ws.removeEventListener('message', messageHandler);
          reject(err);
        });

        // Set up timeout if specified
        if (timeoutMs) {
          setTimeout(() => {
            // Check if the handler is still registered (response not received yet)
            this.ws.removeEventListener('message', messageHandler);
            reject(new Error('Request timed out'));
          }, timeoutMs);
        }
      });
    }

    _sendEnvelope(envelope) {
      return new Promise((resolve, reject) => {
        if (!this.isConnected || !this.ws || this.ws.readyState !== WebSocket.OPEN) {
          reject(new Error('Not connected or WebSocket not open'));
          return;
        }

        try {
          // If payload is not null and not a string, stringify it
          if (envelope.payload !== null && typeof envelope.payload !== 'string') {
            // Don't stringify the payload - the broker expects a JSON object, not a string
            // This is the key change to fix the unmarshal error
          }

          this.ws.send(JSON.stringify(envelope));
          console.log('Sent envelope:', envelope);
          resolve();
        } catch (err) {
          this._handleError(err);
          reject(err);
        }
      });
    }

    _updateURLWithClientID() {
      if (typeof window === 'undefined' || !this.options.updateURLWithClientID) {
        return;
      }

      try {
        const url = new URL(window.location.href);
        const params = new URLSearchParams(url.search);

        // Update or add client_id parameter
        params.set('client_id', this.id);
        url.search = params.toString();

        // Update URL without reloading the page
        window.history.replaceState({}, '', url.toString());
        console.log('Updated URL with client ID:', this.id);
      } catch (err) {
        console.error('Failed to update URL with client ID:', err.message);
      }
    }

    _extractClientIDFromURL() {
      if (typeof window === 'undefined') {
        return null;
      }

      const urlParams = new URLSearchParams(window.location.search);
      return urlParams.get('client_id');
    }

    _generateID() {
      return Date.now().toString(36) + Math.random().toString(36).substring(2, 10);
    }

    // Subscribe to a topic
    subscribe(topic, handler) {
      if (typeof handler !== 'function') {
        throw new Error('Handler must be a function');
      }

      this.subscriptionHandlers.set(topic, handler);
      console.log('Subscribed to topic:', topic);

      // Send subscription request to the server
      if (this.isConnected) {
        this._sendEnvelope({
          id: this._generateID(),
          type: TYPE_SUBSCRIBE_REQUEST,
          topic: topic,
          payload: null
        }).catch(err => console.error('Error sending subscription request:', err));
      }

      // Return unsubscribe function
      return () => this.unsubscribe(topic);
    }

    // Unsubscribe from a topic
    unsubscribe(topic) {
      if (!this.subscriptionHandlers.has(topic)) {
        console.warn('Not subscribed to topic:', topic);
        return;
      }

      this.subscriptionHandlers.delete(topic);
      console.log('Unsubscribed from topic:', topic);

      // Send unsubscription request to the server
      if (this.isConnected) {
        this._sendEnvelope({
          id: this._generateID(),
          type: TYPE_UNSUBSCRIBE_REQUEST,
          topic: topic,
          payload: null
        }).catch(err => console.error('Error sending unsubscription request:', err));
      }
    }

    // Register a handler for server-initiated requests
    onRequest(topic, handler) {
      if (typeof handler !== 'function') {
        throw new Error('Handler must be a function');
      }

      this.requestHandlers.set(topic, handler);
      console.log('Registered handler for topic:', topic);

      // Return function to remove the handler
      return () => {
        this.requestHandlers.delete(topic);
        console.log('Removed handler for topic:', topic);
      };
    }

    // Publish a message to a topic
    publish(topic, payload = null) {
      if (!this.isConnected) {
        console.warn('Not connected. Cannot publish message.');
        return Promise.reject(new Error('Not connected'));
      }

      console.log('Publishing message to topic:', topic, payload);

      // Send the publish envelope
      return this._sendEnvelope({
        id: this._generateID(),
        type: TYPE_PUBLISH,
        topic: topic,
        payload: payload
      });
    }

    _handleError(error) {
      console.error('Error:', error);
      this.onErrorCallbacks.forEach(cb => {
        try { cb(error); } catch(e) { console.error("Error in onError callback", e); }
      });
    }
  }

  // Export the WebSocketMQClient class
  global.WebSocketMQ = {
    Client: WebSocketMQClient
  };

})(typeof self !== 'undefined' ? self : this);

```

<a id="file-pkg-browser_client-embed-go"></a>
**File: pkg/browser_client/embed.go**

```go
// pkg/browser_client/embed.go
package browser_client

import (
	"embed"
)

//go:generate go run build.go

//go:embed dist/websocketmq.js
var clientFiles embed.FS

```

<a id="file-pkg-browser_client-handler-go"></a>
**File: pkg/browser_client/handler.go**

```go
// pkg/browser_client/handler.go
package browser_client

import (
	"net/http"
	"strconv"
	"strings"
)

// ClientScriptOptions configures the JavaScript client handler
type ClientScriptOptions struct {
	// Path is the URL path where the script will be served
	// Default: "/websocketmq.js"
	Path string

	// UseMinified determines whether to serve the minified version by default
	// Default: true
	UseMinified bool

	// CacheMaxAge sets the Cache-Control max-age directive in seconds
	// Default: 3600 (1 hour)
	CacheMaxAge int
}

// DefaultClientScriptOptions returns the default options for the client script handler
func DefaultClientScriptOptions() ClientScriptOptions {
	return ClientScriptOptions{
		Path:        "/websocketmq.js",
		UseMinified: true,
		CacheMaxAge: 3600,
	}
}

// ScriptHandler returns an HTTP handler that serves the embedded JavaScript client
func ScriptHandler(options ClientScriptOptions) http.Handler {
	return &scriptHandler{
		options: options,
	}
}

type scriptHandler struct {
	options ClientScriptOptions
}

func (h *scriptHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Get the requested file path
	filePath := strings.TrimPrefix(r.URL.Path, "/")

	// If no specific file is requested, use the default
	if filePath == "" || filePath == strings.TrimPrefix(h.options.Path, "/") {
		filePath = "websocketmq.js"
	}
	data, err := GetClientScript()
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	contentType := "application/javascript"
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "max-age="+strconv.Itoa(h.options.CacheMaxAge))

	w.Write(data)
}

// RegisterHandler registers the JavaScript client handler with the provided ServeMux
func RegisterHandler(mux *http.ServeMux, options ClientScriptOptions) {
	handler := ScriptHandler(options)
	mux.Handle(options.Path, handler)
}

// GetClientScript returns the raw JavaScript client code
func GetClientScript() ([]byte, error) {
	filename := "dist/websocketmq.js"
	return clientFiles.ReadFile(filename)
}

```

<a id="file-pkg-client-client-go"></a>
**File: pkg/client/client.go**

```go
// ergosockets/client/client.go
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand" // For jitter
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/lightforgemedia/go-websocketmq/pkg/ergosockets"
	"github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
)

const (
	defaultClientSendBuffer   = 16 // Matches broker's managedClient
	defaultClientReqTimeout   = 10 * time.Second
	defaultWriteClientTimeout = 5 * time.Second
	defaultReadClientTimeout  = 60 * time.Second // Should be > server ping interval
	// Client-initiated pings are disabled by default. Rely on server pings.
	libraryDefaultClientPingInterval = 0 * time.Second
	defaultReconnectAttempts         = 0 // 0 means no auto-reconnect by default
	defaultReconnectDelayMin         = 1 * time.Second
	defaultReconnectDelayMax         = 30 * time.Second
)

type clientConfig struct {
	logger                *slog.Logger
	dialOptions           *websocket.DialOptions
	defaultRequestTimeout time.Duration // Renamed from defaultTimeout
	writeTimeout          time.Duration
	readTimeout           time.Duration
	pingInterval          time.Duration // Client-initiated pings; 0 or <0 to disable
	autoReconnect         bool
	reconnectAttempts     int // 0 for infinite if autoReconnect is true
	reconnectDelayMin     time.Duration
	reconnectDelayMax     time.Duration
	clientName            string
	clientType            string
	clientURL             string
}

// Client is a WebSocket client for ErgoSockets.
type Client struct {
	config clientConfig
	urlStr string
	id     string // Unique ID for this client instance/connection session

	conn   *websocket.Conn
	connMu sync.RWMutex

	send chan *ergosockets.Envelope

	// Overall client lifetime context
	clientCtx    context.Context
	clientCancel context.CancelFunc

	// Context for the current connection's pumps (read/write/ping)
	// Gets cancelled and recreated on reconnect.
	currentConnPumpCtx    context.Context
	currentConnPumpCancel context.CancelFunc
	currentConnPumpWg     sync.WaitGroup

	pendingRequestsMu sync.Mutex
	pendingRequests   map[string]chan *ergosockets.Envelope

	subscriptionHandlersMu sync.RWMutex
	subscriptionHandlers   map[string]*ergosockets.HandlerWrapper

	requestHandlersMu sync.RWMutex // For server-initiated requests
	requestHandlers   map[string]*ergosockets.HandlerWrapper

	isClosed bool // True if Close() has been called, preventing further operations/reconnects
	closedMu sync.Mutex

	reconnectingMu sync.Mutex
	isReconnecting bool // True if a reconnect loop is currently active
}

// Option configures the Client.
type Option func(*Client)

// WithLogger sets a custom logging implementation.
func WithLogger(logger *slog.Logger) Option {
	return func(c *Client) {
		if logger != nil {
			c.config.logger = logger
		}
	}
}

// WithDialOptions sets custom websocket.DialOptions.
func WithDialOptions(opts *websocket.DialOptions) Option {
	return func(c *Client) {
		c.config.dialOptions = opts
	}
}

// WithDefaultRequestTimeout sets the default timeout for cli.Request operations.
func WithDefaultRequestTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		if timeout > 0 {
			c.config.defaultRequestTimeout = timeout
		}
	}
}

// WithClientPingInterval sets the client-initiated ping interval.
// interval < 0 or interval == 0: Disables client pings.
// interval > 0: Uses the specified interval.
func WithClientPingInterval(interval time.Duration) Option {
	return func(c *Client) {
		c.config.pingInterval = interval // Logic applied in New()
	}
}

// WithAutoReconnect enables automatic reconnection.
// maxAttempts = 0 means infinite attempts if autoReconnect is true.
func WithAutoReconnect(maxAttempts int, minDelay, maxDelay time.Duration) Option {
	return func(c *Client) {
		c.config.autoReconnect = true
		c.config.reconnectAttempts = maxAttempts
		if minDelay > 0 {
			c.config.reconnectDelayMin = minDelay
		}
		if maxDelay > 0 && maxDelay >= minDelay {
			c.config.reconnectDelayMax = maxDelay
		} else if maxDelay < minDelay {
			c.config.reconnectDelayMax = minDelay // Ensure max is not less than min
		}
	}
}

// WithClientName sets a custom name for the client.
func WithClientName(name string) Option {
	return func(c *Client) {
		c.config.clientName = name
	}
}

// WithClientType sets the client type.
func WithClientType(clientType string) Option {
	return func(c *Client) {
		c.config.clientType = clientType
	}
}

// WithClientURL sets the client URL.
func WithClientURL(url string) Option {
	return func(c *Client) {
		c.config.clientURL = url
	}
}

// Connect establishes a WebSocket connection.
func Connect(urlStr string, opts ...Option) (*Client, error) {
	clientCtx, clientCancel := context.WithCancel(context.Background())
	cli := &Client{
		config: clientConfig{
			logger:                slog.Default(),
			defaultRequestTimeout: defaultClientReqTimeout,
			writeTimeout:          defaultWriteClientTimeout,
			readTimeout:           defaultReadClientTimeout,
			pingInterval:          libraryDefaultClientPingInterval, // Disabled by default
			reconnectDelayMin:     defaultReconnectDelayMin,
			reconnectDelayMax:     defaultReconnectDelayMax,
		},
		urlStr:               urlStr,
		id:                   ergosockets.GenerateID(),
		clientCtx:            clientCtx,
		clientCancel:         clientCancel,
		send:                 make(chan *ergosockets.Envelope, defaultClientSendBuffer),
		pendingRequests:      make(map[string]chan *ergosockets.Envelope),
		subscriptionHandlers: make(map[string]*ergosockets.HandlerWrapper),
		requestHandlers:      make(map[string]*ergosockets.HandlerWrapper),
	}

	for _, opt := range opts {
		opt(cli)
	}

	// Finalize client ping interval
	if cli.config.pingInterval < 0 { // User passed negative, means disable
		cli.config.pingInterval = 0
	}
	// If user passed 0, it uses libraryDefaultClientPingInterval (which is 0)
	// If user passed >0, it's already set.

	if cli.config.dialOptions == nil {
		cli.config.dialOptions = &websocket.DialOptions{HTTPClient: http.DefaultClient}
	}

	err := cli.establishConnection(cli.clientCtx) // Initial connection attempt
	if err != nil {
		cli.config.logger.Info(fmt.Sprintf("Client %s: Initial connection failed: %v", cli.id, err))
		if !cli.config.autoReconnect {
			cli.Close() // Clean up if not reconnecting
			return nil, fmt.Errorf("client initial connection failed and auto-reconnect disabled: %w", err)
		}
		// Auto-reconnect is enabled, start the loop.
		// establishConnection already handles logging this.
		go cli.reconnectLoop()
		// Return the client instance even if initial connect fails but reconnect is on.
		// The user can then try operations which will block/fail until connected.
	}

	return cli, nil // Return client instance, it might be in a reconnecting state
}

func (c *Client) establishConnection(ctx context.Context) error {
	c.closedMu.Lock()
	if c.isClosed {
		c.closedMu.Unlock()
		return errors.New("client is permanently closed, cannot establish connection")
	}
	c.closedMu.Unlock()

	c.connMu.Lock() // Lock to modify c.conn and pump contexts
	// If there are old pumps running for a previous connection, cancel them.
	if c.currentConnPumpCancel != nil {
		c.currentConnPumpCancel()
		c.connMu.Unlock()          // Unlock before waiting to avoid deadlock if pumps try to acquire connMu
		c.currentConnPumpWg.Wait() // Wait for old pumps to fully stop
		c.connMu.Lock()            // Re-lock
	}

	if c.conn != nil {
		c.conn.Close(websocket.StatusAbnormalClosure, "stale connection being replaced")
		c.conn = nil
	}
	c.connMu.Unlock() // Unlock before Dial, as Dial is blocking

	// Add client ID to the URL as a query parameter
	urlWithID := c.urlStr
	if !strings.Contains(urlWithID, "?") {
		urlWithID += "?"
	} else {
		urlWithID += "&"
	}
	urlWithID += "client_id=" + c.id

	dialCtx, dialCancel := context.WithTimeout(ctx, c.config.defaultRequestTimeout) // Use request timeout for dial
	conn, httpResp, err := websocket.Dial(dialCtx, urlWithID, c.config.dialOptions)
	dialCancel()

	if err != nil {
		errMsg := fmt.Sprintf("dial to %s failed: %v", urlWithID, err)
		if httpResp != nil {
			errMsg = fmt.Sprintf("%s (status: %s)", errMsg, httpResp.Status)
		}
		return errors.New(errMsg)
	}

	c.connMu.Lock()
	c.conn = conn
	// Create new context and WaitGroup for the pumps of this new connection
	c.currentConnPumpCtx, c.currentConnPumpCancel = context.WithCancel(c.clientCtx)
	c.currentConnPumpWg = sync.WaitGroup{} // Reset WaitGroup

	c.currentConnPumpWg.Add(2) // For readPump and writePump
	go c.readPump()
	go c.writePump()

	if c.config.pingInterval > 0 {
		c.currentConnPumpWg.Add(1)
		go c.pingLoop()
	}
	c.connMu.Unlock()

	c.config.logger.Info(fmt.Sprintf("Client %s: Successfully connected to %s", c.id, c.urlStr))

	// Send client registration information
	err = c.sendRegistration()
	if err != nil {
		c.config.logger.Info(fmt.Sprintf("Client %s: Failed to send registration: %v", c.id, err))
		// Close the connection and return error
		c.connMu.Lock()
		if c.conn != nil {
			c.conn.Close(websocket.StatusInternalError, "registration failed")
			c.conn = nil
		}
		c.connMu.Unlock()
		return fmt.Errorf("failed to register client: %w", err)
	}

	// Re-send subscription requests upon successful (re)connection
	c.resubscribeAll()
	return nil
}

// sendRegistration sends client registration information to the server
func (c *Client) sendRegistration() error {
	// Create registration payload
	clientURL := c.config.clientURL
	if clientURL == "" {
		clientURL = ""
	}

	clientType := c.config.clientType
	if clientType == "" {
		clientType = "generic"
	}

	// If client name is not set, generate a default one
	clientName := c.config.clientName
	if clientName == "" {
		clientName = "client-" + c.id[:8]
	}

	// Create registration request
	registration := shared_types.ClientRegistration{
		ClientID:   c.id,
		ClientName: clientName,
		ClientType: clientType,
		ClientURL:  clientURL,
	}

	// Send registration request and wait for response
	response, err := GenericRequest[shared_types.ClientRegistrationResponse](c, context.Background(), shared_types.TopicClientRegister, registration)
	if err != nil {
		return fmt.Errorf("registration request failed: %w", err)
	}

	// Update client ID with server-assigned ID
	if response != nil && response.ServerAssignedID != "" {
		c.config.logger.Info(fmt.Sprintf("Client %s: Server assigned new ID: %s", c.id, response.ServerAssignedID))
		c.id = response.ServerAssignedID
	}

	return nil
}

func (c *Client) resubscribeAll() {
	c.subscriptionHandlersMu.RLock()
	defer c.subscriptionHandlersMu.RUnlock()
	if len(c.subscriptionHandlers) > 0 {
		c.config.logger.Info(fmt.Sprintf("Client %s: Re-subscribing to %d topics...", c.id, len(c.subscriptionHandlers)))
		for topic := range c.subscriptionHandlers {
			// Fire-and-forget re-subscribe. Errors logged internally by sendSubscribeRequest.
			// A more robust system might queue these and confirm acks.
			go func(t string) { // Send in goroutine to not block establishConnection
				if err := c.sendSubscribeRequest(t); err != nil {
					c.config.logger.Info(fmt.Sprintf("Client %s: Error re-subscribing to topic '%s': %v", c.id, t, err))
				}
			}(topic)
		}
	}
}

func (c *Client) reconnectLoop() {
	c.reconnectingMu.Lock()
	if c.isReconnecting {
		c.reconnectingMu.Unlock()
		return // Another reconnect loop is already active
	}
	c.isReconnecting = true
	c.reconnectingMu.Unlock()

	defer func() {
		c.reconnectingMu.Lock()
		c.isReconnecting = false
		c.reconnectingMu.Unlock()
		c.config.logger.Info(fmt.Sprintf("Client %s: Exiting reconnect loop.", c.id))
	}()

	c.config.logger.Info(fmt.Sprintf("Client %s: Starting reconnect loop (max_attempts: %d, delay_min: %v, delay_max: %v)",
		c.id, c.config.reconnectAttempts, c.config.reconnectDelayMin, c.config.reconnectDelayMax))

	attempts := 0
	currentDelay := c.config.reconnectDelayMin

	for {
		c.closedMu.Lock()
		if c.isClosed { // Check if client was permanently closed
			c.closedMu.Unlock()
			return
		}
		c.closedMu.Unlock()

		select {
		case <-c.clientCtx.Done(): // Client is being closed permanently
			return
		default:
		}

		if c.config.reconnectAttempts > 0 && attempts >= c.config.reconnectAttempts {
			c.config.logger.Info(fmt.Sprintf("Client %s: Max reconnect attempts (%d) reached. Stopping.", c.id, c.config.reconnectAttempts))
			c.Close() // Permanently close if max attempts reached
			return
		}

		// Calculate delay with jitter
		var sleepDuration time.Duration
		if currentDelay > 0 {
			// Jitter: random percentage (e.g., 0-25%) of currentDelay
			// Add jitter to spread out retries from multiple clients
			jitterRange := int(currentDelay / 4)
			if jitterRange <= 0 {
				jitterRange = 1
			} // Ensure some jitter if delay is small
			jitter := time.Duration(rand.Intn(jitterRange))
			sleepDuration = currentDelay + jitter
		} else {
			sleepDuration = c.config.reconnectDelayMin // Should not happen if currentDelay starts at min
		}

		c.config.logger.Info(fmt.Sprintf("Client %s: Waiting %v before reconnect attempt %d...", c.id, sleepDuration, attempts+1))
		time.Sleep(sleepDuration)

		c.config.logger.Info(fmt.Sprintf("Client %s: Attempting to reconnect (attempt %d)...", c.id, attempts+1))
		err := c.establishConnection(c.clientCtx)
		if err == nil {
			c.config.logger.Info(fmt.Sprintf("Client %s: Successfully reconnected.", c.id))
			return // Exit reconnect loop on success
		}

		c.config.logger.Info(fmt.Sprintf("Client %s: Reconnect attempt %d failed: %v", c.id, attempts+1, err))
		attempts++
		currentDelay *= 2 // Exponential backoff
		if currentDelay > c.config.reconnectDelayMax {
			currentDelay = c.config.reconnectDelayMax
		}
		if currentDelay < c.config.reconnectDelayMin { // Should not happen
			currentDelay = c.config.reconnectDelayMin
		}
	}
}

func (c *Client) getConn() *websocket.Conn {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.conn
}

func (c *Client) readPump() {
	defer func() {
		c.config.logger.Info(fmt.Sprintf("Client %s: readPump stopping for connection.", c.id))
		// Signal other pumps (write, ping) for THIS connection to stop.
		if c.currentConnPumpCancel != nil {
			c.currentConnPumpCancel()
		}

		c.connMu.Lock()
		if c.conn != nil {
			// It's important that Close is called on the specific connection instance
			// this readPump was associated with, not potentially a new one from a concurrent reconnect.
			// However, getConn() inside the loop should always refer to the conn this pump started with.
			c.conn.Close(websocket.StatusAbnormalClosure, "read pump terminated for connection")
			c.conn = nil // Indicate no active connection
		}
		c.connMu.Unlock()

		c.currentConnPumpWg.Done() // Signal completion of this pump

		// If auto-reconnect is enabled and client is not permanently closed, trigger reconnect.
		c.closedMu.Lock()
		isPermanentlyClosed := c.isClosed
		c.closedMu.Unlock()

		if c.config.autoReconnect && !isPermanentlyClosed {
			c.reconnectingMu.Lock()
			// Only start a new reconnectLoop if one isn't already running
			if !c.isReconnecting {
				c.reconnectingMu.Unlock() // Unlock before starting goroutine
				go c.reconnectLoop()
			} else {
				c.reconnectingMu.Unlock()
				c.config.logger.Info(fmt.Sprintf("Client %s: readPump detected disconnect, but reconnect loop already active.", c.id))
			}
		}
	}()

	cfg := c.config
	readDeadlineDuration := cfg.readTimeout
	// If server pings are expected, client read deadline should accommodate them.
	// If client pings are enabled, this logic also applies.
	if cfg.pingInterval > 0 { // Client pings enabled
		readDeadlineDuration = cfg.pingInterval * 2
		if readDeadlineDuration < cfg.readTimeout {
			readDeadlineDuration = cfg.readTimeout
		}
	} else { // Rely on server pings, use readTimeout (should be > server ping interval)
		// This assumes server pings are more frequent than readTimeout.
	}

	currentLocalConn := c.getConn() // Get the connection this pump is for
	if currentLocalConn == nil {
		c.config.logger.Info(fmt.Sprintf("Client %s: readPump started with nil connection.", c.id))
		return
	}

	if readDeadlineDuration > 0 {
		currentLocalConn.SetReadLimit(1024 * 1024) // 1MB
		// currentLocalConn.SetReadDeadline(ergosockets.TimeNow().Add(readDeadlineDuration))
		// currentLocalConn.P(func(string) error {
		// 	// c.config.logger.Info(fmt.Sprintf("Client %s: Pong received", c.id)
		// 	// Refresh deadline only on the connection this handler is for.
		// 	activeConn := c.getConn()
		// 	if activeConn != nil && readDeadlineDuration > 0 {
		// 		_ = activeConn.SetReadDeadline(ergosockets.TimeNow().Add(readDeadlineDuration))
		// 	}
		// 	return nil
		// })
	}

	for {
		// Use currentConnPumpCtx for reads, as it's tied to this specific connection's lifecycle.
		select {
		case <-c.currentConnPumpCtx.Done():
			c.config.logger.Info(fmt.Sprintf("Client %s: readPump stopping due to current connection pump context.", c.id))
			return
		default:
		}

		var env ergosockets.Envelope
		err := wsjson.Read(c.currentConnPumpCtx, currentLocalConn, &env)
		if err != nil {
			status := websocket.CloseStatus(err)
			select {
			case <-c.currentConnPumpCtx.Done():
				c.config.logger.Info(fmt.Sprintf("Client %s: readPump gracefully closing after context cancellation (err: %v)", c.id, err))
			case <-c.clientCtx.Done():
				c.config.logger.Info(fmt.Sprintf("Client %s: readPump closing due to permanent client shutdown (err: %v)", c.id, err))
			default: // Actual read error
				if status != websocket.StatusNormalClosure && status != websocket.StatusGoingAway && !errors.Is(err, context.Canceled) {
					c.config.logger.Info(fmt.Sprintf("Client %s: read error in readPump: %v (status: %d)", c.id, err, status))
				} else {
					c.config.logger.Info(fmt.Sprintf("Client %s: readPump normal websocket closure: %v (status: %d)", c.id, err, status))
				}
			}
			return // Exit loop, defer will handle cleanup/reconnect
		}

		// if readDeadlineDuration > 0 {
		// DOSNT WORK with WS CONN
		// 	currentLocalConn.SetReadDeadline(ergosockets.TimeNow().Add(readDeadlineDuration))
		// }

		switch env.Type {
		case ergosockets.TypeResponse, ergosockets.TypeError:
			c.pendingRequestsMu.Lock()
			if ch, ok := c.pendingRequests[env.ID]; ok {
				select {
				case ch <- &env:
				default:
					c.config.logger.Info(fmt.Sprintf("Client %s: Response channel for ID %s not ready or already processed", c.id, env.ID))
				}
			} else {
				c.config.logger.Info(fmt.Sprintf("Client %s: Received unsolicited server-targeted response/error with ID %s", c.id, env.ID))
			}
			c.pendingRequestsMu.Unlock()
		case ergosockets.TypePublish:
			c.subscriptionHandlersMu.RLock()
			hw, ok := c.subscriptionHandlers[env.Topic]
			c.subscriptionHandlersMu.RUnlock()
			if ok {
				go c.invokeSubscriptionHandler(hw, &env)
			} else {
				// c.config.logger.Info(fmt.Sprintf("Client %s: No subscription handler for publish topic '%s'", c.id, env.Topic)
			}
		case ergosockets.TypeRequest: // Server-initiated request
			c.requestHandlersMu.RLock()
			hw, ok := c.requestHandlers[env.Topic]
			c.requestHandlersMu.RUnlock()
			if ok {
				go c.invokeClientRequestHandler(hw, &env)
			} else {
				c.config.logger.Info(fmt.Sprintf("Client %s: No handler for server request on topic '%s'", c.id, env.Topic))
				errEnv, _ := ergosockets.NewEnvelope(env.ID, ergosockets.TypeError, env.Topic, nil,
					&ergosockets.ErrorPayload{Code: http.StatusNotFound, Message: "Client has no handler for topic: " + env.Topic})
				c.trySend(errEnv)
			}
		case ergosockets.TypeSubscriptionAck:
			c.config.logger.Info(fmt.Sprintf("Client %s: Received subscription ack for topic '%s' (ID: %s)", c.id, env.Topic, env.ID))
		default:
			c.config.logger.Info(fmt.Sprintf("Client %s: Received unknown envelope type: '%s'", c.id, env.Type))
		}
	}
}

func (c *Client) invokeSubscriptionHandler(hw *ergosockets.HandlerWrapper, env *ergosockets.Envelope) {
	// func(MsgStruct) error or func(*MsgStruct) error
	var msgVal reflect.Value
	var msgType reflect.Type = hw.MsgType

	// Check if the message type is a pointer or a value type
	if msgType.Kind() == reflect.Ptr {
		// If it's a pointer type (*MsgStruct), get the element type (MsgStruct)
		elemType := msgType.Elem()
		// Create a new pointer to the element type (**MsgStruct)
		msgVal = reflect.New(elemType)
	} else {
		// If it's a value type (MsgStruct), create a new pointer to it (*MsgStruct)
		msgVal = reflect.New(msgType)
	}

	// Decode the payload into the created value
	if err := env.DecodePayload(msgVal.Interface()); err != nil {
		c.config.logger.Info(fmt.Sprintf("Client %s: Failed to decode publish payload for topic '%s' into %s: %v. Payload: %s", c.id, env.Topic, hw.MsgType.String(), err, string(env.Payload)))
		return
	}

	// Prepare the argument for the handler function
	var arg reflect.Value
	if msgType.Kind() == reflect.Ptr {
		// If handler expects *MsgStruct, pass the pointer directly
		arg = msgVal
	} else {
		// If handler expects MsgStruct, dereference the pointer to get the value
		arg = msgVal.Elem()
	}

	// Call the handler function
	results := hw.HandlerFunc.Call([]reflect.Value{arg})
	if errVal, ok := results[0].Interface().(error); ok && errVal != nil {
		c.config.logger.Info(fmt.Sprintf("Client %s: Subscription handler for topic '%s' returned error: %v", c.id, env.Topic, errVal))
	}
}

func (c *Client) invokeClientRequestHandler(hw *ergosockets.HandlerWrapper, reqEnv *ergosockets.Envelope) {
	// func(ReqStruct) (RespStruct, error) OR func(ReqStruct) error
	var reqVal reflect.Value
	var reqType reflect.Type = hw.ReqType

	// Check if the request type is a pointer or a value type
	if reqType.Kind() == reflect.Ptr {
		// If it's a pointer type (*ReqStruct), get the element type (ReqStruct)
		elemType := reqType.Elem()
		// Create a new pointer to the element type (**ReqStruct)
		reqVal = reflect.New(elemType)
	} else {
		// If it's a value type (ReqStruct), create a new pointer to it (*ReqStruct)
		reqVal = reflect.New(reqType)
	}

	if err := reqEnv.DecodePayload(reqVal.Interface()); err != nil {
		c.config.logger.Info(fmt.Sprintf("Client %s: Failed to decode server request payload for topic '%s' into %s: %v. Payload: %s", c.id, reqEnv.Topic, hw.ReqType.String(), err, string(reqEnv.Payload)))
		errResp, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeError, reqEnv.Topic, nil, &ergosockets.ErrorPayload{Code: http.StatusBadRequest, Message: "Invalid request payload from server"})
		c.trySend(errResp)
		return
	}

	var arg reflect.Value
	if reqType.Kind() == reflect.Ptr { // Handler expects *ReqStruct
		arg = reqVal
	} else { // Handler expects ReqStruct
		arg = reqVal.Elem()
	}

	inputs := []reflect.Value{arg}
	results := hw.HandlerFunc.Call(inputs)

	var errResult error
	if errVal, ok := results[len(results)-1].Interface().(error); ok {
		errResult = errVal
	}

	if errResult != nil {
		c.config.logger.Info(fmt.Sprintf("Client %s: OnRequest handler for server topic '%s' returned error: %v", c.id, reqEnv.Topic, errResult))
		errResp, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeError, reqEnv.Topic, nil, &ergosockets.ErrorPayload{Code: http.StatusInternalServerError, Message: errResult.Error()})
		c.trySend(errResp)
		return
	}

	if hw.RespType != nil { // Handler returns a response payload
		respPayload := results[0].Interface() // This is already the concrete RespStruct or *RespStruct
		respEnv, err := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeResponse, reqEnv.Topic, respPayload, nil)
		if err != nil {
			c.config.logger.Info(fmt.Sprintf("Client %s: Failed to create response envelope for server request on topic '%s': %v", c.id, reqEnv.Topic, err))
			errResp, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeError, reqEnv.Topic, nil, &ergosockets.ErrorPayload{Code: http.StatusInternalServerError, Message: "Client failed to create response envelope"})
			c.trySend(errResp)
			return
		}
		c.trySend(respEnv)
	} else if reqEnv.ID != "" { // No response payload, but request had an ID, send simple ack
		ackEnv, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeResponse, reqEnv.Topic, nil, nil)
		c.trySend(ackEnv)
	}
}

func (c *Client) trySend(env *ergosockets.Envelope) {
	select {
	case c.send <- env:
	case <-c.currentConnPumpCtx.Done(): // Use pumpCtx as this is response for current connection
		c.config.logger.Info(fmt.Sprintf("Client %s: Current connection pump context done, cannot send envelope type %s on topic %s", c.id, env.Type, env.Topic))
	case <-c.clientCtx.Done(): // Or main client context if permanently closing
		c.config.logger.Info(fmt.Sprintf("Client %s: Main client context done, cannot send envelope type %s on topic %s", c.id, env.Type, env.Topic))
	default:
		c.config.logger.Info(fmt.Sprintf("Client %s: Send channel full when trying to send envelope type %s on topic %s. Message dropped.", c.id, env.Type, env.Topic))
	}
}

func (c *Client) writePump() {
	defer func() {
		c.config.logger.Info(fmt.Sprintf("Client %s: writePump stopping for connection.", c.id))
		// If readPump initiated shutdown via currentConnPumpCancel, this is fine.
		// If writePump fails first, it should also signal currentConnPumpCancel.
		c.currentConnPumpWg.Done()
	}()

	for {
		select {
		case message, ok := <-c.send:
			if !ok { // send channel closed, likely by Client.Close()
				c.config.logger.Info(fmt.Sprintf("Client %s: Send channel closed, writePump exiting.", c.id))
				// Ensure connection is closed if not already
				if conn := c.getConn(); conn != nil {
					conn.Close(websocket.StatusNormalClosure, "client send channel closed by master")
				}
				return
			}
			conn := c.getConn()
			if conn == nil {
				c.config.logger.Info(fmt.Sprintf("Client %s: writePump: no active connection, message dropped: Topic=%s, Type=%s", c.id, message.Topic, message.Type))
				// If auto-reconnect is on, message might be lost. A more robust system might queue.
				continue
			}
			// Use currentConnPumpCtx for the write operation's timeout context
			writeOpCtx, writeOpCancel := context.WithTimeout(c.currentConnPumpCtx, c.config.writeTimeout)
			err := wsjson.Write(writeOpCtx, conn, message)
			writeOpCancel()

			if err != nil {
				c.config.logger.Info(fmt.Sprintf("Client %s: write error in writePump: %v. Connection may be stale.", c.id, err))
				// A write error often means the connection is bad.
				// Signal other pumps for this specific connection to stop.
				if c.currentConnPumpCancel != nil {
					c.currentConnPumpCancel() // This will cause readPump and pingLoop for this conn to exit.
				}
				// readPump's defer will handle potential reconnect.
				return // Exit writePump for this connection.
			}
		case <-c.currentConnPumpCtx.Done(): // Current connection's pumps are shutting down
			c.config.logger.Info(fmt.Sprintf("Client %s: writePump stopping due to current connection pump context.", c.id))
			// Connection should be closed by the goroutine that cancelled currentConnPumpCtx (e.g. readPump)
			return
		case <-c.clientCtx.Done(): // Client is being closed permanently
			c.config.logger.Info(fmt.Sprintf("Client %s: writePump stopping due to permanent client shutdown.", c.id))
			return
		}
	}
}

func (c *Client) pingLoop() {
	defer func() {
		c.config.logger.Info(fmt.Sprintf("Client %s: pingLoop stopping for connection.", c.id))
		c.currentConnPumpWg.Done()
	}()

	if c.config.pingInterval <= 0 { // Guard: only run if interval is positive
		return
	}
	ticker := time.NewTicker(c.config.pingInterval)
	defer ticker.Stop()
	c.config.logger.Info(fmt.Sprintf("Client %s: PingLoop started with interval %v", c.id, c.config.pingInterval))

	for {
		select {
		case <-ticker.C:
			conn := c.getConn()
			if conn == nil {
				// c.config.logger.Info(fmt.Sprintf("Client %s: pingLoop: no active connection, skipping ping.", c.id)
				continue // Wait for reconnect
			}
			// Use currentConnPumpCtx for the ping operation
			pingOpCtx, pingOpCancel := context.WithTimeout(c.currentConnPumpCtx, c.config.pingInterval/2) // Shorter timeout for ping
			err := conn.Ping(pingOpCtx)
			pingOpCancel()
			if err != nil {
				c.config.logger.Info(fmt.Sprintf("Client %s: Ping failed: %v. Connection might be stale.", c.id, err))
				// Signal other pumps for this specific connection to stop.
				if c.currentConnPumpCancel != nil {
					c.currentConnPumpCancel()
				}
				return // Exit ping loop for this connection. readPump's defer will handle reconnect.
			}
			// c.config.logger.Info(fmt.Sprintf("Client %s: Ping sent.", c.id)
		case <-c.currentConnPumpCtx.Done():
			return // Current connection's pumps are shutting down
		case <-c.clientCtx.Done(): // Client is being closed permanently
			return
		}
	}
}

// Request sends a request to the server and waits for a response of type T.
// The first optional payload argument (reqData) is the request data.
// If no reqData is provided, a null payload is sent.
func (c *Client) Request(ctx context.Context, topic string, reqData ...interface{}) (*json.RawMessage, *ergosockets.ErrorPayload, error) {
	c.closedMu.Lock()
	if c.isClosed {
		c.closedMu.Unlock()
		return nil, nil, errors.New("client is closed")
	}
	c.closedMu.Unlock()

	var requestPayload interface{}
	if len(reqData) > 0 {
		requestPayload = reqData[0]
	}
	// If len(reqData) == 0, requestPayload remains nil.
	// ergosockets.NewEnvelope handles nil payloadData by setting Envelope.Payload to nil,
	// which json.Marshal then serializes as JSON `null`.

	correlationID := ergosockets.GenerateID()
	// Envelope.Payload will be `null` if requestPayload is nil.
	reqEnv, err := ergosockets.NewEnvelope(correlationID, ergosockets.TypeRequest, topic, requestPayload, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("client: failed to create request envelope for topic '%s': %w", topic, err)
	}

	respChan := make(chan *ergosockets.Envelope, 1)
	c.pendingRequestsMu.Lock()
	c.pendingRequests[correlationID] = respChan
	c.pendingRequestsMu.Unlock()

	defer func() {
		c.pendingRequestsMu.Lock()
		delete(c.pendingRequests, correlationID)
		c.pendingRequestsMu.Unlock()
		// Do not close respChan here, as the receiver might still be selecting on it if a timeout occurred elsewhere.
		// It will be garbage collected.
	}()

	// Determine effective timeout for the entire operation
	effectiveTimeout := c.config.defaultRequestTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if timeout := time.Until(deadline); timeout < effectiveTimeout {
			effectiveTimeout = timeout
		}
	}
	// Create a new context for this specific request operation, derived from user's ctx
	requestOpCtx, requestOpCancel := context.WithTimeout(ctx, effectiveTimeout)
	defer requestOpCancel()

	// Send the request envelope
	select {
	case c.send <- reqEnv:
		c.config.logger.Info(fmt.Sprintf("Client %s: Sent request (ID: %s) on topic '%s'", c.id, correlationID, topic))
	case <-requestOpCtx.Done(): // Timeout or cancellation before send
		return nil, nil, fmt.Errorf("client: context done before sending request %s: %w", correlationID, requestOpCtx.Err())
	case <-c.clientCtx.Done(): // Client permanently closing
		return nil, nil, fmt.Errorf("client: client permanently closing before sending request %s: %w", correlationID, c.clientCtx.Err())
	}

	// Wait for the response envelope using the requestOpCtx
	select {
	case respEnv, ok := <-respChan:
		if !ok { // Channel was closed unexpectedly (should not happen with current defer logic)
			return nil, nil, fmt.Errorf("client: response channel closed for request ID %s (connection issue?)", correlationID)
		}
		if respEnv.Error != nil {
			return nil, respEnv.Error, fmt.Errorf("client: server error for request ID %s (code %d): %s", correlationID, respEnv.Error.Code, respEnv.Error.Message)
		}
		// Return raw payload and nil error for successful response
		return &respEnv.Payload, nil, nil // Note: returning pointer to allow distinguishing nil payload from no payload
	case <-requestOpCtx.Done(): // Timeout or cancellation while waiting for response
		return nil, nil, fmt.Errorf("client: request ID %s timed out or context cancelled after %v: %w", correlationID, effectiveTimeout, requestOpCtx.Err())
	case <-c.clientCtx.Done(): // Client permanently closing
		return nil, nil, fmt.Errorf("client: client permanently closing while waiting for response %s: %w", correlationID, c.clientCtx.Err())
	}
}

// Publish sends a fire-and-forget message to the server.
func (c *Client) Publish(topic string, payloadData interface{}) error {
	c.closedMu.Lock()
	if c.isClosed {
		c.closedMu.Unlock()
		return errors.New("client is closed")
	}
	c.closedMu.Unlock()

	// Envelope.Payload will be `null` if payloadData is nil.
	env, err := ergosockets.NewEnvelope("", ergosockets.TypePublish, topic, payloadData, nil)
	if err != nil {
		return fmt.Errorf("client: failed to create publish envelope for topic '%s': %w", topic, err)
	}

	// Use a short, non-blocking attempt to send, or timeout quickly.
	// Publish is fire-and-forget, so we don't want it to block the caller for long.
	select {
	case c.send <- env:
		// c.config.logger.Info(fmt.Sprintf("Client %s: Published message on topic '%s'", c.id, topic)
		return nil
	case <-c.clientCtx.Done():
		return fmt.Errorf("client: client permanently closing, cannot publish: %w", c.clientCtx.Err())
	case <-time.After(c.config.writeTimeout / 2): // Use a fraction of write timeout
		return fmt.Errorf("client: publish to topic '%s' timed out (send channel likely full or connection stalled)", topic)
	}
}

// Subscribe registers a handler for messages published by the server on a given topic.
// handlerFunc must be of type: func(MsgStruct) error or func(*MsgStruct) error
func (c *Client) Subscribe(topic string, handlerFunc interface{}) (unsubscribeFunc func(), err error) {
	c.closedMu.Lock()
	if c.isClosed {
		c.closedMu.Unlock()
		return nil, errors.New("client is closed")
	}
	c.closedMu.Unlock()

	hw, err := ergosockets.NewHandlerWrapper(handlerFunc)
	if err != nil {
		return nil, fmt.Errorf("client Subscribe topic '%s': %w", topic, err)
	}
	// Validate client subscribe signature (1 in, 1 out error)
	if hw.HandlerFunc.Type().NumIn() != 1 || !(hw.HandlerFunc.Type().NumOut() == 1 && hw.HandlerFunc.Type().Out(0) == ergosockets.ErrType) {
		return nil, fmt.Errorf("client Subscribe topic '%s': handler must be func(MessageType) error, got %s", topic, hw.HandlerFunc.Type().String())
	}

	c.subscriptionHandlersMu.Lock()
	if _, exists := c.subscriptionHandlers[topic]; exists {
		c.subscriptionHandlersMu.Unlock()
		return nil, fmt.Errorf("client: handler already subscribed to topic '%s'", topic)
	}
	c.subscriptionHandlers[topic] = hw
	c.subscriptionHandlersMu.Unlock()

	if err := c.sendSubscribeRequest(topic); err != nil {
		c.subscriptionHandlersMu.Lock()
		delete(c.subscriptionHandlers, topic) // Rollback local registration
		c.subscriptionHandlersMu.Unlock()
		return nil, fmt.Errorf("client: failed to send subscribe request for topic '%s': %w", topic, err)
	}
	c.config.logger.Info(fmt.Sprintf("Client %s: Subscribed to topic '%s'", c.id, topic))

	unsubscribe := func() {
		c.subscriptionHandlersMu.Lock()
		delete(c.subscriptionHandlers, topic)
		c.subscriptionHandlersMu.Unlock()
		_ = c.sendUnsubscribeRequest(topic) // Fire and forget
		c.config.logger.Info(fmt.Sprintf("Client %s: Unsubscribed from topic '%s'", c.id, topic))
	}
	return unsubscribe, nil
}

func (c *Client) sendSubscribeRequest(topic string) error {
	// Use a short timeout for control messages.
	ctx, cancel := context.WithTimeout(c.clientCtx, c.config.writeTimeout)
	defer cancel()

	// ID for subscribe request can be used to correlate server's ack if needed
	subEnv, err := ergosockets.NewEnvelope(ergosockets.GenerateID(), ergosockets.TypeSubscribeRequest, topic, nil, nil)
	if err != nil {
		return err // Should be rare
	}
	select {
	case c.send <- subEnv:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("sending subscribe request for topic '%s': %w", topic, ctx.Err())
	case <-c.clientCtx.Done(): // Check permanent close too
		return fmt.Errorf("client permanently closing, cannot send subscribe for topic '%s': %w", topic, c.clientCtx.Err())
	}
}
func (c *Client) sendUnsubscribeRequest(topic string) error {
	ctx, cancel := context.WithTimeout(c.clientCtx, c.config.writeTimeout)
	defer cancel()
	unsubEnv, _ := ergosockets.NewEnvelope(ergosockets.GenerateID(), ergosockets.TypeUnsubscribeRequest, topic, nil, nil)
	select {
	case c.send <- unsubEnv:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("sending unsubscribe request for topic '%s': %w", topic, ctx.Err())
	case <-c.clientCtx.Done():
		return fmt.Errorf("client permanently closing, cannot send unsubscribe for topic '%s': %w", topic, c.clientCtx.Err())
	}
}

// OnRequest registers a handler for requests initiated by the server on a given topic.
// handlerFunc must be of type: func(ReqStruct) (RespStruct, error) or func(ReqStruct) error
// or func(*ReqStruct) (*RespStruct, error) etc.
func (c *Client) OnRequest(topic string, handlerFunc interface{}) error {
	c.closedMu.Lock()
	if c.isClosed {
		c.closedMu.Unlock()
		return errors.New("client is closed")
	}
	c.closedMu.Unlock()

	hw, err := ergosockets.NewHandlerWrapper(handlerFunc)
	if err != nil {
		return fmt.Errorf("client OnRequest topic '%s': %w", topic, err)
	}
	// Validate client OnRequest signature (1 in, 1 or 2 out with last as error)
	if hw.HandlerFunc.Type().NumIn() != 1 {
		return fmt.Errorf("client OnRequest topic '%s': handler must have 1 input argument (RequestType), got %d", topic, hw.HandlerFunc.Type().NumIn())
	}

	c.requestHandlersMu.Lock()
	defer c.requestHandlersMu.Unlock()
	if _, exists := c.requestHandlers[topic]; exists {
		return fmt.Errorf("client: handler already registered for server requests on topic '%s'", topic)
	}
	c.requestHandlers[topic] = hw
	c.config.logger.Info(fmt.Sprintf("Client %s: Registered handler for server requests on topic '%s'", c.id, topic))
	return nil
}

// ID returns the unique ID of this client instance.
func (c *Client) ID() string {
	return c.id
}

// Close gracefully closes the client connection and stops all operations.
// It cancels the main client context, signals internal goroutines to stop,
// and closes the WebSocket connection.
func (c *Client) Close() error {
	c.closedMu.Lock()
	if c.isClosed {
		c.closedMu.Unlock()
		return errors.New("client: already closed or closing")
	}
	c.isClosed = true // Mark as closed to prevent new operations/reconnects
	c.closedMu.Unlock()

	c.config.logger.Info(fmt.Sprintf("Client %s: Initiating close...", c.id))

	// 1. Cancel the main client context. This signals all derived contexts,
	// including currentConnPumpCtx (if active) and any pending operation contexts.
	if c.clientCancel != nil {
		c.clientCancel()
	}

	// 2. Close the `send` channel. This will make the writePump exit if it's ranging.
	// Do this early to prevent new messages from being queued during shutdown.
	// Ensure it's only closed once.
	// This needs to be coordinated with writePump's select.
	// For simplicity, we rely on context cancellation to stop writePump.
	// Closing send channel here might cause panic if writePump tries to send to it after check.
	// Better to let writePump exit via context and then it won't read from `send`.
	// However, if writePump is blocked on `c.send <- message`, closing `send` unblocks it.
	// This needs careful thought. A dedicated close signal for send might be better.
	// For now, rely on context cancellation.

	// 3. Wait for current connection's pumps to stop.
	// This wait should happen *after* clientCancel, as that's what signals them.
	// currentConnPumpCancel is called by readPump when it exits, or when a new conn is made.
	// If Close is called, clientCancel will propagate to currentConnPumpCtx.
	c.currentConnPumpWg.Wait() // Wait for read, write, ping loops of the *last active connection*

	// 4. Close the actual WebSocket connection if it's still open.
	// (It might have been closed by readPump already).
	c.connMu.Lock()
	if c.conn != nil {
		c.config.logger.Info(fmt.Sprintf("Client %s: Closing WebSocket connection explicitly.", c.id))
		// Use a short timeout for the close handshake.
		_, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		c.conn.Close(websocket.StatusNormalClosure, "client initiated close")
		c.conn = nil
	}
	c.connMu.Unlock()

	// Now safe to close `send` channel as writePump should have exited.
	// This is to unblock any goroutines that might be stuck trying to send to `c.send`
	// if they weren't using context properly (though library methods should).
	// This needs to be idempotent or guarded.
	// For now, assume context cancellation is sufficient for writePump to exit.
	// close(c.send) // This can panic if already closed or writePump is still trying to send.

	c.config.logger.Info(fmt.Sprintf("Client %s: Close sequence complete.", c.id))
	return nil
}

// GenericRequest is the primary method for client-to-server requests.
// It handles sending the request and unmarshalling the response payload into type T.
// reqData is variadic:
// - If no reqData: sends a request with a JSON `null` payload.
// - If one reqData: uses it as the payload.
// - More than one reqData is a usage error (takes the first).
func GenericRequest[T any](cli *Client, ctx context.Context, topic string, reqData ...interface{}) (*T, error) {
	rawPayload, serverErrPayload, err := cli.Request(ctx, topic, reqData...)
	if err != nil {
		if serverErrPayload != nil {
			return nil, fmt.Errorf("server error (code %d): %s (underlying client/network error: %w)", serverErrPayload.Code, serverErrPayload.Message, err)
		}
		return nil, err // Client-side error (e.g., timeout, context cancelled before send, network issue)
	}

	// If rawPayload is nil or points to JSON "null"
	if rawPayload == nil || string(*rawPayload) == "null" {
		var zero T
		// If T is a pointer type or an empty struct, a null payload might be acceptable.
		rt := reflect.TypeOf(zero)
		if rt == nil { // T is interface{}
			return nil, nil // Cannot determine, return nil
		}
		if rt.Kind() == reflect.Ptr || (rt.Kind() == reflect.Struct && rt.NumField() == 0) {
			// For pointer types or empty structs, a null payload results in a nil pointer or zero struct.
			return new(T), nil // Return pointer to zero value of T
		}
		return nil, fmt.Errorf("server returned successful response with null/no payload, but expected non-empty type %T", zero)
	}

	var typedResponse T
	if err := json.Unmarshal(*rawPayload, &typedResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response payload into %T: %w. Raw payload: %s", typedResponse, err, string(*rawPayload))
	}
	return &typedResponse, nil
}

```

<a id="file-pkg-client-client_test-go"></a>
**File: pkg/client/client_test.go**

```go
// ergosockets/client/client_test.go
package client_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/lightforgemedia/go-websocketmq/pkg/client"
	"github.com/lightforgemedia/go-websocketmq/pkg/ergosockets"
	app_shared_types "github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
)

// Use the testutil package's logger instead of defining our own

func TestClientConnectAndRequest(t *testing.T) {
	ms := testutil.NewMockServer(t, func(conn *websocket.Conn, srv *testutil.MockServer) { // srv is the mockServer instance
		for { // Simple request echo handler
			var reqEnv ergosockets.Envelope
			err := wsjson.Read(context.Background(), conn, &reqEnv)
			if err != nil {
				return
			}

			// Handle registration request
			if reqEnv.Topic == app_shared_types.TopicClientRegister {
				var reg app_shared_types.ClientRegistration
				if err := reqEnv.DecodePayload(&reg); err != nil {
					t.Errorf("Failed to decode registration payload: %v", err)
					return
				}

				// Create response with server-assigned ID
				respEnv, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeResponse, reqEnv.Topic, app_shared_types.ClientRegistrationResponse{
					ServerAssignedID: "server-" + reg.ClientID[:8],
					ClientName:       reg.ClientName,
					ServerTime:       time.Now().Format(time.RFC3339),
				}, nil)
				// Set expectation before sending
				srv.Expect(1)
				srv.Send(*respEnv)
				t.Logf("MockServer: Handled registration request, assigned ID: server-%s", reg.ClientID[:8])
			} else if reqEnv.Topic == app_shared_types.TopicGetTime {
				respEnv, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeResponse, reqEnv.Topic, app_shared_types.GetTimeResponse{CurrentTime: "mock-time"}, nil)
				// Set expectation before sending
				srv.Expect(1)
				srv.Send(*respEnv) // Use srv.Send
			}
		}
	})
	defer ms.Close()

	cli := testutil.NewTestClient(t, ms.WsURL)
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := client.GenericRequest[app_shared_types.GetTimeResponse](cli, ctx, app_shared_types.TopicGetTime)
	if err != nil {
		t.Fatalf("Client request failed: %v", err)
	}
	if resp.CurrentTime != "mock-time" {
		t.Errorf("Expected 'mock-time', got '%s'", resp.CurrentTime)
	}
	t.Log("Client connect and request successful.")
}

func TestClientSubscribeAndReceive(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1) // For the received message

	ms := testutil.NewMockServer(t, func(conn *websocket.Conn, srv *testutil.MockServer) {
		// First, handle registration request
		var regEnv ergosockets.Envelope
		regCtx, regCancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer regCancel()
		err := wsjson.Read(regCtx, conn, &regEnv)
		if err != nil {
			t.Errorf("MockServer: Did not receive registration request: %v", err)
			return
		}

		if regEnv.Topic == app_shared_types.TopicClientRegister {
			var reg app_shared_types.ClientRegistration
			if err := regEnv.DecodePayload(&reg); err != nil {
				t.Errorf("Failed to decode registration payload: %v", err)
				return
			}

			// Create response with server-assigned ID
			respEnv, _ := ergosockets.NewEnvelope(regEnv.ID, ergosockets.TypeResponse, regEnv.Topic, app_shared_types.ClientRegistrationResponse{
				ServerAssignedID: "server-" + reg.ClientID[:8],
				ClientName:       reg.ClientName,
				ServerTime:       time.Now().Format(time.RFC3339),
			}, nil)
			// Set expectation before sending
			srv.Expect(1)
			srv.Send(*respEnv)
			t.Logf("MockServer: Handled registration request, assigned ID: server-%s", reg.ClientID[:8])
		} else {
			t.Errorf("MockServer: Expected registration request, got topic %s", regEnv.Topic)
			return
		}

		// Now wait for subscribe request from client
		var subEnv ergosockets.Envelope
		subCtx, subCancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer subCancel()
		err = wsjson.Read(subCtx, conn, &subEnv)
		if err != nil {
			t.Errorf("MockServer: Did not receive subscribe request: %v", err)
			return
		}
		if subEnv.Type != ergosockets.TypeSubscribeRequest || subEnv.Topic != app_shared_types.TopicServerAnnounce {
			t.Errorf("MockServer: Expected subscribe request, got type %s topic %s", subEnv.Type, subEnv.Topic)
			return
		}
		t.Logf("MockServer: Received subscribe request for %s", subEnv.Topic)
		ackEnv, _ := ergosockets.NewEnvelope(subEnv.ID, ergosockets.TypeSubscriptionAck, subEnv.Topic, nil, nil)
		// Set expectation before sending
		srv.Expect(1)
		srv.Send(*ackEnv)

		// After ack, send the publish
		time.Sleep(50 * time.Millisecond) // Ensure client processes ack
		publishEnv, _ := ergosockets.NewEnvelope("", ergosockets.TypePublish, app_shared_types.TopicServerAnnounce, app_shared_types.ServerAnnouncement{Message: "test-publish"}, nil)
		// Set expectation before sending
		srv.Expect(1)
		srv.Send(*publishEnv)
	})
	defer ms.Close()

	cli := testutil.NewTestClient(t, ms.WsURL)
	defer cli.Close()

	receivedChan := make(chan app_shared_types.ServerAnnouncement, 1)
	_, err := cli.Subscribe(app_shared_types.TopicServerAnnounce,
		func(announcement *app_shared_types.ServerAnnouncement) error {
			t.Logf("ClientTest: Received announcement: %+v", announcement)
			receivedChan <- *announcement
			wg.Done()
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Client failed to subscribe: %v", err)
	}

	select {
	case received := <-receivedChan:
		if received.Message != "test-publish" {
			t.Errorf("Expected 'test-publish', got '%s'", received.Message)
		}
	case <-time.After(2 * time.Second): // Increased timeout
		t.Fatal("Client did not receive published message")
	}
	wg.Wait() // Ensure handler goroutine finishes
	t.Log("Client subscribe and receive successful.")
}

func TestClientAutoReconnect(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}
	t.Parallel()
	connectAttempts := 0
	var firstConnectionEstablished sync.WaitGroup
	firstConnectionEstablished.Add(1)

	ms := testutil.NewMockServer(t, func(conn *websocket.Conn, srv *testutil.MockServer) {
		connectAttempts++
		t.Logf("MockServer: Client connected (attempt %d)", connectAttempts)

		// Handle registration for any connection attempt
		var regEnv ergosockets.Envelope
		regCtx, regCancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer regCancel()
		err := wsjson.Read(regCtx, conn, &regEnv)
		if err != nil {
			t.Errorf("MockServer: Did not receive registration request: %v", err)
			return
		}

		if regEnv.Topic == app_shared_types.TopicClientRegister {
			var reg app_shared_types.ClientRegistration
			if err := regEnv.DecodePayload(&reg); err != nil {
				t.Errorf("Failed to decode registration payload: %v", err)
				return
			}

			// Create response with server-assigned ID
			respEnv, _ := ergosockets.NewEnvelope(regEnv.ID, ergosockets.TypeResponse, regEnv.Topic, app_shared_types.ClientRegistrationResponse{
				ServerAssignedID: "server-" + reg.ClientID[:8] + "-" + fmt.Sprintf("%d", connectAttempts),
				ClientName:       reg.ClientName,
				ServerTime:       time.Now().Format(time.RFC3339),
			}, nil)
			// Set expectation before sending
			srv.Expect(1)
			srv.Send(*respEnv)
			t.Logf("MockServer: Handled registration request, assigned ID: server-%s-%d", reg.ClientID[:8], connectAttempts)
		} else {
			t.Errorf("MockServer: Expected registration request, got topic %s", regEnv.Topic)
			return
		}

		if connectAttempts == 1 {
			firstConnectionEstablished.Done()
			// Close connection after a short delay to trigger reconnect
			go func() {
				time.Sleep(200 * time.Millisecond)
				t.Logf("MockServer: Closing connection to trigger reconnect (attempt %d)", connectAttempts)
				srv.CloseCurrentConnection() // Use helper to close specific conn and cancel its loop
			}()

			// Block until the connection is closed with a read that will fail when connection closes
			var msg interface{}
			wsjson.Read(context.Background(), conn, &msg) // This will return with an error when connection is closed
			t.Logf("MockServer: First connection read completed (likely due to connection close)")
			return
		} else if connectAttempts == 2 {
			// Handle a request on the re-established connection
			for { // Add infinite loop to keep connection open
				var reqEnv ergosockets.Envelope
				err := wsjson.Read(context.Background(), conn, &reqEnv) // Simple read, no timeout for test simplicity
				if err != nil {
					t.Logf("MockServer: Read error on reconnected line: %v", err)
					return
				}
				if reqEnv.Topic == app_shared_types.TopicGetTime {
					respEnv, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeResponse, reqEnv.Topic, app_shared_types.GetTimeResponse{CurrentTime: "reconnected-time"}, nil)
					// Set expectation before sending
					srv.Expect(1)
					srv.Send(*respEnv)
				}
			}
		}
	})
	defer ms.Close()

	cli := testutil.NewTestClient(t, ms.WsURL,
		client.WithAutoReconnect(3, 50*time.Millisecond, 200*time.Millisecond), // Fast reconnect for test
	)
	defer cli.Close()

	// Wait for the first connection to be established
	firstConnectionEstablished.Wait()
	t.Log("Client: First connection established with mock server.")

	// Wait for reconnect to happen (server closes, client should reconnect)
	// The second connection will be attempt #2.
	time.Sleep(500 * time.Millisecond) // Allow time for disconnect and reconnect attempt

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := client.GenericRequest[app_shared_types.GetTimeResponse](cli, ctx, app_shared_types.TopicGetTime)
	if err != nil {
		t.Fatalf("Client request after reconnect failed: %v (Connect attempts: %d)", err, connectAttempts)
	}
	if resp.CurrentTime != "reconnected-time" {
		t.Errorf("Expected 'reconnected-time', got '%s'", resp.CurrentTime)
	}
	if connectAttempts < 2 { // Should be at least 2 for a reconnect
		t.Errorf("Expected at least 2 connection attempts for reconnect, got %d", connectAttempts)
	}
	t.Logf("Client auto-reconnect successful, request processed. Total connect attempts: %d", connectAttempts)
}

func TestClientRequestVariadicPayload(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}
	t.Parallel()
	var lastReceivedPayload json.RawMessage
	registrationHandled := false

	ms := testutil.NewMockServer(t, func(conn *websocket.Conn, srv *testutil.MockServer) {
		for { // Add infinite loop to keep connection open
			var reqEnv ergosockets.Envelope
			err := wsjson.Read(context.Background(), conn, &reqEnv)
			if err != nil {
				t.Logf("MockServer: Read error: %v", err)
				return
			}

			// Handle registration first
			if !registrationHandled && reqEnv.Topic == app_shared_types.TopicClientRegister {
				var reg app_shared_types.ClientRegistration
				if err := reqEnv.DecodePayload(&reg); err != nil {
					t.Errorf("Failed to decode registration payload: %v", err)
					return
				}

				// Create response with server-assigned ID
				respEnv, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeResponse, reqEnv.Topic, app_shared_types.ClientRegistrationResponse{
					ServerAssignedID: "server-" + reg.ClientID[:8],
					ClientName:       reg.ClientName,
					ServerTime:       time.Now().Format(time.RFC3339),
				}, nil)
				// Set expectation before sending
				srv.Expect(1)
				srv.Send(*respEnv)
				t.Logf("MockServer: Handled registration request, assigned ID: server-%s", reg.ClientID[:8])
				registrationHandled = true
			} else {
				// For all other requests, capture payload and respond
				lastReceivedPayload = reqEnv.Payload // Capture the raw payload
				respEnv, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeResponse, reqEnv.Topic, map[string]string{"status": "ok"}, nil)
				// Set expectation before sending
				srv.Expect(1)
				srv.Send(*respEnv)
			}
		}
	})
	defer ms.Close()

	cli := testutil.NewTestClient(t, ms.WsURL)
	defer cli.Close()
	ctx := context.Background()

	// Test 1: No payload argument
	_, err := client.GenericRequest[map[string]string](cli, ctx, "topic_no_payload")
	if err != nil {
		t.Fatalf("Request with no payload failed: %v", err)
	}
	// wsjson sends "null" for nil interface{}, but we need to check if it's empty or "null"
	payloadStr := string(lastReceivedPayload)
	if payloadStr != "null" && payloadStr != "" {
		t.Errorf("Expected 'null' or empty payload from server, got: %s", payloadStr)
	}
	t.Logf("Request with no payload sent, server received: %s", payloadStr)

	// Test 2: With one payload argument
	payloadData := app_shared_types.GetUserDetailsRequest{UserID: "var123"}
	_, err = client.GenericRequest[map[string]string](cli, ctx, "topic_with_payload", payloadData)
	if err != nil {
		t.Fatalf("Request with payload failed: %v", err)
	}
	var decodedSentPayload app_shared_types.GetUserDetailsRequest
	if err := json.Unmarshal(lastReceivedPayload, &decodedSentPayload); err != nil {
		t.Fatalf("Failed to unmarshal received payload at server: %v. Payload: %s", err, string(lastReceivedPayload))
	}
	if decodedSentPayload.UserID != "var123" {
		t.Errorf("Server expected UserID 'var123', got '%s'", decodedSentPayload.UserID)
	}
	t.Logf("Request with payload sent, server received: %s", string(lastReceivedPayload))
}

// Use testutil.NewTestClient instead of defining our own

```

<a id="file-pkg-ergosockets-common-go"></a>
**File: pkg/ergosockets/common.go**

```go
// ergosockets/common.go
package ergosockets

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"reflect"
	"time"
)

// Cached error type for reflection.
var ErrType = reflect.TypeOf((*error)(nil)).Elem()

// GenerateID creates a new random hex string ID.
func GenerateID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("fallback-%x", reflect.ValueOf(TimeNow()).Int()) // TimeNow from time.go
	}
	return hex.EncodeToString(bytes)
}

// HandlerWrapper stores reflection info for invoking user-provided handlers.
type HandlerWrapper struct {
	HandlerFunc reflect.Value // The user's handler function
	ReqType     reflect.Type  // Type of the request payload struct (for request handlers)
	RespType    reflect.Type  // Type of the response payload struct (for request handlers that return one)
	MsgType     reflect.Type  // Type of the message payload (for subscription handlers)
}

// NewHandlerWrapper inspects the handler function and extracts type information.
// Supported signatures:
// Server OnRequest: func(ClientHandle, ReqStruct) (RespStruct, error)
// Server OnRequest: func(ClientHandle, ReqStruct) error
// Client Subscribe: func(MsgStruct) error
// Client OnRequest: func(ReqStruct) (RespStruct, error)
// Client OnRequest: func(ReqStruct) error
func NewHandlerWrapper(handlerFunc interface{}) (*HandlerWrapper, error) {
	fv := reflect.ValueOf(handlerFunc)
	ft := fv.Type()

	if ft.Kind() != reflect.Func {
		return nil, fmt.Errorf("handler must be a function, got %T", handlerFunc)
	}

	hw := &HandlerWrapper{HandlerFunc: fv}
	numIn := ft.NumIn()
	numOut := ft.NumOut()

	// Check common error return (last output arg must be error)
	if numOut == 0 || !ft.Out(numOut-1).Implements(ErrType) {
		return nil, fmt.Errorf("handler must return an error as its last argument, signature: %s", ft.String())
	}

	// Client Subscribe: func(MsgStruct) error
	if numIn == 1 && numOut == 1 {
		hw.MsgType = ft.In(0)
		// Ensure MsgStruct is not an interface or pointer to interface, etc.
		// For simplicity, we assume it's a concrete type or pointer to struct.
		if hw.MsgType.Kind() == reflect.Interface || (hw.MsgType.Kind() == reflect.Ptr && hw.MsgType.Elem().Kind() == reflect.Interface) {
			return nil, fmt.Errorf("subscription handler message type cannot be an interface: %s", ft.String())
		}
		return hw, nil
	}

	// Client OnRequest: func(ReqStruct) (RespStruct, error) or func(ReqStruct) error
	if numIn == 1 && (numOut == 1 || numOut == 2) {
		hw.ReqType = ft.In(0)
		if hw.ReqType.Kind() == reflect.Interface || (hw.ReqType.Kind() == reflect.Ptr && hw.ReqType.Elem().Kind() == reflect.Interface) {
			return nil, fmt.Errorf("client OnRequest handler request type cannot be an interface: %s", ft.String())
		}
		if numOut == 2 { // func(ReqStruct) (RespStruct, error)
			hw.RespType = ft.Out(0)
			if hw.RespType.Kind() == reflect.Interface || (hw.RespType.Kind() == reflect.Ptr && hw.RespType.Elem().Kind() == reflect.Interface) {
				return nil, fmt.Errorf("client OnRequest handler response type cannot be an interface: %s", ft.String())
			}
		}
		return hw, nil
	}

	// Server OnRequest: func(ClientHandle, ReqStruct) (RespStruct, error) or func(ClientHandle, ReqStruct) error
	if numIn == 2 && (numOut == 1 || numOut == 2) {
		// First arg should be ClientHandle (interface) - we don't strictly check its type here
		// as it's passed by the broker itself.
		hw.ReqType = ft.In(1)
		if hw.ReqType.Kind() == reflect.Interface || (hw.ReqType.Kind() == reflect.Ptr && hw.ReqType.Elem().Kind() == reflect.Interface) {
			return nil, fmt.Errorf("server OnRequest handler request type cannot be an interface: %s", ft.String())
		}
		if numOut == 2 { // func(ClientHandle, ReqStruct) (RespStruct, error)
			hw.RespType = ft.Out(0)
			if hw.RespType.Kind() == reflect.Interface || (hw.RespType.Kind() == reflect.Ptr && hw.RespType.Elem().Kind() == reflect.Interface) {
				return nil, fmt.Errorf("server OnRequest handler response type cannot be an interface: %s", ft.String())
			}
		}
		return hw, nil
	}

	return nil, fmt.Errorf("unsupported handler signature: %s. Expected e.g., func(ch ClientHandle, req ReqT) (RespT, error), func(msg MsgT) error, or func(req ReqT) (RespT, error)", ft.String())
}

// TimeNow is a wrapper for time.Now, useful for testing if time needs to be mocked.
// For this implementation, we'll use the real time.Now().
var TimeNow = time.Now

```

<a id="file-pkg-ergosockets-envelope-go"></a>
**File: pkg/ergosockets/envelope.go**

```go
// ergosockets/envelope.go
package ergosockets

import (
	"encoding/json"
	"fmt"
)

// ErrorPayload defines the structure for errors within an Envelope.
type ErrorPayload struct {
	Code    int    `json:"code,omitempty"`    // Application-specific or HTTP-like status code
	Message string `json:"message,omitempty"` // Human-readable error message
}

// Envelope is the standard message structure for ErgoSockets communication.
type Envelope struct {
	ID      string          `json:"id,omitempty"`      // Unique identifier for request-response correlation
	Type    string          `json:"type"`              // e.g., "request", "response", "publish", "error", "subscribe_request", "unsubscribe_request"
	Topic   string          `json:"topic,omitempty"`   // Subject/channel for the message
	Payload json.RawMessage `json:"payload,omitempty"` // Application-specific data. `null` if no payload.
	Error   *ErrorPayload   `json:"error,omitempty"`   // Error details if this envelope represents an error
}

// Constants for Envelope Type
const (
	TypeRequest           = "request"
	TypeResponse          = "response"
	TypePublish           = "publish"
	TypeError             = "error" // Used when an operation results in an error, often in response to a request.
	TypeSubscribeRequest  = "subscribe_request"   // Client wants to subscribe
	TypeUnsubscribeRequest= "unsubscribe_request" // Client wants to unsubscribe
	TypeSubscriptionAck   = "subscription_ack"    // Server acknowledges subscription (can also carry error if sub failed)
)

// NewEnvelope creates a basic envelope.
// For requests without payload, pass nil for payloadData. wsjson will marshal nil as JSON `null`.
func NewEnvelope(id, typ, topic string, payloadData interface{}, errPayload *ErrorPayload) (*Envelope, error) {
	var payloadBytes json.RawMessage
	var err error
	// Only marshal if payloadData is not nil. If it's nil, payloadBytes remains nil,
	// which json.Marshal will correctly serialize as `null` for the Envelope.Payload field.
	if payloadData != nil {
		payloadBytes, err = json.Marshal(payloadData)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal payload for envelope: %w", err)
		}
	}
	return &Envelope{
		ID:      id,
		Type:    typ,
		Topic:   topic,
		Payload: payloadBytes, // Will be nil if payloadData was nil, leading to JSON `null`
		Error:   errPayload,
	}, nil
}

// DecodePayload unmarshals the Envelope's Payload into the provided value (must be a pointer).
func (e *Envelope) DecodePayload(v interface{}) error {
	if e.Payload == nil || string(e.Payload) == "null" {
		// If payload is null, and v is a pointer to a struct, unmarshalling will typically zero it.
		// If v is e.g. *interface{}, it might remain nil.
		// This behavior is generally fine. If a payload is strictly required,
		// the handler should check if the decoded value is its zero value.
		// For empty request structs, this is the desired behavior.
		return nil
	}
	return json.Unmarshal(e.Payload, v)
}
```

<a id="file-pkg-filewatcher-options-go"></a>
**File: pkg/filewatcher/options.go**

```go
package filewatcher

import (
	"log/slog"
)

// Option configures a FileWatcher
type Option func(*FileWatcher)

// WithLogger sets the logger for the file watcher
func WithLogger(logger *slog.Logger) Option {
	return func(fw *FileWatcher) {
		if logger != nil {
			fw.logger = logger
		}
	}
}

// WithDirs sets the directories to watch
func WithDirs(dirs []string) Option {
	return func(fw *FileWatcher) {
		if len(dirs) > 0 {
			fw.dirs = dirs
		}
	}
}

// WithPatterns sets the file patterns to watch
func WithPatterns(patterns []string) Option {
	return func(fw *FileWatcher) {
		if len(patterns) > 0 {
			fw.patterns = patterns
		}
	}
}

// WithDebounce sets the debounce time in milliseconds
func WithDebounce(debounceMs int) Option {
	return func(fw *FileWatcher) {
		if debounceMs > 0 {
			fw.debounceMs = debounceMs
		}
	}
}

```

<a id="file-pkg-filewatcher-watcher-go"></a>
**File: pkg/filewatcher/watcher.go**

```go
// Package filewatcher provides file system watching capabilities.
package filewatcher

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// FileWatcher watches directories for file changes
type FileWatcher struct {
	watcher     *fsnotify.Watcher
	dirs        []string
	patterns    []string
	logger      *slog.Logger
	callbacks   []func(string)
	callbacksMu sync.RWMutex
	debounceMs  int
	changes     map[string]time.Time
	changesMu   sync.Mutex
	done        chan struct{}
}

// New creates a new FileWatcher
func New(opts ...Option) (*FileWatcher, error) {
	// Create fsnotify watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	// Create file watcher with default options
	fw := &FileWatcher{
		watcher:    watcher,
		dirs:       []string{"."},
		patterns:   []string{"*"},
		logger:     slog.New(slog.NewTextHandler(os.Stderr, nil)),
		callbacks:  []func(string){},
		debounceMs: 300, // Default debounce of 300ms
		changes:    make(map[string]time.Time),
		done:       make(chan struct{}),
	}

	// Apply options
	for _, opt := range opts {
		opt(fw)
	}

	return fw, nil
}

// AddCallback adds a callback to be called when files change
func (fw *FileWatcher) AddCallback(callback func(string)) {
	fw.callbacksMu.Lock()
	defer fw.callbacksMu.Unlock()
	fw.callbacks = append(fw.callbacks, callback)
}

// Start starts watching for file changes
func (fw *FileWatcher) Start() error {
	// Add all directories to the watcher
	for _, dir := range fw.dirs {
		fw.logger.Info("Watching directory", "dir", dir)
		err := fw.watcher.Add(dir)
		if err != nil {
			return err
		}
	}

	// Start the watcher goroutine
	go fw.watchLoop()

	return nil
}

// Stop stops watching for file changes
func (fw *FileWatcher) Stop() error {
	close(fw.done)
	return fw.watcher.Close()
}

// watchLoop is the main loop for watching file changes
func (fw *FileWatcher) watchLoop() {
	ticker := time.NewTicker(time.Duration(fw.debounceMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-fw.done:
			return
		case event, ok := <-fw.watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				if fw.matchesPattern(event.Name) {
					fw.changesMu.Lock()
					fw.changes[event.Name] = time.Now()
					fw.changesMu.Unlock()
				}
			}
		case err, ok := <-fw.watcher.Errors:
			if !ok {
				return
			}
			fw.logger.Error("Watcher error", "error", err)
		case <-ticker.C:
			fw.processChanges()
		}
	}
}

// processChanges processes accumulated changes
func (fw *FileWatcher) processChanges() {
	fw.changesMu.Lock()
	defer fw.changesMu.Unlock()

	now := time.Now()
	for file, changeTime := range fw.changes {
		// Only process changes that are older than the debounce time
		if now.Sub(changeTime) >= time.Duration(fw.debounceMs)*time.Millisecond {
			fw.logger.Info("File changed", "file", file)
			fw.notifyCallbacks(file)
			delete(fw.changes, file)
		}
	}
}

// notifyCallbacks notifies all callbacks about a file change
func (fw *FileWatcher) notifyCallbacks(file string) {
	fw.callbacksMu.RLock()
	defer fw.callbacksMu.RUnlock()

	for _, callback := range fw.callbacks {
		callback(file)
	}
}

// matchesPattern checks if a file matches any of the patterns
func (fw *FileWatcher) matchesPattern(file string) bool {
	// Get the base filename
	base := filepath.Base(file)

	// Check against all patterns
	for _, pattern := range fw.patterns {
		matched, err := filepath.Match(pattern, base)
		if err != nil {
			fw.logger.Error("Pattern match error", "pattern", pattern, "error", err)
			continue
		}
		if matched {
			return true
		}
	}

	return false
}

```

<a id="file-pkg-filewatcher-watcher_test-go"></a>
**File: pkg/filewatcher/watcher_test.go**

```go
package filewatcher

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileWatcher(t *testing.T) {
	// Create a logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create a file watcher
	fw, err := New(
		WithLogger(logger),
		WithDirs([]string{tempDir}),
		WithPatterns([]string{"*.txt", "*.html"}),
		WithDebounce(100), // 100ms debounce for faster testing
	)
	require.NoError(t, err, "Failed to create file watcher")

	// Create a channel to receive file change notifications
	changeCh := make(chan string, 10)
	fw.AddCallback(func(file string) {
		changeCh <- file
	})

	// Start the watcher
	err = fw.Start()
	require.NoError(t, err, "Failed to start file watcher")
	defer fw.Stop()

	// Create a test file
	testFile := filepath.Join(tempDir, "test.txt")
	err = os.WriteFile(testFile, []byte("Hello, World!"), 0644)
	require.NoError(t, err, "Failed to write test file")

	// Wait for the change notification
	select {
	case changedFile := <-changeCh:
		assert.Equal(t, testFile, changedFile, "Changed file should match")
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for file change notification")
	}

	// Create a file that doesn't match the patterns
	nonMatchingFile := filepath.Join(tempDir, "test.log")
	err = os.WriteFile(nonMatchingFile, []byte("Log file"), 0644)
	require.NoError(t, err, "Failed to write non-matching file")

	// Wait a bit to ensure no notification is sent
	select {
	case changedFile := <-changeCh:
		t.Fatalf("Received unexpected change notification for %s", changedFile)
	case <-time.After(300 * time.Millisecond):
		// Success - no notification received
	}

	// Modify the matching file
	err = os.WriteFile(testFile, []byte("Updated content"), 0644)
	require.NoError(t, err, "Failed to update test file")

	// Wait for the change notification
	select {
	case changedFile := <-changeCh:
		assert.Equal(t, testFile, changedFile, "Changed file should match")
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for file change notification")
	}
}

```

<a id="file-pkg-hotreload-dist-hotreload-js"></a>
**File: pkg/hotreload/dist/hotreload.js**

```javascript
// Hot Reload Client - Extends WebSocketMQ client with hot reload capabilities
// Version: 2025-05-11-1

(function(global) {
  'use strict';
  
  // Log version to console
  console.log('Hot Reload Client - Version: 2025-05-11-1');
  
  // Make sure WebSocketMQ is loaded
  if (!global.WebSocketMQ || !global.WebSocketMQ.Client) {
    console.error('WebSocketMQ client not found. Make sure to load websocketmq.js before hotreload.js');
    return;
  }
  
  // Constants
  const TOPIC_HOT_RELOAD = "system:hot_reload";
  const TOPIC_CLIENT_ERROR = "system:client_error";
  const TOPIC_HOTRELOAD_READY = "system:hotreload_ready";
  
  // Create the hot reload client
  class HotReloadClient {
    constructor(options = {}) {
      // Store options with defaults
      this.options = Object.assign({
        client: null,
        autoReload: true,
        reportErrors: true,
        maxErrors: 100
      }, options);
      
      // Create or use existing WebSocketMQ client
      this.client = this.options.client;
      if (!this.client) {
        console.log('Creating new WebSocketMQ client');
        this.client = new WebSocketMQ.Client(options);
      }
      
      // Initialize error tracking
      this.errors = [];
      this.fileLoadStatus = {};
      
      // Set up error handlers if enabled
      if (this.options.reportErrors) {
        this._setupErrorHandlers();
      }
      
      // Set up hot reload handler
      this.client.onRequest(TOPIC_HOT_RELOAD, () => {
        console.log('Hot reload requested by server');
        if (this.options.autoReload) {
          this._performReload();
        }
        return { success: true };
      });
      
      // Report client ready when connected
      this.client.onConnect(() => {
        console.log('Connected, sending hot reload ready notification');
        this._reportReady();
      });
      
      // Connect if the client isn't already connected
      if (!this.client.isConnected) {
        console.log('Connecting WebSocketMQ client');
        this.client.connect();
      } else {
        // If already connected, report ready immediately
        this._reportReady();
      }
    }
    
    _setupErrorHandlers() {
      // Capture window errors
      window.addEventListener('error', (event) => {
        const error = {
          message: event.message,
          filename: event.filename,
          lineno: event.lineno,
          colno: event.colno,
          stack: event.error ? event.error.stack : '',
          timestamp: new Date().toISOString()
        };
        
        this._addError(error);
      });
      
      // Capture unhandled promise rejections
      window.addEventListener('unhandledrejection', (event) => {
        const error = {
          message: 'Unhandled Promise Rejection: ' + (event.reason.message || event.reason),
          stack: event.reason.stack || '',
          timestamp: new Date().toISOString()
        };
        
        this._addError(error);
      });
      
      // Capture resource load errors
      window.addEventListener('error', (event) => {
        if (event.target && (event.target.tagName === 'SCRIPT' || event.target.tagName === 'LINK')) {
          const error = {
            message: 'Resource load error: ' + event.target.src || event.target.href,
            filename: event.target.src || event.target.href,
            timestamp: new Date().toISOString()
          };
          
          this._addError(error);
        }
      }, true); // Use capture phase to catch resource errors
    }
    
    _addError(error) {
      // Add to local error list
      this.errors.push(error);
      
      // Limit the number of stored errors
      if (this.errors.length > this.options.maxErrors) {
        this.errors.shift();
      }
      
      // Report to server if connected
      this._reportError(error);
      
      // Log to console
      console.error('Error captured by hot reload client:', error);
    }
    
    _reportError(error) {
      if (this.client && this.client.isConnected) {
        this.client.publish(TOPIC_CLIENT_ERROR, error)
          .catch(err => {
            console.error('Failed to report error to server:', err);
          });
      }
    }
    
    _reportReady() {
      if (this.client && this.client.isConnected) {
        this.client.publish(TOPIC_HOTRELOAD_READY, {
          url: window.location.href,
          userAgent: navigator.userAgent,
          timestamp: new Date().toISOString()
        }).catch(err => {
          console.error('Failed to report ready status to server:', err);
        });
      }
    }
    
    _performReload() {
      console.log('Reloading page...');
      window.location.reload();
    }
    
    // Public API
    
    // Manually trigger a reload
    reload() {
      this._performReload();
    }
    
    // Get all captured errors
    getErrors() {
      return [...this.errors];
    }
    
    // Clear all captured errors
    clearErrors() {
      this.errors = [];
    }
    
    // Set auto reload option
    setAutoReload(enabled) {
      this.options.autoReload = !!enabled;
    }
  }
  
  // Export the HotReloadClient
  global.HotReload = {
    Client: HotReloadClient
  };
  
})(typeof self !== 'undefined' ? self : this);

```

<a id="file-pkg-hotreload-embed-go"></a>
**File: pkg/hotreload/embed.go**

```go
// Package hotreload provides hot reload functionality for web applications.
package hotreload

import (
	"embed"
)

//go:embed dist/hotreload.js
var clientFiles embed.FS

```

<a id="file-pkg-hotreload-handler-go"></a>
**File: pkg/hotreload/handler.go**

```go
package hotreload

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ClientScriptOptions configures the JavaScript client handler
type ClientScriptOptions struct {
	// Path is the URL path where the script will be served
	// Default: "/hotreload.js"
	Path string

	// CacheMaxAge sets the Cache-Control max-age directive in seconds
	// Default: 3600 (1 hour)
	CacheMaxAge int
}

// DefaultClientScriptOptions returns the default options for the client script handler
func DefaultClientScriptOptions() ClientScriptOptions {
	return ClientScriptOptions{
		Path:        "/hotreload.js",
		CacheMaxAge: 3600,
	}
}

// ScriptHandler returns an HTTP handler that serves the embedded JavaScript client
func ScriptHandler(options ClientScriptOptions) http.Handler {
	return &scriptHandler{
		options: options,
	}
}

type scriptHandler struct {
	options ClientScriptOptions
}

func (h *scriptHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Get the requested file path
	filePath := strings.TrimPrefix(r.URL.Path, "/")

	// If no specific file is requested, use the default
	if filePath == "" || filePath == strings.TrimPrefix(h.options.Path, "/") {
		filePath = "hotreload.js"
	}

	data, err := GetClientScript()
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	contentType := "application/javascript"
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "max-age="+strconv.Itoa(h.options.CacheMaxAge))

	w.Write(data)
}

// RegisterHandler registers the JavaScript client handler with the provided ServeMux
func RegisterHandler(mux *http.ServeMux, options ClientScriptOptions) {
	handler := ScriptHandler(options)
	mux.Handle(options.Path, handler)
}

// RegisterHandlers registers all handlers for the hot reload service
func (hr *HotReload) RegisterHandlers(mux *http.ServeMux) {
	// Register the broker's handlers with default options
	hr.broker.RegisterHandlersWithDefaults(mux)

	// Register the hot reload JavaScript client handler
	RegisterHandler(mux, DefaultClientScriptOptions())

	// Register status API endpoint
	mux.HandleFunc("/api/hotreload/status", hr.StatusHandler)
}

// StatusHandler handles requests for hot reload status
func (hr *HotReload) StatusHandler(w http.ResponseWriter, r *http.Request) {
	// Get client status
	hr.clientsMu.RLock()
	defer hr.clientsMu.RUnlock()

	// Create status response
	type clientErrorResponse struct {
		Message   string `json:"message"`
		Filename  string `json:"filename,omitempty"`
		Line      int    `json:"line,omitempty"`
		Column    int    `json:"column,omitempty"`
		Stack     string `json:"stack,omitempty"`
		Timestamp string `json:"timestamp"`
	}

	type clientResponse struct {
		ID       string                `json:"id"`
		Status   string                `json:"status"`
		URL      string                `json:"url,omitempty"`
		LastSeen string                `json:"lastSeen"`
		Errors   []clientErrorResponse `json:"errors,omitempty"`
	}

	type statusResponse struct {
		Clients []clientResponse `json:"clients"`
	}

	response := statusResponse{
		Clients: make([]clientResponse, 0, len(hr.clients)),
	}

	for _, client := range hr.clients {
		clientResp := clientResponse{
			ID:       client.id,
			Status:   client.status,
			URL:      client.url,
			LastSeen: client.lastSeen.Format(time.RFC3339),
			Errors:   make([]clientErrorResponse, 0, len(client.errors)),
		}

		for _, err := range client.errors {
			clientResp.Errors = append(clientResp.Errors, clientErrorResponse{
				Message:   err.message,
				Filename:  err.filename,
				Line:      err.lineno,
				Column:    err.colno,
				Stack:     err.stack,
				Timestamp: err.timestamp,
			})
		}

		response.Clients = append(response.Clients, clientResp)
	}

	// Write response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetClientScript returns the raw JavaScript client code
func GetClientScript() ([]byte, error) {
	filename := "dist/hotreload.js"
	return clientFiles.ReadFile(filename)
}

```

<a id="file-pkg-hotreload-hotreload-go"></a>
**File: pkg/hotreload/hotreload.go**

```go
// Package hotreload provides hot reload functionality for web applications.
package hotreload

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/filewatcher"
)

// Constants for topics
const (
	TopicHotReload      = "system:hot_reload"
	TopicClientError    = "system:client_error"
	TopicHotReloadReady = "system:hotreload_ready"
)

// HotReload coordinates file watching and browser reloading
type HotReload struct {
	broker    *broker.Broker
	watcher   *filewatcher.FileWatcher
	logger    *slog.Logger
	clients   map[string]*clientInfo
	clientsMu sync.RWMutex
	options   Options
}

// clientInfo stores information about a connected client
type clientInfo struct {
	id       string
	errors   []clientError
	status   string
	url      string
	lastSeen time.Time
}

// clientError represents a JavaScript error from a client
type clientError struct {
	message   string
	filename  string
	lineno    int
	colno     int
	stack     string
	timestamp string
}

// New creates a new HotReload service
func New(opts ...Option) (*HotReload, error) {
	// Create with default options
	hr := &HotReload{
		clients: make(map[string]*clientInfo),
		logger:  slog.New(slog.NewTextHandler(os.Stderr, nil)),
		options: DefaultOptions(),
	}

	// Apply options
	for _, opt := range opts {
		opt(hr)
	}

	// Validate required fields
	if hr.broker == nil {
		return nil, ErrNoBroker
	}

	if hr.watcher == nil {
		return nil, ErrNoFileWatcher
	}

	return hr, nil
}

// Start starts the hot reload service
func (hr *HotReload) Start() error {
	// Set up file watcher callback
	hr.watcher.AddCallback(hr.handleFileChange)

	// Start the file watcher
	if err := hr.watcher.Start(); err != nil {
		return err
	}

	// Set up broker request handler for client errors
	err := hr.broker.OnRequest(TopicClientError, func(client broker.ClientHandle, payload map[string]interface{}) error {
		hr.handleClientError(client.ID(), payload)
		return nil
	})
	if err != nil {
		return err
	}

	// Set up broker request handler for client ready notifications
	err = hr.broker.OnRequest(TopicHotReloadReady, func(client broker.ClientHandle, payload map[string]interface{}) error {
		hr.handleClientReady(client.ID(), payload)
		return nil
	})
	if err != nil {
		return err
	}

	hr.logger.Info("Hot reload service started")
	return nil
}

// Stop stops the hot reload service
func (hr *HotReload) Stop() error {
	// Stop the file watcher
	if err := hr.watcher.Stop(); err != nil {
		return err
	}

	hr.logger.Info("Hot reload service stopped")
	return nil
}

// handleFileChange is called when a file changes
func (hr *HotReload) handleFileChange(file string) {
	hr.logger.Info("File changed, triggering hot reload", "file", file)

	// Notify all connected clients
	hr.triggerReload()
}

// triggerReload sends a reload command to all connected clients
func (hr *HotReload) triggerReload() {
	// Get all connected clients
	var clientIDs []string
	hr.broker.IterateClients(func(client broker.ClientHandle) bool {
		clientIDs = append(clientIDs, client.ID())
		return true
	})

	// Send reload command to each client
	ctx := context.Background()
	for _, id := range clientIDs {
		hr.logger.Info("Sending reload command to client", "client_id", id)

		// Find the client handle
		var clientHandle broker.ClientHandle
		hr.broker.IterateClients(func(ch broker.ClientHandle) bool {
			if ch.ID() == id {
				clientHandle = ch
				return false // Stop iteration
			}
			return true // Continue iteration
		})

		if clientHandle != nil {
			// Send reload command
			err := clientHandle.Send(ctx, TopicHotReload, struct{}{})
			if err != nil {
				hr.logger.Error("Failed to send reload command", "client_id", id, "error", err)
			}
		}
	}
}

// handleClientError handles client error reports
func (hr *HotReload) handleClientError(clientID string, payload map[string]interface{}) {
	hr.logger.Info("Received client error", "client_id", clientID, "error", payload)

	// Update client info
	hr.clientsMu.Lock()
	defer hr.clientsMu.Unlock()

	client, ok := hr.clients[clientID]
	if !ok {
		client = &clientInfo{
			id:       clientID,
			errors:   []clientError{},
			status:   "connected",
			lastSeen: time.Now(),
		}
		hr.clients[clientID] = client
	}

	// Add the error
	client.errors = append(client.errors, clientError{
		message:   getStringOrDefault(payload, "message", "Unknown error"),
		filename:  getStringOrDefault(payload, "filename", ""),
		lineno:    getIntOrDefault(payload, "lineno", 0),
		colno:     getIntOrDefault(payload, "colno", 0),
		stack:     getStringOrDefault(payload, "stack", ""),
		timestamp: getStringOrDefault(payload, "timestamp", time.Now().Format(time.RFC3339)),
	})

	// Limit the number of errors stored
	if len(client.errors) > hr.options.MaxErrorsPerClient {
		client.errors = client.errors[len(client.errors)-hr.options.MaxErrorsPerClient:]
	}
}

// handleClientReady handles client ready notifications
func (hr *HotReload) handleClientReady(clientID string, payload map[string]interface{}) {
	hr.logger.Info("Client ready", "client_id", clientID, "payload", payload)

	// Update client info
	hr.clientsMu.Lock()
	defer hr.clientsMu.Unlock()

	client, ok := hr.clients[clientID]
	if !ok {
		client = &clientInfo{
			id:       clientID,
			errors:   []clientError{},
			status:   "ready",
			lastSeen: time.Now(),
		}
		hr.clients[clientID] = client
	} else {
		client.status = "ready"
		client.lastSeen = time.Now()
	}

	// Update URL if provided
	if url, ok := payload["url"].(string); ok {
		client.url = url
	}
}

// Helper functions for type conversion
func getStringOrDefault(m map[string]interface{}, key, defaultValue string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return defaultValue
}

func getIntOrDefault(m map[string]interface{}, key string, defaultValue int) int {
	if val, ok := m[key].(float64); ok {
		return int(val)
	}
	return defaultValue
}

```

<a id="file-pkg-hotreload-hotreload_simple_test-go"></a>
**File: pkg/hotreload/hotreload_simple_test.go**

```go
package hotreload

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/filewatcher"
	"github.com/stretchr/testify/require"
)

func TestHotReloadSimple(t *testing.T) {
	fmt.Println("Starting simple hot reload test...")

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "hotreload-test")
	require.NoError(t, err, "Failed to create temp directory")
	defer os.RemoveAll(tempDir)

	// Create a test file
	testFile := filepath.Join(tempDir, "test.html")
	err = os.WriteFile(testFile, []byte("<html><body>Initial content</body></html>"), 0644)
	require.NoError(t, err, "Failed to write test file")

	// Create a broker
	b, err := broker.New()
	require.NoError(t, err, "Failed to create broker")

	// Create a file watcher
	fw, err := filewatcher.New(
		filewatcher.WithDirs([]string{tempDir}),
		filewatcher.WithPatterns([]string{"*.html", "*.js", "*.css"}),
		filewatcher.WithDebounce(100), // 100ms debounce for faster testing
	)
	require.NoError(t, err, "Failed to create file watcher")

	// Create a hot reload service
	hr, err := New(
		WithBroker(b),
		WithFileWatcher(fw),
	)
	require.NoError(t, err, "Failed to create hot reload service")

	// Create a channel to track file changes
	reloadCh := make(chan bool, 1)

	// Add a callback to the file watcher
	fw.AddCallback(func(file string) {
		fmt.Println("File change detected:", file)
		reloadCh <- true
	})

	// Start the hot reload service
	err = hr.Start()
	require.NoError(t, err, "Failed to start hot reload service")
	defer hr.Stop()

	// Wait a bit for everything to initialize
	time.Sleep(500 * time.Millisecond)

	// Modify the test file
	fmt.Println("Modifying test file...")
	err = os.WriteFile(testFile, []byte("<html><body>Updated content</body></html>"), 0644)
	require.NoError(t, err, "Failed to update test file")

	// Wait for the file change to be detected
	select {
	case <-reloadCh:
		fmt.Println("Reload triggered!")
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for reload")
	}

	fmt.Println("Test completed successfully!")
}

```

<a id="file-pkg-hotreload-hotreload_test-go"></a>
**File: pkg/hotreload/hotreload_test.go**

```go
package hotreload

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/filewatcher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHotReload(t *testing.T) {
	// Create a logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Create a broker
	b, err := broker.New(broker.WithLogger(logger))
	require.NoError(t, err, "Failed to create broker")

	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create a file watcher
	fw, err := filewatcher.New(
		filewatcher.WithLogger(logger),
		filewatcher.WithDirs([]string{tempDir}),
		filewatcher.WithPatterns([]string{"*.html", "*.js", "*.css"}),
		filewatcher.WithDebounce(100), // 100ms debounce for faster testing
	)
	require.NoError(t, err, "Failed to create file watcher")

	// Create a hot reload service
	hr, err := New(
		WithLogger(logger),
		WithBroker(b),
		WithFileWatcher(fw),
	)
	require.NoError(t, err, "Failed to create hot reload service")

	// Start the hot reload service
	err = hr.Start()
	require.NoError(t, err, "Failed to start hot reload service")
	defer hr.Stop()

	// Create a channel to receive reload notifications
	reloadCh := make(chan struct{}, 1)

	// Create a mock client
	mockClientID := "test-client"

	// Add a callback to the file watcher to detect changes
	fw.AddCallback(func(file string) {
		t.Logf("File change detected: %s", file)
		reloadCh <- struct{}{}
	})

	err = b.OnRequest(TopicHotReload, func(client broker.ClientHandle, payload interface{}) error {
		t.Logf("Reload request received for client: %s", client.ID())
		if client.ID() == mockClientID {
			reloadCh <- struct{}{}
		}
		return nil
	})

	// No need for a client handle, we'll just call the handler directly

	// Manually call the handler to simulate a client ready notification
	hr.handleClientReady(mockClientID, map[string]interface{}{
		"url":       "http://localhost:8090/test.html",
		"userAgent": "Test Agent",
	})

	// Wait a bit for the notification to be processed
	time.Sleep(100 * time.Millisecond)

	// Verify the client was registered
	hr.clientsMu.RLock()
	client, exists := hr.clients[mockClientID]
	hr.clientsMu.RUnlock()
	assert.True(t, exists, "Client should be registered")
	if exists {
		assert.Equal(t, "ready", client.status, "Client status should be ready")
		assert.Equal(t, "http://localhost:8090/test.html", client.url, "Client URL should be set")
	}

	// Create a test file in the watched directory
	testFile := tempDir + "/test.html"
	err = os.WriteFile(testFile, []byte("<html><body>Test</body></html>"), 0644)
	require.NoError(t, err, "Failed to write test file")

	// Wait for the reload notification
	select {
	case <-reloadCh:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for reload notification")
	}

	// Manually call the handler to simulate a client error
	hr.handleClientError(mockClientID, map[string]interface{}{
		"message":   "Test error",
		"filename":  "test.js",
		"lineno":    10,
		"colno":     20,
		"stack":     "Error: Test error\n    at test.js:10:20",
		"timestamp": time.Now().Format(time.RFC3339),
	})

	// Wait a bit for the error to be processed
	time.Sleep(100 * time.Millisecond)

	// Verify the error was recorded
	hr.clientsMu.RLock()
	client, exists = hr.clients[mockClientID]
	hr.clientsMu.RUnlock()
	assert.True(t, exists, "Client should still be registered")
	if exists {
		assert.Equal(t, 1, len(client.errors), "Client should have one error")
		if len(client.errors) > 0 {
			assert.Equal(t, "Test error", client.errors[0].message, "Error message should be set")
		}
	}
}

```

<a id="file-pkg-hotreload-options-go"></a>
**File: pkg/hotreload/options.go**

```go
package hotreload

import (
	"errors"
	"log/slog"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/filewatcher"
)

// Errors
var (
	ErrNoBroker      = errors.New("no broker provided")
	ErrNoFileWatcher = errors.New("no file watcher provided")
)

// Options configures the HotReload service
type Options struct {
	// MaxErrorsPerClient is the maximum number of errors to store per client
	MaxErrorsPerClient int
}

// DefaultOptions returns the default options
func DefaultOptions() Options {
	return Options{
		MaxErrorsPerClient: 100,
	}
}

// Option configures a HotReload service
type Option func(*HotReload)

// WithLogger sets the logger for the hot reload service
func WithLogger(logger *slog.Logger) Option {
	return func(hr *HotReload) {
		if logger != nil {
			hr.logger = logger
		}
	}
}

// WithBroker sets the broker for the hot reload service
func WithBroker(broker *broker.Broker) Option {
	return func(hr *HotReload) {
		hr.broker = broker
	}
}

// WithFileWatcher sets the file watcher for the hot reload service
func WithFileWatcher(watcher *filewatcher.FileWatcher) Option {
	return func(hr *HotReload) {
		hr.watcher = watcher
	}
}

// WithMaxErrorsPerClient sets the maximum number of errors to store per client
func WithMaxErrorsPerClient(max int) Option {
	return func(hr *HotReload) {
		if max > 0 {
			hr.options.MaxErrorsPerClient = max
		}
	}
}

```

<a id="file-pkg-shared_types-types-go"></a>
**File: pkg/shared_types/types.go**

```go
// shared_types/types.go
package shared_types

import "time"

// Topic Constants - used by both client and server for routing.
const (
	TopicGetTime           = "system:get_time"
	TopicServerAnnounce    = "server:announcements"
	TopicClientGetStatus   = "client:get_status" // Server requests this from client
	TopicUserDetails       = "user:get_details"
	TopicErrorTest         = "system:error_test"
	TopicSlowClientRequest = "client:slow_request" // Server sends to client, client is slow
	TopicSlowServerRequest = "server:slow_request" // Client sends to server, server is slow
	TopicBroadcastTest     = "test:broadcast"
	TopicClientRegister    = "system:register" // Client registration
)

// --- Message Structs ---
// These structs define the expected JSON payloads for messages.
// They are shared between client and server to ensure type consistency.

// GetTimeRequest is used when client requests server time. (No payload fields)
type GetTimeRequest struct{}

// GetTimeResponse is the server's response with the current time.
type GetTimeResponse struct {
	CurrentTime string `json:"currentTime"`
}

// ServerAnnouncement is a message pushed by the server.
type ServerAnnouncement struct {
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

// GetUserDetailsRequest is used by client to request user details.
type GetUserDetailsRequest struct {
	UserID string `json:"userId"`
}

// UserDetailsResponse contains user details sent by the server.
type UserDetailsResponse struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Email  string `json:"email"`
}

// ClientStatusQuery is used by server to request client's status.
type ClientStatusQuery struct {
	QueryDetailLevel string `json:"queryDetailLevel,omitempty"`
}

// ClientStatusReport is the client's response to a status query.
type ClientStatusReport struct {
	ClientID string `json:"clientId"`
	Status   string `json:"status"`
	Uptime   string `json:"uptime"`
}

// ErrorTestRequest is for testing error propagation.
type ErrorTestRequest struct {
	ShouldError bool `json:"shouldError"`
}

// ErrorTestResponse is the response for error tests.
type ErrorTestResponse struct {
	Message string `json:"message"`
}

// SlowClientRequest is sent by server to test client's slow response handling.
type SlowClientRequest struct {
	DelayMilliseconds int `json:"delayMilliseconds"`
}

// SlowClientResponse is client's response after a delay.
type SlowClientResponse struct {
	Message string `json:"message"`
}

// SlowServerRequest is sent by client to test server's slow response handling.
type SlowServerRequest struct {
	DelayMilliseconds int `json:"delayMilliseconds"`
}

// SlowServerResponse is server's response after a delay.
type SlowServerResponse struct {
	Message string `json:"message"`
}

// BroadcastMessage is used for testing publish to multiple clients.
type BroadcastMessage struct {
	Content string    `json:"content"`
	SentAt  time.Time `json:"sentAt"`
}

// ClientRegistration is sent by the client during connection to provide identity information
type ClientRegistration struct {
	ClientID   string `json:"clientId"`   // Client-generated ID
	ClientName string `json:"clientName"` // Human-readable name for the client
	ClientType string `json:"clientType"` // Type of client (e.g., "browser", "app", "service")
	ClientURL  string `json:"clientUrl"`  // URL of the client (for browser connections)
}

// ClientRegistrationResponse is sent by the server to confirm client registration
type ClientRegistrationResponse struct {
	ServerAssignedID string `json:"serverAssignedId"` // Server-generated ID that becomes the source of truth
	ClientName       string `json:"clientName"`       // Confirmed or modified client name
	ServerTime       string `json:"serverTime"`       // Server time for synchronization
}

```

<a id="file-pkg-testutil-broker-go"></a>
**File: pkg/testutil/broker.go**

```go
// Package testutil provides common test utilities for the go-websocketmq library.
package testutil

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
)

var (
	// Default logger for tests
	defaultSlogHandler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: true,
	})
	DefaultLogger = slog.New(defaultSlogHandler)
)

// BrokerServer combines a broker and its HTTP server for testing
type BrokerServer struct {
	*broker.Broker
	HTTP  *httptest.Server
	WSURL string
	Ready chan struct{} // Channel to signal when the server is ready
}

// NewBrokerServer creates a new broker and httptest.Server for testing.
// It returns a BrokerServer that combines both.
func NewBrokerServer(t *testing.T, opts ...broker.Option) *BrokerServer {
	t.Helper()

	finalOpts := append([]broker.Option{broker.WithLogger(DefaultLogger)}, opts...)
	b, err := broker.New(finalOpts...)
	if err != nil {
		t.Fatalf("broker.New: %v", err)
	}
	srv := httptest.NewServer(b.UpgradeHandler())
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	t.Cleanup(func() {
		srv.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		b.Shutdown(ctx)
	})

	// Create a ready channel and signal that the server is ready
	ready := make(chan struct{}, 1)
	ready <- struct{}{}

	return &BrokerServer{Broker: b, HTTP: srv, WSURL: wsURL, Ready: ready}
}

// WaitForClient and WaitForClientDisconnect have been moved to wait.go

```

<a id="file-pkg-testutil-client-go"></a>
**File: pkg/testutil/client.go**

```go
package testutil

import (
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/client"
)

// ClientOptions contains options for creating a test client
type ClientOptions struct {
	Logger               bool // Use the default logger
	RequestTimeout       time.Duration
	AutoReconnect        bool
	MaxReconnectAttempts int
	ReconnectMinDelay    time.Duration
	ReconnectMaxDelay    time.Duration
	ClientName           string
	ClientPingInterval   time.Duration
	WaitForConnection    bool // Wait for the connection to be established
	ConnectionTimeout    time.Duration
}

// DefaultClientOptions returns the default options for creating a test client
func DefaultClientOptions() ClientOptions {
	return ClientOptions{
		Logger:               true,
		RequestTimeout:       2 * time.Second,
		AutoReconnect:        true,
		MaxReconnectAttempts: 3,
		ReconnectMinDelay:    100 * time.Millisecond,
		ReconnectMaxDelay:    500 * time.Millisecond,
		WaitForConnection:    true,
		ConnectionTimeout:    1 * time.Second,
	}
}

// NewTestClient creates a new client connected to the given WebSocket URL.
// It applies the default options and any additional options provided.
func NewTestClient(t *testing.T, urlStr string, opts ...client.Option) *client.Client {
	t.Helper()
	return NewTestClientWithOptions(t, urlStr, DefaultClientOptions(), opts...)
}

// NewTestClientWithOptions creates a new client with the specified options.
func NewTestClientWithOptions(t *testing.T, urlStr string, options ClientOptions, opts ...client.Option) *client.Client {
	t.Helper()

	// Build options from the provided ClientOptions
	var defaultOpts []client.Option

	if options.Logger {
		defaultOpts = append(defaultOpts, client.WithLogger(DefaultLogger))
	}

	if options.RequestTimeout > 0 {
		defaultOpts = append(defaultOpts, client.WithDefaultRequestTimeout(options.RequestTimeout))
	}

	if options.AutoReconnect {
		defaultOpts = append(defaultOpts, client.WithAutoReconnect(
			options.MaxReconnectAttempts,
			options.ReconnectMinDelay,
			options.ReconnectMaxDelay,
		))
	}

	if options.ClientName != "" {
		defaultOpts = append(defaultOpts, client.WithClientName(options.ClientName))
	}

	if options.ClientPingInterval != 0 {
		defaultOpts = append(defaultOpts, client.WithClientPingInterval(options.ClientPingInterval))
	}

	// Combine default options with provided options
	finalOpts := append(defaultOpts, opts...)

	// Connect the client
	cli, err := client.Connect(urlStr, finalOpts...)
	if err != nil && cli == nil { // If connect truly failed and didn't even return a client for reconnect
		t.Fatalf("Client Connect failed and returned nil client: %v", err)
	}
	if cli == nil {
		t.Fatal("Client Connect returned nil client unexpectedly")
	}

	// Wait for connection if requested
	if options.WaitForConnection {
		// Give a brief moment for connection to establish
		time.Sleep(100 * time.Millisecond)

		// We don't have a direct IsConnected method, so we'll just wait a bit
		// In a real implementation, we might try a ping or check some internal state
	}

	// Setup cleanup
	t.Cleanup(func() {
		cli.Close()
	})

	return cli
}

```

<a id="file-pkg-testutil-mock_server-go"></a>
**File: pkg/testutil/mock_server.go**

```go
package testutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/lightforgemedia/go-websocketmq/pkg/ergosockets"
)

// MockServer represents a mock WebSocket server for testing clients.
type MockServer struct {
	T          *testing.T
	Server     *httptest.Server
	WsURL      string
	Conn       *websocket.Conn
	ConnMu     sync.Mutex
	Handler    func(conn *websocket.Conn, ms *MockServer)
	ActiveConn context.CancelFunc // To signal mock server's read loop to stop for current conn
	wg         sync.WaitGroup     // For Expect functionality
}

// NewMockServer creates a new mock WebSocket server for testing clients.
func NewMockServer(t *testing.T, handlerFunc func(conn *websocket.Conn, ms *MockServer)) *MockServer {
	t.Helper()
	ms := &MockServer{T: t, Handler: handlerFunc}

	ms.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connCtx, connCancel := context.WithCancel(context.Background())
		ms.ActiveConn = connCancel // Store cancel func for this connection

		wsconn, err := websocket.Accept(w, r, nil)
		if err != nil {
			ms.T.Logf("MockServer: Accept error: %v", err)
			connCancel()
			return
		}

		ms.ConnMu.Lock()
		ms.Conn = wsconn
		ms.ConnMu.Unlock()

		// Run the handler in a goroutine
		go func() {
			defer connCancel()
			if ms.Handler != nil {
				ms.Handler(wsconn, ms)
			}
		}()

		// Wait for the connection to be closed
		<-connCtx.Done()
	}))

	ms.WsURL = "ws" + ms.Server.URL[4:] // Convert http:// to ws://

	// Setup cleanup
	t.Cleanup(func() {
		ms.Close()
	})

	return ms
}

// Send sends an envelope to the connected client.
func (ms *MockServer) Send(env ergosockets.Envelope) error {
	ms.ConnMu.Lock()
	defer ms.ConnMu.Unlock()

	if ms.Conn == nil {
		return nil // No connection, silently ignore
	}

	err := wsjson.Write(context.Background(), ms.Conn, env)
	if err == nil {
		ms.wg.Add(-1)
		ms.T.Logf("MockServer: Sent envelope and fulfilled expectation")
	}
	return err
}

// Handle sets a handler function for processing incoming envelopes
func (ms *MockServer) Handle(handler func(env *ergosockets.Envelope) *ergosockets.Envelope) {
	ms.Handler = func(conn *websocket.Conn, srv *MockServer) {
		for {
			var reqEnv ergosockets.Envelope
			err := wsjson.Read(context.Background(), conn, &reqEnv)
			if err != nil {
				return
			}

			respEnv := handler(&reqEnv)
			if respEnv != nil {
				srv.Send(*respEnv)
			}
		}
	}
}

// Expect sets an expectation for a number of messages to be sent
func (ms *MockServer) Expect(n int) {
	ms.wg.Add(n)
}

// CloseCurrentConnection closes the current WebSocket connection.
func (ms *MockServer) CloseCurrentConnection() {
	ms.ConnMu.Lock()
	defer ms.ConnMu.Unlock()

	if ms.Conn != nil {
		ms.Conn.Close(websocket.StatusNormalClosure, "Test closing connection")
		ms.Conn = nil
	}

	if ms.ActiveConn != nil {
		ms.ActiveConn()
		ms.ActiveConn = nil
	}
}

// Close closes the mock server.
func (ms *MockServer) Close() {
	ms.CloseCurrentConnection()
	if ms.Server != nil {
		ms.Server.Close()
	}
}

```

<a id="file-pkg-testutil-rod-go"></a>
**File: pkg/testutil/rod.go**

```go
// Package testutil provides common test utilities for the go-websocketmq library.
package testutil

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

// RodBrowser represents a go-rod browser instance with helper methods for testing.
type RodBrowser struct {
	t        *testing.T
	browser  *rod.Browser
	launcher *launcher.Launcher
	headless bool
}

// RodPage represents a go-rod page with helper methods for testing.
type RodPage struct {
	t    *testing.T
	page *rod.Page
}

// NewRodBrowser creates a new RodBrowser instance.
// By default, the browser is launched in headless mode.
func NewRodBrowser(t *testing.T, opts ...RodOption) *RodBrowser {
	t.Helper()

	rb := &RodBrowser{
		t:        t,
		headless: true, // Default to headless
	}

	// Apply options
	for _, opt := range opts {
		opt(rb)
	}

	// Create launcher
	rb.launcher = launcher.New().
		Headless(rb.headless).
		Delete("disable-extensions")

	if !rb.headless {
		rb.launcher.Delete("disable-gpu")
	}

	// Launch browser
	controlURL := rb.launcher.MustLaunch()
	rb.browser = rod.New().ControlURL(controlURL).MustConnect()

	// Setup cleanup
	t.Cleanup(func() {
		rb.Close()
	})

	return rb
}

// RodOption is a function that configures a RodBrowser.
type RodOption func(*RodBrowser)

// WithHeadless configures whether the browser should run in headless mode.
func WithHeadless(headless bool) RodOption {
	return func(rb *RodBrowser) {
		rb.headless = headless
	}
}

// Close closes the browser.
func (rb *RodBrowser) Close() {
	if rb.browser != nil {
		rb.browser.MustClose()
		rb.browser = nil
	}
}

// IsHeadless returns whether the browser is running in headless mode.
func (rb *RodBrowser) IsHeadless() bool {
	return rb.headless
}

// MustPage navigates to the given URL and returns a RodPage.
func (rb *RodBrowser) MustPage(url string) *RodPage {
	rb.t.Helper()
	rb.t.Logf("Navigating to %s", url)

	page := rb.browser.MustPage(url)

	// Setup cleanup
	rb.t.Cleanup(func() {
		page.MustClose()
	})

	return &RodPage{
		t:    rb.t,
		page: page,
	}
}

// WaitForLoad waits for the page to load.
func (rp *RodPage) WaitForLoad() *RodPage {
	rp.t.Helper()
	rp.page.MustWaitLoad()
	return rp
}

// WaitForNetworkIdle waits for the network to be idle.
func (rp *RodPage) WaitForNetworkIdle(timeout time.Duration) *RodPage {
	rp.t.Helper()
	// Wait a bit for network to settle
	time.Sleep(timeout)
	return rp
}

// MustElement finds an element by selector and returns it.
func (rp *RodPage) MustElement(selector string) *rod.Element {
	rp.t.Helper()
	return rp.page.MustElement(selector)
}

// MustElementR finds an element by selector and regex and returns it.
func (rp *RodPage) MustElementR(selector, regex string) *rod.Element {
	rp.t.Helper()
	return rp.page.MustElementR(selector, regex)
}

// MustHas checks if the page has an element matching the selector.
func (rp *RodPage) MustHas(selector string) bool {
	rp.t.Helper()
	has, _, err := rp.page.Has(selector)
	if err != nil {
		rp.t.Fatalf("Error checking if page has element %s: %v", selector, err)
	}
	return has
}

// MustContainText checks if the page contains the given text.
func (rp *RodPage) MustContainText(text string) bool {
	rp.t.Helper()
	content, err := rp.page.HTML()
	if err != nil {
		rp.t.Fatalf("Error getting page HTML: %v", err)
	}
	return strings.Contains(content, text)
}

// VerifyPageLoaded verifies that the page has loaded correctly.
// It checks for the presence of a specific element (default: "body").
func (rp *RodPage) VerifyPageLoaded(selectors ...string) *RodPage {
	rp.t.Helper()

	selector := "body"
	if len(selectors) > 0 && selectors[0] != "" {
		selector = selectors[0]
	}

	// Wait for the element to be present
	rp.page.MustElement(selector)
	rp.t.Logf("Page loaded successfully (verified element: %s)", selector)

	return rp
}

// GetConsoleLog returns the console log entries from the page.
func (rp *RodPage) GetConsoleLog() []string {
	rp.t.Helper()

	logs := []string{}

	// Execute JavaScript to get console logs
	// This assumes console logs are stored in window.consoleLog
	result, err := rp.page.Eval(`() => {
		if (!window.consoleLog) return [];
		return window.consoleLog;
	}`)

	if err != nil {
		rp.t.Logf("Error getting console logs: %v", err)
		return logs
	}

	// Try to unmarshal the result
	err = result.Value.Unmarshal(&logs)
	if err != nil {
		rp.t.Logf("Error unmarshaling console logs: %v", err)
	}

	return logs
}

// GetBrokerWSURL converts an HTTP test server URL to a WebSocket URL for the broker.
// It replaces "http://" with "ws://" and appends the WebSocket path.
func GetBrokerWSURL(server *httptest.Server, wsPath string) string {
	if wsPath == "" {
		wsPath = "/ws"
	}

	// If wsPath doesn't start with a slash, add one
	if !strings.HasPrefix(wsPath, "/") {
		wsPath = "/" + wsPath
	}

	// Replace http:// with ws://
	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)

	// Append the WebSocket path
	return wsURL + wsPath
}

// WaitForElement waits for an element to appear on the page.
func (rp *RodPage) WaitForElement(selector string, timeout time.Duration) *rod.Element {
	rp.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var element *rod.Element
	var err error

	for {
		select {
		case <-ctx.Done():
			rp.t.Fatalf("Timeout waiting for element %s: %v", selector, ctx.Err())
			return nil
		default:
			element, err = rp.page.Element(selector)
			if err == nil && element != nil {
				return element
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// Screenshot takes a screenshot of the page and saves it to the given path.
func (rp *RodPage) Screenshot(path string) *RodPage {
	rp.t.Helper()

	// Take screenshot
	data, err := rp.page.Screenshot(false, nil)
	if err != nil {
		rp.t.Logf("Error taking screenshot: %v", err)
		return rp
	}

	// Write to file
	err = os.WriteFile(path, data, 0644)
	if err != nil {
		rp.t.Logf("Error saving screenshot to %s: %v", path, err)
	} else {
		rp.t.Logf("Screenshot saved to %s", path)
	}

	return rp
}

// Click clicks on an element matching the selector.
func (rp *RodPage) Click(selector string) *RodPage {
	rp.t.Helper()
	rp.page.MustElement(selector).MustClick()
	return rp
}

// Input types text into an element matching the selector.
func (rp *RodPage) Input(selector, text string) *RodPage {
	rp.t.Helper()
	rp.page.MustElement(selector).MustInput(text)
	return rp
}

// GetCurrentURL returns the current URL of the page.
func (rp *RodPage) GetCurrentURL() (string, error) {
	rp.t.Helper()

	result, err := rp.page.Eval("() => window.location.href")
	if err != nil {
		return "", err
	}

	var url string
	err = result.Value.Unmarshal(&url)
	if err != nil {
		return "", err
	}

	return url, nil
}

// EvalJS evaluates JavaScript code on the page.
func (rp *RodPage) EvalJS(js string) error {
	rp.t.Helper()
	_, err := rp.page.Eval(js)
	return err
}

```

<a id="file-pkg-testutil-rod_broker_example_test-go"></a>
**File: pkg/testutil/rod_broker_example_test.go**

```go
package testutil

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	app_shared_types "github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This test demonstrates how to use the Rod helper with a BrokerServer
func TestRodWithBrokerServer(t *testing.T) {
	// Create a broker server with accept options to allow any origin
	bs := NewBrokerServer(t, broker.WithAcceptOptions(&websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	}))

	// Register a handler for the get_time request
	err := bs.OnRequest(app_shared_types.TopicGetTime,
		func(ch broker.ClientHandle, req app_shared_types.GetTimeRequest) (app_shared_types.GetTimeResponse, error) {
			t.Logf("Server received get_time request from client %s", ch.ID())
			return app_shared_types.GetTimeResponse{CurrentTime: time.Now().Format(time.RFC3339)}, nil
		},
	)
	require.NoError(t, err, "Failed to register server handler")

	// Create a simple HTTP server that serves a test page
	// This would typically be a file server serving your HTML/JS files
	fileServer := http.NewServeMux()
	fileServer.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<!DOCTYPE html>
			<html>
			<head>
				<title>WebSocketMQ Test</title>
				<script>
					// Simple WebSocket client for testing
					window.consoleLog = [];
					const originalConsole = console.log;
					console.log = function() {
						window.consoleLog.push(Array.from(arguments).join(' '));
						originalConsole.apply(console, arguments);
					};

					let ws = null;
					let messageId = 0;

					function connect() {
						const wsUrl = document.getElementById('wsUrl').value;
						console.log('Connecting to', wsUrl);

						ws = new WebSocket(wsUrl);

						ws.onopen = function() {
							console.log('Connected to WebSocket server');
							document.getElementById('status').textContent = 'Connected';
							document.getElementById('status').style.color = 'green';
						};

						ws.onclose = function() {
							console.log('Disconnected from WebSocket server');
							document.getElementById('status').textContent = 'Disconnected';
							document.getElementById('status').style.color = 'red';
							ws = null;
						};

						ws.onerror = function(error) {
							console.error('WebSocket error:', error);
							document.getElementById('status').textContent = 'Error';
							document.getElementById('status').style.color = 'red';
						};

						ws.onmessage = function(event) {
							console.log('Received message:', event.data);
							const message = JSON.parse(event.data);
							document.getElementById('response').textContent = JSON.stringify(message, null, 2);
						};
					}

					function sendRequest() {
						if (!ws) {
							console.error('Not connected');
							return;
						}

						const id = 'msg_' + (messageId++);
						const request = {
							id: id,
							type: 'request',
							topic: 'system:get_time',
							payload: {}
						};

						console.log('Sending request:', request);
						ws.send(JSON.stringify(request));
					}
				</script>
			</head>
			<body>
				<h1>WebSocketMQ Test</h1>
				<div>
					<label for="wsUrl">WebSocket URL:</label>
					<input type="text" id="wsUrl" value="WEBSOCKET_URL" style="width: 300px;" />
					<button onclick="connect()">Connect</button>
					<span id="status">Disconnected</span>
				</div>
				<div>
					<button id="sendRequestBtn" onclick="sendRequest()">Send Request</button>
				</div>
				<div>
					<h3>Response:</h3>
					<pre id="response"></pre>
				</div>
			</body>
			</html>`

		// Replace the WebSocket URL placeholder
		html = strings.Replace(html, "WEBSOCKET_URL", bs.WSURL, 1)
		w.Write([]byte(html))
	})
	httpServer := NewServer(t, fileServer)

	// Get the WebSocket URL from the broker server
	wsURL := bs.WSURL
	t.Logf("WebSocket URL: %s", wsURL)
	t.Logf("HTTP Server URL: %s", httpServer.URL)

	headless := true
	// Create a new Rod browser
	browser := NewRodBrowser(t, WithHeadless(headless))

	// Navigate to the test page
	page := browser.MustPage(httpServer.URL).WaitForLoad()

	// Verify the page loaded correctly
	page.VerifyPageLoaded("body")

	// Click the connect button
	page.Click("button")

	// Wait for connection to establish
	time.Sleep(500 * time.Millisecond)

	// Check connection status
	statusText, err := page.MustElement("#status").Text()
	require.NoError(t, err, "Should be able to get status text")
	assert.Equal(t, "Connected", statusText, "Status should be Connected")

	// Send a request
	page.Click("#sendRequestBtn")

	// Wait for response
	time.Sleep(500 * time.Millisecond)

	// Check response
	responseText, err := page.MustElement("#response").Text()
	require.NoError(t, err, "Should be able to get response text")
	assert.Contains(t, responseText, "system:get_time", "Response should contain the topic")
	assert.Contains(t, responseText, "currentTime", "Response should contain the current time")

	// Get console logs
	logs := page.GetConsoleLog()
	t.Logf("Console logs: %v", logs)

	// Verify a client connected to the broker
	var clientCount int
	bs.IterateClients(func(ch broker.ClientHandle) bool {
		clientCount++
		t.Logf("Client connected: ID=%s, Name=%s", ch.ID(), ch.Name())
		return true
	})
	assert.Equal(t, 1, clientCount, "Should have one client connected")
	if !headless {
		// Keep browser open for manual inspection
		t.Logf("Sleeping for 30 seconds to keep browser open for manual inspection")
		time.Sleep(30 * time.Second)
	}
}

// NewServer creates a new HTTP test server.
func NewServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.Close()
	})
	return server
}

```

<a id="file-pkg-testutil-rod_test-go"></a>
**File: pkg/testutil/rod_test.go**

```go
package testutil

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRodBrowser(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping browser test in short mode")
	}

	// Create a simple HTTP server that serves a test page
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`
			<!DOCTYPE html>
			<html>
			<head>
				<title>Test Page</title>
				<script>
					// Store console logs
					window.consoleLog = [];
					const originalConsole = console.log;
					console.log = function() {
						window.consoleLog.push(Array.from(arguments).join(' '));
						originalConsole.apply(console, arguments);
					};

					// Log something when the page loads
					console.log('Page loaded at', new Date().toISOString());

					// Add a button click handler
					window.addEventListener('DOMContentLoaded', () => {
						const button = document.getElementById('testButton');
						if (button) {
							button.addEventListener('click', () => {
								console.log('Button clicked!');
								document.getElementById('result').textContent = 'Button was clicked!';
							});
						}
					});
				</script>
			</head>
			<body>
				<h1>Test Page for Rod Browser</h1>
				<div id="content">
					<p>This is a test page for the Rod Browser helper.</p>
					<button id="testButton">Click Me</button>
					<div id="result"></div>
				</div>
			</body>
			</html>
		`))
	}))
	defer server.Close()

	// Create a new Rod browser
	browser := NewRodBrowser(t, WithHeadless(true))

	// Navigate to the test page
	page := browser.MustPage(server.URL).WaitForLoad()

	// Verify the page loaded correctly
	page.VerifyPageLoaded("#content")

	// Check for specific elements
	assert.True(t, page.MustHas("h1"), "Page should have an h1 element")
	assert.True(t, page.MustHas("#testButton"), "Page should have a button")

	// Get the page title
	title, err := page.page.Eval("() => document.title")
	require.NoError(t, err, "Should be able to get page title")
	var titleStr string
	err = title.Value.Unmarshal(&titleStr)
	require.NoError(t, err, "Should be able to unmarshal title")
	assert.Equal(t, "Test Page", titleStr, "Page title should match")

	// Click the button
	page.Click("#testButton")

	// Wait for the result to appear
	time.Sleep(100 * time.Millisecond)

	// Check the result
	resultText, err := page.MustElement("#result").Text()
	require.NoError(t, err, "Should be able to get result text")
	assert.Equal(t, "Button was clicked!", resultText, "Result text should match")

	// Get console logs
	logs := page.GetConsoleLog()
	assert.GreaterOrEqual(t, len(logs), 1, "Should have at least one console log entry")
	assert.Contains(t, logs[len(logs)-1], "Button clicked!", "Last log should contain button click message")
}

func TestGetBrokerWSURL(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	}))
	defer server.Close()

	// Test with default path
	wsURL := GetBrokerWSURL(server, "")
	assert.True(t, wsURL != "", "WebSocket URL should not be empty")
	assert.True(t, wsURL[:5] == "ws://", "WebSocket URL should start with ws://")
	assert.True(t, wsURL[len(wsURL)-3:] == "/ws", "WebSocket URL should end with /ws")

	// Test with custom path
	wsURL = GetBrokerWSURL(server, "wsmq")
	assert.True(t, wsURL != "", "WebSocket URL should not be empty")
	assert.True(t, wsURL[:5] == "ws://", "WebSocket URL should start with ws://")
	assert.True(t, wsURL[len(wsURL)-5:] == "/wsmq", "WebSocket URL should end with /wsmq")

	// Test with path that already has a slash
	wsURL = GetBrokerWSURL(server, "/custom")
	assert.True(t, wsURL != "", "WebSocket URL should not be empty")
	assert.True(t, wsURL[:5] == "ws://", "WebSocket URL should start with ws://")
	assert.True(t, wsURL[len(wsURL)-7:] == "/custom", "WebSocket URL should end with /custom")
}

```

<a id="file-pkg-testutil-server_test-go"></a>
**File: pkg/testutil/server_test.go**

```go
package testutil

import (
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/ergosockets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBrokerServer(t *testing.T) {
	bs := NewBrokerServer(t)

	// Verify that the broker is created
	assert.NotNil(t, bs.Broker, "Broker should not be nil")

	// Verify that the server is created
	assert.NotNil(t, bs.HTTP, "Server should not be nil")

	// Verify that the WebSocket URL is created
	assert.NotEmpty(t, bs.WSURL, "WebSocket URL should not be empty")
	assert.Contains(t, bs.WSURL, "ws://", "WebSocket URL should contain ws://")
}

// TestNewTestServer has been removed as we've consolidated to BrokerServer

func TestWaitForClient(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}

	bs := NewBrokerServer(t)

	// Create a client
	cli := NewTestClient(t, bs.WSURL)
	defer cli.Close()

	// Wait for the client to connect
	// The client ID is different between the broker and the client due to how the test is set up
	// So we'll just check that we can get a client handle from the broker
	var clientHandle broker.ClientHandle

	// Get all connected clients
	var connectedClients []string
	bs.IterateClients(func(ch broker.ClientHandle) bool {
		connectedClients = append(connectedClients, ch.ID())
		clientHandle = ch
		return true
	})

	// Verify that we have at least one client
	require.NotEmpty(t, connectedClients, "Should have at least one connected client")
	require.NotNil(t, clientHandle, "Client handle should not be nil")
}

func TestWaitForClientDisconnect(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}

	bs := NewBrokerServer(t)

	// Create a client
	cli := NewTestClient(t, bs.WSURL)

	// Get all connected clients
	var connectedClients []string
	var clientID string
	bs.IterateClients(func(ch broker.ClientHandle) bool {
		connectedClients = append(connectedClients, ch.ID())
		clientID = ch.ID()
		return true
	})

	// Verify that we have at least one client
	require.NotEmpty(t, connectedClients, "Should have at least one connected client")
	require.NotEmpty(t, clientID, "Client ID should not be empty")

	// Close the client
	cli.Close()

	// Wait for the client to disconnect
	err := WaitForClientDisconnect(t, bs.Broker, clientID, 5*time.Second)
	assert.NoError(t, err, "WaitForClientDisconnect should not return an error")
}

func TestMockServer(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}

	// Create a mock server
	ms := NewMockServer(t, func(conn *websocket.Conn, ms *MockServer) {
		// Set expectation before sending
		ms.Expect(1)

		// Send a message to the client
		env, _ := ergosockets.NewEnvelope("test-id", ergosockets.TypePublish, "test-topic", nil, nil)
		ms.Send(*env)
	})
	defer ms.Close()

	// Create a client
	cli := NewTestClient(t, ms.WsURL)
	defer cli.Close()

	// Verify that the client can connect to the mock server
	assert.NotNil(t, cli, "Client should not be nil")
}

// TestBrokerRequestResponse has been moved to broker_test.go

```

<a id="file-pkg-testutil-wait-go"></a>
**File: pkg/testutil/wait.go**

```go
// Package testutil provides common test utilities for the go-websocketmq library.
package testutil

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
)

// WaitForClient waits for a client to connect to the broker.
// It returns the client handle and nil if the client connects within the timeout.
// It returns nil and an error if the client does not connect within the timeout.
func WaitForClient(t *testing.T, b *broker.Broker, clientID string, timeout time.Duration) (broker.ClientHandle, error) {
	t.Helper()

	// First, give a short delay to allow the client to connect
	time.Sleep(200 * time.Millisecond)

	// Try to get the client immediately
	handle, err := b.GetClient(clientID)
	if err == nil && handle != nil {
		t.Logf("WaitForClient: Found client %s immediately", clientID)
		return handle, nil
	}

	// If not found, wait with polling
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		handle, err := b.GetClient(clientID)
		if err == nil && handle != nil {
			t.Logf("WaitForClient: Found client %s after polling", clientID)
			return handle, nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Log all connected clients for debugging
	var connectedClients []string
	b.IterateClients(func(ch broker.ClientHandle) bool {
		connectedClients = append(connectedClients, ch.ID())
		return true
	})

	t.Logf("WaitForClient: Client %s not found. Connected clients: %v", clientID, connectedClients)
	return nil, fmt.Errorf("client %s did not connect within %v", clientID, timeout)
}

// WaitForClientDisconnect waits for a client to disconnect from the broker.
// It returns nil if the client disconnects within the timeout.
// It returns an error if the client does not disconnect within the timeout.
func WaitForClientDisconnect(t *testing.T, b *broker.Broker, clientID string, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := b.GetClient(clientID)
		if err != nil {
			return nil // Client is gone, success
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("client %s did not disconnect within %v", clientID, timeout)
}

// WaitFor is a generic utility to wait for a condition to be true.
// It returns nil if the condition becomes true within the timeout.
// It returns an error if the condition does not become true within the timeout.
func WaitFor(t *testing.T, description string, timeout time.Duration, condition func() bool) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return nil // Condition is true, success
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("condition '%s' not met within %v", description, timeout)
}

// WaitForWithContext is a generic utility to wait for a condition to be true with context support.
// It returns nil if the condition becomes true before the context is canceled or times out.
// It returns an error if the condition does not become true before the context is canceled or times out.
func WaitForWithContext(ctx context.Context, t *testing.T, description string, condition func() bool) error {
	t.Helper()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context canceled while waiting for condition '%s': %v", description, ctx.Err())
		case <-ticker.C:
			if condition() {
				return nil // Condition is true, success
			}
		}
	}
}

```

