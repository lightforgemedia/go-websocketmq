package client_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test structs for subscription and request handlers
type ClientValueMessage struct {
	EventType string `json:"eventType"`
	Timestamp int64  `json:"timestamp"`
	Data      string `json:"data"`
}

type ClientPtrMessage struct {
	ID        *string    `json:"id,omitempty"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	Items     *[]string  `json:"items,omitempty"`
}

// Test structs for server requests
type ClientReqFromServer struct {
	Action  string            `json:"action"`
	Options map[string]string `json:"options,omitempty"`
}

type ClientRespToServer struct {
	Success bool        `json:"success"`
	Error   string      `json:"error,omitempty"`
	Result  interface{} `json:"result,omitempty"`
}

// Tests for subscription handlers
func TestClientSubscriptionDecode(t *testing.T) {
	t.Run("Value type messages", func(t *testing.T) {
		bs := testutil.NewBrokerServer(t)
		defer bs.Shutdown(context.Background())

		const topicName = "test:sub_value_decode"
		messageReceived := make(chan ClientValueMessage, 1)

		// Create client and subscribe
		cli := testutil.NewTestClient(t, bs.WSURL)
		defer cli.Close()

		// Subscribe with value type handler
		_, err := cli.Subscribe(topicName, func(msg ClientValueMessage) error {
			t.Logf("Received message: %+v", msg)
			messageReceived <- msg
			return nil
		})
		require.NoError(t, err)

		// Wait for subscription to be processed
		time.Sleep(200 * time.Millisecond)

		// Publish a message from the broker
		ctx := context.Background()
		testMsg := ClientValueMessage{
			EventType: "notification",
			Timestamp: time.Now().Unix(),
			Data:      "Test notification message",
		}
		err = bs.Publish(ctx, topicName, testMsg)
		require.NoError(t, err)

		// Wait for the message to be received
		select {
		case receivedMsg := <-messageReceived:
			assert.Equal(t, testMsg.EventType, receivedMsg.EventType)
			assert.Equal(t, testMsg.Timestamp, receivedMsg.Timestamp)
			assert.Equal(t, testMsg.Data, receivedMsg.Data)
		case <-time.After(1 * time.Second):
			t.Fatal("Timeout waiting for message to be received")
		}
	})

	t.Run("Pointer type messages", func(t *testing.T) {
		bs := testutil.NewBrokerServer(t)
		defer bs.Shutdown(context.Background())

		const topicName = "test:sub_ptr_decode"
		messageReceived := make(chan *ClientPtrMessage, 1)

		// Create client and subscribe
		cli := testutil.NewTestClient(t, bs.WSURL)
		defer cli.Close()

		// Subscribe with pointer type handler
		_, err := cli.Subscribe(topicName, func(msg *ClientPtrMessage) error {
			t.Logf("Received pointer message: %+v", msg)
			messageReceived <- msg
			return nil
		})
		require.NoError(t, err)

		// Wait for subscription to be processed
		time.Sleep(200 * time.Millisecond)

		// Publish a message from the broker
		ctx := context.Background()
		id := "msg-123"
		now := time.Now()
		items := []string{"item1", "item2"}
		testMsg := &ClientPtrMessage{
			ID:        &id,
			CreatedAt: &now,
			Items:     &items,
		}
		err = bs.Publish(ctx, topicName, testMsg)
		require.NoError(t, err)

		// Wait for the message to be received
		select {
		case receivedMsg := <-messageReceived:
			require.NotNil(t, receivedMsg)
			require.NotNil(t, receivedMsg.ID)
			assert.Equal(t, id, *receivedMsg.ID)
			require.NotNil(t, receivedMsg.CreatedAt)
			assert.True(t, now.Equal(*receivedMsg.CreatedAt))
			require.NotNil(t, receivedMsg.Items)
			assert.Equal(t, items, *receivedMsg.Items)
		case <-time.After(1 * time.Second):
			t.Fatal("Timeout waiting for message to be received")
		}
	})

	t.Run("Null message with value type", func(t *testing.T) {
		bs := testutil.NewBrokerServer(t)
		defer bs.Shutdown(context.Background())

		const topicName = "test:null_msg_value_decode"
		messageReceived := make(chan bool, 1)

		// Create client and subscribe
		cli := testutil.NewTestClient(t, bs.WSURL)
		defer cli.Close()

		// Subscribe with value type handler
		_, err := cli.Subscribe(topicName, func(msg ClientValueMessage) error {
			// Should receive a zero-value struct
			assert.Equal(t, "", msg.EventType)
			assert.Equal(t, int64(0), msg.Timestamp)
			assert.Equal(t, "", msg.Data)
			messageReceived <- true
			return nil
		})
		require.NoError(t, err)

		// Wait for subscription to be processed
		time.Sleep(200 * time.Millisecond)

		// Publish a null message from the broker
		ctx := context.Background()
		err = bs.Broker.Publish(ctx, topicName, nil)
		require.NoError(t, err)

		// Wait for the message to be received
		select {
		case <-messageReceived:
			// Success - handler received zero-value struct
		case <-time.After(1 * time.Second):
			t.Fatal("Timeout waiting for null message to be received")
		}
	})

	t.Run("Null message with pointer type", func(t *testing.T) {
		bs := testutil.NewBrokerServer(t)
		defer bs.Shutdown(context.Background())

		const topicName = "test:null_msg_ptr_decode"
		messageReceived := make(chan bool, 1)

		// Create client and subscribe
		cli := testutil.NewTestClient(t, bs.WSURL)
		defer cli.Close()

		// Subscribe with pointer type handler
		_, err := cli.Subscribe(topicName, func(msg *ClientPtrMessage) error {
			// Should receive a nil pointer
			assert.Nil(t, msg)
			messageReceived <- true
			return nil
		})
		require.NoError(t, err)

		// Wait for subscription to be processed
		time.Sleep(200 * time.Millisecond)

		// Publish a null message from the broker
		ctx := context.Background()
		err = bs.Broker.Publish(ctx, topicName, nil)
		require.NoError(t, err)

		// Wait for the message to be received
		select {
		case <-messageReceived:
			// Success - handler received nil pointer
		case <-time.After(1 * time.Second):
			t.Fatal("Timeout waiting for null message to be received")
		}
	})

	t.Run("Handler returns error", func(t *testing.T) {
		bs := testutil.NewBrokerServer(t)
		defer bs.Shutdown(context.Background())

		const topicName = "test:handler_error_decode"
		handlerCalled := make(chan error, 1)

		// Create client and subscribe
		cli := testutil.NewTestClient(t, bs.WSURL)
		defer cli.Close()

		// Subscribe with handler that returns an error
		expectedError := fmt.Errorf("test handler error")
		_, err := cli.Subscribe(topicName, func(msg ClientValueMessage) error {
			if msg.EventType == "error" {
				handlerCalled <- expectedError
				return expectedError
			}
			handlerCalled <- nil
			return nil
		})
		require.NoError(t, err)

		// Wait for subscription to be processed
		time.Sleep(200 * time.Millisecond)

		// Publish a message that will trigger an error
		ctx := context.Background()
		err = bs.Publish(ctx, topicName, ClientValueMessage{
			EventType: "error",
			Data:      "This message will cause an error",
		})
		require.NoError(t, err)

		// Wait for the handler to be called
		select {
		case handlerErr := <-handlerCalled:
			assert.Equal(t, expectedError, handlerErr)
		case <-time.After(1 * time.Second):
			t.Fatal("Timeout waiting for handler to be called")
		}
	})

	t.Run("Invalid JSON message", func(t *testing.T) {
		bs := testutil.NewBrokerServer(t)
		defer bs.Shutdown(context.Background())

		const topicName = "test:invalid_json_msg"
		var wg sync.WaitGroup
		wg.Add(1)

		// Create client and subscribe
		cli := testutil.NewTestClient(t, bs.WSURL)
		defer cli.Close()

		handlerCalled := false
		_, err := cli.Subscribe(topicName, func(msg ClientValueMessage) error {
			handlerCalled = true // Should not be called for invalid JSON
			wg.Done()
			return nil
		})
		require.NoError(t, err)

		// Wait for subscription to be processed
		time.Sleep(200 * time.Millisecond)

		// Publish invalid JSON message
		ctx := context.Background()
		err = bs.Broker.Publish(ctx, topicName, map[string]interface{}{
			"eventType": "test",
			"timestamp": "invalid", // This will still be valid JSON, but not match the expected type
		})
		require.NoError(t, err)

		// Give some time for any potential handler call
		go func() {
			time.Sleep(500 * time.Millisecond)
			if !handlerCalled {
				wg.Done() // Release the wait if handler wasn't called
			}
		}()

		wg.Wait()
		assert.False(t, handlerCalled, "Handler should not be called for invalid JSON")
	})
}

// Tests for client request handlers (server-to-client requests)
func TestClientRequestHandlerDecode(t *testing.T) {
	t.Run("Value request and response", func(t *testing.T) {
		bs := testutil.NewBrokerServer(t)
		defer bs.Shutdown(context.Background())

		const topicName = "test:req_value_decode"

		// Create client with handler
		cli := testutil.NewTestClient(t, bs.WSURL)
		defer cli.Close()

		// Register handler for server requests
		err := cli.HandleServerRequest(topicName, func(req ClientReqFromServer) (ClientRespToServer, error) {
			// Verify request
			assert.Equal(t, "query", req.Action)
			assert.Equal(t, map[string]string{"param1": "value1", "param2": "value2"}, req.Options)

			// Return response
			return ClientRespToServer{
				Success: true,
				Result:  "Query processed successfully",
			}, nil
		})
		require.NoError(t, err)

		// Wait for client registration
		clientHandle, err := testutil.WaitForClient(t, bs.Broker, cli.ID(), 3*time.Second)
		require.NoError(t, err)

		// Send request from server to client
		ctx := context.Background()
		var response ClientRespToServer
		err = clientHandle.SendClientRequest(ctx, topicName, ClientReqFromServer{
			Action: "query",
			Options: map[string]string{
				"param1": "value1",
				"param2": "value2",
			},
		}, &response, 0)
		require.NoError(t, err)

		// Verify response
		assert.True(t, response.Success)
		assert.Equal(t, "Query processed successfully", response.Result)
	})

	t.Run("Pointer request and response", func(t *testing.T) {
		bs := testutil.NewBrokerServer(t)
		defer bs.Shutdown(context.Background())

		const topicName = "test:req_ptr_decode"

		// Create client with handler
		cli := testutil.NewTestClient(t, bs.WSURL)
		defer cli.Close()

		// Register handler for server requests with pointer types
		err := cli.HandleServerRequest(topicName, func(req *ClientReqFromServer) (*ClientRespToServer, error) {
			// Verify request
			require.NotNil(t, req)
			assert.Equal(t, "update", req.Action)

			// Return response
			return &ClientRespToServer{
				Success: true,
				Result:  map[string]interface{}{"updated": true, "count": 1},
			}, nil
		})
		require.NoError(t, err)

		// Wait for client registration
		clientHandle, err := testutil.WaitForClient(t, bs.Broker, cli.ID(), 3*time.Second)
		require.NoError(t, err)

		// Send request from server to client
		ctx := context.Background()
		var response *ClientRespToServer
		err = clientHandle.SendClientRequest(ctx, topicName, &ClientReqFromServer{
			Action: "update",
		}, &response, 0)
		require.NoError(t, err)

		// Verify response
		require.NotNil(t, response)
		assert.True(t, response.Success)
		result, ok := response.Result.(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, true, result["updated"])
		assert.Equal(t, float64(1), result["count"]) // JSON numbers are float64
	})

	t.Run("Null request from server", func(t *testing.T) {
		bs := testutil.NewBrokerServer(t)
		defer bs.Shutdown(context.Background())

		const topicName = "test:null_req_from_server"

		// Create client with handler for null requests
		cli := testutil.NewTestClient(t, bs.WSURL)
		defer cli.Close()

		// Register handler that expects a pointer type (can be nil)
		err := cli.HandleServerRequest(topicName, func(req *ClientReqFromServer) (ClientRespToServer, error) {
			// Should receive nil for null request
			assert.Nil(t, req)
			return ClientRespToServer{
				Success: true,
				Result:  "Handled null request",
			}, nil
		})
		require.NoError(t, err)

		// Wait for client registration
		clientHandle, err := testutil.WaitForClient(t, bs.Broker, cli.ID(), 3*time.Second)
		require.NoError(t, err)

		// Send null request from server to client
		ctx := context.Background()
		var response ClientRespToServer
		err = clientHandle.SendClientRequest(ctx, topicName, nil, &response, 0)
		require.NoError(t, err)

		// Verify response
		assert.True(t, response.Success)
		assert.Equal(t, "Handled null request", response.Result)
	})

	t.Run("Handler returns error", func(t *testing.T) {
		bs := testutil.NewBrokerServer(t)
		defer bs.Shutdown(context.Background())

		const topicName = "test:req_handler_error"
		const errorMsg = "Client refused this action"

		// Create client with handler that returns an error
		cli := testutil.NewTestClient(t, bs.WSURL)
		defer cli.Close()

		// Register handler that returns an error for certain actions
		err := cli.HandleServerRequest(topicName, func(req ClientReqFromServer) (ClientRespToServer, error) {
			if req.Action == "forbidden" {
				return ClientRespToServer{}, fmt.Errorf(errorMsg)
			}
			return ClientRespToServer{Success: true}, nil
		})
		require.NoError(t, err)

		// Wait for client registration
		clientHandle, err := testutil.WaitForClient(t, bs.Broker, cli.ID(), 3*time.Second)
		require.NoError(t, err)

		// Send request that will trigger an error
		ctx := context.Background()
		var response ClientRespToServer
		err = clientHandle.SendClientRequest(ctx, topicName, ClientReqFromServer{
			Action: "forbidden",
		}, &response, 0)

		// Should have an error
		require.Error(t, err)
		assert.Contains(t, err.Error(), errorMsg)
	})

	t.Run("No handler for topic", func(t *testing.T) {
		bs := testutil.NewBrokerServer(t)
		defer bs.Shutdown(context.Background())

		const topicName = "test:nonexistent_handler"

		// Create client without registering a handler
		cli := testutil.NewTestClient(t, bs.WSURL)
		defer cli.Close()

		// Wait for client registration
		clientHandle, err := testutil.WaitForClient(t, bs.Broker, cli.ID(), 3*time.Second)
		require.NoError(t, err)

		// Send request to a topic with no handler
		ctx := context.Background()
		var response ClientRespToServer
		err = clientHandle.SendClientRequest(ctx, topicName, ClientReqFromServer{}, &response, 0)

		// Should have an error
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Client has no handler for topic")
	})

	t.Run("Handler with no response type", func(t *testing.T) {
		bs := testutil.NewBrokerServer(t)
		defer bs.Shutdown(context.Background())

		const topicName = "test:no_response_from_handler"
		handlerCalled := false

		// Create client with handler that doesn't return a response
		cli := testutil.NewTestClient(t, bs.WSURL)
		defer cli.Close()

		// Register handler that only returns error (no response payload)
		err := cli.HandleServerRequest(topicName, func(req ClientReqFromServer) error {
			handlerCalled = true
			assert.Equal(t, "notify", req.Action)
			return nil
		})
		require.NoError(t, err)

		// Wait for client registration
		clientHandle, err := testutil.WaitForClient(t, bs.Broker, cli.ID(), 3*time.Second)
		require.NoError(t, err)

		// Send request
		ctx := context.Background()
		var response json.RawMessage // Use raw JSON to verify null/empty response
		err = clientHandle.SendClientRequest(ctx, topicName, ClientReqFromServer{
			Action: "notify",
		}, &response, 0)
		require.NoError(t, err)

		// Should succeed with null/empty response
		assert.True(t, handlerCalled)
		
		// Response should be null or empty
		if response != nil {
			assert.Contains(t, []string{"null", "{}", ""}, string(response))
		}
	})
}