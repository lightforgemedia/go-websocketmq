// internal/devwatch/watcher.go
package devwatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
)

// WatchOptions configures the file watcher
type WatchOptions struct {
	Paths       []string        // Paths to watch
	Extensions  []string        // File extensions to watch
	ExcludeDirs []string        // Directories to exclude
	Debounce    time.Duration   // Debounce duration to prevent multiple events
}

// DefaultWatchOptions returns default options for development watcher
func DefaultWatchOptions() WatchOptions {
	return WatchOptions{
		Paths:       []string{"."},
		Extensions:  []string{".go", ".js", ".html", ".css"},
		ExcludeDirs: []string{".git", "node_modules", "vendor", "dist"},
		Debounce:    300 * time.Millisecond,
	}
}

// StartWatcher starts a file watcher that publishes hot-reload events
func StartWatcher(ctx context.Context, b broker.Broker, logger broker.Logger, opts WatchOptions) (func() error, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	// Recursively add all directories to the watcher
	for _, path := range opts.Paths {
		if err := addDirsToWatcher(watcher, path, opts.ExcludeDirs); err != nil {
			watcher.Close()
			return nil, err
		}
	}

	// The topic for hot reload events
	const hotReloadTopic = "_dev.hotreload"

	// Start the watcher goroutine
	go func() {
		// Create a timer for debouncing
		var debounceTimer *time.Timer
		var pendingReload bool

		// Context done channel
		done := ctx.Done()

		for {
			select {
			case <-done:
				watcher.Close()
				return

			case event, ok := <-watcher.Events:
				if !ok {
					return
				}

				// Check if this is a relevant event (create, write, rename, but not chmod)
				if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) || event.Has(fsnotify.Rename) {
					// Check if this file has a relevant extension
					ext := strings.ToLower(filepath.Ext(event.Name))
					isRelevant := false
					for _, watchExt := range opts.Extensions {
						if ext == watchExt {
							isRelevant = true
							break
						}
					}

					if isRelevant {
						// If this is a create event on a directory, add it to the watcher
						if event.Has(fsnotify.Create) {
							isDir, _ := isDirectory(event.Name)
							if isDir {
								addDirsToWatcher(watcher, event.Name, opts.ExcludeDirs)
							}
						}

						// Debounce the hot-reload event
						if debounceTimer == nil {
							debounceTimer = time.AfterFunc(opts.Debounce, func() {
								if pendingReload {
									// Create a hot-reload message
									msg := model.NewEvent(hotReloadTopic, map[string]interface{}{
										"file":      event.Name,
										"timestamp": time.Now().UnixMilli(),
									})

									// Publish the hot-reload message
									if err := b.Publish(ctx, msg); err != nil {
										logger.Error("Failed to publish hot-reload event: %v", err)
									} else {
										logger.Info("Published hot-reload event for %s", event.Name)
									}

									pendingReload = false
								}
							})
						} else {
							debounceTimer.Reset(opts.Debounce)
						}
						pendingReload = true
					}
				}

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logger.Error("Watcher error: %v", err)
			}
		}
	}()

	// Return a function to close the watcher
	return watcher.Close, nil
}

// addDirsToWatcher recursively adds directories to the watcher
func addDirsToWatcher(watcher *fsnotify.Watcher, path string, excludeDirs []string) error {
	// Process the initial path
	isDir, err := isDirectory(path)
	if err != nil {
		return err
	}

	if !isDir {
		// If it's a file, add its parent directory
		return watcher.Add(filepath.Dir(path))
	}

	// Walk the directory tree
	entries, err := filepath.Glob(filepath.Join(path, "*"))
	if err != nil {
		return err
	}

	// Add the root directory
	if err := watcher.Add(path); err != nil {
		return err
	}

	// Process subdirectories
	for _, entry := range entries {
		isDir, err := isDirectory(entry)
		if err != nil {
			continue
		}

		if isDir {
			// Skip excluded directories
			baseName := filepath.Base(entry)
			skip := false
			for _, exclude := range excludeDirs {
				if baseName == exclude {
					skip = true
					break
				}
			}

			if !skip {
				if err := addDirsToWatcher(watcher, entry, excludeDirs); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// isDirectory checks if a path is a directory
func isDirectory(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}