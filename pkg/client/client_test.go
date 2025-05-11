// ergosockets/client/client_test.go
package client_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/lightforgemedia/go-websocketmq/pkg/client"
	"github.com/lightforgemedia/go-websocketmq/pkg/ergosockets"
	app_shared_types "github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
)

var testSlogHandlerClient = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug, AddSource: true})
var testLoggerClient = slog.New(testSlogHandlerClient)

type mockServer struct {
	t          *testing.T
	server     *httptest.Server
	wsURL      string
	conn       *websocket.Conn
	connMu     sync.Mutex
	handler    http.HandlerFunc   // More flexible handler
	activeConn context.CancelFunc // To signal mock server's read loop to stop for current conn
}

func newMockServer(t *testing.T, handlerFunc func(conn *websocket.Conn, ms *mockServer)) *mockServer {
	t.Helper()
	ms := &mockServer{t: t}
	ms.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connCtx, connCancel := context.WithCancel(context.Background())
		ms.activeConn = connCancel // Store cancel func for this connection

		wsconn, err := websocket.Accept(w, r, nil)
		if err != nil {
			ms.t.Logf("MockServer: Accept error: %v", err)
			connCancel()
			return
		}
		ms.connMu.Lock()
		ms.conn = wsconn
		ms.connMu.Unlock()
		ms.t.Logf("MockServer: Client connected  %s")

		defer func() {
			wsconn.Close(websocket.StatusNormalClosure, "mock server handler finished")
			ms.connMu.Lock()
			if ms.conn == wsconn { // Ensure we're clearing the correct conn if multiple connects happen fast
				ms.conn = nil
			}
			ms.connMu.Unlock()
			connCancel() // Signal read loop to stop
			ms.t.Logf("MockServer: Client connection handler finished for %v.", wsconn)
		}()

		if handlerFunc != nil {
			handlerFunc(wsconn, ms) // Pass ms for Send capability
		} else { // Default echo behavior if no specific handler
			for {
				select {
				case <-connCtx.Done():
					return
				default:
					var v interface{}
					errRead := wsjson.Read(connCtx, wsconn, &v)
					if errRead != nil {
						return
					}
					wsjson.Write(connCtx, wsconn, v)
				}
			}
		}
	}))
	ms.wsURL = "ws" + strings.TrimPrefix(ms.server.URL, "http")
	return ms
}

func (ms *mockServer) Send(env *ergosockets.Envelope) error {
	ms.connMu.Lock()
	conn := ms.conn
	ms.connMu.Unlock()
	if conn == nil {
		return fmt.Errorf("mockServer: no active connection to send to")
	}
	ms.t.Logf("MockServer: Sending envelope to client: Type=%s, Topic=%s, ID=%s", env.Type, env.Topic, env.ID)
	// Use a timeout for the send operation
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	return wsjson.Write(ctx, conn, env)
}

func (ms *mockServer) CloseCurrentConnection() {
	if ms.activeConn != nil {
		ms.activeConn() // This cancels the context for the mock server's read loop for that conn
	}
	ms.connMu.Lock()
	if ms.conn != nil {
		ms.conn.Close(websocket.StatusGoingAway, "mock server force close current conn")
		ms.conn = nil
	}
	ms.connMu.Unlock()
}

func (ms *mockServer) Close() {
	ms.server.Close()
	ms.CloseCurrentConnection()
}

func TestClientConnectAndRequest(t *testing.T) {
	t.Parallel()
	ms := newMockServer(t, func(conn *websocket.Conn, srv *mockServer) { // srv is the mockServer instance
		for { // Simple request echo handler
			var reqEnv ergosockets.Envelope
			err := wsjson.Read(context.Background(), conn, &reqEnv)
			if err != nil {
				return
			}
			if reqEnv.Topic == app_shared_types.TopicGetTime {
				respEnv, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeResponse, reqEnv.Topic, app_shared_types.GetTimeResponse{CurrentTime: "mock-time"}, nil)
				srv.Send(respEnv) // Use srv.Send
			}
		}
	})
	defer ms.Close()

	cli := newTestClient(t, ms.wsURL)
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
	t.Parallel()
	var wg sync.WaitGroup
	wg.Add(1) // For the received message

	ms := newMockServer(t, func(conn *websocket.Conn, srv *mockServer) {
		// Wait for subscribe request from client
		var subEnv ergosockets.Envelope
		subCtx, subCancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer subCancel()
		err := wsjson.Read(subCtx, conn, &subEnv)
		if err != nil {
			srv.t.Errorf("MockServer: Did not receive subscribe request: %v", err)
			return
		}
		if subEnv.Type != ergosockets.TypeSubscribeRequest || subEnv.Topic != app_shared_types.TopicServerAnnounce {
			srv.t.Errorf("MockServer: Expected subscribe request, got type %s topic %s", subEnv.Type, subEnv.Topic)
			return
		}
		srv.t.Logf("MockServer: Received subscribe request for %s", subEnv.Topic)
		ackEnv, _ := ergosockets.NewEnvelope(subEnv.ID, ergosockets.TypeSubscriptionAck, subEnv.Topic, nil, nil)
		srv.Send(ackEnv)

		// After ack, send the publish
		time.Sleep(50 * time.Millisecond) // Ensure client processes ack
		publishEnv, _ := ergosockets.NewEnvelope("", ergosockets.TypePublish, app_shared_types.TopicServerAnnounce, app_shared_types.ServerAnnouncement{Message: "test-publish"}, nil)
		srv.Send(publishEnv)
	})
	defer ms.Close()

	cli := newTestClient(t, ms.wsURL)
	defer cli.Close()

	receivedChan := make(chan app_shared_types.ServerAnnouncement, 1)
	_, err := cli.Subscribe(app_shared_types.TopicServerAnnounce,
		func(announcement app_shared_types.ServerAnnouncement) error {
			t.Logf("ClientTest: Received announcement: %+v", announcement)
			receivedChan <- announcement
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
	t.Parallel()
	connectAttempts := 0
	var firstConnectionEstablished sync.WaitGroup
	firstConnectionEstablished.Add(1)

	ms := newMockServer(t, func(conn *websocket.Conn, srv *mockServer) {
		connectAttempts++
		srv.t.Logf("MockServer: Client connected (attempt %d)", connectAttempts)
		if connectAttempts == 1 {
			firstConnectionEstablished.Done()
			// Close connection after a short delay to trigger reconnect
			go func() {
				time.Sleep(200 * time.Millisecond)
				srv.t.Logf("MockServer: Closing connection to trigger reconnect (attempt %d)", connectAttempts)
				srv.CloseCurrentConnection() // Use helper to close specific conn and cancel its loop
			}()
		} else if connectAttempts == 2 {
			// Handle a request on the re-established connection
			var reqEnv ergosockets.Envelope
			err := wsjson.Read(context.Background(), conn, &reqEnv) // Simple read, no timeout for test simplicity
			if err != nil {
				srv.t.Logf("MockServer: Read error on reconnected line: %v", err)
				return
			}
			if reqEnv.Topic == app_shared_types.TopicGetTime {
				respEnv, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeResponse, reqEnv.Topic, app_shared_types.GetTimeResponse{CurrentTime: "reconnected-time"}, nil)
				srv.Send(respEnv)
			}
		}
	})
	defer ms.Close()

	cli := newTestClient(t, ms.wsURL,
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
	t.Parallel()
	var lastReceivedPayload json.RawMessage

	ms := newMockServer(t, func(conn *websocket.Conn, srv *mockServer) {
		var reqEnv ergosockets.Envelope
		err := wsjson.Read(context.Background(), conn, &reqEnv)
		if err != nil {
			return
		}
		lastReceivedPayload = reqEnv.Payload // Capture the raw payload
		respEnv, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeResponse, reqEnv.Topic, map[string]string{"status": "ok"}, nil)
		srv.Send(respEnv)
	})
	defer ms.Close()

	cli := newTestClient(t, ms.wsURL)
	defer cli.Close()
	ctx := context.Background()

	// Test 1: No payload argument
	_, err := client.GenericRequest[map[string]string](cli, ctx, "topic_no_payload")
	if err != nil {
		t.Fatalf("Request with no payload failed: %v", err)
	}
	// wsjson sends "null" for nil interface{}
	if string(lastReceivedPayload) != "null" {
		t.Errorf("Expected 'null' payload from server, got: %s", string(lastReceivedPayload))
	}
	t.Logf("Request with no payload sent, server received: %s", string(lastReceivedPayload))

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

// newTestClient is a helper from broker_test, adapted slightly
func newTestClient(t *testing.T, urlStr string, opts ...client.Option) *client.Client {
	t.Helper()
	finalOpts := append([]client.Option{client.WithLogger(testLoggerClient)}, opts...)
	c, err := client.Connect(urlStr, finalOpts...)
	if err != nil && c == nil { // Only fatal if client is nil (no reconnect possible)
		t.Fatalf("Failed to connect client and client is nil: %v", err)
	}
	if c == nil {
		t.Fatal("Connect returned nil client")
	}
	// Give a brief moment for connection to establish, especially if reconnecting
	time.Sleep(100 * time.Millisecond)
	return c
}
