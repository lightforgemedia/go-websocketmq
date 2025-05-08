package ps_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
)

// TestJavaScriptClient tests the JavaScript client implementation
func TestJavaScriptClient(t *testing.T) {
	// Skip by default - run manually when needed
	t.Skip("Skipping JavaScript client test - run manually when needed")

	// Start the WebSocketMQ server
	server := NewTestServer(t)
	defer server.Close()
	<-server.Ready

	// Create channels for test verification
	clientConnected := make(chan string) // Will receive the client ID
	clientEchoReceived := make(chan struct{})
	errorTestReceived := make(chan struct{})

	// Register handlers on the server
	// Handler for client registration
	server.RegisterHandler(broker.TopicClientRegistered, func(ctx context.Context, msg *model.Message, _ string) (*model.Message, error) {
		t.Logf("Client registered: %+v", msg.Body)
		if bodyMap, ok := msg.Body.(map[string]interface{}); ok {
			if clientID, ok := bodyMap["brokerClientID"].(string); ok {
				clientConnected <- clientID
			}
		} else if bodyMap, ok := msg.Body.(map[string]string); ok {
			if clientID, ok := bodyMap["brokerClientID"]; ok {
				clientConnected <- clientID
			}
		}
		return nil, nil
	})

	// Handler for client echo test
	server.RegisterHandler("client.echo", func(ctx context.Context, msg *model.Message, clientID string) (*model.Message, error) {
		t.Logf("Received client.echo from %s: %+v", clientID, msg.Body)
		close(clientEchoReceived)
		return model.NewResponse(msg, map[string]string{
			"result": "echo-response",
		}), nil
	})

	// Handler for client error test
	server.RegisterHandler("client.error", func(ctx context.Context, msg *model.Message, clientID string) (*model.Message, error) {
		t.Logf("Received client.error from %s: %+v", clientID, msg.Body)
		close(errorTestReceived)
		return model.NewErrorMessage(msg, map[string]string{
			"error": "intentional error for testing",
		}), nil
	})

	// Start a simple HTTP server to serve the test files
	httpServer := &http.Server{
		Addr:    ":8081",
		Handler: http.FileServer(http.Dir("testdata")),
	}
	go func() {
		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			t.Logf("HTTP server error: %v", err)
		}
	}()
	defer httpServer.Close()

	// Wait a bit for the HTTP server to start
	time.Sleep(100 * time.Millisecond)

	// Launch the browser with go-rod
	url := launcher.New().
		Headless(false). // Set to true for headless mode in CI
		MustLaunch()

	browser := rod.New().ControlURL(url).MustConnect()
	defer browser.MustClose()

	// Navigate to the test page
	wsURL := fmt.Sprintf("ws://%s/ws", server.Server.Listener.Addr().String())
	pageURL := fmt.Sprintf("http://localhost:8081/test.html?ws=%s", wsURL)
	t.Logf("Opening browser to %s", pageURL)

	page := browser.MustPage(pageURL)

	// Wait for the client to connect
	var clientID string
	select {
	case clientID = <-clientConnected:
		t.Logf("JavaScript client connected with ID: %s", clientID)
	case <-time.After(5 * time.Second):
		t.Fatal("Timed out waiting for JavaScript client to connect")
	}

	// Wait for Test 1 (Basic Connectivity) to pass
	waitForTestStatus(t, page, "test1", "passed", 5*time.Second)

	// Wait for Test 2 (Client-to-Server RPC) to start and complete
	waitForTestStatus(t, page, "test2", "passed", 5*time.Second)

	// Verify that the server received the client echo request
	select {
	case <-clientEchoReceived:
		t.Log("Server received client.echo request")
	case <-time.After(5 * time.Second):
		t.Fatal("Timed out waiting for client.echo request")
	}

	// Test 3: Server-to-Client RPC
	t.Log("Running Test 3: Server-to-Client RPC")
	resp, err := server.SendRPCToClient(context.Background(), clientID, "server.echo", map[string]string{
		"param": "server-param",
	}, 5000)
	if err != nil {
		t.Fatalf("Failed to send RPC to client: %v", err)
	}
	t.Logf("Received response from client: %+v", resp)

	// Wait for Test 3 to pass on the client side
	waitForTestStatus(t, page, "test3", "passed", 5*time.Second)

	// Test 4: Broadcast Event
	t.Log("Running Test 4: Broadcast Event")

	// For broadcast events, we'll use an RPC request that the client will handle as an event
	_, err = server.SendRPCToClient(context.Background(), clientID, "event.broadcast", map[string]string{
		"message": "broadcast-message",
	}, 5000)
	if err != nil {
		t.Fatalf("Failed to send broadcast event: %v", err)
	}

	// Wait for Test 4 to pass on the client side
	waitForTestStatus(t, page, "test4", "passed", 5*time.Second)

	// Wait for Test 5 (Error Handling) to complete
	waitForTestStatus(t, page, "test5", "passed", 5*time.Second)

	// Verify that the server received the error test request
	select {
	case <-errorTestReceived:
		t.Log("Server received client.error request")
	case <-time.After(5 * time.Second):
		t.Fatal("Timed out waiting for client.error request")
	}

	// Test 6: Timeout Handling
	t.Log("Running Test 6: Timeout Handling")
	_, err = server.SendRPCToClient(context.Background(), clientID, "server.timeout", map[string]string{
		"param": "timeout-param",
	}, 1000) // Short timeout
	if err == nil {
		t.Fatal("Expected timeout error but got success")
	}
	t.Logf("Received expected timeout error: %v", err)

	// Wait for Test 6 to pass on the client side
	waitForTestStatus(t, page, "test6", "passed", 5*time.Second)

	// Wait a bit to ensure all tests have completed
	time.Sleep(1 * time.Second)

	// Take a screenshot for debugging
	page.MustScreenshot("testdata/js_client_test_result.png")

	// Verify all tests have passed
	for i := 1; i <= 6; i++ {
		testID := fmt.Sprintf("test%d", i)
		status := page.MustElement(fmt.Sprintf("#%s", testID)).MustText()
		if status != "Passed" && !contains(status, "Passed") {
			t.Errorf("Test %d failed: %s", i, status)
		}
	}
}

// waitForTestStatus waits for a test to reach the specified status
func waitForTestStatus(t *testing.T, page *rod.Page, testID, expectedStatus string, timeout time.Duration) {
	t.Logf("Waiting for %s to be %s", testID, expectedStatus)

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status := page.MustElement(fmt.Sprintf("#%s", testID)).MustAttribute("class")
		if contains(*status, expectedStatus) {
			t.Logf("%s is now %s", testID, expectedStatus)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf("Timed out waiting for %s to be %s", testID, expectedStatus)
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return s != "" && s != "status pending" && s != "status failed" && (s == "status "+substr || s == substr)
}
