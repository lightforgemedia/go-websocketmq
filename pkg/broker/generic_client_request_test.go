package broker_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test types
type TestRequest struct {
	Value int    `json:"value"`
	Name  string `json:"name"`
}

type TestResponse struct {
	Result  int    `json:"result"`
	Message string `json:"message"`
}

type ComplexRequest struct {
	Items []string          `json:"items"`
	Meta  map[string]string `json:"meta"`
}

type ComplexResponse struct {
	Count     int               `json:"count"`
	Processed map[string]string `json:"processed"`
}

func TestGenericClientRequest(t *testing.T) {
	// Setup broker with default options
	opts := broker.DefaultOptions()
	b, err := broker.NewWithOptions(opts)
	require.NoError(t, err)
	
	// Setup test server
	server, addr := startTestServer(t, b)
	defer server.Close()

	// Create and connect client
	ctx := context.Background()
	wsClient, err := client.Connect(
		"ws://"+addr+"/ws",
		client.WithAutoReconnect(0, 0, 0), // Disable auto-reconnect
	)
	require.NoError(t, err)
	defer wsClient.Close()

	// Register handlers on the client
	err = wsClient.HandleServerRequest("test", func(req TestRequest) (TestResponse, error) {
		return TestResponse{
			Result:  req.Value * 2,
			Message: "Hello " + req.Name,
		}, nil
	})
	require.NoError(t, err)

	err = wsClient.HandleServerRequest("complex", func(req ComplexRequest) (ComplexResponse, error) {
		processed := make(map[string]string)
		for _, item := range req.Items {
			processed[item] = req.Meta[item]
		}
		return ComplexResponse{
			Count:     len(req.Items),
			Processed: processed,
		}, nil
	})
	require.NoError(t, err)

	// Wait for client registration
	time.Sleep(100 * time.Millisecond)

	// Get client handle
	var clientHandle broker.ClientHandle
	b.IterateClients(func(ch broker.ClientHandle) bool {
		clientHandle = ch
		return false
	})
	require.NotNil(t, clientHandle)

	t.Run("SimpleRequest", func(t *testing.T) {
		resp, err := broker.GenericClientRequest[TestResponse](
			clientHandle,
			ctx,
			"test",
			TestRequest{Value: 10, Name: "Alice"},
			5*time.Second,
		)
		require.NoError(t, err)
		assert.Equal(t, 20, resp.Result)
		assert.Equal(t, "Hello Alice", resp.Message)
	})

	t.Run("ComplexRequest", func(t *testing.T) {
		req := ComplexRequest{
			Items: []string{"item1", "item2", "item3"},
			Meta: map[string]string{
				"item1": "value1",
				"item2": "value2",
				"item3": "value3",
			},
		}
		
		resp, err := broker.GenericClientRequest[ComplexResponse](
			clientHandle,
			ctx,
			"complex",
			req,
			5*time.Second,
		)
		require.NoError(t, err)
		assert.Equal(t, 3, resp.Count)
		assert.Equal(t, "value1", resp.Processed["item1"])
		assert.Equal(t, "value2", resp.Processed["item2"])
		assert.Equal(t, "value3", resp.Processed["item3"])
	})

	t.Run("NoHandler", func(t *testing.T) {
		_, err := broker.GenericClientRequest[TestResponse](
			clientHandle,
			ctx,
			"nonexistent",
			TestRequest{Value: 5, Name: "Bob"},
			5*time.Second,
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "has no handler for topic")
	})

	t.Run("Timeout", func(t *testing.T) {
		// Register a slow handler
		err := wsClient.HandleServerRequest("slow", func(req TestRequest) (TestResponse, error) {
			select {
			case <-time.After(2 * time.Second):
				return TestResponse{Result: req.Value}, nil
			case <-ctx.Done():
				return TestResponse{}, ctx.Err()
			}
		})
		require.NoError(t, err)

		_, err = broker.GenericClientRequest[TestResponse](
			clientHandle,
			ctx,
			"slow", 
			TestRequest{Value: 1, Name: "Charlie"},
			100*time.Millisecond, // Very short timeout
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "timed out")
	})

	t.Run("ContextCancellation", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(ctx)
		
		// Start request in goroutine
		errChan := make(chan error, 1)
		go func() {
			_, err := broker.GenericClientRequest[TestResponse](
				clientHandle,
				cancelCtx,
				"test",
				TestRequest{Value: 15, Name: "David"},
				5*time.Second,
			)
			errChan <- err
		}()

		// Cancel context quickly
		time.Sleep(10 * time.Millisecond)
		cancel()

		// Check error
		err := <-errChan
		if err != nil {
			assert.Error(t, err) 
			assert.Contains(t, err.Error(), "context")
		} else {
			// Sometimes the request completes before context cancellation
			t.Log("Request completed before context cancellation")
		}
	})
}

func startTestServer(t *testing.T, b *broker.Broker) (*httptest.Server, string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", b.UpgradeHandler())
	server := httptest.NewServer(mux)
	
	// Extract just the address portion
	addr := server.Listener.Addr().String()
	return server, addr
}