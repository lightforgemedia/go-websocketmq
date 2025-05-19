package broker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/ergosockets"
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
// It validates the options and creates the broker with the specified configuration.
//
// Example:
//     opts := broker.DefaultOptions()
//     opts.Logger = myLogger
//     opts.PingInterval = 15 * time.Second
//     b, err := broker.NewWithOptions(opts)
//
// Returns an error if validation fails.
func NewWithOptions(opts Options) (*Broker, error) {
	// Validate options
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	
	// Create broker with the specified configuration
	mainCtx, mainCancel := context.WithCancel(context.Background())
	b := &Broker{
		config: brokerConfig{
			logger:               opts.Logger,
			clientSendBuffer:     opts.ClientSendBuffer,
			writeTimeout:         opts.WriteTimeout,
			readTimeout:          opts.ReadTimeout,
			pingInterval:         opts.PingInterval,
			serverRequestTimeout: opts.ServerRequestTimeout,
			acceptOptions:        opts.AcceptOptions,
		},
		managedClients:     make(map[string]*managedClient),
		sessionIndex:       make(map[string]*managedClient),
		requestHandlers:    make(map[string]*ergosockets.HandlerWrapper),
		publishSubscribers: make(map[string]map[*managedClient]struct{}),
		shutdownChan:       make(chan struct{}),
		mainCtx:            mainCtx,
		mainCancel:         mainCancel,
	}
	
	// Apply defaults for zero values
	if b.config.logger == nil {
		b.config.logger = slog.Default()
	}
	if b.config.clientSendBuffer == 0 {
		b.config.clientSendBuffer = defaultClientSendBuffer
	}
	if b.config.writeTimeout == 0 {
		b.config.writeTimeout = defaultWriteTimeout
	}
	if b.config.readTimeout == 0 {
		b.config.readTimeout = defaultReadTimeout
	}
	if b.config.serverRequestTimeout == 0 {
		b.config.serverRequestTimeout = defaultServerRequestTimeout
	}
	
	// Handle ping interval logic
	if b.config.pingInterval == 0 {
		b.config.pingInterval = libraryDefaultPingInterval
	} else if b.config.pingInterval < 0 {
		b.config.pingInterval = 0 // Disable ping
	}
	
	if b.config.acceptOptions == nil {
		b.config.acceptOptions = &websocket.AcceptOptions{}
	}
	
	// Add default handlers (registration, proxy, list clients)
	b.setupDefaultHandlers()
	
	b.config.logger.Info(fmt.Sprintf("Broker: Initialized. Ping interval: %v, Client send buffer: %d", b.config.pingInterval, b.config.clientSendBuffer))
	return b, nil
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
