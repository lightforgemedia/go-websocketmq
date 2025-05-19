package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/filewatcher"
	"github.com/lightforgemedia/go-websocketmq/pkg/hotreload"
)

func main() {
	// Setup basic logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Create broker with appropriate CORS settings using Options pattern
	opts := broker.DefaultOptions()
	opts.Logger = logger
	opts.AcceptOptions = &websocket.AcceptOptions{OriginPatterns: []string{"localhost:*"}}
	
	b, err := broker.NewWithOptions(opts)
	if err != nil {
		logger.Error("Failed to create broker", "error", err)
		os.Exit(1)
	}

	os.MkdirAll("./static", 0755)

	// Create file watcher for web assets
	fw, err := filewatcher.New(
		filewatcher.WithLogger(logger),
		filewatcher.WithDirs([]string{"./static"}),
		filewatcher.WithPatterns([]string{"*.html", "*.js", "*.css"}),
	)
	if err != nil {
		logger.Error("Failed to create file watcher", "error", err)
		os.Exit(1)
	}

	// Create hot reload service
	hr, err := hotreload.New(
		hotreload.WithLogger(logger),
		hotreload.WithBroker(b),
		hotreload.WithFileWatcher(fw),
	)
	if err != nil {
		logger.Error("Failed to create hot reload service", "error", err)
		os.Exit(1)
	}

	// Start the hot reload service
	if err := hr.Start(); err != nil {
		logger.Error("Failed to start hot reload service", "error", err)
		os.Exit(1)
	}
	defer hr.Stop()

	// Create an HTTP server mux
	mux := http.NewServeMux()

	// Register hot reload handlers
	hr.RegisterHandlers(mux)

	// Serve static files
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))

	// Redirect root to demo page
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/static/index.html", http.StatusFound)
	})

	// Create a simple demo HTML file
	createDemoFile("./static/index.html")

	// Start server
	addr := "localhost:8090"
	fmt.Printf("Starting server at http://%s\n", addr)
	fmt.Printf("Watching directory: ./static\n")
	fmt.Printf("Try editing files in ./static to see hot reload in action\n")

	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil {
			logger.Error("Server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for signal to exit
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	fmt.Println("Shutting down...")
}

func createDemoFile(path string) {
	demoHTML := `<!DOCTYPE html>
<html>
<head>
    <title>Hot Reload Demo</title>
    <script src="/websocketmq.js"></script>
    <script src="/hotreload.js"></script>
    <script>
        // Initialize hot reload client when page loads
        document.addEventListener('DOMContentLoaded', function() {
            const hrClient = new HotReload.Client({
                autoReload: true,
                reportErrors: true
            });
            
            // Update status display when connection changes
            hrClient.client.onConnect(() => {
                document.getElementById('status').textContent = 'Connected';
                document.getElementById('status').style.color = 'green';
            });
            
            hrClient.client.onDisconnect(() => {
                document.getElementById('status').textContent = 'Disconnected';
                document.getElementById('status').style.color = 'red';
            });
        });
    </script>
    <style>
        body { font-family: system-ui, sans-serif; max-width: 800px; margin: 0 auto; padding: 2rem; }
        .box { border: 1px solid #ccc; padding: 1rem; margin: 1rem 0; border-radius: 4px; }
        .instructions { background: #f5f5f5; }
    </style>
</head>
<body>
    <h1>Hot Reload Demo</h1>
    <div class="box">
        <p>Status: <span id="status">Connecting...</span></p>
    </div>
    <div class="box instructions">
        <h2>Instructions</h2>
        <p>Edit this file (static/index.html) to see hot reload in action.</p>
        <p>Try changing this text or styling and save the file.</p>
        <p>The page should automatically reload with your changes.</p>
    </div>
</body>
</html>`

	os.WriteFile(path, []byte(demoHTML), 0644)
}
