package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/lightforgemedia/go-websocketmq/pkg/client"
	"github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	// Connect to the broker
	cli, err := connectToBroker()
	if err != nil {
		log.Fatalf("Failed to connect to broker: %v", err)
	}
	defer cli.Close()

	ctx := context.Background()

	switch command {
	case "list":
		listClients(ctx, cli)
	case "navigate":
		if len(os.Args) < 4 {
			fmt.Println("Usage: control navigate <client-id> <url>")
			os.Exit(1)
		}
		navigate(ctx, cli, os.Args[2], os.Args[3])
	case "alert":
		if len(os.Args) < 4 {
			fmt.Println("Usage: control alert <client-id> <message>")
			os.Exit(1)
		}
		showAlert(ctx, cli, os.Args[2], os.Args[3])
	case "exec":
		if len(os.Args) < 4 {
			fmt.Println("Usage: control exec <client-id> <javascript-code>")
			os.Exit(1)
		}
		executeJS(ctx, cli, os.Args[2], os.Args[3])
	case "info":
		if len(os.Args) < 3 {
			fmt.Println("Usage: control info <client-id>")
			os.Exit(1)
		}
		getInfo(ctx, cli, os.Args[2])
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Browser Control CLI")
	fmt.Println("Usage: control <command> [arguments]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  list                          - List connected clients")
	fmt.Println("  navigate <client-id> <url>    - Navigate browser to URL")
	fmt.Println("  alert <client-id> <message>   - Show alert in browser")
	fmt.Println("  exec <client-id> <js-code>    - Execute JavaScript in browser")
	fmt.Println("  info <client-id>              - Get browser information")
}

func connectToBroker() (*client.Client, error) {
	// Using the Options pattern for client configuration
	opts := client.DefaultOptions()
	opts.ClientName = "Control CLI"
	opts.ClientType = "cli"

	return client.ConnectWithOptions("ws://localhost:8080/ws", opts)
}

func listClients(ctx context.Context, cli *client.Client) {
	req := shared_types.ListClientsRequest{}
	resp, err := client.GenericRequest[shared_types.ListClientsResponse](cli, ctx, shared_types.TopicListClients, req)
	if err != nil {
		log.Fatalf("Failed to list clients: %v", err)
	}

	fmt.Println("Connected clients:")
	fmt.Println("ID\t\t\t\tName\t\t\tType\t\tURL")
	fmt.Println("------------------------------------------------------------")
	for _, client := range resp.Clients {
		fmt.Printf("%s\t%s\t%s\t\t%s\n", client.ID, client.Name, client.ClientType, client.ClientURL)
	}
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

func navigate(ctx context.Context, cli *client.Client, targetID, url string) {
	req := map[string]string{"url": url}
	rawResp, err := sendProxyRequest(ctx, cli, targetID, "browser.navigate", req)
	if err != nil {
		log.Fatalf("Failed to send navigate command: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rawResp, &resp); err != nil {
		log.Fatalf("Failed to parse response: %v", err)
	}

	if success, ok := resp["success"].(bool); ok && success {
		fmt.Printf("Successfully sent navigate command to %s\n", targetID)
	} else {
		fmt.Printf("Navigate command failed: %v\n", resp["error"])
	}
}

func showAlert(ctx context.Context, cli *client.Client, targetID, message string) {
	req := map[string]string{"message": message}
	rawResp, err := sendProxyRequest(ctx, cli, targetID, "browser.alert", req)
	if err != nil {
		log.Fatalf("Failed to send alert command: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rawResp, &resp); err != nil {
		log.Fatalf("Failed to parse response: %v", err)
	}

	if success, ok := resp["success"].(bool); ok && success {
		fmt.Printf("Successfully showed alert on %s\n", targetID)
	} else {
		fmt.Printf("Alert command failed: %v\n", resp["error"])
	}
}

func executeJS(ctx context.Context, cli *client.Client, targetID, code string) {
	req := map[string]string{"code": code}
	rawResp, err := sendProxyRequest(ctx, cli, targetID, "browser.exec", req)
	if err != nil {
		log.Fatalf("Failed to send execute command: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rawResp, &resp); err != nil {
		log.Fatalf("Failed to parse response: %v", err)
	}

	if success, ok := resp["success"].(bool); ok && success {
		fmt.Printf("Successfully executed JavaScript on %s\n", targetID)
		fmt.Printf("Result: %v\n", resp["result"])
	} else {
		fmt.Printf("Execute command failed: %v\n", resp["error"])
	}
}

func getInfo(ctx context.Context, cli *client.Client, targetID string) {
	req := map[string]string{}
	rawResp, err := sendProxyRequest(ctx, cli, targetID, "browser.info", req)
	if err != nil {
		log.Fatalf("Failed to send info command: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rawResp, &resp); err != nil {
		log.Fatalf("Failed to parse response: %v", err)
	}

	fmt.Printf("Browser information for %s:\n", targetID)
	fmt.Printf("User Agent: %v\n", resp["userAgent"])
	fmt.Printf("URL: %v\n", resp["url"])
	fmt.Printf("Title: %v\n", resp["title"])
	if viewport, ok := resp["viewport"].(map[string]interface{}); ok {
		fmt.Printf("Viewport: %vx%v\n", viewport["width"], viewport["height"])
	}
}
