// ergosockets/broker/broker_test.go
package broker_test

import (
	"context"
	"encoding/json"
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

type helloReq struct {
	Msg string `json:"msg"`
}
type helloResp struct {
	Reply string `json:"reply"`
}

func TestClientProxyRequest(t *testing.T) {
	bs := testutil.NewBrokerServer(t, broker.WithPingInterval(-1))

	clientA := testutil.NewTestClient(t, bs.WSURL, client.WithClientPingInterval(-1))
	clientB := testutil.NewTestClient(t, bs.WSURL, client.WithClientPingInterval(-1))

	// Client A handler
	err := clientA.OnRequest("demo.hello", func(req helloReq) (helloResp, error) {
		return helloResp{Reply: "hi " + req.Msg}, nil
	})
	if err != nil {
		t.Fatalf("clientA handler: %v", err)
	}

	// Wait for both clients to connect
	_, err = testutil.WaitForClient(t, bs.Broker, clientA.ID(), 2*time.Second)
	if err != nil {
		t.Fatalf("wait clientA: %v", err)
	}
	_, err = testutil.WaitForClient(t, bs.Broker, clientB.ID(), 2*time.Second)
	if err != nil {
		t.Fatalf("wait clientB: %v", err)
	}

	payloadBytes, _ := json.Marshal(helloReq{Msg: "B"})
	proxyReq := app_shared_types.ProxyRequest{TargetID: clientA.ID(), Topic: "demo.hello", Payload: payloadBytes}

	rawResp, errPayload, err := clientB.SendServerRequest(context.Background(), app_shared_types.TopicProxyRequest, proxyReq)
	if err != nil || errPayload != nil {
		t.Fatalf("proxy request failed: %v %v", err, errPayload)
	}

	var resp helloResp
	_ = json.Unmarshal(*rawResp, &resp)
	if resp.Reply != "hi B" {
		t.Errorf("unexpected reply %v", resp.Reply)
	}
}

func TestListClients(t *testing.T) {
	bs := testutil.NewBrokerServer(t, broker.WithPingInterval(-1))

	cli1 := testutil.NewTestClient(t, bs.WSURL, client.WithClientName("A"), client.WithClientType("browser"), client.WithClientPingInterval(-1))
	cli2 := testutil.NewTestClient(t, bs.WSURL, client.WithClientName("B"), client.WithClientType("service"), client.WithClientPingInterval(-1))

	_, _ = testutil.WaitForClient(t, bs.Broker, cli1.ID(), 2*time.Second)
	_, _ = testutil.WaitForClient(t, bs.Broker, cli2.ID(), 2*time.Second)

	listReq := app_shared_types.ListClientsRequest{}
	rawResp, errPayload, err := cli1.SendServerRequest(context.Background(), app_shared_types.TopicListClients, listReq)
	if err != nil || errPayload != nil {
		t.Fatalf("list clients request failed: %v %v", err, errPayload)
	}

	var resp app_shared_types.ListClientsResponse
	_ = json.Unmarshal(*rawResp, &resp)
	if len(resp.Clients) < 2 {
		t.Errorf("expected at least 2 clients, got %d", len(resp.Clients))
	}
}
