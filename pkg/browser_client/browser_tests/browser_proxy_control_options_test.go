// Package browser_tests contains tests for the WebSocketMQ browser client proxy control functionality.
package browser_tests

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/client"
	"github.com/lightforgemedia/go-websocketmq/pkg/shared_types"
	"github.com/lightforgemedia/go-websocketmq/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBrowserProxyControlWithOptions tests the proxy control functionality using the Options pattern
func TestBrowserProxyControlWithOptions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping browser test in short mode")
	}

	// Create a custom logger for this test
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Use DefaultOptions and customize them
	opts := broker.DefaultOptions()
	opts.Logger = logger
	opts.AcceptOptions = &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	}
	opts.PingInterval = 15 * time.Second // Custom ping interval
	opts.ClientSendBuffer = 32           // Larger buffer for this test

	// Create broker using NewWithOptions
	b, err := broker.NewWithOptions(opts)
	require.NoError(t, err)

	// Create the test server
	bs := &testutil.BrokerServer{
		Broker: b,
		Ready:  make(chan struct{}),
	}

	// Create an HTTP server mux
	mux := http.NewServeMux()

	// Register both WebSocket and JavaScript client handlers
	b.RegisterHandlersWithDefaults(mux)

	// Create the HTTP server
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	bs.HTTP = httpServer
	bs.WSURL = "ws" + strings.TrimPrefix(httpServer.URL, "http")

	// Serve JavaScript helpers
	mux.Handle("/js_helpers/", http.StripPrefix("/js_helpers/", http.FileServer(http.Dir("js_helpers"))))
	
	// Create a test HTML page with proxy control handlers
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<!DOCTYPE html>
<html>
<head>
    <title>Browser Proxy Control Test</title>
    <script src="/websocketmq.js"></script>
    <script src="/js_helpers/console_override.js"></script>
</head>
<body>
    <h1>Browser Proxy Control Test</h1>
    <div id="status">Initializing...</div>
    <div>Client ID: <span id="client-id">Unknown</span></div>
    <script>
    let client;
    
    function setupProxyHandlers() {
        console.log('Setting up proxy handlers');
        
        // Browser navigation handler
        client.handleServerRequest('browser.navigate', async (request) => {
            console.log('Navigate request:', request);
            const url = request.url;
            document.getElementById('status').textContent = 'Navigated to: ' + url;
            return { success: true, url: url };
        });
        
        // Alert handler
        client.handleServerRequest('browser.alert', async (request) => {
            console.log('Alert request:', request);
            const message = request.message;
            document.getElementById('status').textContent = 'Alert: ' + message;
            return { success: true, message: message };
        });
        
        // Execute handler
        client.handleServerRequest('browser.exec', async (request) => {
            console.log('Execute request:', request);
            try {
                const result = eval(request.code);
                return { success: true, result: String(result) };
            } catch (error) {
                return { success: false, error: error.message };
            }
        });
        
        // Info handler
        client.handleServerRequest('browser.info', async (request) => {
            console.log('Info request:', request);
            return {
                userAgent: navigator.userAgent,
                location: window.location.href,
                title: document.title
            };
        });
    }
    
    async function initializeClient() {
        try {
            console.log('Creating WebSocketMQ client...');
            
            // Create client
            client = new window.WebSocketMQ.Client({});
            console.log('Client created successfully');
            
            // Set up connect callback
            client.onConnect(() => {
                console.log('Connected to WebSocket server with ID:', client.getID());
                document.getElementById('status').textContent = 'Connected and ready';
                document.getElementById('client-id').textContent = client.getID();
                
                // Set up proxy handlers after connection
                setupProxyHandlers();
            });
            
            // Set up disconnect callback
            client.onDisconnect(() => {
                console.log('Disconnected from WebSocket server');
                document.getElementById('status').textContent = 'Disconnected';
            });
            
            // Set up error callback
            client.onError((error) => {
                console.error('WebSocket error:', error);
                document.getElementById('status').textContent = 'Error: ' + error.message;
            });
            
            // Connect to server
            console.log('Connecting to WebSocket server...');
            client.connect();
            console.log('Connect method called');
            
        } catch (error) {
            console.error('Failed to initialize client:', error);
            document.getElementById('status').textContent = 'Error: ' + error.message;
        }
    }
    
    // Initialize when page loads
    window.addEventListener('load', initializeClient);
    </script>
</body>
</html>`
		w.Write([]byte(html))
	})

	// Create Rod browser (headless for CI)
	headless := os.Getenv("HEADLESS") != "false"  // Allow override
	browser := testutil.NewRodBrowser(t, testutil.WithHeadless(headless))
	
	// Navigate to the test page
	page := browser.MustPage(httpServer.URL + "/").WaitForLoad()
	
	page.WaitForElement("#status", 5*time.Second)

	// Wait for the browser to connect
	assert.Eventually(t, func() bool {
		statusText := page.MustElement("#status").MustText()
		t.Logf("Browser status: %s", statusText)
		
		// Print console logs
		logs := page.GetConsoleLog()
		for _, log := range logs {
			t.Logf("Browser console: %s", log)
		}
		
		return statusText == "Connected and ready"
	}, 10*time.Second, 100*time.Millisecond, "Browser failed to connect")

	// Create a control client
	ctx := context.Background()
	controlClient, err := client.Connect(
		bs.WSURL+"/wsmq",  // Use the correct path
		client.WithLogger(logger),
		client.WithAutoReconnect(0, 0, 0),
	)
	require.NoError(t, err)
	defer controlClient.Close()

	// Wait for registration
	time.Sleep(100 * time.Millisecond)

	// Find the browser client
	var browserClient shared_types.ClientSummary
	var resp shared_types.ListClientsResponse
	req := shared_types.ListClientsRequest{ClientType: "browser"}
	rawResp, errResp, err := controlClient.SendServerRequest(ctx, "system:list_clients", req)
	require.NoError(t, err)
	require.Nil(t, errResp)
	require.NotNil(t, rawResp)
	err = json.Unmarshal(*rawResp, &resp)
	require.NoError(t, err)
	require.NotEmpty(t, resp.Clients, "No browser clients found")

	for _, c := range resp.Clients {
		if c.ClientType == "browser" {
			browserClient = c
			break
		}
	}
	require.NotEqual(t, "", browserClient.ID, "Browser client not found")

	t.Run("ProxyNavigation", func(t *testing.T) {
		req := shared_types.ProxyRequest{
			TargetID: browserClient.ID,
			Topic:    "browser.navigate",
			Payload:  json.RawMessage(`{"url": "https://example.com"}`),
		}

		// Use the control client to send the proxy request to the broker
		rawResp, errResp, err := controlClient.SendServerRequest(ctx, "system:proxy", req)
		require.NoError(t, err)
		require.Nil(t, errResp)
		require.NotNil(t, rawResp)

		// Check the browser status
		assert.Eventually(t, func() bool {
			return strings.Contains(page.MustElement("#status").MustText(), "Navigated to: https://example.com")
		}, 5*time.Second, 100*time.Millisecond)
	})

	t.Run("ProxyAlert", func(t *testing.T) {
		req := shared_types.ProxyRequest{
			TargetID: browserClient.ID,
			Topic:    "browser.alert",
			Payload:  json.RawMessage(`{"message": "Hello from proxy!"}`),
		}

		rawResp, errResp, err := controlClient.SendServerRequest(ctx, "system:proxy", req)
		require.NoError(t, err)
		require.Nil(t, errResp)
		require.NotNil(t, rawResp)

		// Check the browser status
		assert.Eventually(t, func() bool {
			return strings.Contains(page.MustElement("#status").MustText(), "Alert: Hello from proxy!")
		}, 5*time.Second, 100*time.Millisecond)
	})

	t.Run("ProxyExecute", func(t *testing.T) {
		type ExecResponse struct {
			Success bool   `json:"success"`
			Result  string `json:"result"`
		}

		req := shared_types.ProxyRequest{
			TargetID: browserClient.ID,
			Topic:    "browser.exec",
			Payload:  json.RawMessage(`{"code": "2 + 2"}`),
		}

		rawResp, errResp, err := controlClient.SendServerRequest(ctx, "system:proxy", req)
		require.NoError(t, err)
		require.Nil(t, errResp)
		require.NotNil(t, rawResp)

		var execResp ExecResponse
		err = json.Unmarshal(*rawResp, &execResp)
		require.NoError(t, err)
		assert.True(t, execResp.Success)
		assert.Equal(t, "4", execResp.Result)
	})

	t.Run("ProxyInfo", func(t *testing.T) {
		type InfoResponse struct {
			UserAgent string `json:"userAgent"`
			Location  string `json:"location"`
			Title     string `json:"title"`
		}

		req := shared_types.ProxyRequest{
			TargetID: browserClient.ID,
			Topic:    "browser.info",
			Payload:  json.RawMessage(`{}`),
		}

		rawResp, errResp, err := controlClient.SendServerRequest(ctx, "system:proxy", req)
		require.NoError(t, err)
		require.Nil(t, errResp)
		require.NotNil(t, rawResp)

		var infoResp InfoResponse
		err = json.Unmarshal(*rawResp, &infoResp)
		require.NoError(t, err)
		assert.NotEmpty(t, infoResp.UserAgent)
		assert.Contains(t, infoResp.Location, httpServer.URL)
		assert.Equal(t, "Browser Proxy Control Test", infoResp.Title)
	})

	// Test error handling
	t.Run("InvalidTarget", func(t *testing.T) {
		req := shared_types.ProxyRequest{
			TargetID: "invalid-client-id",
			Topic:    "browser.info",
			Payload:  json.RawMessage(`{}`),
		}

		_, errResp, err := controlClient.SendServerRequest(ctx, "system:proxy", req)
		if err != nil {
			// The error could be wrapped, check for "not found" in the error message
			assert.Contains(t, err.Error(), "not found")
		} else if errResp != nil {
			// Or it could be in the error response
			assert.Contains(t, errResp.Message, "not found")
		} else {
			t.Fatal("Expected an error for invalid target")
		}
	})
}