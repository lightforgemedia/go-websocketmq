package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/client"
	"github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
)

// TestBrowserControl tests the browser control proxy functionality
func TestBrowserControl(t *testing.T) {
	// Create a test broker
	opts := broker.DefaultOptions()
	opts.PingInterval = -1 // Disable pings for test
	bs := testutil.NewBrokerServer(t, opts)

	// Create "browser" client that will receive commands
	browserClientOpts := client.DefaultOptions()
	browserClientOpts.ClientName = "Test Browser"
	browserClientOpts.ClientType = "browser"
	browserClientOpts.PingInterval = -1 // Disable pings for test
	browserClient, err := client.ConnectWithOptions(bs.WSURL, browserClientOpts)
	if err != nil {
		t.Fatalf("Failed to create browser client: %v", err)
	}

	// Set up browser handlers
	setupBrowserHandlers(t, browserClient)

	// Create control client that will send commands
	controlClientOpts := client.DefaultOptions()
	controlClientOpts.ClientName = "Test Control"
	controlClientOpts.ClientType = "cli"
	controlClientOpts.PingInterval = -1 // Disable pings for test
	controlClient, err := client.ConnectWithOptions(bs.WSURL, controlClientOpts)
	if err != nil {
		t.Fatalf("Failed to create control client: %v", err)
	}

	// Wait for both clients to connect
	_, err = testutil.WaitForClient(t, bs.Broker, browserClient.ID(), 2*time.Second)
	if err != nil {
		t.Fatalf("Browser client did not connect: %v", err)
	}

	_, err = testutil.WaitForClient(t, bs.Broker, controlClient.ID(), 2*time.Second)
	if err != nil {
		t.Fatalf("Control client did not connect: %v", err)
	}

	// Test browser.navigate command
	t.Run("Navigate", func(t *testing.T) {
		navigateReq := map[string]string{"url": "https://example.com"}
		resp, err := sendProxyCommand(t, controlClient, browserClient.ID(), "browser.navigate", navigateReq)
		if err != nil {
			t.Fatalf("Failed to send navigate command: %v", err)
		}

		if !resp["success"].(bool) {
			t.Errorf("Navigate command failed: %v", resp["error"])
		}
	})

	// Test browser.alert command
	t.Run("Alert", func(t *testing.T) {
		alertReq := map[string]string{"message": "Test alert"}
		resp, err := sendProxyCommand(t, controlClient, browserClient.ID(), "browser.alert", alertReq)
		if err != nil {
			t.Fatalf("Failed to send alert command: %v", err)
		}

		if !resp["success"].(bool) {
			t.Errorf("Alert command failed: %v", resp["error"])
		}
	})

	// Test browser.exec command
	t.Run("Execute", func(t *testing.T) {
		execReq := map[string]string{"code": "return 2 + 2"}
		resp, err := sendProxyCommand(t, controlClient, browserClient.ID(), "browser.exec", execReq)
		if err != nil {
			t.Fatalf("Failed to send exec command: %v", err)
		}

		if !resp["success"].(bool) {
			t.Errorf("Exec command failed: %v", resp["error"])
		}

		if resp["result"] != "4" {
			t.Errorf("Expected result '4', got '%v'", resp["result"])
		}
	})

	// Test browser.info command
	t.Run("Info", func(t *testing.T) {
		infoReq := map[string]string{}
		resp, err := sendProxyCommand(t, controlClient, browserClient.ID(), "browser.info", infoReq)
		if err != nil {
			t.Fatalf("Failed to send info command: %v", err)
		}

		// Check that we got expected fields
		if _, ok := resp["userAgent"]; !ok {
			t.Error("Missing userAgent in info response")
		}
		if _, ok := resp["url"]; !ok {
			t.Error("Missing url in info response")
		}
	})
}

func setupBrowserHandlers(t *testing.T, browserClient *client.Client) {
	// Mock browser.navigate handler
	err := browserClient.HandleServerRequest("browser.navigate", func(req map[string]string) (map[string]interface{}, error) {
		url := req["url"]
		t.Logf("Mock browser navigating to: %s", url)
		return map[string]interface{}{"success": true, "message": "Navigated to " + url}, nil
	})
	if err != nil {
		t.Fatalf("Failed to register navigate handler: %v", err)
	}

	// Mock browser.alert handler
	err = browserClient.HandleServerRequest("browser.alert", func(req map[string]string) (map[string]interface{}, error) {
		message := req["message"]
		t.Logf("Mock browser showing alert: %s", message)
		return map[string]interface{}{"success": true, "message": "Alert shown"}, nil
	})
	if err != nil {
		t.Fatalf("Failed to register alert handler: %v", err)
	}

	// Mock browser.exec handler
	err = browserClient.HandleServerRequest("browser.exec", func(req map[string]string) (map[string]interface{}, error) {
		code := req["code"]
		t.Logf("Mock browser executing: %s", code)
		// Simulate simple execution
		if code == "return 2 + 2" {
			return map[string]interface{}{"success": true, "result": "4"}, nil
		}
		return map[string]interface{}{"success": true, "result": "undefined"}, nil
	})
	if err != nil {
		t.Fatalf("Failed to register exec handler: %v", err)
	}

	// Mock browser.info handler
	err = browserClient.HandleServerRequest("browser.info", func(req map[string]string) (map[string]interface{}, error) {
		t.Log("Mock browser returning info")
		return map[string]interface{}{
			"userAgent": "Mock Browser 1.0",
			"url":       "http://localhost/test",
			"title":     "Test Page",
			"viewport": map[string]int{
				"width":  1024,
				"height": 768,
			},
		}, nil
	})
	if err != nil {
		t.Fatalf("Failed to register info handler: %v", err)
	}
}

func sendProxyCommand(t *testing.T, controlClient *client.Client, targetID, topic string, payload interface{}) (map[string]interface{}, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	proxyReq := shared_types.ProxyRequest{
		TargetID: targetID,
		Topic:    topic,
		Payload:  payloadBytes,
	}

	rawResp, _, err := controlClient.SendServerRequest(context.Background(), shared_types.TopicProxyRequest, proxyReq)
	if err != nil {
		return nil, err
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(*rawResp, &resp); err != nil {
		return nil, err
	}

	return resp, nil
}