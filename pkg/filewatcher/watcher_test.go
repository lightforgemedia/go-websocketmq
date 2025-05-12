package filewatcher

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileWatcher(t *testing.T) {
	// Create a logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create a file watcher
	fw, err := New(
		WithLogger(logger),
		WithDirs([]string{tempDir}),
		WithPatterns([]string{"*.txt", "*.html"}),
		WithDebounce(100), // 100ms debounce for faster testing
	)
	require.NoError(t, err, "Failed to create file watcher")

	// Create a channel to receive file change notifications
	changeCh := make(chan string, 10)
	fw.AddCallback(func(file string) {
		changeCh <- file
	})

	// Start the watcher
	err = fw.Start()
	require.NoError(t, err, "Failed to start file watcher")
	defer fw.Stop()

	// Create a test file
	testFile := filepath.Join(tempDir, "test.txt")
	err = os.WriteFile(testFile, []byte("Hello, World!"), 0644)
	require.NoError(t, err, "Failed to write test file")

	// Wait for the change notification
	select {
	case changedFile := <-changeCh:
		assert.Equal(t, testFile, changedFile, "Changed file should match")
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for file change notification")
	}

	// Create a file that doesn't match the patterns
	nonMatchingFile := filepath.Join(tempDir, "test.log")
	err = os.WriteFile(nonMatchingFile, []byte("Log file"), 0644)
	require.NoError(t, err, "Failed to write non-matching file")

	// Wait a bit to ensure no notification is sent
	select {
	case changedFile := <-changeCh:
		t.Fatalf("Received unexpected change notification for %s", changedFile)
	case <-time.After(300 * time.Millisecond):
		// Success - no notification received
	}

	// Modify the matching file
	err = os.WriteFile(testFile, []byte("Updated content"), 0644)
	require.NoError(t, err, "Failed to update test file")

	// Wait for the change notification
	select {
	case changedFile := <-changeCh:
		assert.Equal(t, testFile, changedFile, "Changed file should match")
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for file change notification")
	}
}
