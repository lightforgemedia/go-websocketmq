package ps_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps/testutil"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"github.com/lightforgemedia/go-websocketmq/pkg/server"
	"nhooyr.io/websocket"
)

// TestServer represents a WebSocketMQ server for testing
type TestServer struct {
	Server   *httptest.Server
	Broker   broker.Broker
	Handler  *server.Handler
	Logger   *testutil.TestLogger
	Ready    chan struct{}
	Shutdown chan struct{}
}

// NewTestServer creates and starts a new test server
func NewTestServer(t *testing.T) *TestServer {
	ts := &TestServer{
		Logger:   testutil.NewTestLogger(t),
		Ready:    make(chan struct{}),
		Shutdown: make(chan struct{}),
	}

	// Create broker
	brokerOpts := broker.DefaultOptions()
	ts.Broker = ps.New(ts.Logger, brokerOpts)

	// Create WebSocket handler
	handlerOpts := server.DefaultHandlerOptions()
	ts.Handler = server.NewHandler(ts.Broker, ts.Logger, handlerOpts)

	// Create HTTP server
	mux := http.NewServeMux()
	mux.Handle("/ws", ts.Handler)

	// Start server
	ts.Server = httptest.NewServer(mux)

	// Signal that the server is ready
	close(ts.Ready)

	return ts
}

// RegisterHandler registers an RPC handler on the server
func (ts *TestServer) RegisterHandler(topic string, handler broker.MessageHandler) error {
	return ts.Broker.Subscribe(context.Background(), topic, handler)
}

// SendRPCToClient sends an RPC request to a specific client
func (ts *TestServer) SendRPCToClient(ctx context.Context, clientID string, topic string, body interface{}, timeoutMs int64) (*model.Message, error) {
	req := model.NewRequest(topic, body, timeoutMs)
	return ts.Broker.RequestToClient(ctx, clientID, req, timeoutMs)
}

// Close shuts down the test server
func (ts *TestServer) Close() {
	ts.Server.Close()
	ts.Broker.Close()
	close(ts.Shutdown)
}

// TestClient represents a WebSocketMQ client for testing
type TestClient struct {
	Conn             *websocket.Conn
	URL              string
	PageSessionID    string
	BrokerClientID   string
	Logger           *testutil.TestLogger
	Connected        chan struct{}
	Closed           chan struct{}
	Handlers         map[string]broker.MessageHandler
	HandlersMutex    sync.RWMutex
	ResponseChannels map[string]chan *model.Message
	ResponseMutex    sync.RWMutex
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup
}

// NewTestClient creates a new test client
func NewTestClient(t *testing.T, serverURL string) *TestClient {
	wsURL := strings.Replace(serverURL, "http://", "ws://", 1) + "/ws"

	ctx, cancel := context.WithCancel(context.Background())

	tc := &TestClient{
		URL:              wsURL,
		PageSessionID:    "test-session-" + model.RandomID(),
		Logger:           testutil.NewTestLogger(t),
		Connected:        make(chan struct{}),
		Closed:           make(chan struct{}),
		Handlers:         make(map[string]broker.MessageHandler),
		ResponseChannels: make(map[string]chan *model.Message),
		ctx:              ctx,
		cancel:           cancel,
	}

	return tc
}

// Connect establishes a WebSocket connection to the server
func (tc *TestClient) Connect(ctx context.Context) error {
	var err error
	tc.Conn, _, err = websocket.Dial(ctx, tc.URL, nil)
	if err != nil {
		return err
	}

	// Start message handler
	tc.wg.Add(1)
	go tc.handleMessages()

	// Subscribe to the client registration topic
	tc.RegisterHandler(broker.TopicClientRegistered, func(ctx context.Context, msg *model.Message, _ string) (*model.Message, error) {
		tc.Logger.Debug("Received client registration message in handler: %+v", msg)
		if bodyMap, ok := msg.Body.(map[string]interface{}); ok {
			if clientID, ok := bodyMap["brokerClientID"].(string); ok {
				tc.BrokerClientID = clientID
				tc.Logger.Debug("Set broker client ID from handler: %s", clientID)
			}
		}
		return nil, nil
	})

	// Register with the server
	err = tc.register(ctx)
	if err != nil {
		tc.Conn.Close(websocket.StatusInternalError, "registration failed")
		return err
	}

	close(tc.Connected)
	return nil
}

// register sends a registration message to the server
func (tc *TestClient) register(ctx context.Context) error {
	regMsg := model.NewEvent("_client.register", map[string]string{
		"pageSessionID": tc.PageSessionID,
	})

	return tc.sendMessage(ctx, regMsg)
}

// sendMessage sends a message to the server
func (tc *TestClient) sendMessage(ctx context.Context, msg *model.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	return tc.Conn.Write(ctx, websocket.MessageText, data)
}

// handleMessages processes incoming messages from the server
func (tc *TestClient) handleMessages() {
	defer tc.wg.Done()
	defer close(tc.Closed)

	for {
		select {
		case <-tc.ctx.Done():
			return
		default:
			// Set a read deadline to allow checking tc.ctx.Done() periodically
			readCtx, cancelRead := context.WithTimeout(tc.ctx, 5*time.Second)
			msgType, data, err := tc.Conn.Read(readCtx)
			cancelRead()

			if err != nil {
				if tc.ctx.Err() != nil {
					// Context canceled, expected closure
					return
				}

				// Check for normal closure
				closeStatus := websocket.CloseStatus(err)
				if closeStatus == websocket.StatusNormalClosure ||
					closeStatus == websocket.StatusGoingAway {
					tc.Logger.Debug("WebSocket closed normally")
					return
				}

				if errors.Is(err, context.DeadlineExceeded) {
					// Read timeout, loop again
					continue
				}

				tc.Logger.Error("Error reading message: %v", err)
				return
			}

			if msgType != websocket.MessageText {
				tc.Logger.Warn("Received non-text message, ignoring")
				continue
			}

			var msg model.Message
			if err := json.Unmarshal(data, &msg); err != nil {
				tc.Logger.Error("Error unmarshaling message: %v", err)
				continue
			}

			tc.Logger.Debug("Received message: Type=%s, Topic=%s, CorrID=%s",
				msg.Header.Type, msg.Header.Topic, msg.Header.CorrelationID)

			// Store broker client ID if this is a registration response
			if msg.Header.Topic == broker.TopicClientRegistered {
				tc.Logger.Debug("Received registration response: %+v", msg.Body)
				if bodyMap, ok := msg.Body.(map[string]interface{}); ok {
					if clientID, ok := bodyMap["brokerClientID"].(string); ok {
						tc.BrokerClientID = clientID
						tc.Logger.Debug("Received broker client ID: %s", clientID)
					}
				}
			}

			// First check if there's a handler registered for this topic
			tc.HandlersMutex.RLock()
			handler, handlerExists := tc.Handlers[msg.Header.Topic]

			// Also check for wildcard handlers
			wildcardHandler, wildcardExists := tc.Handlers["#"]
			tc.HandlersMutex.RUnlock()

			// If there's a specific handler for this topic, call it
			if handlerExists {
				tc.Logger.Debug("Found handler for topic %s", msg.Header.Topic)
				go func(m model.Message) {
					handler(tc.ctx, &m, "")
				}(msg)
			}

			// If there's a wildcard handler, call it too
			if wildcardExists {
				tc.Logger.Debug("Found wildcard handler for topic %s", msg.Header.Topic)
				go func(m model.Message) {
					wildcardHandler(tc.ctx, &m, "")
				}(msg)
			}

			// Handle different message types
			switch msg.Header.Type {
			case model.KindRequest:
				// Handle RPC request from server
				go tc.handleRequest(tc.ctx, &msg)
			case model.KindResponse, model.KindError:
				// Handle RPC response via registered response channels
				tc.ResponseMutex.RLock()
				ch, exists := tc.ResponseChannels[msg.Header.CorrelationID]
				tc.ResponseMutex.RUnlock()

				if exists {
					tc.Logger.Debug("Found response channel for CorrID %s", msg.Header.CorrelationID)
					select {
					case ch <- &msg:
						tc.Logger.Debug("Response delivered for CorrID %s", msg.Header.CorrelationID)
					default:
						tc.Logger.Warn("Response channel full or closed for CorrID %s",
							msg.Header.CorrelationID)
					}
				} else {
					tc.Logger.Warn("No response channel found for CorrID %s", msg.Header.CorrelationID)
				}
			}
		}
	}
}

// handleRequest processes an RPC request from the server
func (tc *TestClient) handleRequest(ctx context.Context, req *model.Message) {
	tc.HandlersMutex.RLock()
	handler, exists := tc.Handlers[req.Header.Topic]
	tc.HandlersMutex.RUnlock()

	if !exists {
		// No handler for this topic
		errMsg := model.NewErrorMessage(req, map[string]string{
			"error": "no handler for topic: " + req.Header.Topic,
		})
		tc.sendMessage(ctx, errMsg)
		return
	}

	// Call the handler
	resp, err := handler(ctx, req, "")
	if err != nil {
		// Handler returned an error
		errMsg := model.NewErrorMessage(req, map[string]string{
			"error": err.Error(),
		})
		tc.sendMessage(ctx, errMsg)
		return
	}

	// Send the response if one was returned
	if resp != nil {
		tc.sendMessage(ctx, resp)
	}
}

// RegisterHandler registers an RPC handler for a specific topic
func (tc *TestClient) RegisterHandler(topic string, handler broker.MessageHandler) {
	tc.HandlersMutex.Lock()
	tc.Handlers[topic] = handler
	tc.HandlersMutex.Unlock()
}

// SendRPC sends an RPC request to the server and waits for a response
func (tc *TestClient) SendRPC(ctx context.Context, topic string, body interface{}, timeoutMs int64) (*model.Message, error) {
	// Create a request message
	req := model.NewRequest(topic, body, timeoutMs)

	tc.Logger.Debug("Sending RPC request: Topic=%s, CorrID=%s", topic, req.Header.CorrelationID)

	// Create a channel to receive the response
	respChan := make(chan *model.Message, 1)

	// Register the response channel
	tc.ResponseMutex.Lock()
	tc.ResponseChannels[req.Header.CorrelationID] = respChan
	tc.ResponseMutex.Unlock()

	tc.Logger.Debug("Registered response channel for CorrID %s", req.Header.CorrelationID)

	// Clean up when done
	defer func() {
		tc.ResponseMutex.Lock()
		delete(tc.ResponseChannels, req.Header.CorrelationID)
		tc.ResponseMutex.Unlock()
		tc.Logger.Debug("Cleaned up response channel for CorrID %s", req.Header.CorrelationID)
	}()

	// Send the request
	if err := tc.sendMessage(ctx, req); err != nil {
		return nil, err
	}

	tc.Logger.Debug("Waiting for response to CorrID %s", req.Header.CorrelationID)

	// Wait for the response or timeout
	select {
	case <-ctx.Done():
		tc.Logger.Debug("Context done while waiting for response to CorrID %s", req.Header.CorrelationID)
		return nil, ctx.Err()
	case resp := <-respChan:
		tc.Logger.Debug("Received response for CorrID %s", req.Header.CorrelationID)
		return resp, nil
	case <-time.After(time.Duration(timeoutMs) * time.Millisecond):
		tc.Logger.Debug("Timed out waiting for response to CorrID %s", req.Header.CorrelationID)
		return nil, fmt.Errorf("request timed out")
	}
}

// Close closes the WebSocket connection
func (tc *TestClient) Close() error {
	tc.cancel() // Signal handleMessages to stop

	if tc.Conn != nil {
		err := tc.Conn.Close(websocket.StatusNormalClosure, "client closing")
		tc.wg.Wait() // Wait for handleMessages to exit
		return err
	}

	return nil
}

// TestBasicConnectivity tests that a client can connect to the server
func TestBasicConnectivity(t *testing.T) {
	// Start the server
	server := NewTestServer(t)
	defer server.Close()

	// Wait for the server to be ready
	<-server.Ready

	// Create and connect a client
	client := NewTestClient(t, server.Server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect client: %v", err)
	}
	defer client.Close()

	// Wait for the client to be connected
	<-client.Connected

	// Success if we got here
	t.Log("Client successfully connected to server")
}

// TestBasicConnectivity tests that a client can connect to the server
func TestBasicConnectivity2(t *testing.T) {
	// Start the server
	server := NewTestServer(t)
	defer server.Close()

	// Wait for the server to be ready
	<-server.Ready

	// Create and connect a client
	client := NewTestClient(t, server.Server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect client: %v", err)
	}
	defer client.Close()

	// Wait for the client to be connected
	<-client.Connected

	// Success if we got here
	t.Log("Client successfully connected to server")
}
