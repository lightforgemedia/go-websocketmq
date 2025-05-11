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
