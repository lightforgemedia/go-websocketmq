package client_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/client"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Request and response types for testing
type EchoRequest struct {
	Message string `json:"message"`
}

type EchoResponse struct {
	EchoedMessage string `json:"echoedMessage"`
	TargetClient  string `json:"targetClient"`
}

type EmptyRequest struct{}
type EmptyResponse struct{}

type ErrorTriggerRequest struct {
	ShouldError  bool   `json:"shouldError"`
	ErrorMessage string `json:"errorMessage"`
}

func TestSendToClientRequest(t *testing.T) {
	// Create broker server with disabled ping for test stability
	opts := broker.DefaultOptions()
	opts.PingInterval = -1
	bs := testutil.NewBrokerServer(t, opts)
	defer bs.Shutdown(context.Background())

	// Create target client that will handle echo requests
	targetClient := testutil.NewTestClient(t, bs.WSURL, 
		client.WithClientName("TargetClient"),
		client.WithClientType("echo-service"),
		client.WithClientPingInterval(-1))
	defer targetClient.Close()

	// Register echo handler on target client
	err := targetClient.HandleServerRequest("echo.request", func(req EchoRequest) (EchoResponse, error) {
		if req.Message == "trigger-error" {
			return EchoResponse{}, errors.New("intentional error from echo handler")
		}
		return EchoResponse{
			EchoedMessage: "Echo: " + req.Message,
			TargetClient:  "TargetClient",
		}, nil
	})
	require.NoError(t, err, "Should register echo handler on target client")

	// Register error handler on target client
	err = targetClient.HandleServerRequest("error.request", func(req ErrorTriggerRequest) (EchoResponse, error) {
		if req.ShouldError {
			return EchoResponse{}, errors.New(req.ErrorMessage)
		}
		return EchoResponse{
			EchoedMessage: "No error triggered",
			TargetClient:  "TargetClient",
		}, nil
	})
	require.NoError(t, err, "Should register error handler on target client")

	// Register empty handler on target client
	err = targetClient.HandleServerRequest("empty.request", func(req EmptyRequest) (EmptyResponse, error) {
		return EmptyResponse{}, nil
	})
	require.NoError(t, err, "Should register empty handler on target client")

	// Create source client that will send requests to target
	sourceClient := testutil.NewTestClient(t, bs.WSURL, 
		client.WithClientName("SourceClient"),
		client.WithClientType("api-client"),
		client.WithClientPingInterval(-1))
	defer sourceClient.Close()

	// Wait for both clients to connect to broker
	_, err = testutil.WaitForClient(t, bs.Broker, targetClient.ID(), 2*time.Second)
	require.NoError(t, err, "Target client should connect to broker")
	_, err = testutil.WaitForClient(t, bs.Broker, sourceClient.ID(), 2*time.Second)
	require.NoError(t, err, "Source client should connect to broker")

	t.Logf("Source client ID: %s, Target client ID: %s", sourceClient.ID(), targetClient.ID())

	// Test cases
	t.Run("Happy Path", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		request := EchoRequest{Message: "hello from source"}
		response, err := client.SendToClientRequest[EchoResponse](
			sourceClient,
			ctx,
			targetClient.ID(),
			"echo.request",
			request,
		)

		require.NoError(t, err, "SendToClientRequest should succeed")
		require.NotNil(t, response, "Response should not be nil")
		assert.Equal(t, "Echo: hello from source", response.EchoedMessage, "Response message should match")
		assert.Equal(t, "TargetClient", response.TargetClient, "Target client name should match")
	})

	t.Run("Empty Request", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// No request payload (sends null)
		response, err := client.SendToClientRequest[EchoResponse](
			sourceClient,
			ctx,
			targetClient.ID(),
			"echo.request",
		)

		require.NoError(t, err, "SendToClientRequest with empty request should succeed")
		require.NotNil(t, response, "Response should not be nil")
		assert.Equal(t, "Echo: ", response.EchoedMessage, "Response should echo empty string")
	})

	t.Run("Empty Response", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// Empty request and response types
		response, err := client.SendToClientRequest[EmptyResponse](
			sourceClient,
			ctx,
			targetClient.ID(),
			"empty.request",
			EmptyRequest{},
		)

		require.NoError(t, err, "SendToClientRequest with empty response type should succeed")
		require.NotNil(t, response, "Response should not be nil even when empty")
	})

	t.Run("Target Handler Returns Error", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		request := ErrorTriggerRequest{
			ShouldError:  true,
			ErrorMessage: "test error from handler",
		}
		
		response, err := client.SendToClientRequest[EchoResponse](
			sourceClient,
			ctx,
			targetClient.ID(),
			"error.request",
			request,
		)

		require.Error(t, err, "SendToClientRequest should return error when target handler errors")
		assert.Contains(t, err.Error(), "test error from handler", "Error should contain handler's error message")
		assert.Nil(t, response, "Response should be nil when error occurs")
	})

	t.Run("Unknown Target Client", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		nonExistentClientID := "non-existent-client-id"
		response, err := client.SendToClientRequest[EchoResponse](
			sourceClient,
			ctx,
			nonExistentClientID,
			"echo.request",
			EchoRequest{Message: "hello"},
		)

		require.Error(t, err, "SendToClientRequest to non-existent client should fail")
		assert.Contains(t, err.Error(), "not found", "Error should indicate client not found")
		assert.Nil(t, response, "Response should be nil when target client not found")
	})

	t.Run("Unknown Topic", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		response, err := client.SendToClientRequest[EchoResponse](
			sourceClient,
			ctx,
			targetClient.ID(),
			"unknown.topic",
			EchoRequest{Message: "hello"},
		)

		require.Error(t, err, "SendToClientRequest with unknown topic should fail")
		assert.Contains(t, err.Error(), "no handler", "Error should indicate no handler for topic")
		assert.Nil(t, response, "Response should be nil when topic has no handler")
	})

	t.Run("Context Timeout", func(t *testing.T) {
		// Create a context with a very short timeout
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
		defer cancel()
		
		// Sleep to ensure context expires
		time.Sleep(5 * time.Millisecond)

		response, err := client.SendToClientRequest[EchoResponse](
			sourceClient,
			ctx,
			targetClient.ID(),
			"echo.request",
			EchoRequest{Message: "hello"},
		)

		require.Error(t, err, "SendToClientRequest with expired context should fail")
		assert.Contains(t, err.Error(), "context", "Error should mention context issue")
		assert.Nil(t, response, "Response should be nil when context times out")
	})

	t.Run("Type Mismatch", func(t *testing.T) {
		// Define an incompatible type for unmarshaling
		type WrongResponseType struct {
			CompletelyDifferentField int `json:"completelyDifferentField"`
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		response, err := client.SendToClientRequest[WrongResponseType](
			sourceClient,
			ctx,
			targetClient.ID(),
			"echo.request",
			EchoRequest{Message: "hello"},
		)

		require.Error(t, err, "SendToClientRequest with incompatible response type should fail")
		assert.Contains(t, err.Error(), "unmarshal", "Error should indicate unmarshaling issue")
		assert.Nil(t, response, "Response should be nil when unmarshaling fails")
	})
}