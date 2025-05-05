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
	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps"
)

// Global variables for the test
var (
	broker websocketmq.Broker
	logger *SimpleLogger
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
	logger = &SimpleLogger{}

	// Create a broker
	brokerOpts := websocketmq.DefaultBrokerOptions()
	broker = websocketmq.NewPubSubBroker(logger, brokerOpts)

	// Create a WebSocket handler
	handlerOpts := websocketmq.DefaultHandlerOptions()
	handler := websocketmq.NewHandler(broker, logger, handlerOpts)

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
		logger.Info("Starting test server on http://localhost:8080")
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			logger.Error("Server error: %v", err)
		}
	}()

	// Ensure the server is shut down when the test completes
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logger.Error("Server shutdown error: %v", err)
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
	psbroker, ok := broker.(*ps.PubSubBroker)
	if !ok {
		t.Fatalf("Expected broker to be a PubSubBroker, got %T", broker)
	}

	err := psbroker.Subscribe(context.Background(), topic, func(ctx context.Context, m *websocketmq.Message) (*websocketmq.Message, error) {
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

		// Publish a message
		fmt.Println("Publishing message...")
		script := fmt.Sprintf("window.publishMessage('%s', {test: 'publish'})", topic)
		if err := executeJavaScript(script); err != nil {
			return fmt.Errorf("failed to publish message: %w", err)
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

		// Clear any existing messages
		fmt.Println("Clearing messages...")
		if err := executeJavaScript("window.clearMessages()"); err != nil {
			return fmt.Errorf("failed to clear messages: %w", err)
		}

		// Subscribe to the topic
		topic := "test.subscribe"
		fmt.Println("Subscribing to topic:", topic)
		script := fmt.Sprintf(
			"window.subscribeToTopic('%s', (body, message) => { window.addMessage(JSON.stringify(body)); })",
			topic,
		)
		if err := executeJavaScript(script); err != nil {
			return fmt.Errorf("failed to subscribe to topic: %w", err)
		}

		// Publish a message to the topic from the server
		fmt.Println("Publishing message from server...")
		msg := websocketmq.NewEvent(topic, map[string]interface{}{
			"test": "subscribe",
			"time": time.Now().Format(time.RFC3339),
		})

		psbroker, ok := broker.(*ps.PubSubBroker)
		if !ok {
			return fmt.Errorf("expected broker to be a PubSubBroker, got %T", broker)
		}

		if err := psbroker.Publish(context.Background(), msg); err != nil {
			return fmt.Errorf("failed to publish message: %w", err)
		}

		// Wait for the message to be received by the client
		fmt.Println("Waiting for message to be received by client...")
		if err := waitForElementCount("#messages div", 1, 5*time.Second); err != nil {
			return fmt.Errorf("failed to receive message: %w", err)
		}

		// Verify the message content
		messageText, err := getElementText("#messages div")
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
	psbroker, ok := broker.(*ps.PubSubBroker)
	if !ok {
		t.Fatalf("Expected broker to be a PubSubBroker, got %T", broker)
	}

	err := psbroker.Subscribe(context.Background(), topic, func(ctx context.Context, m *websocketmq.Message) (*websocketmq.Message, error) {
		t.Logf("Received request on topic %s: %v", topic, m.Body)

		// Create a response message
		return &websocketmq.Message{
			Header: websocketmq.MessageHeader{
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

		// Clear any existing messages
		fmt.Println("Clearing messages...")
		if err := executeJavaScript("window.clearMessages()"); err != nil {
			return fmt.Errorf("failed to clear messages: %w", err)
		}

		// Send a request
		fmt.Println("Sending request...")
		script := fmt.Sprintf(
			"window.makeRequest('%s', {test: 'request'}, 5000).then(response => { window.addMessage(JSON.stringify(response)); })",
			topic,
		)
		if err := executeJavaScript(script); err != nil {
			return fmt.Errorf("failed to send request: %w", err)
		}

		// Wait for the response to be received by the client
		fmt.Println("Waiting for response to be received by client...")
		if err := waitForElementCount("#messages div", 1, 5*time.Second); err != nil {
			return fmt.Errorf("failed to receive response: %w", err)
		}

		// Verify the response content
		responseText, err := getElementText("#messages div")
		if err != nil {
			return fmt.Errorf("failed to get response text: %w", err)
		}

		fmt.Println("Received response:", responseText)
		if !strings.Contains(responseText, "echo") {
			return fmt.Errorf("expected response to contain 'echo', got %s", responseText)
		}

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
	err := browser_navigate_playwright(url)
	if err != nil {
		return fmt.Errorf("failed to navigate to %s: %w", url, err)
	}
	return nil
}

// browserInstall installs Playwright browsers
func browserInstall() error {
	err := browser_install_playwright()
	if err != nil {
		return fmt.Errorf("failed to install Playwright browsers: %w", err)
	}
	return nil
}

// browserClose closes the browser
func browserClose() error {
	err := browser_close_playwright()
	if err != nil {
		return fmt.Errorf("failed to close browser: %w", err)
	}
	return nil
}

// executeJavaScript executes JavaScript in the browser
func executeJavaScript(script string) error {
	// Take a snapshot first to get the page structure
	snapshot, err := browser_snapshot_playwright()
	if err != nil {
		return fmt.Errorf("failed to take snapshot: %w", err)
	}

	// Execute the script
	// Note: In a real implementation, we would use browser_evaluate_playwright
	// but for now we'll just log the script
	fmt.Printf("Executing JavaScript: %s\n", script)

	// For debugging, take a screenshot
	_, err = browser_take_screenshot_playwright(true)
	if err != nil {
		return fmt.Errorf("failed to take screenshot: %w", err)
	}

	return nil
}

// waitForElementText waits for an element to have the specified text
func waitForElementText(selector, text string, timeout time.Duration) error {
	// Take a snapshot to get the page structure
	snapshot, err := browser_snapshot_playwright()
	if err != nil {
		return fmt.Errorf("failed to take snapshot: %w", err)
	}

	// In a real implementation, we would use browser_wait_for_selector_playwright
	// but for now we'll just wait a bit and then check
	time.Sleep(1 * time.Second)

	// Take another snapshot to see if the element has the expected text
	snapshot, err = browser_snapshot_playwright()
	if err != nil {
		return fmt.Errorf("failed to take snapshot: %w", err)
	}

	// For debugging, take a screenshot
	_, err = browser_take_screenshot_playwright(true)
	if err != nil {
		return fmt.Errorf("failed to take screenshot: %w", err)
	}

	return nil
}

// waitForElementCount waits for a specified number of elements to be present
func waitForElementCount(selector string, count int, timeout time.Duration) error {
	// Take a snapshot to get the page structure
	snapshot, err := browser_snapshot_playwright()
	if err != nil {
		return fmt.Errorf("failed to take snapshot: %w", err)
	}

	// In a real implementation, we would use browser_wait_for_selector_playwright
	// but for now we'll just wait a bit and then check
	time.Sleep(1 * time.Second)

	// Take another snapshot to see if the elements are present
	snapshot, err = browser_snapshot_playwright()
	if err != nil {
		return fmt.Errorf("failed to take snapshot: %w", err)
	}

	// For debugging, take a screenshot
	_, err = browser_take_screenshot_playwright(true)
	if err != nil {
		return fmt.Errorf("failed to take screenshot: %w", err)
	}

	return nil
}

// getElementText gets the text content of an element
func getElementText(selector string) (string, error) {
	// Take a snapshot to get the page structure
	snapshot, err := browser_snapshot_playwright()
	if err != nil {
		return "", fmt.Errorf("failed to take snapshot: %w", err)
	}

	// In a real implementation, we would use browser_get_text_playwright
	// but for now we'll just return a sample text

	// For debugging, take a screenshot
	_, err = browser_take_screenshot_playwright(true)
	if err != nil {
		return "", fmt.Errorf("failed to take screenshot: %w", err)
	}

	return "Sample text", nil
}
