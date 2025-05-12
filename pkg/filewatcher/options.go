package filewatcher

import (
	"log/slog"
)

// Option configures a FileWatcher
type Option func(*FileWatcher)

// WithLogger sets the logger for the file watcher
func WithLogger(logger *slog.Logger) Option {
	return func(fw *FileWatcher) {
		if logger != nil {
			fw.logger = logger
		}
	}
}

// WithDirs sets the directories to watch
func WithDirs(dirs []string) Option {
	return func(fw *FileWatcher) {
		if len(dirs) > 0 {
			fw.dirs = dirs
		}
	}
}

// WithPatterns sets the file patterns to watch
func WithPatterns(patterns []string) Option {
	return func(fw *FileWatcher) {
		if len(patterns) > 0 {
			fw.patterns = patterns
		}
	}
}

// WithDebounce sets the debounce time in milliseconds
func WithDebounce(debounceMs int) Option {
	return func(fw *FileWatcher) {
		if debounceMs > 0 {
			fw.debounceMs = debounceMs
		}
	}
}
