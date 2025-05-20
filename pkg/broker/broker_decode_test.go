package broker_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/client"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test structs with various JSON structures
type BrokerValueRequest struct {
	Command string `json:"command"`
	Count   int    `json:"count"`
}

type BrokerValueResponse struct {
	Result string `json:"result"`
	Items  []int  `json:"items"`
}

type BrokerPtrRequest struct {
	QueryID  string    `json:"queryId"`
	FromDate time.Time `json:"fromDate"`
}

type BrokerPtrResponse struct {
	Success    bool   `json:"success"`
	RecordID   string `json:"recordId,omitempty"`
	RecordData *struct {
		Field1 string `json:"field1"`
		Field2 int    `json:"field2"`
	} `json:"recordData,omitempty"`
}

// Tests for handleClientRequest focusing on the DecodeAndPrepareArg functionality
func TestBrokerHandleClientRequestDecode(t *testing.T) {
	t.Run("Value type request and response", func(t *testing.T) {
		bs := testutil.NewBrokerServer(t)
		defer bs.Shutdown(context.Background())

		const topicName = "test:value_decode"

		// Register handler for value types
		err := bs.HandleClientRequest(topicName,
			func(ch broker.ClientHandle, req BrokerValueRequest) (BrokerValueResponse, error) {
				// Verify the request was decoded correctly
				assert.Equal(t, "fetch", req.Command)
				assert.Equal(t, 3, req.Count)

				// Create a response with items based on the count
				items := make([]int, req.Count)
				for i := 0; i < req.Count; i++ {
					items[i] = i + 1
				}

				return BrokerValueResponse{
					Result: fmt.Sprintf("Processed %s command", req.Command),
					Items:  items,
				}, nil
			},
		)
		require.NoError(t, err)

		// Create a client
		cli := testutil.NewTestClient(t, bs.WSURL)
		defer cli.Close()

		// Send request
		ctx := context.Background()
		resp, err := client.GenericRequest[BrokerValueResponse](cli, ctx, topicName, BrokerValueRequest{
			Command: "fetch",
			Count:   3,
		})

		// Verify response
		require.NoError(t, err)
		assert.Equal(t, "Processed fetch command", resp.Result)
		assert.Equal(t, []int{1, 2, 3}, resp.Items)
	})

	t.Run("Pointer type request and response", func(t *testing.T) {
		bs := testutil.NewBrokerServer(t)
		defer bs.Shutdown(context.Background())

		const topicName = "test:ptr_decode"

		// Register handler for pointer types
		err := bs.HandleClientRequest(topicName,
			func(ch broker.ClientHandle, req *BrokerPtrRequest) (*BrokerPtrResponse, error) {
				// Verify the request was decoded correctly
				require.NotNil(t, req)
				assert.Equal(t, "query123", req.QueryID)
				
				// Check if the date was parsed correctly
				expectedDate, _ := time.Parse(time.RFC3339, "2023-01-15T10:00:00Z")
				assert.True(t, expectedDate.Equal(req.FromDate), 
					"Expected %v, got %v", expectedDate, req.FromDate)

				// Return a pointer response
				return &BrokerPtrResponse{
					Success:  true,
					RecordID: "record-" + req.QueryID,
					RecordData: &struct {
						Field1 string `json:"field1"`
						Field2 int    `json:"field2"`
					}{
						Field1: "test data",
						Field2: 42,
					},
				}, nil
			},
		)
		require.NoError(t, err)

		// Create a client
		cli := testutil.NewTestClient(t, bs.WSURL)
		defer cli.Close()

		// Send request
		ctx := context.Background()
		fromDate, _ := time.Parse(time.RFC3339, "2023-01-15T10:00:00Z")
		resp, err := client.GenericRequest[BrokerPtrResponse](cli, ctx, topicName, &BrokerPtrRequest{
			QueryID:  "query123",
			FromDate: fromDate,
		})

		// Verify response
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.True(t, resp.Success)
		assert.Equal(t, "record-query123", resp.RecordID)
		require.NotNil(t, resp.RecordData)
		assert.Equal(t, "test data", resp.RecordData.Field1)
		assert.Equal(t, 42, resp.RecordData.Field2)
	})

	t.Run("Null request payload", func(t *testing.T) {
		bs := testutil.NewBrokerServer(t)
		defer bs.Shutdown(context.Background())

		const topicName = "test:null_req_decode"
		requestHandled := false

		// Register handler that accepts null/nil request
		err := bs.HandleClientRequest(topicName,
			func(ch broker.ClientHandle, req *BrokerPtrRequest) (BrokerValueResponse, error) {
				requestHandled = true
				// A nil pointer should be received when null JSON is passed
				assert.Nil(t, req, "Expected nil request for null JSON payload")
				return BrokerValueResponse{
					Result: "Processed null request",
					Items:  []int{},
				}, nil
			},
		)
		require.NoError(t, err)

		// Create a client
		cli := testutil.NewTestClient(t, bs.WSURL)
		defer cli.Close()

		// Send request with explicitly null payload
		ctx := context.Background()
		nullPayload := json.RawMessage("null")
		
		// Get the raw request function to send null explicitly
		rawResp, errPayload, err := cli.SendServerRequest(ctx, topicName, nullPayload)
		require.NoError(t, err)
		require.Nil(t, errPayload)
		require.NotNil(t, rawResp)
		
		// Decode the response
		var resp BrokerValueResponse
		err = json.Unmarshal(*rawResp, &resp)
		require.NoError(t, err)
		
		// Verify response
		assert.True(t, requestHandled, "Request handler should have been called")
		assert.Equal(t, "Processed null request", resp.Result)
		assert.Empty(t, resp.Items)
	})

	t.Run("Invalid request JSON", func(t *testing.T) {
		bs := testutil.NewBrokerServer(t)
		defer bs.Shutdown(context.Background())

		const topicName = "test:invalid_json_decode"
		requestHandled := false

		// Register handler
		err := bs.HandleClientRequest(topicName,
			func(ch broker.ClientHandle, req BrokerValueRequest) (BrokerValueResponse, error) {
				requestHandled = true // Should not be called for invalid JSON
				return BrokerValueResponse{}, nil
			},
		)
		require.NoError(t, err)

		// Create a client
		cli := testutil.NewTestClient(t, bs.WSURL)
		defer cli.Close()

		// Send request with invalid JSON
		ctx := context.Background()
		invalidJSON := json.RawMessage(`{"command": "test", "count": invalid}`)
		
		// Get the raw request function to send invalid JSON
		_, errPayload, err := cli.SendServerRequest(ctx, topicName, invalidJSON)
		
		// Should have an error
		require.Error(t, err)
		// We don't require errPayload to be non-nil because sometimes the error occurs
		// during marshaling/sending, not at the broker level
		if errPayload != nil {
			assert.Contains(t, errPayload.Message, "Invalid request payload")
		} else {
			// If we don't have an error payload, the error should be from marshaling
			assert.Contains(t, err.Error(), "json")
		}
		assert.False(t, requestHandled, "Handler should not be called for invalid JSON")
	})

	t.Run("Handler returns error", func(t *testing.T) {
		bs := testutil.NewBrokerServer(t)
		defer bs.Shutdown(context.Background())

		const topicName = "test:handler_error_decode"
		const errorMsg = "Handler refused to process this command"

		// Register handler that returns an error
		err := bs.HandleClientRequest(topicName,
			func(ch broker.ClientHandle, req BrokerValueRequest) (BrokerValueResponse, error) {
				if req.Command == "forbidden" {
					return BrokerValueResponse{}, fmt.Errorf("%s", errorMsg)
				}
				return BrokerValueResponse{
					Result: "This should not be returned",
				}, nil
			},
		)
		require.NoError(t, err)

		// Create a client
		cli := testutil.NewTestClient(t, bs.WSURL)
		defer cli.Close()

		// Send request that will trigger the error
		ctx := context.Background()
		_, errPayload, err := cli.SendServerRequest(ctx, topicName, BrokerValueRequest{
			Command: "forbidden",
			Count:   1,
		})
		
		// Should have an error
		require.Error(t, err)
		require.NotNil(t, errPayload)
		assert.Contains(t, errPayload.Message, errorMsg)
		assert.Equal(t, 500, errPayload.Code) // Internal server error
	})

	t.Run("No handler for topic", func(t *testing.T) {
		bs := testutil.NewBrokerServer(t)
		defer bs.Shutdown(context.Background())

		const topicName = "test:nonexistent_topic"

		// Create a client
		cli := testutil.NewTestClient(t, bs.WSURL)
		defer cli.Close()

		// Send request to topic with no handler
		ctx := context.Background()
		_, errPayload, err := cli.SendServerRequest(ctx, topicName, BrokerValueRequest{})
		
		// Should have an error
		require.Error(t, err)
		require.NotNil(t, errPayload)
		assert.Contains(t, errPayload.Message, "No handler for topic")
		assert.Equal(t, 404, errPayload.Code) // Not found
	})

	t.Run("Handler with no response type", func(t *testing.T) {
		bs := testutil.NewBrokerServer(t)
		defer bs.Shutdown(context.Background())

		const topicName = "test:no_response_decode"
		handlerCalled := false

		// Register handler that doesn't return a response (only error)
		err := bs.HandleClientRequest(topicName,
			func(ch broker.ClientHandle, req BrokerValueRequest) error {
				handlerCalled = true
				assert.Equal(t, "process", req.Command)
				assert.Equal(t, 5, req.Count)
				return nil
			},
		)
		require.NoError(t, err)

		// Create a client
		cli := testutil.NewTestClient(t, bs.WSURL)
		defer cli.Close()

		// Send request
		ctx := context.Background()
		rawResp, errPayload, err := cli.SendServerRequest(ctx, topicName, BrokerValueRequest{
			Command: "process",
			Count:   5,
		})
		
		// Should succeed but with null/empty response
		require.NoError(t, err)
		require.Nil(t, errPayload)
		assert.True(t, handlerCalled)
		
		// Response should be null or empty object
		if rawResp != nil {
			assert.Contains(t, []string{"null", "{}", ""}, string(*rawResp))
		}
	})
}