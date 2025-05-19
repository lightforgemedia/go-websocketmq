package hotreload

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/filewatcher"
	"github.com/stretchr/testify/require"
)

func TestHotReloadSimple(t *testing.T) {
	fmt.Println("Starting simple hot reload test...")

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "hotreload-test")
	require.NoError(t, err, "Failed to create temp directory")
	defer os.RemoveAll(tempDir)

	// Create a test file
	testFile := filepath.Join(tempDir, "test.html")
	err = os.WriteFile(testFile, []byte("<html><body>Initial content</body></html>"), 0644)
	require.NoError(t, err, "Failed to write test file")

	// Create a broker using Options pattern
	opts := broker.DefaultOptions()
	
	b, err := broker.NewWithOptions(opts)
	require.NoError(t, err, "Failed to create broker")

	// Create a file watcher
	fw, err := filewatcher.New(
		filewatcher.WithDirs([]string{tempDir}),
		filewatcher.WithPatterns([]string{"*.html", "*.js", "*.css"}),
		filewatcher.WithDebounce(100), // 100ms debounce for faster testing
	)
	require.NoError(t, err, "Failed to create file watcher")

	// Create a hot reload service
	hr, err := New(
		WithBroker(b),
		WithFileWatcher(fw),
	)
	require.NoError(t, err, "Failed to create hot reload service")

	// Create a channel to track file changes
	reloadCh := make(chan bool, 1)

	// Add a callback to the file watcher
	fw.AddCallback(func(file string) {
		fmt.Println("File change detected:", file)
		reloadCh <- true
	})

	// Start the hot reload service
	err = hr.Start()
	require.NoError(t, err, "Failed to start hot reload service")
	defer hr.Stop()

	// Wait a bit for everything to initialize
	time.Sleep(500 * time.Millisecond)

	// Modify the test file
	fmt.Println("Modifying test file...")
	err = os.WriteFile(testFile, []byte("<html><body>Updated content</body></html>"), 0644)
	require.NoError(t, err, "Failed to update test file")

	// Wait for the file change to be detected
	select {
	case <-reloadCh:
		fmt.Println("Reload triggered!")
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for reload")
	}

	fmt.Println("Test completed successfully!")
}
