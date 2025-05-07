# WebSocketMQ Integration Test Analysis

## Failing Tests

The tests that are currently failing are:

1. **TestClientToServerRPC** in `pkg/broker/ps/integration_test.go`
   - Error: `Failed to send RPC: request timed out`
   - The client sends an RPC request to the server, but never receives a response

2. **TestServerToClientRPC** in `pkg/broker/ps/integration_test.go`
   - Error: `Client did not receive broker client ID` or `Timed out waiting for client registration event`
   - The client doesn't receive its broker client ID, which prevents the server from sending targeted RPC requests

## Root Causes of Failures

### 1. Client-to-Server RPC Failure

The logs show:
```
Publishing message: Topic=test.echo, Type=request, CorrID=1746651136846080000-2234070406429784733, SrcClientID=wsconn-1746651136845815000-313338814587531895
Dispatching to handler for topic test.echo, subID 0
Publishing message: Topic=1746651136846080000-2234070406429784733, Type=response, CorrID=1746651136846080000-2234070406429784733, SrcClientID=
```

This indicates:
1. The request is successfully published to the broker
2. The server handler is called and generates a response
3. The response is published to the correlation ID topic
4. However, the client never receives this response

The likely issues are:
- The client may not be properly subscribed to the correlation ID topic
- The response message may not be properly routed back to the client
- There might be a race condition where the response is published before the client is ready to receive it

### 2. Server-to-Client RPC Failure

The logs show:
```
Publishing message: Topic=_client.register, Type=event, CorrID=, SrcClientID=wsconn-1746651141950314000-4691227655437797902
```

But we don't see a corresponding message being published to `_internal.client.registered`.

The likely issues are:
- The server handler may not be correctly processing the client registration message
- The internal event may not be properly published or routed
- There might be a mismatch between the expected message format and what's being sent

## Suggested Next Steps

### 1. Debug Client-to-Server RPC

1. **Verify Response Routing**:
   ```go
   // In TestServer.RegisterHandler, add logging to track the response
   err := server.RegisterHandler("test.echo", func(ctx context.Context, msg *model.Message, clientID string) (*model.Message, error) {
       t.Logf("Handler received request: %+v", msg)
       resp := model.NewResponse(msg, map[string]string{"result": "echo-response"})
       t.Logf("Handler returning response: %+v", resp)
       return resp, nil
   })
   ```

2. **Add Subscription Tracing**:
   ```go
   // In TestClient.SendRPC, add explicit subscription to the correlation ID
   corrID := req.Header.CorrelationID
   tc.Logger.Debug("Explicitly subscribing to correlation ID: %s", corrID)
   tc.RegisterHandler(corrID, func(ctx context.Context, msg *model.Message, _ string) (*model.Message, error) {
       tc.Logger.Debug("Received message on correlation ID topic: %+v", msg)
       return nil, nil
   })
   ```

3. **Simplify the Test**:
   Create a simpler test that just verifies the broker can route messages from one subscriber to another, without the WebSocket layer.

### 2. Debug Server-to-Client RPC

1. **Verify Registration Processing**:
   ```go
   // In TestClient.register, modify the registration message to ensure it matches what the server expects
   regMsg := model.NewEvent("_client.register", map[string]string{
       "pageSessionID": tc.PageSessionID,
   })
   tc.Logger.Debug("Sending registration message: %+v", regMsg)
   ```

2. **Add Direct Subscription**:
   ```go
   // In TestServer, add a direct subscription to the client registration topic
   server.Broker.Subscribe(context.Background(), "_client.register", func(ctx context.Context, msg *model.Message, clientID string) (*model.Message, error) {
       t.Logf("Server received direct registration message: %+v", msg)
       return nil, nil
   })
   ```

3. **Inspect Server Handler**:
   Review the server handler implementation to ensure it's correctly processing the client registration message and publishing the internal event.

### 3. Implement Simplified Tests

1. **Create a Basic Pub/Sub Test**:
   ```go
   func TestBasicPubSub(t *testing.T) {
       broker := ps.New(NewTestLogger(t), broker.DefaultOptions())
       defer broker.Close()
       
       received := make(chan struct{})
       broker.Subscribe(context.Background(), "test.topic", func(ctx context.Context, msg *model.Message, _ string) (*model.Message, error) {
           t.Logf("Received message: %+v", msg)
           close(received)
           return nil, nil
       })
       
       broker.Publish(context.Background(), model.NewEvent("test.topic", "test message"))
       
       select {
       case <-received:
           // Success
       case <-time.After(1 * time.Second):
           t.Fatal("Timed out waiting for message")
       }
   }
   ```

2. **Create a Basic Request/Response Test**:
   ```go
   func TestBasicRequestResponse(t *testing.T) {
       broker := ps.New(NewTestLogger(t), broker.DefaultOptions())
       defer broker.Close()
       
       broker.Subscribe(context.Background(), "test.echo", func(ctx context.Context, msg *model.Message, _ string) (*model.Message, error) {
           return model.NewResponse(msg, "echo response"), nil
       })
       
       ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
       defer cancel()
       
       resp, err := broker.Request(ctx, model.NewRequest("test.echo", "test request", 5000), 5000)
       if err != nil {
           t.Fatalf("Request failed: %v", err)
       }
       
       t.Logf("Received response: %+v", resp)
   }
   ```

### 4. Improve Test Infrastructure

1. **Add More Logging**:
   Add detailed logging throughout the test infrastructure to better understand the flow of messages.

2. **Implement Retry Logic**:
   Add retry logic for operations that might fail due to timing issues.

3. **Use Explicit Synchronization**:
   Replace timeouts with explicit synchronization using channels and WaitGroups.

## Current Working Tests

We have successfully implemented and verified:

1. **TestBasicConnectivity** - Verifies that a client can connect to the server
2. **TestBasicConnectivity2** - A duplicate test to confirm the reliability of the connection process

These tests confirm that the basic WebSocket connection and initial handshake are working correctly, which provides a foundation for debugging the more complex RPC functionality.

## Implementation Plan

1. Start with the simplified broker tests (TestBasicPubSub and TestBasicRequestResponse) to verify the core message routing functionality
2. Add the debugging code to the client-to-server RPC test to identify why responses aren't being received
3. Fix the client registration process to ensure the client receives its broker client ID
4. Implement the server-to-client RPC test once the registration process is working
5. Add error handling tests to verify the system's robustness

By following this plan, we should be able to identify and fix the issues with the integration tests, leading to a more robust test suite for the WebSocketMQ library.
