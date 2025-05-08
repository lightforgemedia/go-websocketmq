package ps

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"github.com/lightforgemedia/go-websocketmq/pkg/server"
)

// TestServer represents a WebSocketMQ server for testing
type TestServer struct {
	Server   *httptest.Server
	Broker   broker.Broker
	Handler  *server.Handler
	Ready    chan struct{}
	Shutdown chan struct{}
}

// TestLogger is a simple logger for tests
type TestLogger struct {
	t *testing.T
}

// Debug logs a debug message
func (l *TestLogger) Debug(format string, args ...interface{}) {
	l.t.Logf("DEBUG: "+format, args...)
}

// Info logs an info message
func (l *TestLogger) Info(format string, args ...interface{}) {
	l.t.Logf("INFO: "+format, args...)
}

// Warn logs a warning message
func (l *TestLogger) Warn(format string, args ...interface{}) {
	l.t.Logf("WARN: "+format, args...)
}

// Error logs an error message
func (l *TestLogger) Error(format string, args ...interface{}) {
	l.t.Logf("ERROR: "+format, args...)
}

// NewTestServer creates and starts a new test server
func NewTestServer(t *testing.T) *TestServer {
	ts := &TestServer{
		Ready:    make(chan struct{}),
		Shutdown: make(chan struct{}),
	}

	// Create a logger
	logger := &TestLogger{t: t}

	// Create broker
	brokerOpts := broker.DefaultOptions()
	ts.Broker = New(logger, brokerOpts)

	// Create WebSocket handler with CORS enabled
	handlerOpts := server.DefaultHandlerOptions()
	handlerOpts.AllowedOrigins = []string{"*"} // Allow all origins for testing
	ts.Handler = server.NewHandler(ts.Broker, logger, handlerOpts)

	// Create HTTP server
	mux := http.NewServeMux()
	mux.Handle("/ws", ts.Handler)

	// Start server
	ts.Server = httptest.NewServer(mux)

	// Signal that the server is ready
	close(ts.Ready)

	return ts
}

// RegisterHandler registers an RPC handler on the server
func (ts *TestServer) RegisterHandler(topic string, handler broker.MessageHandler) error {
	return ts.Broker.Subscribe(context.Background(), topic, handler)
}

// SendRPCToClient sends an RPC request to a specific client
func (ts *TestServer) SendRPCToClient(ctx context.Context, clientID string, topic string, body interface{}, timeoutMs int64) (*model.Message, error) {
	req := model.NewRequest(topic, body, timeoutMs)
	return ts.Broker.RequestToClient(ctx, clientID, req, timeoutMs)
}

// Close shuts down the test server
func (ts *TestServer) Close() {
	ts.Server.Close()
	ts.Broker.Close()
	close(ts.Shutdown)
}

// TestJavaScriptClientSimple tests that a JavaScript client can connect to the server
func TestJavaScriptClientSimple(t *testing.T) {
	// Skip by default - run manually when needed
	t.Skip("Skipping JavaScript client test - run manually when needed")
	// Start the WebSocketMQ server using the existing TestServer from integration_test.go
	server := NewTestServer(t)
	defer server.Close()
	<-server.Ready

	// Print server information
	t.Logf("WebSocket server URL: %s", server.Server.URL)

	// Create a channel for client connection verification
	clientConnected := make(chan string) // Will receive the client ID

	// Register handler for client registration
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

	// Start a simple HTTP server to serve the test files
	t.Log("Starting HTTP server to serve test files from testdata directory")

	// Get the project root directory (3 levels up from the test file)
	_, filename, _, _ := runtime.Caller(0)
	projectRoot := filepath.Join(filepath.Dir(filename), "../../..")
	testdataPath := filepath.Join(projectRoot, "testdata")

	// Check if testdata directory exists
	if _, err := os.Stat(testdataPath); os.IsNotExist(err) {
		t.Fatalf("testdata directory does not exist: %v", err)
	} else {
		t.Log("testdata directory exists at: " + testdataPath)
	}

	// Create a file server on port 8081
	fileServer := http.FileServer(http.Dir(testdataPath))
	httpServer := &http.Server{
		Addr:    ":8081", // Use port 8081 (not 8080)
		Handler: fileServer,
	}

	// Start the file server
	go func() {
		t.Logf("HTTP file server listening on http://localhost:8081")
		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			t.Logf("HTTP file server error: %v", err)
		}
	}()
	defer httpServer.Shutdown(context.Background())

	// Wait a bit for the HTTP server to start
	time.Sleep(500 * time.Millisecond)

	// Launch the browser with go-rod
	url := launcher.New().
		Headless(false). // Set to true for headless mode in CI
		MustLaunch()

	browser := rod.New().ControlURL(url).MustConnect()
	defer browser.MustClose()

	// Get the WebSocket URL from the test server
	wsURL := strings.Replace(server.Server.URL, "http://", "ws://", 1) + "/ws"
	t.Logf("WebSocket URL: %s", wsURL)

	// Create the test page URL
	pageURL := fmt.Sprintf("http://localhost:8081/simple_test.html?ws=%s", wsURL)
	t.Logf("Opening browser to test page: %s", pageURL)

	// Navigate to the test page
	page := browser.MustPage(pageURL)

	// Wait a bit for the page to load and connect
	time.Sleep(5 * time.Second)

	// Take a screenshot of the test page
	page.MustScreenshot("testdata/simple_test_result.png")
	t.Log("Took screenshot of test page")

	// Check if we got a connection
	statusText := page.MustElement("#status").MustText()
	t.Logf("Connection status: %s", statusText)

	if statusText == "Connected" {
		t.Log("WebSocket test connected successfully")

		// Wait for the client to connect
		var clientID string
		select {
		case clientID = <-clientConnected:
			t.Logf("JavaScript client connected with ID: %s", clientID)
			t.Log("JavaScript client successfully connected to the server")
		case <-time.After(10 * time.Second):
			t.Fatal("Timed out waiting for JavaScript client to connect")
		}
	} else {
		t.Fatalf("WebSocket test failed to connect. Status: %s", statusText)
	}
}

// TestJavaScriptClientRPC tests RPC communication between the server and JavaScript client
func TestJavaScriptClientRPC(t *testing.T) {
	// Skip by default - run manually when needed
	t.Skip("Skipping JavaScript client RPC test - run manually when needed")

	// Start the WebSocketMQ server
	server := NewTestServer(t)
	defer server.Close()
	<-server.Ready

	// Print server information
	t.Logf("WebSocket server URL: %s", server.Server.URL)

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
	t.Log("Starting HTTP server to serve test files from testdata directory")

	// Get the project root directory (3 levels up from the test file)
	_, filename, _, _ := runtime.Caller(0)
	projectRoot := filepath.Join(filepath.Dir(filename), "../../..")
	testdataPath := filepath.Join(projectRoot, "testdata")

	// Check if testdata directory exists
	if _, err := os.Stat(testdataPath); os.IsNotExist(err) {
		t.Fatalf("testdata directory does not exist: %v", err)
	} else {
		t.Log("testdata directory exists at: " + testdataPath)
	}

	// Create a file server on port 8081
	fileServer := http.FileServer(http.Dir(testdataPath))
	httpServer := &http.Server{
		Addr:    ":8081", // Use port 8081 (not 8080)
		Handler: fileServer,
	}

	// Start the file server
	go func() {
		t.Logf("HTTP file server listening on http://localhost:8081")
		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			t.Logf("HTTP file server error: %v", err)
		}
	}()
	defer httpServer.Shutdown(context.Background())

	// Wait a bit for the HTTP server to start
	time.Sleep(500 * time.Millisecond)

	// Launch the browser with go-rod
	url := launcher.New().
		Headless(false). // Set to true for headless mode in CI
		MustLaunch()

	browser := rod.New().ControlURL(url).MustConnect()
	defer browser.MustClose()

	// Get the WebSocket URL from the test server
	wsURL := strings.Replace(server.Server.URL, "http://", "ws://", 1) + "/ws"
	t.Logf("WebSocket URL: %s", wsURL)

	// Create the test page URL - use the full test page
	pageURL := fmt.Sprintf("http://localhost:8081/test.html?ws=%s", wsURL)
	t.Logf("Opening browser to test page: %s", pageURL)

	// Navigate to the test page
	page := browser.MustPage(pageURL)

	// Wait for the client to connect
	var clientID string
	select {
	case clientID = <-clientConnected:
		t.Logf("JavaScript client connected with ID: %s", clientID)
	case <-time.After(30 * time.Second): // Increased timeout for connection
		t.Fatal("Timed out waiting for JavaScript client to connect")
	}

	// Wait for Test 1 (Basic Connectivity) to pass
	waitForTestStatus(t, page, "test1", "passed", 15*time.Second)

	// Wait for Test 2 (Client-to-Server RPC) to start and complete
	waitForTestStatus(t, page, "test2", "passed", 15*time.Second)

	// Verify that the server received the client echo request
	select {
	case <-clientEchoReceived:
		t.Log("Server received client.echo request")
	case <-time.After(15 * time.Second):
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
	waitForTestStatus(t, page, "test3", "passed", 15*time.Second)

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
	waitForTestStatus(t, page, "test4", "passed", 15*time.Second)

	// Wait for Test 5 (Error Handling) to complete
	waitForTestStatus(t, page, "test5", "passed", 15*time.Second)

	// Verify that the server received the error test request
	select {
	case <-errorTestReceived:
		t.Log("Server received client.error request")
	case <-time.After(15 * time.Second):
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
	waitForTestStatus(t, page, "test6", "passed", 15*time.Second)

	// Wait a bit to ensure all tests have completed
	time.Sleep(1 * time.Second)

	// Take a screenshot for debugging
	page.MustScreenshot("testdata/js_client_rpc_test_result.png")

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
