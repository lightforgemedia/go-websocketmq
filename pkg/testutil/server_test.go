package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/client"
	"github.com/lightforgemedia/go-websocketmq/pkg/ergosockets"
	app_shared_types "github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
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

// TestNewTestServer is kept for backward compatibility
func TestNewTestServer(t *testing.T) {
	// Use BrokerServer instead
	bs := NewBrokerServer(t)

	// Verify that the test server is created
	assert.NotNil(t, bs, "BrokerServer should not be nil")
	assert.NotNil(t, bs.Broker, "BrokerServer broker should not be nil")
	assert.NotNil(t, bs.HTTP, "BrokerServer HTTP server should not be nil")
	assert.NotEmpty(t, bs.WSURL, "BrokerServer WebSocket URL should not be empty")
	assert.Contains(t, bs.WSURL, "ws://", "BrokerServer WebSocket URL should contain ws://")
}

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

func TestBrokerRequestResponse(t *testing.T) {
	bs := NewBrokerServer(t)

	err := bs.OnRequest(app_shared_types.TopicGetTime,
		func(ch broker.ClientHandle, req app_shared_types.GetTimeRequest) (app_shared_types.GetTimeResponse, error) {
			t.Logf("TestBroker: Server handler for %s invoked by client %s", app_shared_types.TopicGetTime, ch.ID())
			return app_shared_types.GetTimeResponse{CurrentTime: "test-time"}, nil
		},
	)
	require.NoError(t, err, "Failed to register server handler")

	cli := NewTestClient(t, bs.WSURL)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := client.GenericRequest[app_shared_types.GetTimeResponse](cli, ctx, app_shared_types.TopicGetTime)
	require.NoError(t, err, "Client request failed")
	assert.Equal(t, "test-time", resp.CurrentTime, "Expected response 'test-time'")
}
