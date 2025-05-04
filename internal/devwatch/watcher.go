// Package devwatch provides file watching functionality for development hot-reload.
package devwatch

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
)

// Logger defines the interface for logging within the devwatch package.
type Logger interface {
	Debug(msg string, args ...interface{})
	Info(msg string, args ...interface{})
	Warn(msg string, args ...interface{})
	Error(msg string, args ...interface{})
}

// Options contains configuration options for the file watcher.
type Options struct {
	// Paths is a list of file paths or directories to watch for changes.
	Paths []string

	// Extensions is a list of file extensions to watch for changes.
	// If empty, all files are watched.
	Extensions []string

	// IgnorePaths is a list of paths to ignore.
	IgnorePaths []string
}

// Watcher watches files for changes and publishes events to a broker.
type Watcher struct {
	broker  broker.Broker
	watcher *fsnotify.Watcher
	logger  Logger
	options Options
	done    chan struct{}
}

// New creates a new file watcher.
func New(b broker.Broker, logger Logger, options Options) (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &Watcher{
		broker:  b,
		watcher: fsWatcher,
		logger:  logger,
		options: options,
		done:    make(chan struct{}),
	}, nil
}

// Start starts watching files for changes.
func (w *Watcher) Start(ctx context.Context) error {
	// Add paths to watcher
	for _, path := range w.options.Paths {
		if err := w.watcher.Add(path); err != nil {
			w.logger.Error("Error adding path to watcher: %v", err)
		}
	}

	// Start watching for events
	go w.watch(ctx)

	return nil
}

// Stop stops watching files for changes.
func (w *Watcher) Stop() {
	close(w.done)
	w.watcher.Close()
}

// watch watches for file changes and publishes events to the broker.
func (w *Watcher) watch(ctx context.Context) {
	// Debounce events to prevent multiple events for the same file
	debounceTime := 100 * time.Millisecond
	debounceTimer := time.NewTimer(debounceTime)
	debounceTimer.Stop()
	var lastEvent fsnotify.Event

	for {
		select {
		case <-ctx.Done():
			w.Stop()
			return
		case <-w.done:
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}

			// Check if the file should be ignored
			if w.shouldIgnore(event.Name) {
				continue
			}

			// Check if the file has a watched extension
			if !w.hasWatchedExtension(event.Name) {
				continue
			}

			// Debounce events
			lastEvent = event
			debounceTimer.Reset(debounceTime)

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			w.logger.Error("Watcher error: %v", err)

		case <-debounceTimer.C:
			// Publish event to broker
			w.publishEvent(ctx, lastEvent)
		}
	}
}

// shouldIgnore returns true if the file should be ignored.
func (w *Watcher) shouldIgnore(path string) bool {
	for _, ignorePath := range w.options.IgnorePaths {
		if strings.Contains(path, ignorePath) {
			return true
		}
	}
	return false
}

// hasWatchedExtension returns true if the file has a watched extension.
func (w *Watcher) hasWatchedExtension(path string) bool {
	// If no extensions are specified, watch all files
	if len(w.options.Extensions) == 0 {
		return true
	}

	ext := filepath.Ext(path)
	for _, watchExt := range w.options.Extensions {
		if ext == watchExt {
			return true
		}
	}
	return false
}

// publishEvent publishes a file change event to the broker.
func (w *Watcher) publishEvent(ctx context.Context, event fsnotify.Event) {
	// Create event message
	msg := model.NewEvent("_dev.hotreload", map[string]interface{}{
		"path":      event.Name,
		"operation": event.Op.String(),
		"timestamp": time.Now().UnixMilli(),
	})

	// Publish to broker
	if err := w.broker.Publish(ctx, msg); err != nil {
		w.logger.Error("Error publishing hot-reload event: %v", err)
	} else {
		w.logger.Info("Published hot-reload event for %s", event.Name)
	}
}
