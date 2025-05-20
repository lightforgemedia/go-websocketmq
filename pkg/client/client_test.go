// ergosockets/client/client_test.go
package client_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/lightforgemedia/go-websocketmq/pkg/client"
	"github.com/lightforgemedia/go-websocketmq/pkg/ergosockets"
	app_shared_types "github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
)

// Use the testutil package's logger instead of defining our own

func TestClientConnectAndRequest(t *testing.T) {
	ms := testutil.NewMockServer(t, func(conn *websocket.Conn, srv *testutil.MockServer) { // srv is the mockServer instance
		for { // Simple request echo handler
			var reqEnv ergosockets.Envelope
			err := wsjson.Read(context.Background(), conn, &reqEnv)
			if err != nil {
				return
			}

			// Handle registration request
			if reqEnv.Topic == app_shared_types.TopicClientRegister {
				var reg app_shared_types.ClientRegistration
				if err := reqEnv.DecodePayload(&reg); err != nil {
					t.Errorf("Failed to decode registration payload: %v", err)
					return
				}

				// Create response with server-assigned ID
				respEnv, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeResponse, reqEnv.Topic, app_shared_types.ClientRegistrationResponse{
					ServerAssignedID: "server-" + reg.ClientID[:8],
					ClientName:       reg.ClientName,
					ServerTime:       time.Now().Format(time.RFC3339),
				}, nil)
				// Set expectation before sending
				srv.Expect(1)
				srv.Send(*respEnv)
				t.Logf("MockServer: Handled registration request, assigned ID: server-%s", reg.ClientID[:8])
			} else if reqEnv.Topic == app_shared_types.TopicGetTime {
				respEnv, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeResponse, reqEnv.Topic, app_shared_types.GetTimeResponse{CurrentTime: "mock-time"}, nil)
				// Set expectation before sending
				srv.Expect(1)
				srv.Send(*respEnv) // Use srv.Send
			}
		}
	})
	defer ms.Close()

	cli := testutil.NewTestClient(t, ms.WsURL)
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := client.Request[app_shared_types.GetTimeResponse](cli, ctx, app_shared_types.TopicGetTime)
	if err != nil {
		t.Fatalf("Client request failed: %v", err)
	}
	if resp.CurrentTime != "mock-time" {
		t.Errorf("Expected 'mock-time', got '%s'", resp.CurrentTime)
	}
	t.Log("Client connect and request successful.")
}

func TestClientSubscribeAndReceive(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1) // For the received message

	ms := testutil.NewMockServer(t, func(conn *websocket.Conn, srv *testutil.MockServer) {
		// First, handle registration request
		var regEnv ergosockets.Envelope
		regCtx, regCancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer regCancel()
		err := wsjson.Read(regCtx, conn, &regEnv)
		if err != nil {
			t.Errorf("MockServer: Did not receive registration request: %v", err)
			return
		}

		if regEnv.Topic == app_shared_types.TopicClientRegister {
			var reg app_shared_types.ClientRegistration
			if err := regEnv.DecodePayload(&reg); err != nil {
				t.Errorf("Failed to decode registration payload: %v", err)
				return
			}

			// Create response with server-assigned ID
			respEnv, _ := ergosockets.NewEnvelope(regEnv.ID, ergosockets.TypeResponse, regEnv.Topic, app_shared_types.ClientRegistrationResponse{
				ServerAssignedID: "server-" + reg.ClientID[:8],
				ClientName:       reg.ClientName,
				ServerTime:       time.Now().Format(time.RFC3339),
			}, nil)
			// Set expectation before sending
			srv.Expect(1)
			srv.Send(*respEnv)
			t.Logf("MockServer: Handled registration request, assigned ID: server-%s", reg.ClientID[:8])
		} else {
			t.Errorf("MockServer: Expected registration request, got topic %s", regEnv.Topic)
			return
		}

		// Now wait for subscribe request from client
		var subEnv ergosockets.Envelope
		subCtx, subCancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer subCancel()
		err = wsjson.Read(subCtx, conn, &subEnv)
		if err != nil {
			t.Errorf("MockServer: Did not receive subscribe request: %v", err)
			return
		}
		if subEnv.Type != ergosockets.TypeSubscribeRequest || subEnv.Topic != app_shared_types.TopicServerAnnounce {
			t.Errorf("MockServer: Expected subscribe request, got type %s topic %s", subEnv.Type, subEnv.Topic)
			return
		}
		t.Logf("MockServer: Received subscribe request for %s", subEnv.Topic)
		ackEnv, _ := ergosockets.NewEnvelope(subEnv.ID, ergosockets.TypeSubscriptionAck, subEnv.Topic, nil, nil)
		// Set expectation before sending
		srv.Expect(1)
		srv.Send(*ackEnv)

		// After ack, send the publish
		time.Sleep(50 * time.Millisecond) // Ensure client processes ack
		publishEnv, _ := ergosockets.NewEnvelope("", ergosockets.TypePublish, app_shared_types.TopicServerAnnounce, app_shared_types.ServerAnnouncement{Message: "test-publish"}, nil)
		// Set expectation before sending
		srv.Expect(1)
		srv.Send(*publishEnv)
	})
	defer ms.Close()

	cli := testutil.NewTestClient(t, ms.WsURL)
	defer cli.Close()

	receivedChan := make(chan app_shared_types.ServerAnnouncement, 1)
	_, err := cli.Subscribe(app_shared_types.TopicServerAnnounce,
		func(announcement *app_shared_types.ServerAnnouncement) error {
			t.Logf("ClientTest: Received announcement: %+v", announcement)
			receivedChan <- *announcement
			wg.Done()
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Client failed to subscribe: %v", err)
	}

	select {
	case received := <-receivedChan:
		if received.Message != "test-publish" {
			t.Errorf("Expected 'test-publish', got '%s'", received.Message)
		}
	case <-time.After(2 * time.Second): // Increased timeout
		t.Fatal("Client did not receive published message")
	}
	wg.Wait() // Ensure handler goroutine finishes
	t.Log("Client subscribe and receive successful.")
}

func TestClientAutoReconnect(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}
	t.Parallel()
	connectAttempts := 0
	var firstConnectionEstablished sync.WaitGroup
	firstConnectionEstablished.Add(1)

	ms := testutil.NewMockServer(t, func(conn *websocket.Conn, srv *testutil.MockServer) {
		connectAttempts++
		t.Logf("MockServer: Client connected (attempt %d)", connectAttempts)

		// Handle registration for any connection attempt
		var regEnv ergosockets.Envelope
		regCtx, regCancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer regCancel()
		err := wsjson.Read(regCtx, conn, &regEnv)
		if err != nil {
			t.Errorf("MockServer: Did not receive registration request: %v", err)
			return
		}

		if regEnv.Topic == app_shared_types.TopicClientRegister {
			var reg app_shared_types.ClientRegistration
			if err := regEnv.DecodePayload(&reg); err != nil {
				t.Errorf("Failed to decode registration payload: %v", err)
				return
			}

			// Create response with server-assigned ID
			respEnv, _ := ergosockets.NewEnvelope(regEnv.ID, ergosockets.TypeResponse, regEnv.Topic, app_shared_types.ClientRegistrationResponse{
				ServerAssignedID: "server-" + reg.ClientID[:8] + "-" + fmt.Sprintf("%d", connectAttempts),
				ClientName:       reg.ClientName,
				ServerTime:       time.Now().Format(time.RFC3339),
			}, nil)
			// Set expectation before sending
			srv.Expect(1)
			srv.Send(*respEnv)
			t.Logf("MockServer: Handled registration request, assigned ID: server-%s-%d", reg.ClientID[:8], connectAttempts)
		} else {
			t.Errorf("MockServer: Expected registration request, got topic %s", regEnv.Topic)
			return
		}

		if connectAttempts == 1 {
			firstConnectionEstablished.Done()
			// Close connection after a short delay to trigger reconnect
			go func() {
				time.Sleep(200 * time.Millisecond)
				t.Logf("MockServer: Closing connection to trigger reconnect (attempt %d)", connectAttempts)
				srv.CloseCurrentConnection() // Use helper to close specific conn and cancel its loop
			}()

			// Block until the connection is closed with a read that will fail when connection closes
			var msg interface{}
			wsjson.Read(context.Background(), conn, &msg) // This will return with an error when connection is closed
			t.Logf("MockServer: First connection read completed (likely due to connection close)")
			return
		} else if connectAttempts == 2 {
			// Handle a request on the re-established connection
			for { // Add infinite loop to keep connection open
				var reqEnv ergosockets.Envelope
				err := wsjson.Read(context.Background(), conn, &reqEnv) // Simple read, no timeout for test simplicity
				if err != nil {
					t.Logf("MockServer: Read error on reconnected line: %v", err)
					return
				}
				if reqEnv.Topic == app_shared_types.TopicGetTime {
					respEnv, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeResponse, reqEnv.Topic, app_shared_types.GetTimeResponse{CurrentTime: "reconnected-time"}, nil)
					// Set expectation before sending
					srv.Expect(1)
					srv.Send(*respEnv)
				}
			}
		}
	})
	defer ms.Close()

	cli := testutil.NewTestClient(t, ms.WsURL,
		client.WithAutoReconnect(3, 50*time.Millisecond, 200*time.Millisecond), // Fast reconnect for test
	)
	defer cli.Close()

	// Wait for the first connection to be established
	firstConnectionEstablished.Wait()
	t.Log("Client: First connection established with mock server.")

	// Wait for reconnect to happen (server closes, client should reconnect)
	// The second connection will be attempt #2.
	time.Sleep(1000 * time.Millisecond) // Increase time for disconnect and reconnect attempt

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := client.Request[app_shared_types.GetTimeResponse](cli, ctx, app_shared_types.TopicGetTime)
	if err != nil {
		t.Fatalf("Client request after reconnect failed: %v (Connect attempts: %d)", err, connectAttempts)
	}
	if resp.CurrentTime != "reconnected-time" {
		t.Errorf("Expected 'reconnected-time', got '%s'", resp.CurrentTime)
	}
	if connectAttempts < 2 { // Should be at least 2 for a reconnect
		t.Errorf("Expected at least 2 connection attempts for reconnect, got %d", connectAttempts)
	}
	t.Logf("Client auto-reconnect successful, request processed. Total connect attempts: %d", connectAttempts)
}

func TestClientRequestVariadicPayload(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}
	t.Parallel()
	var lastReceivedPayload json.RawMessage
	registrationHandled := false

	ms := testutil.NewMockServer(t, func(conn *websocket.Conn, srv *testutil.MockServer) {
		for { // Add infinite loop to keep connection open
			var reqEnv ergosockets.Envelope
			err := wsjson.Read(context.Background(), conn, &reqEnv)
			if err != nil {
				t.Logf("MockServer: Read error: %v", err)
				return
			}

			// Handle registration first
			if !registrationHandled && reqEnv.Topic == app_shared_types.TopicClientRegister {
				var reg app_shared_types.ClientRegistration
				if err := reqEnv.DecodePayload(&reg); err != nil {
					t.Errorf("Failed to decode registration payload: %v", err)
					return
				}

				// Create response with server-assigned ID
				respEnv, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeResponse, reqEnv.Topic, app_shared_types.ClientRegistrationResponse{
					ServerAssignedID: "server-" + reg.ClientID[:8],
					ClientName:       reg.ClientName,
					ServerTime:       time.Now().Format(time.RFC3339),
				}, nil)
				// Set expectation before sending
				srv.Expect(1)
				srv.Send(*respEnv)
				t.Logf("MockServer: Handled registration request, assigned ID: server-%s", reg.ClientID[:8])
				registrationHandled = true
			} else {
				// For all other requests, capture payload and respond
				lastReceivedPayload = reqEnv.Payload // Capture the raw payload
				respEnv, _ := ergosockets.NewEnvelope(reqEnv.ID, ergosockets.TypeResponse, reqEnv.Topic, map[string]string{"status": "ok"}, nil)
				// Set expectation before sending
				srv.Expect(1)
				srv.Send(*respEnv)
			}
		}
	})
	defer ms.Close()

	cli := testutil.NewTestClient(t, ms.WsURL)
	defer cli.Close()
	ctx := context.Background()

	// Test 1: No payload argument
	_, err := client.Request[map[string]string](cli, ctx, "topic_no_payload")
	if err != nil {
		t.Fatalf("Request with no payload failed: %v", err)
	}
	// wsjson sends "null" for nil interface{}, but we need to check if it's empty or "null"
	payloadStr := string(lastReceivedPayload)
	if payloadStr != "null" && payloadStr != "" {
		t.Errorf("Expected 'null' or empty payload from server, got: %s", payloadStr)
	}
	t.Logf("Request with no payload sent, server received: %s", payloadStr)

	// Test 2: With one payload argument
	payloadData := app_shared_types.GetUserDetailsRequest{UserID: "var123"}
	_, err = client.Request[map[string]string](cli, ctx, "topic_with_payload", payloadData)
	if err != nil {
		t.Fatalf("Request with payload failed: %v", err)
	}
	var decodedSentPayload app_shared_types.GetUserDetailsRequest
	if err := json.Unmarshal(lastReceivedPayload, &decodedSentPayload); err != nil {
		t.Fatalf("Failed to unmarshal received payload at server: %v. Payload: %s", err, string(lastReceivedPayload))
	}
	if decodedSentPayload.UserID != "var123" {
		t.Errorf("Server expected UserID 'var123', got '%s'", decodedSentPayload.UserID)
	}
	t.Logf("Request with payload sent, server received: %s", string(lastReceivedPayload))
}

func TestClientWithContext(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}
	t.Parallel()

	ms := testutil.NewMockServer(t, func(conn *websocket.Conn, srv *testutil.MockServer) {
		// Handle registration and then block reading
		var regEnv ergosockets.Envelope
		regCtx, regCancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer regCancel()
		err := wsjson.Read(regCtx, conn, &regEnv)
		if err != nil {
			t.Errorf("MockServer: Did not receive registration request: %v", err)
			return
		}

		if regEnv.Topic == app_shared_types.TopicClientRegister {
			var reg app_shared_types.ClientRegistration
			if err := regEnv.DecodePayload(&reg); err != nil {
				t.Errorf("Failed to decode registration payload: %v", err)
				return
			}

			// Create response with server-assigned ID
			respEnv, _ := ergosockets.NewEnvelope(regEnv.ID, ergosockets.TypeResponse, regEnv.Topic, app_shared_types.ClientRegistrationResponse{
				ServerAssignedID: "server-" + reg.ClientID[:8],
				ClientName:       reg.ClientName,
				ServerTime:       time.Now().Format(time.RFC3339),
			}, nil)
			srv.Expect(1)
			srv.Send(*respEnv)
			t.Logf("MockServer: Handled registration request, assigned ID: server-%s", reg.ClientID[:8])
		}

		// Keep reading until connection closes
		for {
			var msg interface{}
			if err := wsjson.Read(context.Background(), conn, &msg); err != nil {
				t.Logf("MockServer: Connection closed: %v", err)
				return
			}
		}
	})
	defer ms.Close()

	// Create a parent context that we can cancel
	parentCtx, parentCancel := context.WithCancel(context.Background())

	// Create client with parent context
	cli := testutil.NewTestClient(t, ms.WsURL, client.WithContext(parentCtx))

	// Give the client time to establish connection
	time.Sleep(100 * time.Millisecond)

	// Cancel the parent context
	parentCancel()

	// Give some time for cleanup
	time.Sleep(100 * time.Millisecond)

	// Try to make a request - it should fail since context was cancelled
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := client.Request[app_shared_types.GetTimeResponse](cli, ctx, app_shared_types.TopicGetTime)
	if err == nil {
		t.Error("Expected error after parent context cancellation, but got none")
	}

	t.Logf("Client correctly shut down after parent context cancellation: %v", err)
}

// Use testutil.NewTestClient instead of defining our own
