// ergosockets/broker/broker_test.go
package broker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/client"
	app_shared_types "github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrokerRequestResponse(t *testing.T) {
	bs := testutil.NewBrokerServer(t)

	err := bs.HandleClientRequest(app_shared_types.TopicGetTime,
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
	opts := broker.DefaultOptions()
	opts.PingInterval = -1 // Disable pings for predictability
	bs := testutil.NewBrokerServer(t, opts)

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
	opts := broker.DefaultOptions()
	opts.PingInterval = -1 // Disable pings for predictability
	bs := testutil.NewBrokerServer(t, opts)

	cli := testutil.NewTestClient(t, bs.WSURL, client.WithClientPingInterval(-1)) // Disable client pings too

	clientHandlerInvoked := make(chan bool, 1)
	expectedClientUptime := "test-uptime-refined"
	err := cli.HandleServerRequest(app_shared_types.TopicClientGetStatus,
		func(req app_shared_types.ClientStatusQuery) (app_shared_types.ClientStatusReport, error) {
			t.Logf("TestBroker: Client HandleServerRequest handler for %s invoked with query: %s", app_shared_types.TopicClientGetStatus, req.QueryDetailLevel)
			clientHandlerInvoked <- true
			return app_shared_types.ClientStatusReport{ClientID: cli.ID(), Status: "client-test-ok-refined", Uptime: expectedClientUptime}, nil
		},
	)
	if err != nil {
		t.Fatalf("Client failed to register HandleServerRequest handler: %v", err)
	}

	clientHandle, err := testutil.WaitForClient(t, bs.Broker, cli.ID(), 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to get client handle from broker: %v", err)
	}

	var responsePayload app_shared_types.ClientStatusReport
	ctxReq, cancelReq := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelReq()

	// ClientHandle.SendClientRequest takes responsePayloadPtr interface{}
	err = clientHandle.SendClientRequest(ctxReq, app_shared_types.TopicClientGetStatus,
		app_shared_types.ClientStatusQuery{QueryDetailLevel: "full-refined"}, &responsePayload, 0)

	if err != nil {
		t.Fatalf("Server failed to make request to client: %v", err)
	}

	select {
	case <-clientHandlerInvoked:
		t.Log("Client HandleServerRequest handler was invoked.")
	case <-time.After(1 * time.Second):
		t.Fatal("Client HandleServerRequest handler was not invoked in time.")
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
	opts := broker.DefaultOptions()
	opts.ClientSendBuffer = 1
	opts.PingInterval = -1
	bs := testutil.NewBrokerServer(t, opts)

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
	opts := broker.DefaultOptions()
	opts.PingInterval = 1000 * time.Millisecond // Faster pings for test
	bs := testutil.NewBrokerServer(t, opts)
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

// Types for Proxy Tests
type ProxyEchoRequest struct {
	Data string `json:"data"`
}
type ProxyEchoResponse struct {
	EchoedData string `json:"echoedData"`
	HandledBy  string `json:"handledBy"`
}
type ProxyErrorRequest struct {
	ShouldError  bool   `json:"shouldError"`
	ErrorMessage string `json:"errorMessage"`
}
type ProxySlowRequest struct {
	DelayMS int `json:"delayMs"`
}
type ProxySlowResponse struct {
	Message string `json:"message"`
}
type DaisyChainRequest struct {
	OriginalMessage string `json:"originalMessage"`
}
type DaisyChainResponse struct {
	ProcessedMessage string `json:"processedMessage"`
	Path             string `json:"path"`
}

// Helper to setup clientA with specific handlers for proxy tests
func setupClientWithNamedHandler(t *testing.T, bs *testutil.BrokerServer, clientName, handlerTopic string, handlerFunc interface{}) *client.Client {
	t.Helper()
	cli := testutil.NewTestClient(t, bs.WSURL, client.WithClientName(clientName), client.WithClientType("test-target"), client.WithClientPingInterval(-1))
	err := cli.HandleServerRequest(handlerTopic, handlerFunc)
	require.NoError(t, err, "Failed to set up handler for %s on client %s", handlerTopic, clientName)
	_, err = testutil.WaitForClient(t, bs.Broker, cli.ID(), 3*time.Second) // Wait for client to fully connect and register
	require.NoError(t, err, "Client %s (ID: %s) failed to connect to broker", clientName, cli.ID())
	return cli
}

func TestClientProxyHappyPathWithDiscovery(t *testing.T) {
	t.Parallel()
	opts := broker.DefaultOptions()
	opts.PingInterval = -1
	bs := testutil.NewBrokerServer(t, opts)

	clientA_Name := "ClientA_HappyPath_Proxy"
	clientA_HandlerTopic := "echo.request.proxy"

	clientA := setupClientWithNamedHandler(t, bs, clientA_Name, clientA_HandlerTopic,
		func(req ProxyEchoRequest) (ProxyEchoResponse, error) {
			t.Logf("%s: Echo handler invoked with: %s", clientA_Name, req.Data)
			return ProxyEchoResponse{EchoedData: "proxied-" + req.Data, HandledBy: clientA_Name}, nil
		},
	)
	defer clientA.Close()

	clientB := testutil.NewTestClient(t, bs.WSURL, client.WithClientName("ClientB_HappyPath_Proxy"), client.WithClientPingInterval(-1))
	defer clientB.Close()
	_, err := testutil.WaitForClient(t, bs.Broker, clientB.ID(), 2*time.Second)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	foundClientAInfo, err := client.FindClient(clientB, ctx, client.FindClientCriteria{Name: clientA_Name, ClientType: "test-target"})
	require.NoError(t, err, "ClientB failed to find ClientA")
	require.NotNil(t, foundClientAInfo, "ClientA should be found by ClientB")
	require.Equal(t, clientA_Name, foundClientAInfo.Name)
	t.Logf("ClientB discovered ClientA with server ID: %s", foundClientAInfo.ID)

	proxyPayload := ProxyEchoRequest{Data: "helloA_via_proxy"}
	proxyPayloadBytes, _ := json.Marshal(proxyPayload)
	proxyReq := app_shared_types.ProxyRequest{
		TargetID: foundClientAInfo.ID,
		Topic:    clientA_HandlerTopic,
		Payload:  proxyPayloadBytes,
	}

	rawResp, errPayload, err := clientB.SendServerRequest(ctx, app_shared_types.TopicProxyRequest, proxyReq)
	require.NoError(t, err, "ClientB's proxy request failed")
	require.Nil(t, errPayload, "ClientB's proxy request should not have error payload")
	require.NotNil(t, rawResp)

	var actualResp ProxyEchoResponse
	err = json.Unmarshal(*rawResp, &actualResp)
	require.NoError(t, err)
	assert.Equal(t, "proxied-helloA_via_proxy", actualResp.EchoedData)
	assert.Equal(t, clientA_Name, actualResp.HandledBy)
	t.Logf("ClientB received correct proxied response from ClientA: %+v", actualResp)
}

func TestClientProxyTargetNotFound(t *testing.T) {
	t.Parallel()
	opts := broker.DefaultOptions()
	opts.PingInterval = -1
	bs := testutil.NewBrokerServer(t, opts)

	clientB := testutil.NewTestClient(t, bs.WSURL, client.WithClientName("ClientB_ProxyTargetNotFound"), client.WithClientPingInterval(-1))
	defer clientB.Close()
	_, err := testutil.WaitForClient(t, bs.Broker, clientB.ID(), 2*time.Second)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	proxyReq := app_shared_types.ProxyRequest{
		TargetID: "non-existent-client-id-for-proxy",
		Topic:    "any.topic",
		Payload:  json.RawMessage(`{}`),
	}

	_, _, err = clientB.SendServerRequest(ctx, app_shared_types.TopicProxyRequest, proxyReq)
	// Our code now returns the error as the main err with wrapped error chain
	assert.Error(t, err, "Expected an error from SendServerRequest")
	assert.Contains(t, err.Error(), "proxy target client")
	assert.Contains(t, err.Error(), "client with ID 'non-existent-client-id-for-proxy' not found")
	t.Logf("ClientB correctly received error for non-existent target: %v", err)
}

func TestClientProxyTargetNoHandler(t *testing.T) {
	t.Parallel()
	opts := broker.DefaultOptions()
	opts.PingInterval = -1
	bs := testutil.NewBrokerServer(t, opts)

	clientA_Name := "ClientA_ProxyNoHandler"
	clientA := testutil.NewTestClient(t, bs.WSURL, client.WithClientName(clientA_Name), client.WithClientPingInterval(-1))
	defer clientA.Close()
	clientAHandle, err := testutil.WaitForClient(t, bs.Broker, clientA.ID(), 2*time.Second)
	require.NoError(t, err)

	clientB := testutil.NewTestClient(t, bs.WSURL, client.WithClientName("ClientB_ProxyNoHandler"), client.WithClientPingInterval(-1))
	defer clientB.Close()
	_, err = testutil.WaitForClient(t, bs.Broker, clientB.ID(), 2*time.Second)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	unhandledTopic := "unhandled.topic.on.A.for.proxy"
	proxyReq := app_shared_types.ProxyRequest{
		TargetID: clientAHandle.ID(),
		Topic:    unhandledTopic,
		Payload:  json.RawMessage(`{}`),
	}

	_, _, err = clientB.SendServerRequest(ctx, app_shared_types.TopicProxyRequest, proxyReq)
	// Our code now returns the error as the main err with wrapped error chain
	assert.Error(t, err, "Expected an error from SendServerRequest")
	assert.Contains(t, err.Error(), "proxy target error")
	assert.Contains(t, err.Error(), "Client has no handler for topic: "+unhandledTopic)
	t.Logf("ClientB correctly received error from broker about ClientA (no handler): %v", err)
}

func TestClientProxyTargetHandlerError(t *testing.T) {
	t.Parallel()
	opts := broker.DefaultOptions()
	opts.PingInterval = -1
	bs := testutil.NewBrokerServer(t, opts)

	clientA_Name := "ClientA_ProxyHandlerError"
	clientA_HandlerTopic := "error.topic.proxy"
	errorMessageFromA := "Intentional error from ClientA proxy handler"

	clientA := setupClientWithNamedHandler(t, bs, clientA_Name, clientA_HandlerTopic,
		func(req ProxyErrorRequest) (ProxyEchoResponse, error) {
			t.Logf("%s: Error handler invoked, returning error.", clientA_Name)
			return ProxyEchoResponse{}, fmt.Errorf("%s", errorMessageFromA)
		},
	)
	defer clientA.Close()

	clientB := testutil.NewTestClient(t, bs.WSURL, client.WithClientName("ClientB_ProxyHandlerError"), client.WithClientPingInterval(-1))
	defer clientB.Close()
	_, err := testutil.WaitForClient(t, bs.Broker, clientB.ID(), 2*time.Second)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	proxyPayloadBytes, _ := json.Marshal(ProxyErrorRequest{})
	proxyReq := app_shared_types.ProxyRequest{
		TargetID: clientA.ID(), Topic: clientA_HandlerTopic, Payload: proxyPayloadBytes,
	}

	_, _, err = clientB.SendServerRequest(ctx, app_shared_types.TopicProxyRequest, proxyReq)
	// Our code now returns the error as the main err with wrapped error chain
	assert.Error(t, err, "Expected an error from SendServerRequest")
	assert.Contains(t, err.Error(), "proxy target error")
	assert.Contains(t, err.Error(), errorMessageFromA)
	t.Logf("ClientB correctly received error from broker about ClientA handler error: %v", err)
}

func TestClientProxyTargetTimesOut(t *testing.T) {
	t.Parallel()
	brokerSideTimeout := 150 * time.Millisecond
	opts := broker.DefaultOptions()
	opts.PingInterval = -1
	opts.ServerRequestTimeout = brokerSideTimeout
	bs := testutil.NewBrokerServer(t, opts)

	clientA_Name := "ClientA_ProxySlowTarget"
	clientA_HandlerTopic := "slow.reply.proxy"
	clientA_SleepDuration := brokerSideTimeout + (100 * time.Millisecond)

	clientA := setupClientWithNamedHandler(t, bs, clientA_Name, clientA_HandlerTopic,
		func(req ProxySlowRequest) (ProxySlowResponse, error) {
			t.Logf("%s: Slow handler invoked, sleeping for %v", clientA_Name, clientA_SleepDuration)
			time.Sleep(clientA_SleepDuration)
			return ProxySlowResponse{Message: "finally done"}, nil
		},
	)
	defer clientA.Close()

	clientB := testutil.NewTestClient(t, bs.WSURL, client.WithClientName("ClientB_ProxyTargetTimeout"), client.WithClientPingInterval(-1))
	defer clientB.Close()
	_, err := testutil.WaitForClient(t, bs.Broker, clientB.ID(), 2*time.Second)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	proxyPayloadBytes, _ := json.Marshal(ProxySlowRequest{})
	proxyReq := app_shared_types.ProxyRequest{
		TargetID: clientA.ID(), Topic: clientA_HandlerTopic, Payload: proxyPayloadBytes,
	}

	_, errPayload, err := clientB.SendServerRequest(ctx, app_shared_types.TopicProxyRequest, proxyReq)
	require.Error(t, err)
	
	// Our updated proxy handler now returns error with code through errPayload 
	// instead of directly in err
	if errPayload != nil {
		assert.Contains(t, errPayload.Message, "timed out after "+brokerSideTimeout.String())
		t.Logf("ClientB correctly received timeout error from broker in error payload: %v", errPayload.Message)
	} else {
		require.Nil(t, errPayload, "Expected nil error payload")
		assert.Contains(t, err.Error(), "timed out after "+brokerSideTimeout.String())
		t.Logf("ClientB correctly received timeout error from broker: %v", err)
	}
}

func TestClientProxyInitiatorDisconnects(t *testing.T) {
	t.Parallel()
	opts := broker.DefaultOptions()
	opts.PingInterval = -1
	opts.ServerRequestTimeout = 500 * time.Millisecond
	bs := testutil.NewBrokerServer(t, opts)

	clientA_Name := "ClientA_ProxyInitiatorDisconnect"
	clientA_HandlerTopic := "very.slow.reply.proxy"
	clientA_SleepDuration := 1 * time.Second

	clientA := setupClientWithNamedHandler(t, bs, clientA_Name, clientA_HandlerTopic,
		func(req ProxySlowRequest) (ProxySlowResponse, error) {
			t.Logf("%s: Very slow handler invoked, sleeping for %v", clientA_Name, clientA_SleepDuration)
			time.Sleep(clientA_SleepDuration)
			t.Logf("%s: Very slow handler finished sleeping.", clientA_Name)
			return ProxySlowResponse{Message: "clientA done"}, nil
		},
	)
	defer clientA.Close()

	clientB := testutil.NewTestClient(t, bs.WSURL, client.WithClientName("ClientB_ProxyInitiatorDisconnect"), client.WithClientPingInterval(-1))
	_, err := testutil.WaitForClient(t, bs.Broker, clientB.ID(), 2*time.Second)
	require.NoError(t, err)

	ctxForB, cancelForB := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelForB()

	proxyPayloadBytes, _ := json.Marshal(ProxySlowRequest{})
	proxyReq := app_shared_types.ProxyRequest{
		TargetID: clientA.ID(), Topic: clientA_HandlerTopic, Payload: proxyPayloadBytes,
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _, errB := clientB.SendServerRequest(ctxForB, app_shared_types.TopicProxyRequest, proxyReq)
		require.Error(t, errB)
		// The error could be "client is closed" or "context canceled" depending on timing.
		// Check for either, or more specifically that it contains client B's context error or closed state.
		assert.True(t, errors.Is(errB, context.Canceled) || // If ctxForB was cancelled by clientB.Close()->clientCancel
			strings.Contains(errB.Error(), "client is closed") ||
			strings.Contains(errB.Error(), "client permanently closing"),
			"Expected error related to client B closing, got: "+errB.Error())
		t.Logf("ClientB's SendServerRequest goroutine finished with error: %v", errB)
	}()

	time.Sleep(100 * time.Millisecond) // Give SendServerRequest a moment to start
	t.Log("ClientB closing connection...")
	clientB.Close() // This will cancel clientB's main context
	wg.Wait()
	t.Log("Test for initiator disconnect completed.")
}

func TestClientProxyTargetDisconnects(t *testing.T) {
	t.Parallel()
	opts := broker.DefaultOptions()
	opts.PingInterval = -1
	opts.ServerRequestTimeout = 500 * time.Millisecond // Shorter timeout to fail faster
	bs := testutil.NewBrokerServer(t, opts)

	clientA_Name := "ClientA_ProxyTargetDisconnect"
	clientA_HandlerTopic := "disconnect.during.handle.proxy"
	clientA_ID_Chan := make(chan string, 1)
	handlerCalled := make(chan struct{}, 1)

	// Declare clientAInstance so it can be captured by the closure
	var clientAInstance *client.Client

	clientAInstance = setupClientWithNamedHandler(t, bs, clientA_Name, clientA_HandlerTopic,
		func(req ProxySlowRequest) (ProxySlowResponse, error) {
			t.Logf("%s: Handler invoked, will disconnect client A shortly.", clientA_Name)
			handlerCalled <- struct{}{}
			if clientAInstance == nil { // Should not happen if setup is correct
				t.Errorf("clientAInstance is nil in handler for %s", clientA_Name)
				return ProxySlowResponse{}, fmt.Errorf("internal test error: clientAInstance nil")
			}
			clientA_ID_Chan <- clientAInstance.ID() // Correctly call ID()
			
			// Immediately close in the same goroutine to ensure it happens
			t.Logf("%s: Closing its own connection BEFORE responding.", clientA_Name)
			clientAInstance.Close() // Correctly call Close()
			
			// Return an error because the client is self-terminating
			return ProxySlowResponse{Message: "should not be sent"}, fmt.Errorf("client %s self-terminated during handling", clientA_Name)
		},
	)
	// Note: clientAInstance will be closed by its own handler, so no `defer clientAInstance.Close()` here.

	// Make sure handler is registered before proceeding
	_, err := testutil.WaitForClient(t, bs.Broker, clientAInstance.ID(), 2*time.Second)
	require.NoError(t, err)
	
	// Store client A's ID for later use
	clientAID := clientAInstance.ID()
	t.Logf("ClientA's ID is: %s", clientAID)

	clientB := testutil.NewTestClient(t, bs.WSURL, client.WithClientName("ClientB_ProxyTargetDisconnect"), client.WithClientPingInterval(-1))
	defer clientB.Close()
	_, err = testutil.WaitForClient(t, bs.Broker, clientB.ID(), 2*time.Second)
	require.NoError(t, err)

	// Use a shorter timeout for the client request
	ctxForB, cancelForB := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancelForB()

	proxyPayloadBytes, _ := json.Marshal(ProxySlowRequest{})
	proxyReq := app_shared_types.ProxyRequest{
		TargetID: clientAID,
		Topic:    clientA_HandlerTopic,
		Payload:  proxyPayloadBytes,
	}

	// Send request and verify we get expected error
	_, errPayload, errB := clientB.SendServerRequest(ctxForB, app_shared_types.TopicProxyRequest, proxyReq)
	
	// Wait for handler to be called if it hasn't been yet
	select {
	case <-handlerCalled:
		t.Log("Handler was called successfully")
	case <-time.After(500 * time.Millisecond):
		t.Log("Warning: Handler might not have been called")
	}
	
	// The error could be in one of two places depending on how the client implementation works:
	// 1. In the errB (main error)
	// 2. In the errPayload (structured error payload)
	// We need to check both and look for any indication of client disconnection
	
	t.Logf("ClientB received main error: %v", errB)
	if errPayload != nil {
		t.Logf("ClientB received error payload: %+v", errPayload)
	}
	
	errorFound := false
	
	// Check main error
	if errB != nil {
		errorText := errB.Error()
		if strings.Contains(errorText, "client "+clientAID+" context done") || 
		   strings.Contains(errorText, "proxy target client") ||
		   strings.Contains(errorText, "client not found") {
			errorFound = true
		}
	}
	
	// Check error payload
	if errPayload != nil {
		errorText := errPayload.Message
		if strings.Contains(errorText, "client "+clientAID+" context done") || 
		   strings.Contains(errorText, "proxy target client") ||
		   strings.Contains(errorText, "client not found") {
			errorFound = true
		}
	}
	
	// Ensure we got at least one error either way
	assert.True(t, errB != nil || errPayload != nil, "Expected either main error or error payload")
	assert.True(t, errorFound, "Error should indicate client disconnection or not found in either main error or error payload")
}

func TestClientProxyNullPayloads(t *testing.T) {
	t.Parallel()
	opts := broker.DefaultOptions()
	opts.PingInterval = -1
	bs := testutil.NewBrokerServer(t, opts)

	clientA_Name := "ClientA_ProxyNullPayloads"
	clientA_EchoTopic := "echo.null.proxy"
	clientA_NullRespTopic := "null.response.proxy"

	clientA := testutil.NewTestClient(t, bs.WSURL, client.WithClientName(clientA_Name), client.WithClientPingInterval(-1))
	defer clientA.Close()

	err := clientA.HandleServerRequest(clientA_EchoTopic, func(req *ProxyEchoRequest) (*ProxyEchoResponse, error) {
		if req == nil {
			t.Logf("%s: EchoNull handler received nil request struct.", clientA_Name)
			return &ProxyEchoResponse{EchoedData: "request_was_null_proxy", HandledBy: clientA_Name}, nil
		}
		t.Logf("%s: EchoNull handler invoked with: %v", clientA_Name, req)
		return &ProxyEchoResponse{EchoedData: "proxied-" + req.Data, HandledBy: clientA_Name}, nil
	})
	require.NoError(t, err)

	err = clientA.HandleServerRequest(clientA_NullRespTopic, func(req ProxyEchoRequest) (*ProxyEchoResponse, error) {
		t.Logf("%s: NullResponse handler invoked.", clientA_Name)
		return nil, nil
	})
	require.NoError(t, err)
	_, err = testutil.WaitForClient(t, bs.Broker, clientA.ID(), 2*time.Second)
	require.NoError(t, err)

	clientB := testutil.NewTestClient(t, bs.WSURL, client.WithClientName("ClientB_ProxyNullPayloads"), client.WithClientPingInterval(-1))
	defer clientB.Close()
	_, err = testutil.WaitForClient(t, bs.Broker, clientB.ID(), 2*time.Second)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	t.Run("ClientBSendsNullPayloadViaProxy", func(t *testing.T) {
		proxyReq := app_shared_types.ProxyRequest{
			TargetID: clientA.ID(), Topic: clientA_EchoTopic, Payload: json.RawMessage("null"),
		}
		rawResp, errPl, errProxy := clientB.SendServerRequest(ctx, app_shared_types.TopicProxyRequest, proxyReq)
		require.NoError(t, errProxy)
		require.Nil(t, errPl)
		require.NotNil(t, rawResp)
		var actualResp ProxyEchoResponse
		err = json.Unmarshal(*rawResp, &actualResp)
		require.NoError(t, err)
		assert.Equal(t, "request_was_null_proxy", actualResp.EchoedData)
	})

	t.Run("ClientAReturnsNullPayloadViaProxy", func(t *testing.T) {
		proxyPlBytes, _ := json.Marshal(ProxyEchoRequest{Data: "anything"})
		proxyReq := app_shared_types.ProxyRequest{
			TargetID: clientA.ID(), Topic: clientA_NullRespTopic, Payload: proxyPlBytes,
		}
		rawResp, errPl, errProxy := clientB.SendServerRequest(ctx, app_shared_types.TopicProxyRequest, proxyReq)
		require.NoError(t, errProxy)
		require.Nil(t, errPl)
		require.NotNil(t, rawResp)
		assert.Equal(t, "null", string(*rawResp))
	})
}

func TestClientProxyInitiatorContextCancel(t *testing.T) {
	t.Parallel()
	opts := broker.DefaultOptions()
	opts.PingInterval = -1
	opts.ServerRequestTimeout = 1 * time.Second
	bs := testutil.NewBrokerServer(t, opts)

	clientA_Name := "ClientA_ProxyInitiatorCtxCancel"
	clientA_HandlerTopic := "slow.for.ctx.cancel.proxy"
	clientA_SleepDuration := 500 * time.Millisecond

	clientA := setupClientWithNamedHandler(t, bs, clientA_Name, clientA_HandlerTopic,
		func(req ProxySlowRequest) (ProxySlowResponse, error) {
			t.Logf("%s: Handler invoked, sleeping for %v", clientA_Name, clientA_SleepDuration)
			time.Sleep(clientA_SleepDuration)
			return ProxySlowResponse{Message: "clientA done"}, nil
		},
	)
	defer clientA.Close()

	clientB := testutil.NewTestClient(t, bs.WSURL, client.WithClientName("ClientB_ProxyInitiatorCtxCancel"), client.WithClientPingInterval(-1))
	defer clientB.Close()
	_, err := testutil.WaitForClient(t, bs.Broker, clientB.ID(), 2*time.Second)
	require.NoError(t, err)

	ctxForBRequest, cancelForBRequest := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelForBRequest()

	proxyPlBytes, _ := json.Marshal(ProxySlowRequest{})
	proxyReq := app_shared_types.ProxyRequest{
		TargetID: clientA.ID(), Topic: clientA_HandlerTopic, Payload: proxyPlBytes,
	}

	_, _, errB := clientB.SendServerRequest(ctxForBRequest, app_shared_types.TopicProxyRequest, proxyReq)
	require.Error(t, errB)
	assert.ErrorIs(t, errB, context.DeadlineExceeded)
	// The error message contains a dynamic duration, so just check it contains the key phrase
	assert.Contains(t, errB.Error(), "timed out or context cancelled")
	t.Logf("ClientB's request correctly timed out by its own context: %v", errB)
}

func TestClientProxyConcurrentRequests(t *testing.T) {
	t.Parallel()
	opts := broker.DefaultOptions()
	opts.PingInterval = -1
	bs := testutil.NewBrokerServer(t, opts)

	clientA_Name := "ClientA_ProxyConcurrent"
	clientA_HandlerTopic := "echo.concurrent.proxy"

	clientA := setupClientWithNamedHandler(t, bs, clientA_Name, clientA_HandlerTopic,
		func(req ProxyEchoRequest) (ProxyEchoResponse, error) {
			time.Sleep(5 * time.Millisecond)
			return ProxyEchoResponse{EchoedData: req.Data, HandledBy: clientA_Name}, nil
		},
	)
	defer clientA.Close()

	clientB := testutil.NewTestClient(t, bs.WSURL, client.WithClientName("ClientB_ProxyConcurrent"), client.WithClientPingInterval(-1))
	defer clientB.Close()
	_, err := testutil.WaitForClient(t, bs.Broker, clientB.ID(), 2*time.Second)
	require.NoError(t, err)

	numConcurrent := 20
	var wg sync.WaitGroup
	wg.Add(numConcurrent)
	errChan := make(chan error, numConcurrent)

	for i := 0; i < numConcurrent; i++ {
		go func(idx int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			payloadStr := fmt.Sprintf("concurrent_payload_proxy_%d", idx)
			proxyPl := ProxyEchoRequest{Data: payloadStr}
			proxyPlBytes, _ := json.Marshal(proxyPl)
			proxyReq := app_shared_types.ProxyRequest{TargetID: clientA.ID(), Topic: clientA_HandlerTopic, Payload: proxyPlBytes}

			rawResp, errPl, errProxy := clientB.SendServerRequest(ctx, app_shared_types.TopicProxyRequest, proxyReq)
			if errProxy != nil {
				errChan <- fmt.Errorf("g%d: proxy err: %w", idx, errProxy)
				return
			}
			if errPl != nil {
				errChan <- fmt.Errorf("g%d: proxy errPl: %+v", idx, errPl)
				return
			}
			if rawResp == nil {
				errChan <- fmt.Errorf("g%d: nil rawResp", idx)
				return
			}
			var actualResp ProxyEchoResponse
			if errUn := json.Unmarshal(*rawResp, &actualResp); errUn != nil {
				errChan <- fmt.Errorf("g%d: unmarshal: %w", idx, errUn)
				return
			}
			if actualResp.EchoedData != payloadStr {
				errChan <- fmt.Errorf("g%d: echo data mismatch", idx)
				return
			}
			if actualResp.HandledBy != clientA_Name {
				errChan <- fmt.Errorf("g%d: handledBy mismatch", idx)
				return
			}
		}(i)
	}
	wg.Wait()
	close(errChan)
	for err := range errChan {
		t.Error(err)
	}
	if t.Failed() {
		t.Fatal("One or more concurrent proxy requests failed.")
	}
	t.Logf("All %d concurrent proxy requests completed successfully.", numConcurrent)
}

func TestClientProxyToSelf(t *testing.T) {
	t.Parallel()
	opts := broker.DefaultOptions()
	opts.PingInterval = -1
	bs := testutil.NewBrokerServer(t, opts)

	clientA_Name := "ClientA_ProxyToSelfTest"
	clientA_HandlerTopic := "self.echo.proxy"

	clientA := setupClientWithNamedHandler(t, bs, clientA_Name, clientA_HandlerTopic,
		func(req ProxyEchoRequest) (ProxyEchoResponse, error) {
			t.Logf("%s: Self-echo handler invoked with: %s", clientA_Name, req.Data)
			return ProxyEchoResponse{EchoedData: "self-proxied-" + req.Data, HandledBy: clientA_Name}, nil
		},
	)
	defer clientA.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	proxyPl := ProxyEchoRequest{Data: "helloSelfViaProxy"}
	proxyPlBytes, _ := json.Marshal(proxyPl)
	proxyReq := app_shared_types.ProxyRequest{
		TargetID: clientA.ID(), Topic: clientA_HandlerTopic, Payload: proxyPlBytes,
	}

	rawResp, errPl, err := clientA.SendServerRequest(ctx, app_shared_types.TopicProxyRequest, proxyReq)
	require.NoError(t, err)
	require.Nil(t, errPl)
	require.NotNil(t, rawResp)
	var actualResp ProxyEchoResponse
	err = json.Unmarshal(*rawResp, &actualResp)
	require.NoError(t, err)
	assert.Equal(t, "self-proxied-helloSelfViaProxy", actualResp.EchoedData)
	assert.Equal(t, clientA_Name, actualResp.HandledBy)
	t.Logf("ClientA received correct proxied response from itself: %+v", actualResp)
}

func TestClientProxyDaisyChain(t *testing.T) {
	t.Parallel()
	opts := broker.DefaultOptions()
	opts.PingInterval = -1
	bs := testutil.NewBrokerServer(t, opts)

	clientA_Name := "ClientA_DaisyFinalProxy"
	topicOnA := "process.A.proxy"
	clientA := setupClientWithNamedHandler(t, bs, clientA_Name, topicOnA,
		func(req DaisyChainRequest) (DaisyChainResponse, error) {
			t.Logf("%s: Handler on A received: %s", clientA_Name, req.OriginalMessage)
			return DaisyChainResponse{ProcessedMessage: "A(" + req.OriginalMessage + ")", Path: "A"}, nil
		},
	)
	defer clientA.Close()

	clientB_Name := "ClientB_DaisyIntermediateProxy"
	topicOnB := "proxy.BtoA.proxy"
	clientB := testutil.NewTestClient(t, bs.WSURL, client.WithClientName(clientB_Name), client.WithClientType("intermediate-target"), client.WithClientPingInterval(-1))
	defer clientB.Close()
	err := clientB.HandleServerRequest(topicOnB,
		func(req DaisyChainRequest) (DaisyChainResponse, error) {
			t.Logf("%s: Handler on B received: %s. Will proxy to A.", clientB_Name, req.OriginalMessage)
			ctxB, cancelB := context.WithTimeout(context.Background(), 1500*time.Millisecond)
			defer cancelB()
			clientAInfo, errF := client.FindClient(clientB, ctxB, client.FindClientCriteria{Name: clientA_Name})
			if errF != nil || clientAInfo == nil {
				return DaisyChainResponse{}, fmt.Errorf("%s: find A failed: %v", clientB_Name, errF)
			}

			plForABytes, _ := json.Marshal(req)
			proxyToA := app_shared_types.ProxyRequest{TargetID: clientAInfo.ID, Topic: topicOnA, Payload: plForABytes}
			rawRespA, errPlA, errPrA := clientB.SendServerRequest(ctxB, app_shared_types.TopicProxyRequest, proxyToA)
			if errPrA != nil {
				return DaisyChainResponse{}, fmt.Errorf("%s: proxy to A err: %w", clientB_Name, errPrA)
			}
			if errPlA != nil {
				return DaisyChainResponse{}, fmt.Errorf("%s: errPl from A: %d %s", clientB_Name, errPlA.Code, errPlA.Message)
			}

			var respA DaisyChainResponse
			if errUn := json.Unmarshal(*rawRespA, &respA); errUn != nil {
				return DaisyChainResponse{}, fmt.Errorf("%s: unmarshal A resp: %w", clientB_Name, errUn)
			}
			respA.ProcessedMessage = "B(" + respA.ProcessedMessage + ")"
			respA.Path = "B->" + respA.Path
			return respA, nil
		},
	)
	require.NoError(t, err)
	_, err = testutil.WaitForClient(t, bs.Broker, clientB.ID(), 2*time.Second)
	require.NoError(t, err)

	clientC_Name := "ClientC_DaisyInitiatorProxy"
	clientC := testutil.NewTestClient(t, bs.WSURL, client.WithClientName(clientC_Name), client.WithClientPingInterval(-1))
	defer clientC.Close()
	_, err = testutil.WaitForClient(t, bs.Broker, clientC.ID(), 2*time.Second)
	require.NoError(t, err)

	ctxC, cancelC := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelC()
	clientBInfo, err := client.FindClient(clientC, ctxC, client.FindClientCriteria{Name: clientB_Name, ClientType: "intermediate-target"})
	require.NoError(t, err)
	require.NotNil(t, clientBInfo)

	originalMsg := "C_sends_this_proxy"
	plForBBytes, _ := json.Marshal(DaisyChainRequest{OriginalMessage: originalMsg})
	proxyToB := app_shared_types.ProxyRequest{TargetID: clientBInfo.ID, Topic: topicOnB, Payload: plForBBytes}

	rawFinalResp, finalErrPl, finalErr := clientC.SendServerRequest(ctxC, app_shared_types.TopicProxyRequest, proxyToB)
	require.NoError(t, finalErr)
	require.Nil(t, finalErrPl)
	require.NotNil(t, rawFinalResp)
	var finalResponse DaisyChainResponse
	err = json.Unmarshal(*rawFinalResp, &finalResponse)
	require.NoError(t, err)

	expectedProcessedMsg := "B(A(" + originalMsg + "))"
	expectedPath := "B->A"
	assert.Equal(t, expectedProcessedMsg, finalResponse.ProcessedMessage)
	assert.Equal(t, expectedPath, finalResponse.Path)
	t.Logf("ClientC received correctly daisy-chained response: %+v", finalResponse)
}

// TestListClients (from original prompt, keeping it)
func TestListClients(t *testing.T) {
	opts := broker.DefaultOptions()
	opts.PingInterval = -1
	bs := testutil.NewBrokerServer(t, opts)

	cli1 := testutil.NewTestClient(t, bs.WSURL, client.WithClientName("ClientA_ListTest"), client.WithClientType("browser"), client.WithClientPingInterval(-1))
	cli2 := testutil.NewTestClient(t, bs.WSURL, client.WithClientName("ClientB_ListTest"), client.WithClientType("service"), client.WithClientPingInterval(-1))

	_, err := testutil.WaitForClient(t, bs.Broker, cli1.ID(), 2*time.Second)
	require.NoError(t, err)
	_, err = testutil.WaitForClient(t, bs.Broker, cli2.ID(), 2*time.Second)
	require.NoError(t, err)

	// Test listing all clients
	listReqAll := app_shared_types.ListClientsRequest{}
	respAll, err := client.GenericRequest[app_shared_types.ListClientsResponse](cli1, context.Background(), app_shared_types.TopicListClients, listReqAll)
	require.NoError(t, err)
	require.NotNil(t, respAll)
	assert.Len(t, respAll.Clients, 2, "Expected 2 clients when listing all")

	// Test listing by type "browser"
	listReqBrowser := app_shared_types.ListClientsRequest{ClientType: "browser"}
	respBrowser, err := client.GenericRequest[app_shared_types.ListClientsResponse](cli1, context.Background(), app_shared_types.TopicListClients, listReqBrowser)
	require.NoError(t, err)
	require.NotNil(t, respBrowser)
	if assert.Len(t, respBrowser.Clients, 1, "Expected 1 browser client") {
		assert.Equal(t, "ClientA_ListTest", respBrowser.Clients[0].Name)
		assert.Equal(t, "browser", respBrowser.Clients[0].ClientType)
	}

	// Test listing by type "service" using FindClient helper
	foundService, err := client.FindClient(cli2, context.Background(), client.FindClientCriteria{ClientType: "service", Name: "ClientB_ListTest"})
	require.NoError(t, err)
	require.NotNil(t, foundService)
	assert.Equal(t, "ClientB_ListTest", foundService.Name)
	assert.Equal(t, "service", foundService.ClientType)
	assert.Equal(t, cli2.ID(), foundService.ID) // Check if the ID matches the server-assigned one

	// Test FindClient for non-existent client
	foundNonExistent, err := client.FindClient(cli1, context.Background(), client.FindClientCriteria{Name: "NonExistentClient"})
	require.NoError(t, err)
	assert.Nil(t, foundNonExistent)
}
