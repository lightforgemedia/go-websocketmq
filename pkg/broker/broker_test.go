// ergosockets/broker/broker_test.go
package broker_test

import (
	"context"
	// "encoding/json" // Not directly used, but good to have if debugging payloads
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/client"
	app_shared_types "github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
	// "github.com/coder/websocket/wsjson" // Not directly used by test, but by client/broker
)

var testSlogHandler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug, AddSource: true})
var testLogger = slog.New(testSlogHandler)

func newTestBroker(t *testing.T, opts ...broker.Option) (*broker.Broker, *httptest.Server, string) {
	t.Helper()
	finalOpts := append([]broker.Option{broker.WithLogger(testLogger)}, opts...)
	b, err := broker.New(finalOpts...)
	if err != nil {
		t.Fatalf("Failed to create broker: %v", err)
	}

	s := httptest.NewServer(b.UpgradeHandler())
	wsURL := "ws" + strings.TrimPrefix(s.URL, "http") // No /ws needed if handler is at root
	return b, s, wsURL
}

func newTestClient(t *testing.T, urlStr string, clientOpts ...client.Option) *client.Client {
	t.Helper()
	defaultOpts := []client.Option{
		client.WithLogger(testLogger),
		client.WithDefaultRequestTimeout(2 * time.Second),
	}
	finalOpts := append(defaultOpts, clientOpts...)

	cli, err := client.Connect(urlStr, finalOpts...)
	if err != nil && cli == nil { // If connect truly failed and didn't even return a client for reconnect
		t.Fatalf("Client Connect failed and returned nil client: %v", err)
	}
	if cli == nil {
		t.Fatal("Client Connect returned nil client unexpectedly")
	}
	// Give a moment for connection to establish, especially if it might be reconnecting
	// or if the test server is slow to start.
	time.Sleep(150 * time.Millisecond) // Increased slightly
	return cli
}

// waitForClient waits for a client with a specific ID to be known by the broker.
func waitForClient(t *testing.T, b *broker.Broker, clientID string, timeout time.Duration) (broker.ClientHandle, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out waiting for client %s: %w", clientID, ctx.Err())
		case <-ticker.C:
			ch, err := b.GetClient(clientID)
			if err == nil && ch != nil {
				t.Logf("waitForClient: Found client %s", clientID)
				return ch, nil
			}
		}
	}
}

func TestBrokerRequestResponse(t *testing.T) {
	t.Parallel()
	b, s, wsURL := newTestBroker(t)
	defer s.Close()
	defer b.Shutdown(context.Background())

	err := b.OnRequest(app_shared_types.TopicGetTime,
		func(ch broker.ClientHandle, req app_shared_types.GetTimeRequest) (app_shared_types.GetTimeResponse, error) {
			t.Logf("TestBroker: Server handler for %s invoked by client %s", app_shared_types.TopicGetTime, ch.ID())
			return app_shared_types.GetTimeResponse{CurrentTime: "test-time-refined"}, nil
		},
	)
	if err != nil {
		t.Fatalf("Failed to register server handler: %v", err)
	}

	cli := newTestClient(t, wsURL)
	defer cli.Close()

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
	t.Parallel()
	b, s, wsURL := newTestBroker(t)
	defer s.Close()
	defer b.Shutdown(context.Background())

	cli := newTestClient(t, wsURL)
	defer cli.Close()

	receivedChan := make(chan app_shared_types.ServerAnnouncement, 1)
	_, err := cli.Subscribe(app_shared_types.TopicServerAnnounce,
		func(announcement app_shared_types.ServerAnnouncement) error {
			t.Logf("TestBroker: Client received announcement: %+v", announcement)
			receivedChan <- announcement
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Client failed to subscribe: %v", err)
	}
	time.Sleep(150 * time.Millisecond) // Allow subscribe to propagate

	testAnnouncement := app_shared_types.ServerAnnouncement{Message: "hello-broker-test-refined", Timestamp: "nowish"}
	err = b.Publish(context.Background(), app_shared_types.TopicServerAnnounce, testAnnouncement)
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

func TestBrokerServerToClientRequest(t *testing.T) {
	t.Parallel()
	b, s, wsURL := newTestBroker(t, broker.WithPingInterval(-1)) // Disable pings for predictability
	defer s.Close()
	defer b.Shutdown(context.Background())

	cli := newTestClient(t, wsURL, client.WithClientPingInterval(-1)) // Disable client pings too
	defer cli.Close()

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

	clientHandle, err := waitForClient(t, b, cli.ID(), 2*time.Second)
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
	t.Parallel()
	// Small send buffer for the client on the broker side
	b, s, wsURL := newTestBroker(t, broker.WithClientSendBuffer(1), broker.WithPingInterval(-1))
	defer s.Close()
	// No defer b.Shutdown here, we want to inspect after client is gone

	cli := newTestClient(t, wsURL, client.WithClientPingInterval(-1)) // Client doesn't need to be special
	defer cli.Close()                                                 // Ensure client is closed at end of test

	// Wait for client to connect
	clientHandle, err := waitForClient(t, b, cli.ID(), 1*time.Second)
	if err != nil {
		t.Fatalf("Client did not connect: %v", err)
	}
	t.Logf("Client %s connected.", clientHandle.ID())

	// Client subscribes but will not read from its WebSocket connection
	// This requires the client library to have a way to "not read" or for us to
	// simply not process messages on the client side for this test.
	// For this test, we'll rely on the broker's send buffer filling up.
	// The client.Subscribe is not strictly needed if we just flood the send channel.

	// Flood the client's send buffer from the broker
	// The buffer is 1. Send 5 messages.
	// The writePump of the managedClient should detect the full buffer and close.
	for i := 0; i < 5; i++ {
		// Use clientHandle.Send which is non-blocking in its attempt to queue
		// but the underlying channel will block if full.
		// The broker's Publish also uses a non-blocking select to queue.
		// Let's use Publish to simulate a topic the client might be subscribed to.
		go b.Publish(context.Background(), "flood_topic", app_shared_types.BroadcastMessage{Content: fmt.Sprintf("flood %d", i)})
		// A direct send would also work if the client was subscribed to nothing:
		// go clientHandle.Send(context.Background(), "direct_flood", map[string]int{"i":i})
	}

	// Wait for the broker to detect the slow client and disconnect it
	var disconnected bool
	for i := 0; i < 50; i++ { // Poll for ~2.5 seconds
		_, errGet := b.GetClient(cli.ID())
		if errGet != nil { // Client no longer found
			disconnected = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !disconnected {
		t.Fatal("Broker did not disconnect the slow client in time")
	}
	t.Log("Broker successfully disconnected the slow client.")

	b.Shutdown(context.Background()) // Now shutdown broker
}

// TestBrokerClientDisconnect (from previous) - refined
func TestBrokerClientDisconnect(t *testing.T) {
	t.Parallel()
	b, s, wsURL := newTestBroker(t, broker.WithPingInterval(100*time.Millisecond)) // Faster pings for test
	defer s.Close()

	cli := newTestClient(t, wsURL, client.WithClientPingInterval(-1)) // Client doesn't ping

	clientHandle, err := waitForClient(t, b, cli.ID(), 1*time.Second)
	if err != nil {
		t.Fatalf("Client did not connect for disconnect test: %v", err)
	}
	t.Logf("Client %s connected for disconnect test.", clientHandle.ID())

	// Get initial count (should be 1)
	var initialClientCount int
	b.IterateClients(func(ch broker.ClientHandle) bool { initialClientCount++; return true })
	if initialClientCount != 1 {
		t.Fatalf("Expected 1 client, got %d", initialClientCount)
	}

	cli.Close() // Client closes connection

	// Wait for broker to remove client (due to readPump error or ping failure)
	var clientRemoved bool
	for i := 0; i < 60; i++ { // Wait up to 3s (generous for ping cycle + processing)
		_, errGet := b.GetClient(cli.ID())
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
	b.IterateClients(func(ch broker.ClientHandle) bool { finalClientCount++; return true })
	if finalClientCount != 0 {
		t.Errorf("Expected 0 clients after disconnect, got %d", finalClientCount)
	}

	b.Shutdown(context.Background())
}
