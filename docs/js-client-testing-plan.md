# JavaScript Client Testing Plan for WebSocketMQ

## Overview

This document outlines the plan for implementing and testing the JavaScript client for WebSocketMQ, where the Go server communicates with a JavaScript client running in a browser.

## Testing Approach

1. **Server Side**: Use the existing Go WebSocketMQ server implementation.

2. **Client Side**: Create a JavaScript client that runs in a web browser and automatically connects to the WebSocketMQ server when the page loads.

3. **Test Automation**: Use go-rod for browser automation.

4. **Automatic Test Execution**: Tests will run automatically when the browser opens, without requiring any user interaction.

## Test Implementation Details

### 1. Test Server Setup

```go
// Start the WebSocketMQ server
server := NewTestServer(t)
defer server.Close()
<-server.Ready

// Set up server-side handlers and verification channels
messageReceived := make(chan struct{})
server.RegisterHandler("client.message", func(ctx context.Context, msg *model.Message, clientID string) (*model.Message, error) {
    // Verify message from JavaScript client
    close(messageReceived)
    return model.NewResponse(msg, "response"), nil
})

// Serve the test HTML page
go http.ListenAndServe(":8081", http.FileServer(http.Dir("./testdata")))
```

### 2. JavaScript Client Implementation

The JavaScript client will be embedded in an HTML page that's served during the test:

```html
<!DOCTYPE html>
<html>
<head>
    <title>WebSocketMQ JavaScript Client Test</title>
    <script src="/websocketmq.js"></script>
    <script>
        // Automatically connect and run tests when page loads
        window.onload = function() {
            const client = new WebSocketMQClient('ws://localhost:8080/ws');
            
            client.onConnect = function() {
                console.log('Connected to WebSocketMQ server');
                runTests(client);
            };
            
            client.onError = function(error) {
                console.error('Connection error:', error);
                document.getElementById('status').textContent = 'Error: ' + error;
            };
            
            function runTests(client) {
                // Test 1: Basic RPC
                client.sendRPC('client.message', { test: 'basic-rpc' }, 5000)
                    .then(response => {
                        console.log('Test 1 response:', response);
                        document.getElementById('test1').textContent = 'Passed';
                    })
                    .catch(err => {
                        console.error('Test 1 failed:', err);
                        document.getElementById('test1').textContent = 'Failed: ' + err;
                    });
                
                // Additional tests will be added here
            }
        };
    </script>
</head>
<body>
    <h1>WebSocketMQ JavaScript Client Test</h1>
    <div id="status">Connecting...</div>
    <div>Test 1 (Basic RPC): <span id="test1">Pending</span></div>
    <!-- Additional test result elements will be added here -->
</body>
</html>
```

### 3. Browser Automation with go-rod

```go
// Launch browser with go-rod
browser := rod.New().MustConnect()
defer browser.MustClose()

// Navigate to the test page
page := browser.MustPage("http://localhost:8081/test.html")

// Wait for connection to be established
page.MustElement("#status").MustContain("Connected")

// Verify that the server received the message from the JavaScript client
select {
case <-messageReceived:
    t.Log("Server received message from JavaScript client")
case <-time.After(5 * time.Second):
    t.Fatal("Timed out waiting for message from JavaScript client")
}

// Verify test results on the page
page.MustElement("#test1").MustContain("Passed")
```

## Test Cases to Implement

We will implement the following test cases, mirroring our Go-to-Go tests:

1. **TestJSClientBasicConnectivity**: Verify that the JavaScript client can connect to the server.

2. **TestJSClientToServerRPC**: Test that the JavaScript client can send RPC requests to the server and receive responses.

3. **TestServerToJSClientRPC**: Test that the server can send RPC requests to the JavaScript client and receive responses.

4. **TestBroadcastEventToJSClient**: Test that the server can broadcast events to the JavaScript client.

5. **TestJSClientHandlesError**: Test that the JavaScript client properly handles error responses.

6. **TestJSClientRPCTimeout**: Test that the JavaScript client properly handles timeouts.

7. **TestJSClientReconnection**: Test that the JavaScript client can reconnect after a disconnection.

## Implementation Requirements

1. **Automatic Execution**: All tests must run automatically when the page loads, without requiring any user interaction.

2. **Visual Feedback**: The test page should provide visual feedback about the test status for debugging purposes.

3. **Comprehensive Logging**: Both client-side and server-side logs should be captured for debugging.

4. **Error Handling**: Tests should properly handle and report errors.

5. **Cleanup**: All resources (servers, browser instances) should be properly cleaned up after tests.

## Next Steps

1. Implement the JavaScript client library (`websocketmq.js`)
2. Create the test HTML page
3. Implement the Go test that uses go-rod to automate the browser
4. Run and verify the tests
5. Iterate and improve as needed
