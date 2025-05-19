# Browser Control Example

This example demonstrates how to use go-websocketmq's proxy capability to control a browser client from another client (CLI or service).

## Architecture

1. **Broker Server**: Routes messages between clients and handles proxy requests (uses Options pattern for configuration)
2. **Browser Client**: A web page with WebSocketMQ JavaScript client that can be controlled remotely
3. **Control Client**: A CLI client that sends commands to control the browser (uses Options pattern for connection)

## How Proxy Requests Work

The proxy mechanism uses the `system:proxy` topic with the following flow:

1. Control client sends a proxy request to the broker containing:
   - TargetID: The browser client's ID
   - Topic: The command topic (e.g., "browser.navigate", "browser.alert")
   - Payload: The command data

2. Broker forwards the request to the target browser client

3. Browser client processes the command and sends a response back through the broker

4. Control client receives the response

## Running the Example

1. Start the broker server:
   ```bash
   go run main.go
   ```

2. Open the browser client at http://localhost:8080/

3. Note the browser client ID displayed on the page

4. Run the control client with commands:
   ```bash
   # List connected clients
   go run cmd/control/main.go list

   # Navigate browser to a URL
   go run cmd/control/main.go navigate <client-id> https://example.com

   # Show alert in browser
   go run cmd/control/main.go alert <client-id> "Hello from CLI!"

   # Execute JavaScript in browser
   go run cmd/control/main.go exec <client-id> "document.body.style.backgroundColor = 'lightblue'"
   ```

## Security Note

This example is for demonstration purposes. In production, you should implement:
- Authentication and authorization
- Command validation and sanitization
- Rate limiting
- Audit logging