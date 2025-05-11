//go:build js_client
// +build js_client

package js_tests

import (
	"strings"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/js_tests"
	app_shared_types "github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJSClient_Request tests the JavaScript client request functionality
func TestJSClient_Request(t *testing.T) {
	// Create a test server
	server := js_tests.NewTestServer(t)
	defer server.Close()

	// Register request handlers
	err := server.Broker.OnRequest(app_shared_types.TopicGetTime,
		func(client broker.ClientHandle, req app_shared_types.GetTimeRequest) (app_shared_types.GetTimeResponse, error) {
			t.Logf("Server: Client %s requested time", client.ID())
			return app_shared_types.GetTimeResponse{CurrentTime: "2025-05-10T12:00:00Z"}, nil
		},
	)
	require.NoError(t, err, "Failed to register GetTime handler")

	err = server.Broker.OnRequest(app_shared_types.TopicUserDetails,
		func(client broker.ClientHandle, req app_shared_types.GetUserDetailsRequest) (app_shared_types.UserDetailsResponse, error) {
			t.Logf("Server: Client %s requested user details for user %s", client.ID(), req.UserID)
			if req.UserID == "user123" {
				return app_shared_types.UserDetailsResponse{
					UserID: req.UserID,
					Name:   "Jane Doe",
					Email:  "jane@example.com",
				}, nil
			}
			return app_shared_types.UserDetailsResponse{}, nil
		},
	)
	require.NoError(t, err, "Failed to register UserDetails handler")

	err = server.Broker.OnRequest(app_shared_types.TopicErrorTest,
		func(client broker.ClientHandle, req app_shared_types.ErrorTestRequest) (app_shared_types.ErrorTestResponse, error) {
			t.Logf("Server: Client %s requested error test with shouldError=%v", client.ID(), req.ShouldError)
			if req.ShouldError {
				return app_shared_types.ErrorTestResponse{}, assert.AnError
			}
			return app_shared_types.ErrorTestResponse{Message: "No error"}, nil
		},
	)
	require.NoError(t, err, "Failed to register ErrorTest handler")

	// Start a browser and navigate to the test page
	page := server.StartBrowser()
	defer page.MustClose()

	// Connect the JavaScript client to the server
	server.ConnectJSClient()

	// Test case 1: Happy path - Client requests time from server
	t.Run("HappyPath_ClientRequestsTime", func(t *testing.T) {
		// Select the topic
		topicSelect := server.WaitForElement("#request-topic", 5*time.Second)
		topicSelect.MustSelect(app_shared_types.TopicGetTime)

		// Send the request
		sendBtn := server.WaitForElement("#send-request-btn", 5*time.Second)
		sendBtn.MustClick()

		// Wait for the response
		time.Sleep(1 * time.Second)

		// Verify the response
		responseText := server.GetJSClientResponseText()
		assert.Contains(t, responseText, "2025-05-10T12:00:00Z", "Response should contain the expected time")
	})

	// Test case 2: Base case - Client requests user details from server
	t.Run("BaseCase_ClientRequestsUserDetails", func(t *testing.T) {
		// Select the topic
		topicSelect := server.WaitForElement("#request-topic", 5*time.Second)
		topicSelect.MustSelect(app_shared_types.TopicUserDetails)

		// Set the payload
		payloadInput := server.WaitForElement("#request-payload", 5*time.Second)
		payloadInput.MustSelectAllText().MustInput(`{"userID": "user123"}`)

		// Send the request
		sendBtn := server.WaitForElement("#send-request-btn", 5*time.Second)
		sendBtn.MustClick()

		// Wait for the response
		time.Sleep(1 * time.Second)

		// Verify the response
		responseText := server.GetJSClientResponseText()
		assert.Contains(t, responseText, "Jane Doe", "Response should contain the user's name")
		assert.Contains(t, responseText, "jane@example.com", "Response should contain the user's email")
	})

	// Test case 3: Negative case - Client requests error test from server
	t.Run("NegativeCase_ClientRequestsErrorTest", func(t *testing.T) {
		// Select the topic
		topicSelect := server.WaitForElement("#request-topic", 5*time.Second)
		topicSelect.MustSelect(app_shared_types.TopicErrorTest)

		// Set the payload
		payloadInput := server.WaitForElement("#request-payload", 5*time.Second)
		payloadInput.MustSelectAllText().MustInput(`{"shouldError": true}`)

		// Send the request
		sendBtn := server.WaitForElement("#send-request-btn", 5*time.Second)
		sendBtn.MustClick()

		// Wait for the response
		time.Sleep(1 * time.Second)

		// Verify the response
		responseText := server.GetJSClientResponseText()
		assert.Contains(t, responseText, "Error", "Response should contain an error message")
	})

	// Test case 4: Edge case - Client requests non-existent topic
	t.Run("EdgeCase_ClientRequestsNonExistentTopic", func(t *testing.T) {
		// Select the topic (we'll use a custom input for this)
		wsUrlInput := server.WaitForElement("#ws-url", 5*time.Second)
		originalUrl := wsUrlInput.MustText()

		// Execute JavaScript to add a new option to the select element
		page.MustEval(`() => {
			const select = document.getElementById('request-topic');
			const option = document.createElement('option');
			option.value = 'non:existent';
			option.text = 'Non-existent Topic';
			select.add(option);
			select.value = 'non:existent';
		}`)

		// Send the request
		sendBtn := server.WaitForElement("#send-request-btn", 5*time.Second)
		sendBtn.MustClick()

		// Wait for the response
		time.Sleep(1 * time.Second)

		// Verify the response
		responseText := server.GetJSClientResponseText()
		assert.True(t,
			strings.Contains(responseText, "Error") ||
				strings.Contains(responseText, "error") ||
				strings.Contains(responseText, "not found") ||
				strings.Contains(responseText, "unknown"),
			"Response should indicate an error for non-existent topic")

		// Reset the URL input
		wsUrlInput.MustSelectAllText().MustInput(originalUrl)
	})
}
