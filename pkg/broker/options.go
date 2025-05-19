package broker

import (
	"errors"
	"log/slog"
	"time"

	"github.com/coder/websocket"
)

// Options contains configuration values for creating a Broker using NewWithOptions.
// All fields have reasonable defaults provided by DefaultOptions().
type Options struct {
	// Logger for structured logging. Defaults to slog.Default().
	Logger *slog.Logger
	
	// AcceptOptions configures the WebSocket accept behavior.
	// Defaults to &websocket.AcceptOptions{}.
	AcceptOptions *websocket.AcceptOptions
	
	// ClientSendBuffer sets the buffer size for outgoing messages per client.
	// Must be greater than 0. Defaults to 16.
	ClientSendBuffer int
	
	// WriteTimeout is the timeout for writing messages to clients.
	// Must be positive. Defaults to 10 seconds.
	WriteTimeout time.Duration
	
	// ReadTimeout is the timeout for reading messages from clients.
	// Should be greater than PingInterval. Must be positive. Defaults to 60 seconds.
	ReadTimeout time.Duration
	
	// PingInterval is the interval between ping messages.
	// Use 0 for library default (30s), negative to disable. Defaults to 30 seconds.
	PingInterval time.Duration
	
	// ServerRequestTimeout is the default timeout for server-initiated requests.
	// Must be positive. Defaults to 10 seconds.
	ServerRequestTimeout time.Duration
}

// DefaultOptions returns an Options struct populated with library defaults.
func DefaultOptions() Options {
	return Options{
		Logger:               slog.Default(),
		AcceptOptions:        &websocket.AcceptOptions{},
		ClientSendBuffer:     defaultClientSendBuffer,
		WriteTimeout:         defaultWriteTimeout,
		ReadTimeout:          defaultReadTimeout,
		PingInterval:         libraryDefaultPingInterval,
		ServerRequestTimeout: defaultServerRequestTimeout,
	}
}

// NewWithOptions creates a new Broker using an Options struct.
// It validates the options and converts them to functional options before calling New().
// Additional functional options may be supplied and will override values from the struct.
//
// Example:
//     opts := broker.DefaultOptions()
//     opts.Logger = myLogger
//     opts.PingInterval = 15 * time.Second
//     b, err := broker.NewWithOptions(opts)
//
// Returns an error if validation fails.
func NewWithOptions(opts Options, extraOpts ...Option) (*Broker, error) {
	// Validate options
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	
	// Build functional options, only including non-zero values
	optionFns := []Option{}
	
	// Always set logger (nil is valid)
	optionFns = append(optionFns, WithLogger(opts.Logger))
	
	// Always set AcceptOptions (nil is valid)
	optionFns = append(optionFns, WithAcceptOptions(opts.AcceptOptions))
	
	// Only apply non-zero values to avoid overriding defaults
	if opts.ClientSendBuffer > 0 {
		optionFns = append(optionFns, WithClientSendBuffer(opts.ClientSendBuffer))
	}
	if opts.WriteTimeout > 0 {
		optionFns = append(optionFns, WithWriteTimeout(opts.WriteTimeout))
	}
	if opts.ReadTimeout > 0 {
		optionFns = append(optionFns, WithReadTimeout(opts.ReadTimeout))
	}
	// PingInterval: 0 means default, negative means disable, both are valid
	if opts.PingInterval != 0 {
		optionFns = append(optionFns, WithPingInterval(opts.PingInterval))
	}
	if opts.ServerRequestTimeout > 0 {
		optionFns = append(optionFns, WithServerRequestTimeout(opts.ServerRequestTimeout))
	}
	
	// Append extra options which can override struct values
	optionFns = append(optionFns, extraOpts...)
	
	return New(optionFns...)
}

// validateOptions validates the Options struct fields.
func validateOptions(opts Options) error {
	// Validate ClientSendBuffer
	if opts.ClientSendBuffer < 0 {
		return errors.New("ClientSendBuffer must be non-negative")
	}
	
	// Validate WriteTimeout
	if opts.WriteTimeout < 0 {
		return errors.New("WriteTimeout must be non-negative")
	}
	
	// Validate ReadTimeout
	if opts.ReadTimeout < 0 {
		return errors.New("ReadTimeout must be non-negative")
	}
	
	// Validate ServerRequestTimeout
	if opts.ServerRequestTimeout < 0 {
		return errors.New("ServerRequestTimeout must be non-negative")
	}
	
	// PingInterval can be any value (0 = default, negative = disabled)
	
	// Warning if ReadTimeout is not greater than PingInterval (when both are positive)
	if opts.ReadTimeout > 0 && opts.PingInterval > 0 && opts.ReadTimeout <= opts.PingInterval {
		// This is a warning, not an error - the broker will handle it
		// but it's good practice to have ReadTimeout > PingInterval
	}
	
	return nil
}
