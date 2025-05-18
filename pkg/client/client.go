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

// WithContext sets a parent context for the client. When the parent context is cancelled,
// the client will shut down all operations. This allows integrating the client's lifetime
// with application-level context management.
// WithContext sets a custom context for the client, allowing for external
// lifecycle management and cancellation control. The provided context will be
// used as the parent context for all client operations.
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
//	defer cancel()
//	client, err := Connect("ws://localhost:8080/ws", WithContext(ctx))
func WithContext(ctx context.Context) Option {
	return func(c *Client) {
		c.clientCtx, c.clientCancel = context.WithCancel(ctx)
	}
}

// WithWriteTimeout sets the write timeout for sending messages to the server.
func WithWriteTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		if timeout > 0 {
			c.config.writeTimeout = timeout
		}
	}
}

// WithReadTimeout sets the read timeout for responses from the server.
func WithReadTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		if timeout > 0 {
			c.config.readTimeout = timeout
		}
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
		c.config.logger.Info(fmt.Sprintf("Client %s: HandleServerRequest handler for server topic '%s' returned error: %v", c.id, reqEnv.Topic, errResult))
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

// SendServerRequest sends a request to the server and waits for a response of type T.
// The first optional payload argument (reqData) is the request data.
// If no reqData is provided, a null payload is sent.
func (c *Client) SendServerRequest(ctx context.Context, topic string, reqData ...interface{}) (*json.RawMessage, *ergosockets.ErrorPayload, error) {
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

// HandleServerRequest registers a handler for requests initiated by the server on a given topic.
// handlerFunc must be of type: func(ReqStruct) (RespStruct, error) or func(ReqStruct) error
// or func(*ReqStruct) (*RespStruct, error) etc.
func (c *Client) HandleServerRequest(topic string, handlerFunc interface{}) error {
	c.closedMu.Lock()
	if c.isClosed {
		c.closedMu.Unlock()
		return errors.New("client is closed")
	}
	c.closedMu.Unlock()

	hw, err := ergosockets.NewHandlerWrapper(handlerFunc)
	if err != nil {
		return fmt.Errorf("client HandleServerRequest topic '%s': %w", topic, err)
	}
	// Validate client HandleServerRequest signature (1 in, 1 or 2 out with last as error)
	if hw.HandlerFunc.Type().NumIn() != 1 {
		return fmt.Errorf("client HandleServerRequest topic '%s': handler must have 1 input argument (RequestType), got %d", topic, hw.HandlerFunc.Type().NumIn())
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
	rawPayload, serverErrPayload, err := cli.SendServerRequest(ctx, topic, reqData...)
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
