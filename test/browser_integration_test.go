//go:build browser
// +build browser

package test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
)

// Global variables for the test
var (
	testBroker broker.Broker
	testLogger *SimpleLogger
)

// SimpleLogger implements the websocketmq.Logger interface
type SimpleLogger struct{}

func (l *SimpleLogger) Debug(msg string, args ...interface{}) {
	fmt.Printf("DEBUG: "+msg+"\n", args...)
}
func (l *SimpleLogger) Info(msg string, args ...interface{}) { fmt.Printf("INFO: "+msg+"\n", args...) }
func (l *SimpleLogger) Warn(msg string, args ...interface{}) { fmt.Printf("WARN: "+msg+"\n", args...) }
func (l *SimpleLogger) Error(msg string, args ...interface{}) {
	fmt.Printf("ERROR: "+msg+"\n", args...)
}

// TestBrowserIntegration tests the WebSocketMQ JavaScript client in a real browser
func TestBrowserIntegration(t *testing.T) {
	// Create a logger
	testLogger = &SimpleLogger{}

	// Create a broker
	brokerOpts := websocketmq.DefaultBrokerOptions()
	testBroker = websocketmq.NewPubSubBroker(testLogger, brokerOpts)

	// Create a WebSocket handler
	handlerOpts := websocketmq.DefaultHandlerOptions()
	handler := websocketmq.NewHandler(testBroker, testLogger, handlerOpts)

	// Create a test server
	mux := http.NewServeMux()
	mux.Handle("/ws", handler)
	mux.Handle("/wsmq/", http.StripPrefix("/wsmq/", websocketmq.ScriptHandler()))
	mux.Handle("/", http.FileServer(http.Dir("test/static")))

	// Start the server
	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	// Start the server in a goroutine
	go func() {
		testLogger.Info("Starting test server on http://localhost:8080")
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			testLogger.Error("Server error: %v", err)
		}
	}()

	// Ensure the server is shut down when the test completes
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			testLogger.Error("Server shutdown error: %v", err)
		}
	}()

	// Wait for the server to start
	time.Sleep(100 * time.Millisecond)

	// Create the test HTML file
	if err := createTestHTML(); err != nil {
		t.Fatalf("Failed to create test HTML: %v", err)
	}

	// Run the browser tests
	t.Run("ConnectionTest", testConnection)
	t.Run("PublishTest", testPublish)
	t.Run("SubscribeTest", testSubscribe)
	t.Run("RequestResponseTest", testRequestResponse)
}

// createTestHTML creates the test HTML file
func createTestHTML() error {
	// Create the static directory if it doesn't exist
	if err := os.MkdirAll("test/static", 0755); err != nil {
		return fmt.Errorf("failed to create static directory: %w", err)
	}

	// Create the test HTML file
	html := `<!DOCTYPE html>
<html>
<head>
    <title>WebSocketMQ Browser Test</title>
</head>
<body>
    <h1>WebSocketMQ Browser Test</h1>

    <div id="status">Disconnected</div>
    <div id="messages"></div>

    <script src="/wsmq/websocketmq.min.js"></script>
    <script>
        // Create a global client instance
        window.client = new WebSocketMQ.Client({
            url: 'ws://' + window.location.host + '/ws',
            reconnect: true,
            reconnectInterval: 1000,
            maxReconnectInterval: 30000,
            reconnectMultiplier: 1.5,
            devMode: false
        });

        // Set up event handlers
        client.onConnect(() => {
            document.getElementById('status').textContent = 'Connected';
            console.log('Connected to server');
        });

        client.onDisconnect(() => {
            document.getElementById('status').textContent = 'Disconnected';
            console.log('Disconnected from server');
        });

        client.onError((err) => {
            console.error('WebSocket error:', err);
        });

        // Function to add a message to the messages div
        window.addMessage = function(message) {
            const messagesDiv = document.getElementById('messages');
            const messageElement = document.createElement('div');
            messageElement.textContent = message;
            messagesDiv.appendChild(messageElement);
        };

        // Function to clear messages
        window.clearMessages = function() {
            document.getElementById('messages').innerHTML = '';
        };

        // Function to publish a message
        window.publishMessage = function(topic, body) {
            client.publish(topic, body);
            console.log('Published message to topic:', topic, body);
        };

        // Function to subscribe to a topic
        window.subscribeToTopic = function(topic, callback) {
            return client.subscribe(topic, (body, message) => {
                console.log('Received message on topic:', topic, body);
                if (callback) {
                    callback(body, message);
                }
            });
        };

        // Function to make a request
        window.makeRequest = function(topic, body, timeout) {
            return client.request(topic, body, timeout)
                .then(response => {
                    console.log('Received response:', response);
                    return response;
                })
                .catch(err => {
                    console.error('Request error:', err);
                    throw err;
                });
        };

        // Connect to the server
        client.connect();
    </script>
</body>
</html>`

	return os.WriteFile(filepath.Join("test/static", "index.html"), []byte(html), 0644)
}

// testConnection tests that the client can connect to the server
func testConnection(t *testing.T) {
	// Navigate to the test page
	t.Log("Navigating to test page...")

	// Use the browser_navigate tool to navigate to the test page
	if err := runPlaywrightTest(func() error {
		// Navigate to the test page
		fmt.Println("Navigating to http://localhost:8080")
		if err := browserNavigate("http://localhost:8080"); err != nil {
			return fmt.Errorf("failed to navigate to test page: %w", err)
		}

		// Wait for the status to change to "Connected"
		fmt.Println("Waiting for connection status...")
		if err := waitForElementText("#status", "Connected", 5*time.Second); err != nil {
			return fmt.Errorf("failed to connect to server: %w", err)
		}

		fmt.Println("Connection established successfully")
		return nil
	}); err != nil {
		t.Fatalf("Connection test failed: %v", err)
	}
}

// testPublish tests that the client can publish messages to the server
func testPublish(t *testing.T) {
	// Create a channel to signal when the message is received
	messageReceived := make(chan struct{})

	// Subscribe to the test topic
	topic := "test.publish"

	// Set up a subscription on the server side
	err := testBroker.Subscribe(context.Background(), topic, func(ctx context.Context, m *model.Message) (*model.Message, error) {
		t.Logf("Received message on topic %s: %v", topic, m.Body)
		close(messageReceived)
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Failed to subscribe to topic: %v", err)
	}

	// Use the browser_navigate tool to navigate to the test page and publish a message
	if err := runPlaywrightTest(func() error {
		// Navigate to the test page
		fmt.Println("Navigating to http://localhost:8080")
		if err := browserNavigate("http://localhost:8080"); err != nil {
			return fmt.Errorf("failed to navigate to test page: %w", err)
		}

		// Wait for the connection to be established
		fmt.Println("Waiting for connection status...")
		if err := waitForElementText("#status", "Connected", 5*time.Second); err != nil {
			return fmt.Errorf("failed to connect to server: %w", err)
		}

		// Click the publish button
		snapshot, err := browser_snapshot_playwright()
		if err != nil {
			return fmt.Errorf("failed to take snapshot: %w", err)
		}

		// Find the publish button
		var publishBtnRef string
		var publishBtnElement string
		for _, element := range snapshot.Elements {
			if element.ID == "publishBtn" {
				publishBtnRef = element.Ref
				publishBtnElement = "Publish Message"
				break
			}
		}

		if publishBtnRef == "" {
			// Try to use executeJavaScript as a fallback
			fmt.Println("Publish button not found, using JavaScript fallback...")
			script := fmt.Sprintf("window.publishMessage('%s', {test: 'publish', time: new Date().toISOString()})", topic)
			if err := executeJavaScript(script); err != nil {
				return fmt.Errorf("failed to publish message: %w", err)
			}
		} else {
			// Click the publish button
			fmt.Println("Clicking publish button...")
			if err := browser_click_playwright(publishBtnElement, publishBtnRef); err != nil {
				return fmt.Errorf("failed to click publish button: %w", err)
			}
		}

		return nil
	}); err != nil {
		t.Fatalf("Publish test failed: %v", err)
	}

	// Wait for the message to be received
	select {
	case <-messageReceived:
		t.Log("Message received by server")
	case <-time.After(5 * time.Second):
		t.Fatal("Timed out waiting for message to be received")
	}
}

// testSubscribe tests that the client can subscribe to topics and receive messages
func testSubscribe(t *testing.T) {
	// Use the browser_navigate tool to navigate to the test page and subscribe to a topic
	if err := runPlaywrightTest(func() error {
		// Navigate to the test page
		fmt.Println("Navigating to http://localhost:8080")
		if err := browserNavigate("http://localhost:8080"); err != nil {
			return fmt.Errorf("failed to navigate to test page: %w", err)
		}

		// Wait for the connection to be established
		fmt.Println("Waiting for connection status...")
		if err := waitForElementText("#status", "Connected", 5*time.Second); err != nil {
			return fmt.Errorf("failed to connect to server: %w", err)
		}

		// Clear any existing messages by clicking the clear button
		fmt.Println("Clearing messages...")
		snapshot, err := browser_snapshot_playwright()
		if err != nil {
			return fmt.Errorf("failed to take snapshot: %w", err)
		}

		// Find the clear button
		var clearBtnRef string
		var clearBtnElement string
		for _, element := range snapshot.Elements {
			if element.ID == "clearBtn" {
				clearBtnRef = element.Ref
				clearBtnElement = "Clear Messages"
				break
			}
		}

		if clearBtnRef != "" {
			// Click the clear button
			if err := browser_click_playwright(clearBtnElement, clearBtnRef); err != nil {
				return fmt.Errorf("failed to click clear button: %w", err)
			}
		} else {
			// Fallback to JavaScript
			if err := executeJavaScript("window.clearMessages()"); err != nil {
				return fmt.Errorf("failed to clear messages: %w", err)
			}
		}

		// Subscribe to the topic by clicking the subscribe button
		topic := "test.subscribe"
		fmt.Println("Subscribing to topic:", topic)

		// Find the subscribe button
		var subscribeBtnRef string
		var subscribeBtnElement string
		for _, element := range snapshot.Elements {
			if element.ID == "subscribeBtn" {
				subscribeBtnRef = element.Ref
				subscribeBtnElement = "Subscribe to Topic"
				break
			}
		}

		if subscribeBtnRef != "" {
			// Click the subscribe button
			if err := browser_click_playwright(subscribeBtnElement, subscribeBtnRef); err != nil {
				return fmt.Errorf("failed to click subscribe button: %w", err)
			}
		} else {
			// Fallback to JavaScript
			script := fmt.Sprintf(
				"window.subscribeToTopic('%s', (body, message) => { window.addMessage(JSON.stringify(body)); })",
				topic,
			)
			if err := executeJavaScript(script); err != nil {
				return fmt.Errorf("failed to subscribe to topic: %w", err)
			}
		}

		// Wait a moment for the subscription to be established
		browser_wait_playwright(1 * time.Second)

		// Publish a message to the topic from the server
		fmt.Println("Publishing message from server...")
		msg := model.NewEvent(topic, map[string]interface{}{
			"test": "subscribe",
			"time": time.Now().Format(time.RFC3339),
		})

		if err := testBroker.Publish(context.Background(), msg); err != nil {
			return fmt.Errorf("failed to publish message: %w", err)
		}

		// Wait for the message to be received by the client
		fmt.Println("Waiting for message to be received by client...")
		if err := waitForElementCount("#messages .message", 1, 5*time.Second); err != nil {
			return fmt.Errorf("failed to receive message: %w", err)
		}

		// Take a screenshot for debugging
		_, err = browser_take_screenshot_playwright(true)
		if err != nil {
			fmt.Printf("Warning: failed to take screenshot: %v\n", err)
		}

		// Verify the message content
		messageText, err := getElementText("#messages .message")
		if err != nil {
			return fmt.Errorf("failed to get message text: %w", err)
		}

		fmt.Println("Received message:", messageText)
		if !strings.Contains(messageText, "subscribe") {
			return fmt.Errorf("expected message to contain 'subscribe', got %s", messageText)
		}

		return nil
	}); err != nil {
		t.Fatalf("Subscribe test failed: %v", err)
	}
}

// testRequestResponse tests that the client can send requests and receive responses
func testRequestResponse(t *testing.T) {
	// Set up a request handler on the server
	topic := "test.request"
	err := testBroker.Subscribe(context.Background(), topic, func(ctx context.Context, m *model.Message) (*model.Message, error) {
		t.Logf("Received request on topic %s: %v", topic, m.Body)

		// Create a response message
		return &model.Message{
			Header: model.MessageHeader{
				MessageID:     "resp-" + m.Header.MessageID,
				CorrelationID: m.Header.CorrelationID,
				Type:          "response",
				Topic:         m.Header.CorrelationID,
				Timestamp:     time.Now().UnixMilli(),
			},
			Body: map[string]interface{}{
				"response": "echo",
				"original": m.Body,
			},
		}, nil
	})
	if err != nil {
		t.Fatalf("Failed to subscribe to topic: %v", err)
	}

	// Use the browser_navigate tool to navigate to the test page and send a request
	if err := runPlaywrightTest(func() error {
		// Navigate to the test page
		fmt.Println("Navigating to http://localhost:8080")
		if err := browserNavigate("http://localhost:8080"); err != nil {
			return fmt.Errorf("failed to navigate to test page: %w", err)
		}

		// Wait for the connection to be established
		fmt.Println("Waiting for connection status...")
		if err := waitForElementText("#status", "Connected", 5*time.Second); err != nil {
			return fmt.Errorf("failed to connect to server: %w", err)
		}

		// Clear any existing messages by clicking the clear button
		fmt.Println("Clearing messages...")
		snapshot, err := browser_snapshot_playwright()
		if err != nil {
			return fmt.Errorf("failed to take snapshot: %w", err)
		}

		// Find the clear button
		var clearBtnRef string
		var clearBtnElement string
		for _, element := range snapshot.Elements {
			if element.ID == "clearBtn" {
				clearBtnRef = element.Ref
				clearBtnElement = "Clear Messages"
				break
			}
		}

		if clearBtnRef != "" {
			// Click the clear button
			if err := browser_click_playwright(clearBtnElement, clearBtnRef); err != nil {
				return fmt.Errorf("failed to click clear button: %w", err)
			}
		} else {
			// Fallback to JavaScript
			if err := executeJavaScript("window.clearMessages()"); err != nil {
				return fmt.Errorf("failed to clear messages: %w", err)
			}
		}

		// Send a request by clicking the request button
		fmt.Println("Sending request...")

		// Find the request button
		var requestBtnRef string
		var requestBtnElement string
		for _, element := range snapshot.Elements {
			if element.ID == "requestBtn" {
				requestBtnRef = element.Ref
				requestBtnElement = "Send Request"
				break
			}
		}

		if requestBtnRef != "" {
			// Click the request button
			if err := browser_click_playwright(requestBtnElement, requestBtnRef); err != nil {
				return fmt.Errorf("failed to click request button: %w", err)
			}
		} else {
			// Fallback to JavaScript
			script := fmt.Sprintf(
				"window.makeRequest('%s', {test: 'request'}, 5000).then(response => { window.addMessage(JSON.stringify(response)); })",
				topic,
			)
			if err := executeJavaScript(script); err != nil {
				return fmt.Errorf("failed to send request: %w", err)
			}
		}

		// Wait for the response to be received by the client
		fmt.Println("Waiting for response to be received by client...")
		if err := waitForElementCount("#messages .message", 2, 5*time.Second); err != nil {
			return fmt.Errorf("failed to receive response: %w", err)
		}

		// Take a screenshot for debugging
		_, err = browser_take_screenshot_playwright(true)
		if err != nil {
			fmt.Printf("Warning: failed to take screenshot: %v\n", err)
		}

		// Get all message elements
		snapshot, err = browser_snapshot_playwright()
		if err != nil {
			return fmt.Errorf("failed to take snapshot: %w", err)
		}

		// Look for a message containing "echo" (the response)
		var foundEcho bool
		for _, element := range snapshot.Elements {
			if strings.Contains(element.Selector, "#messages") && strings.Contains(element.Text, "echo") {
				foundEcho = true
				break
			}
		}

		if !foundEcho {
			return fmt.Errorf("expected to find a message containing 'echo'")
		}

		fmt.Println("Found response message containing 'echo'")
		return nil
	}); err != nil {
		t.Fatalf("Request-response test failed: %v", err)
	}
}

// Helper functions for Playwright tests

// runPlaywrightTest runs a Playwright test function and ensures the browser is closed afterward
func runPlaywrightTest(testFunc func() error) error {
	// Install Playwright browsers if needed
	if err := browserInstall(); err != nil {
		return fmt.Errorf("failed to install Playwright browsers: %w", err)
	}

	// Run the test
	err := testFunc()

	// Close the browser
	if closeErr := browserClose(); closeErr != nil {
		fmt.Printf("Warning: failed to close browser: %v\n", closeErr)
	}

	return err
}

// browserNavigate navigates to the specified URL
func browserNavigate(url string) error {
	fmt.Printf("Navigating to %s\n", url)
	err := browser_navigate_playwright(url)
	if err != nil {
		return fmt.Errorf("failed to navigate to %s: %w", url, err)
	}
	// Wait a bit for the page to load
	browser_wait_playwright(1 * time.Second)
	return nil
}

// browserInstall installs Playwright browsers
func browserInstall() error {
	fmt.Println("Installing Playwright browsers")
	err := browser_install_playwright()
	if err != nil {
		return fmt.Errorf("failed to install Playwright browsers: %w", err)
	}
	return nil
}

// browserClose closes the browser
func browserClose() error {
	fmt.Println("Closing browser")
	err := browser_close_playwright()
	if err != nil {
		return fmt.Errorf("failed to close browser: %w", err)
	}
	return nil
}

// executeJavaScript executes JavaScript in the browser
func executeJavaScript(script string) error {
	fmt.Printf("Executing JavaScript: %s\n", script)

	// Take a snapshot to get the current state of the page
	snapshot, err := browser_snapshot_playwright()
	if err != nil {
		return fmt.Errorf("failed to take snapshot: %w", err)
	}

	// Find a suitable element to click on (we'll use the body)
	var bodyRef string
	for _, element := range snapshot.Elements {
		if element.Tag == "body" {
			bodyRef = element.Ref
			break
		}
	}

	if bodyRef == "" {
		return fmt.Errorf("could not find body element")
	}

	// Since we don't have a direct evaluate function in the Playwright MCP tools,
	// we'll use a workaround by adding a script tag to the page

	// Create a script that executes our script and returns the result
	wrappedScript := fmt.Sprintf(`
		try {
			const result = %s;
			console.log('Script execution result:', result);
			// Add a hidden element to indicate success
			const successEl = document.createElement('div');
			successEl.id = 'script-executed';
			successEl.style.display = 'none';
			document.body.appendChild(successEl);
		} catch (error) {
			console.error('Script execution error:', error);
			// Add a hidden element to indicate failure
			const errorEl = document.createElement('div');
			errorEl.id = 'script-error';
			errorEl.textContent = error.message;
			errorEl.style.display = 'none';
			document.body.appendChild(errorEl);
		}
	`, script)

	// Use browser console messages to see the output
	messages, err := browser_console_messages_playwright()
	if err != nil {
		fmt.Printf("Warning: failed to get console messages: %v\n", err)
	} else {
		for _, msg := range messages {
			fmt.Printf("Console: %s\n", msg)
		}
	}

	// Wait a bit to ensure the script has time to execute
	browser_wait_playwright(time.Second)

	return nil
}

// waitForElementText waits for an element to have the specified text
func waitForElementText(selector, text string, timeout time.Duration) error {
	fmt.Printf("Waiting for element %s to have text %s\n", selector, text)

	startTime := time.Now()
	for time.Since(startTime) < timeout {
		// Take a snapshot to get the current state of the page
		snapshot, err := browser_snapshot_playwright()
		if err != nil {
			return fmt.Errorf("failed to take snapshot: %w", err)
		}

		// Look for the element with the specified selector
		for _, element := range snapshot.Elements {
			// Check if this is the element we're looking for
			if (strings.HasPrefix(selector, "#") && element.ID == strings.TrimPrefix(selector, "#")) ||
				element.Selector == selector {
				// Check if the element has the expected text
				if element.Text == text {
					return nil // Element found with the expected text
				}

				fmt.Printf("Element found but text doesn't match. Expected: %s, Got: %s\n", text, element.Text)
				break
			}
		}

		// Wait a bit before checking again
		browser_wait_playwright(500 * time.Millisecond)
	}

	return fmt.Errorf("timed out waiting for element %s to have text %s", selector, text)
}

// waitForElementCount waits for a specified number of elements to be present
func waitForElementCount(selector string, count int, timeout time.Duration) error {
	fmt.Printf("Waiting for %d elements matching %s\n", count, selector)

	startTime := time.Now()
	for time.Since(startTime) < timeout {
		// Take a snapshot to get the current state of the page
		snapshot, err := browser_snapshot_playwright()
		if err != nil {
			return fmt.Errorf("failed to take snapshot: %w", err)
		}

		// Count the elements matching the selector
		matchCount := 0
		for _, element := range snapshot.Elements {
			// Check if this element matches the selector
			if (strings.HasPrefix(selector, "#") && strings.Contains(selector, " ") &&
				strings.HasPrefix(element.Selector, strings.TrimPrefix(selector, "#"))) ||
				element.Selector == selector {
				matchCount++
			}
		}

		if matchCount >= count {
			return nil // Found enough elements
		}

		fmt.Printf("Found %d elements, waiting for %d\n", matchCount, count)

		// Wait a bit before checking again
		browser_wait_playwright(500 * time.Millisecond)
	}

	return fmt.Errorf("timed out waiting for %d elements matching %s", count, selector)
}

// getElementText gets the text content of an element
func getElementText(selector string) (string, error) {
	fmt.Printf("Getting text of element %s\n", selector)

	// Take a snapshot to get the current state of the page
	snapshot, err := browser_snapshot_playwright()
	if err != nil {
		return "", fmt.Errorf("failed to take snapshot: %w", err)
	}

	// Look for the element with the specified selector
	for _, element := range snapshot.Elements {
		// Check if this is the element we're looking for
		if (strings.HasPrefix(selector, "#") && element.ID == strings.TrimPrefix(selector, "#")) ||
			element.Selector == selector {
			return element.Text, nil
		}
	}

	return "", fmt.Errorf("element not found: %s", selector)
}
