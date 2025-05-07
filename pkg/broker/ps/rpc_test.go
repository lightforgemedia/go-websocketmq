package ps_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps/testutil"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"github.com/lightforgemedia/go-websocketmq/pkg/server"
)

// TestEnhancedClientToServerRPC tests client-to-server RPC with additional debugging
func TestEnhancedClientToServerRPC(t *testing.T) {
	// Start the server with additional debugging
	serverLogger := testutil.NewTestLogger(t)
	serverLogger.Debug("Creating broker")

	// Create broker with default options
	brokerOpts := broker.DefaultOptions()
	b := ps.New(serverLogger, brokerOpts)

	// Create WebSocket handler
	handlerOpts := server.DefaultHandlerOptions()
	h := server.NewHandler(b, serverLogger, handlerOpts)

	// Create HTTP server
	mux := http.NewServeMux()
	mux.Handle("/ws", h)

	// Start server
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()
	defer b.Close()

	serverLogger.Debug("Server started at %s", httpServer.URL)

	// Subscribe to all topics for debugging
	b.Subscribe(context.Background(), "_internal.#", func(ctx context.Context, msg *model.Message, clientID string) (*model.Message, error) {
		t.Logf("Server received internal message: Topic=%s, Type=%s, CorrID=%s, ClientID=%s",
			msg.Header.Topic, msg.Header.Type, msg.Header.CorrelationID, clientID)
		return nil, nil
	})

	// Register an RPC handler on the server with detailed logging
	handlerCalled := make(chan struct{})
	expectedParam := "test-param"

	err := b.Subscribe(context.Background(), "test.echo", func(ctx context.Context, msg *model.Message, clientID string) (*model.Message, error) {
		t.Logf("Server handler received request: %+v from client %s", msg, clientID)

		// Verify the request parameters
		params, ok := msg.Body.(map[string]interface{})
		if !ok {
			t.Errorf("Expected map[string]interface{}, got %T", msg.Body)
		} else if params["param"] != expectedParam {
			t.Errorf("Expected param=%s, got %v", expectedParam, params["param"])
		}

		// Signal that the handler was called
		close(handlerCalled)

		// Create response
		resp := model.NewResponse(msg, map[string]string{
			"result": "echo-" + expectedParam,
		})

		t.Logf("Server handler returning response: %+v", resp)
		return resp, nil
	})

	if err != nil {
		t.Fatalf("Failed to register handler: %v", err)
	}

	// Create and connect a client
	client := NewTestClient(t, httpServer.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Add explicit subscription to all topics for debugging
	client.RegisterHandler("#", func(ctx context.Context, msg *model.Message, _ string) (*model.Message, error) {
		t.Logf("Client received message on wildcard subscription: %+v", msg)
		return nil, nil
	})

	err = client.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect client: %v", err)
	}
	defer client.Close()

	// Wait for the client to be connected
	<-client.Connected

	// Wait a bit to ensure all subscriptions are set up
	time.Sleep(500 * time.Millisecond)

	// Send an RPC request to the server with explicit correlation ID subscription
	corrID := model.RandomID()
	req := model.NewRequest("test.echo", map[string]string{
		"param": expectedParam,
	}, 5000)
	req.Header.CorrelationID = corrID

	// Explicitly subscribe to the correlation ID topic
	responseReceived := make(chan *model.Message, 1)

	// Add a wildcard handler to catch all messages
	client.RegisterHandler("#", func(ctx context.Context, msg *model.Message, _ string) (*model.Message, error) {
		t.Logf("Client received message on wildcard handler: Topic=%s, Type=%s, CorrID=%s",
			msg.Header.Topic, msg.Header.Type, msg.Header.CorrelationID)

		// Check if this is a response to our request
		if (msg.Header.Type == model.KindResponse || msg.Header.Type == model.KindError) &&
			msg.Header.CorrelationID == corrID {
			t.Logf("Found matching response for our request: %+v", msg)
			select {
			case responseReceived <- msg:
				t.Logf("Successfully sent response to channel")
			default:
				t.Logf("Channel full or closed, couldn't send response")
			}
		}
		return nil, nil
	})

	t.Logf("Client sending request: %+v", req)
	err = client.sendMessage(ctx, req)
	if err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}

	// Wait for the handler to be called
	select {
	case <-handlerCalled:
		t.Log("Server handler was called")
	case <-time.After(5 * time.Second):
		t.Fatal("Timed out waiting for server handler to be called")
	}

	// Wait for the response
	select {
	case resp := <-responseReceived:
		t.Logf("Client received response: %+v", resp)

		// Verify the response
		if resp.Header.Type != model.KindResponse {
			t.Errorf("Expected response type %s, got %s", model.KindResponse, resp.Header.Type)
		}

		if resp.Header.CorrelationID != corrID {
			t.Errorf("Expected correlation ID %s, got %s", corrID, resp.Header.CorrelationID)
		}

		result, ok := resp.Body.(map[string]interface{})
		if !ok {
			t.Errorf("Expected map[string]interface{}, got %T", resp.Body)
		} else if result["result"] != "echo-"+expectedParam {
			t.Errorf("Expected result=echo-%s, got %v", expectedParam, result["result"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Timed out waiting for response")
	}
}
