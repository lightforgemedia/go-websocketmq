// Package filewatcher provides file system watching capabilities.
package filewatcher

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// FileWatcher watches directories for file changes
type FileWatcher struct {
	watcher     *fsnotify.Watcher
	dirs        []string
	patterns    []string
	logger      *slog.Logger
	callbacks   []func(string)
	callbacksMu sync.RWMutex
	debounceMs  int
	changes     map[string]time.Time
	changesMu   sync.Mutex
	done        chan struct{}
}

// New creates a new FileWatcher
func New(opts ...Option) (*FileWatcher, error) {
	// Create fsnotify watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	// Create file watcher with default options
	fw := &FileWatcher{
		watcher:    watcher,
		dirs:       []string{"."},
		patterns:   []string{"*"},
		logger:     slog.New(slog.NewTextHandler(os.Stderr, nil)),
		callbacks:  []func(string){},
		debounceMs: 300, // Default debounce of 300ms
		changes:    make(map[string]time.Time),
		done:       make(chan struct{}),
	}

	// Apply options
	for _, opt := range opts {
		opt(fw)
	}

	return fw, nil
}

// AddCallback adds a callback to be called when files change
func (fw *FileWatcher) AddCallback(callback func(string)) {
	fw.callbacksMu.Lock()
	defer fw.callbacksMu.Unlock()
	fw.callbacks = append(fw.callbacks, callback)
}

// Start starts watching for file changes
func (fw *FileWatcher) Start() error {
	// Add all directories to the watcher
	for _, dir := range fw.dirs {
		fw.logger.Info("Watching directory", "dir", dir)
		err := fw.watcher.Add(dir)
		if err != nil {
			return err
		}
	}

	// Start the watcher goroutine
	go fw.watchLoop()

	return nil
}

// Stop stops watching for file changes
func (fw *FileWatcher) Stop() error {
	close(fw.done)
	return fw.watcher.Close()
}

// watchLoop is the main loop for watching file changes
func (fw *FileWatcher) watchLoop() {
	ticker := time.NewTicker(time.Duration(fw.debounceMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-fw.done:
			return
		case event, ok := <-fw.watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				if fw.matchesPattern(event.Name) {
					fw.changesMu.Lock()
					fw.changes[event.Name] = time.Now()
					fw.changesMu.Unlock()
				}
			}
		case err, ok := <-fw.watcher.Errors:
			if !ok {
				return
			}
			fw.logger.Error("Watcher error", "error", err)
		case <-ticker.C:
			fw.processChanges()
		}
	}
}

// processChanges processes accumulated changes
func (fw *FileWatcher) processChanges() {
	fw.changesMu.Lock()
	defer fw.changesMu.Unlock()

	now := time.Now()
	for file, changeTime := range fw.changes {
		// Only process changes that are older than the debounce time
		if now.Sub(changeTime) >= time.Duration(fw.debounceMs)*time.Millisecond {
			fw.logger.Info("File changed", "file", file)
			fw.notifyCallbacks(file)
			delete(fw.changes, file)
		}
	}
}

// notifyCallbacks notifies all callbacks about a file change
func (fw *FileWatcher) notifyCallbacks(file string) {
	fw.callbacksMu.RLock()
	defer fw.callbacksMu.RUnlock()

	for _, callback := range fw.callbacks {
		callback(file)
	}
}

// matchesPattern checks if a file matches any of the patterns
func (fw *FileWatcher) matchesPattern(file string) bool {
	// Get the base filename
	base := filepath.Base(file)

	// Check against all patterns
	for _, pattern := range fw.patterns {
		matched, err := filepath.Match(pattern, base)
		if err != nil {
			fw.logger.Error("Pattern match error", "pattern", pattern, "error", err)
			continue
		}
		if matched {
			return true
		}
	}

	return false
}
