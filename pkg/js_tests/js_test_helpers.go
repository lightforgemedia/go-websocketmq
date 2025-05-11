package js_tests

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/stretchr/testify/require"
)

// TestServer represents a test server for JavaScript client tests
type TestServer struct {
	T           *testing.T
	Broker      *broker.Broker
	Server      *httptest.Server
	FileServer  *httptest.Server
	Logger      *slog.Logger
	Ready       chan struct{}
	BrowserPage *rod.Page
	Browser     *rod.Browser
}

// NewTestServer creates a new test server for JavaScript client tests
func NewTestServer(t *testing.T) *TestServer {
	t.Helper()

	// Create a logger
	logHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	logger := slog.New(logHandler)

	// Create a broker
	b, err := broker.New(
		broker.WithLogger(logger),
		broker.WithPingInterval(5*time.Second),
	)
	require.NoError(t, err, "Failed to create broker")

	// Create a server that handles both WebSocket and static files
	mux := http.NewServeMux()

	// WebSocket endpoint
	mux.Handle("/ws", b.UpgradeHandler())

	// Static file handlers
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Logf("File server request: %s", r.URL.Path)

		// Serve the test page
		if r.URL.Path == "/" || r.URL.Path == "/index.html" || r.URL.Path == "/test_page.html" {
			http.ServeFile(w, r, "testdata/js/test_page.html")
			return
		}

		// Serve the WebSocketMQ JavaScript client
		if r.URL.Path == "/websocketmq.js" {
			http.ServeFile(w, r, "testdata/js/websocketmq.js")
			return
		}

		// Default 404
		http.NotFound(w, r)
	})

	// Create a test server
	server := httptest.NewServer(mux)

	// Create a ready channel
	ready := make(chan struct{}, 1)
	ready <- struct{}{}

	return &TestServer{
		T:          t,
		Broker:     b,
		Server:     server,
		FileServer: server, // Use the same server for both WebSocket and files
		Logger:     logger,
		Ready:      ready,
	}
}

// Close closes the test server
func (s *TestServer) Close() {
	if s.Browser != nil {
		s.Browser.MustClose()
	}
	if s.Server != nil {
		s.Server.Close()
	}
	if s.FileServer != nil {
		s.FileServer.Close()
	}
	if s.Broker != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.Broker.Shutdown(ctx)
	}
}

// StartBrowser starts a browser and navigates to the test page
func (s *TestServer) StartBrowser() *rod.Page {
	s.T.Helper()

	// Get the test page URL
	pageURL := fmt.Sprintf("%s/test_page.html", s.Server.URL)
	s.T.Logf("Test page URL: %s", pageURL)

	// Launch a browser
	l := launcher.New().Headless(true).MustLaunch()
	browser := rod.New().ControlURL(l).MustConnect()
	s.Browser = browser

	// Navigate to the test page
	page := browser.MustPage(pageURL).MustWaitLoad()
	s.BrowserPage = page

	return page
}

// WaitForElement waits for an element to be present on the page
func (s *TestServer) WaitForElement(selector string, timeout time.Duration) *rod.Element {
	s.T.Helper()

	deadline := time.Now().Add(timeout)
	var element *rod.Element
	var err error

	for time.Now().Before(deadline) {
		element, err = s.BrowserPage.Element(selector)
		if err == nil && element != nil {
			return element
		}
		time.Sleep(100 * time.Millisecond)
	}

	s.T.Fatalf("Element %s not found within timeout", selector)
	return nil
}

// WaitForElementText waits for an element to have the expected text
func (s *TestServer) WaitForElementText(selector, expectedText string, timeout time.Duration) {
	s.T.Helper()

	deadline := time.Now().Add(timeout)
	var text string

	for time.Now().Before(deadline) {
		element, err := s.BrowserPage.Element(selector)
		if err == nil && element != nil {
			text, err = element.Text()
			if err == nil && text == expectedText {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	s.T.Fatalf("Element %s did not have text '%s' within timeout, current text: '%s'", selector, expectedText, text)
}

// ConnectJSClient connects the JavaScript client to the server
func (s *TestServer) ConnectJSClient() {
	s.T.Helper()

	// Click the connect button
	connectBtn := s.WaitForElement("#connect-btn", 5*time.Second)
	connectBtn.MustClick()

	// Wait for connection to be established
	s.WaitForElementText("#connection-status", "Connected", 5*time.Second)
}

// DisconnectJSClient disconnects the JavaScript client from the server
func (s *TestServer) DisconnectJSClient() {
	s.T.Helper()

	// Click the disconnect button
	disconnectBtn := s.WaitForElement("#disconnect-btn", 5*time.Second)
	disconnectBtn.MustClick()

	// Wait for disconnection
	s.WaitForElementText("#connection-status", "Disconnected", 5*time.Second)
}

// SendJSClientRequest sends a request from the JavaScript client to the server
func (s *TestServer) SendJSClientRequest(topic string, payload string) {
	s.T.Helper()

	// Select the topic
	topicSelect := s.WaitForElement("#request-topic", 5*time.Second)
	topicSelect.MustSelect(topic)

	// Set the payload if provided
	if payload != "" {
		payloadInput := s.WaitForElement("#request-payload", 5*time.Second)
		payloadInput.MustSelectAllText().MustInput(payload)
	}

	// Click the send request button
	sendBtn := s.WaitForElement("#send-request-btn", 5*time.Second)
	sendBtn.MustClick()
}

// SubscribeJSClient subscribes the JavaScript client to a topic
func (s *TestServer) SubscribeJSClient(topic string) {
	s.T.Helper()

	// Select the topic
	topicSelect := s.WaitForElement("#subscribe-topic", 5*time.Second)
	topicSelect.MustSelect(topic)

	// Click the subscribe button
	subscribeBtn := s.WaitForElement("#subscribe-btn", 5*time.Second)
	subscribeBtn.MustClick()
}

// UnsubscribeJSClient unsubscribes the JavaScript client from a topic
func (s *TestServer) UnsubscribeJSClient(topic string) {
	s.T.Helper()

	// Select the topic
	topicSelect := s.WaitForElement("#subscribe-topic", 5*time.Second)
	topicSelect.MustSelect(topic)

	// Click the unsubscribe button
	unsubscribeBtn := s.WaitForElement("#unsubscribe-btn", 5*time.Second)
	unsubscribeBtn.MustClick()
}

// GetJSClientID gets the client ID from the JavaScript client
func (s *TestServer) GetJSClientID() string {
	s.T.Helper()

	// Get the client ID element
	clientIDElement := s.WaitForElement("#client-id", 5*time.Second)
	clientID, err := clientIDElement.Text()
	require.NoError(s.T, err, "Failed to get client ID text")

	return clientID
}

// GetJSClientResponseText gets the response text from the JavaScript client
func (s *TestServer) GetJSClientResponseText() string {
	s.T.Helper()

	// Get the response element
	responseElement := s.WaitForElement("#request-response", 5*time.Second)
	response, err := responseElement.Text()
	require.NoError(s.T, err, "Failed to get response text")

	return response
}

// GetJSClientSubscriptionMessages gets the subscription messages from the JavaScript client
func (s *TestServer) GetJSClientSubscriptionMessages() string {
	s.T.Helper()

	// Get the subscription messages element
	messagesElement := s.WaitForElement("#subscription-messages", 5*time.Second)
	messages, err := messagesElement.Text()
	require.NoError(s.T, err, "Failed to get subscription messages text")

	return messages
}

// GetJSClientServerRequests gets the server requests from the JavaScript client
func (s *TestServer) GetJSClientServerRequests() string {
	s.T.Helper()

	// Get the server requests element
	requestsElement := s.WaitForElement("#server-requests", 5*time.Second)
	requests, err := requestsElement.Text()
	require.NoError(s.T, err, "Failed to get server requests text")

	return requests
}

// TakeScreenshot takes a screenshot of the browser page
func (s *TestServer) TakeScreenshot(filename string) {
	s.T.Helper()

	// Create the directory if it doesn't exist
	dir := filepath.Dir(filename)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err := os.MkdirAll(dir, 0755)
		require.NoError(s.T, err, "Failed to create directory for screenshot")
	}

	// Take the screenshot
	s.BrowserPage.MustScreenshot(filename)
	s.T.Logf("Screenshot saved to %s", filename)
}

// GetPortFromURL extracts the port number from a URL
func GetPortFromURL(urlStr string) int {
	// Parse the URL
	u, err := url.Parse(urlStr)
	if err != nil {
		return 0
	}

	// If the port is explicitly specified in the URL
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil {
			return 0
		}
		return port
	}

	// Default ports based on scheme
	switch u.Scheme {
	case "http":
		return 80
	case "https":
		return 443
	default:
		return 0
	}
}
