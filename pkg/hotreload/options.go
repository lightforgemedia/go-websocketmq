package hotreload

import (
	"errors"
	"log/slog"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/filewatcher"
)

// Errors
var (
	ErrNoBroker      = errors.New("no broker provided")
	ErrNoFileWatcher = errors.New("no file watcher provided")
)

// Options configures the HotReload service
type Options struct {
	// MaxErrorsPerClient is the maximum number of errors to store per client
	MaxErrorsPerClient int
}

// DefaultOptions returns the default options
func DefaultOptions() Options {
	return Options{
		MaxErrorsPerClient: 100,
	}
}

// Option configures a HotReload service
type Option func(*HotReload)

// WithLogger sets the logger for the hot reload service
func WithLogger(logger *slog.Logger) Option {
	return func(hr *HotReload) {
		if logger != nil {
			hr.logger = logger
		}
	}
}

// WithBroker sets the broker for the hot reload service
func WithBroker(broker *broker.Broker) Option {
	return func(hr *HotReload) {
		hr.broker = broker
	}
}

// WithFileWatcher sets the file watcher for the hot reload service
func WithFileWatcher(watcher *filewatcher.FileWatcher) Option {
	return func(hr *HotReload) {
		hr.watcher = watcher
	}
}

// WithMaxErrorsPerClient sets the maximum number of errors to store per client
func WithMaxErrorsPerClient(max int) Option {
	return func(hr *HotReload) {
		if max > 0 {
			hr.options.MaxErrorsPerClient = max
		}
	}
}
