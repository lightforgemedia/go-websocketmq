package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/client"
	"github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
)

func main() {
	if len(os.Args) < 3 {
		printUsage()
		os.Exit(1)
	}

	clientID := os.Args[1]
	command := os.Args[2]

	// Connect to the broker
	cli, err := connectToBroker()
	if err != nil {
		log.Fatalf("Failed to connect to broker: %v", err)
	}
	defer cli.Close()

	ctx := context.Background()

	switch command {
	case "fill":
		fillForm(ctx, cli, clientID)
	case "submit":
		submitForm(ctx, cli, clientID)
	case "clear":
		clearForm(ctx, cli, clientID)
	case "screenshot":
		takeScreenshot(ctx, cli, clientID)
	case "scroll":
		if len(os.Args) < 5 {
			fmt.Println("Usage: automate <client-id> scroll <x> <y>")
			os.Exit(1)
		}
		var x, y int
		fmt.Sscanf(os.Args[3], "%d", &x)
		fmt.Sscanf(os.Args[4], "%d", &y)
		scrollPage(ctx, cli, clientID, x, y)
	case "click":
		if len(os.Args) < 4 {
			fmt.Println("Usage: automate <client-id> click <selector>")
			os.Exit(1)
		}
		clickElement(ctx, cli, clientID, os.Args[3])
	case "demo":
		runFullDemo(ctx, cli, clientID)
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Browser Automation CLI")
	fmt.Println("Usage: automate <client-id> <command> [arguments]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  fill               - Fill the demo form with sample data")
	fmt.Println("  submit             - Submit the form")
	fmt.Println("  clear              - Clear the form")
	fmt.Println("  screenshot         - Take a screenshot")
	fmt.Println("  scroll <x> <y>     - Scroll to position")
	fmt.Println("  click <selector>   - Click an element")
	fmt.Println("  demo               - Run full automation demo")
}

func connectToBroker() (*client.Client, error) {
	return client.Connect("ws://localhost:8080/ws",
		client.WithClientName("Automation Control"),
		client.WithClientType("cli"),
	)
}

func sendProxyRequest(ctx context.Context, cli *client.Client, targetID, topic string, payload interface{}) (json.RawMessage, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %v", err)
	}

	proxyReq := shared_types.ProxyRequest{
		TargetID: targetID,
		Topic:    topic,
		Payload:  payloadBytes,
	}

	rawResp, _, err := cli.SendServerRequest(ctx, shared_types.TopicProxyRequest, proxyReq)
	if err != nil {
		return nil, err
	}

	return *rawResp, nil
}

func fillForm(ctx context.Context, cli *client.Client, targetID string) {
	req := map[string]interface{}{
		"fields": map[string]string{
			"name":    "John Doe",
			"email":   "john.doe@example.com",
			"website": "https://example.com",
			"message": "This is an automated test message from the WebSocketMQ browser control demo.",
		},
	}

	rawResp, err := sendProxyRequest(ctx, cli, targetID, "browser.fillForm", req)
	if err != nil {
		log.Fatalf("Failed to fill form: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rawResp, &resp); err != nil {
		log.Fatalf("Failed to parse response: %v", err)
	}

	if success, ok := resp["success"].(bool); ok && success {
		fmt.Println("Successfully filled form fields:")
		if filled, ok := resp["filled"].([]interface{}); ok {
			for _, field := range filled {
				fmt.Printf("  - %v\n", field)
			}
		}
	} else {
		fmt.Printf("Failed to fill form: %v\n", resp["error"])
	}
}

func submitForm(ctx context.Context, cli *client.Client, targetID string) {
	rawResp, err := sendProxyRequest(ctx, cli, targetID, "browser.submitForm", map[string]string{})
	if err != nil {
		log.Fatalf("Failed to submit form: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rawResp, &resp); err != nil {
		log.Fatalf("Failed to parse response: %v", err)
	}

	if success, ok := resp["success"].(bool); ok && success {
		fmt.Println("Successfully submitted form")
		if data, ok := resp["data"].(map[string]interface{}); ok {
			fmt.Println("Form data:")
			for key, value := range data {
				fmt.Printf("  %s: %v\n", key, value)
			}
		}
	} else {
		fmt.Printf("Failed to submit form: %v\n", resp["error"])
	}
}

func clearForm(ctx context.Context, cli *client.Client, targetID string) {
	rawResp, err := sendProxyRequest(ctx, cli, targetID, "browser.clearForm", map[string]string{})
	if err != nil {
		log.Fatalf("Failed to clear form: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rawResp, &resp); err != nil {
		log.Fatalf("Failed to parse response: %v", err)
	}

	if success, ok := resp["success"].(bool); ok && success {
		fmt.Println("Successfully cleared form")
	} else {
		fmt.Printf("Failed to clear form: %v\n", resp["error"])
	}
}

func takeScreenshot(ctx context.Context, cli *client.Client, targetID string) {
	rawResp, err := sendProxyRequest(ctx, cli, targetID, "browser.screenshot", map[string]string{})
	if err != nil {
		log.Fatalf("Failed to take screenshot: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rawResp, &resp); err != nil {
		log.Fatalf("Failed to parse response: %v", err)
	}

	if success, ok := resp["success"].(bool); ok && success {
		fmt.Println("Successfully took screenshot")
		// In a real implementation, you could save the dataUrl to a file
	} else {
		fmt.Printf("Failed to take screenshot: %v\n", resp["error"])
	}
}

func scrollPage(ctx context.Context, cli *client.Client, targetID string, x, y int) {
	req := map[string]interface{}{
		"x": x,
		"y": y,
	}

	rawResp, err := sendProxyRequest(ctx, cli, targetID, "browser.scroll", req)
	if err != nil {
		log.Fatalf("Failed to scroll: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rawResp, &resp); err != nil {
		log.Fatalf("Failed to parse response: %v", err)
	}

	if success, ok := resp["success"].(bool); ok && success {
		fmt.Printf("Successfully scrolled to position (%v, %v)\n", resp["scrollX"], resp["scrollY"])
	} else {
		fmt.Printf("Failed to scroll: %v\n", resp["error"])
	}
}

func clickElement(ctx context.Context, cli *client.Client, targetID string, selector string) {
	req := map[string]string{
		"selector": selector,
	}

	rawResp, err := sendProxyRequest(ctx, cli, targetID, "browser.click", req)
	if err != nil {
		log.Fatalf("Failed to click element: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rawResp, &resp); err != nil {
		log.Fatalf("Failed to parse response: %v", err)
	}

	if success, ok := resp["success"].(bool); ok && success {
		fmt.Printf("Successfully clicked element: %v\n", resp["element"])
	} else {
		fmt.Printf("Failed to click element: %v\n", resp["error"])
	}
}

func runFullDemo(ctx context.Context, cli *client.Client, targetID string) {
	fmt.Println("Running full automation demo...")
	fmt.Println()

	// Step 1: Clear the form
	fmt.Println("Step 1: Clearing form...")
	clearForm(ctx, cli, targetID)
	time.Sleep(1 * time.Second)

	// Step 2: Fill the form
	fmt.Println("\nStep 2: Filling form with sample data...")
	fillForm(ctx, cli, targetID)
	time.Sleep(2 * time.Second)

	// Step 3: Take a screenshot
	fmt.Println("\nStep 3: Taking screenshot of filled form...")
	takeScreenshot(ctx, cli, targetID)
	time.Sleep(1 * time.Second)

	// Step 4: Submit the form
	fmt.Println("\nStep 4: Submitting form...")
	submitForm(ctx, cli, targetID)
	time.Sleep(2 * time.Second)

	// Step 5: Scroll down
	fmt.Println("\nStep 5: Scrolling to show results...")
	scrollPage(ctx, cli, targetID, 0, 300)
	time.Sleep(1 * time.Second)

	fmt.Println("\nDemo completed!")
}