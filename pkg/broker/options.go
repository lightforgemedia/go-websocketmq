package broker

import (
	"log/slog"
	"time"

	"github.com/coder/websocket"
)

// Options contains configuration values for creating a Broker using NewWithOptions.
type Options struct {
	Logger               *slog.Logger
	AcceptOptions        *websocket.AcceptOptions
	ClientSendBuffer     int
	WriteTimeout         time.Duration
	ReadTimeout          time.Duration
	PingInterval         time.Duration
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
// Additional functional options may be supplied and will override values from the struct.
func NewWithOptions(opts Options, extraOpts ...Option) (*Broker, error) {
	optionFns := []Option{
		WithLogger(opts.Logger),
		WithAcceptOptions(opts.AcceptOptions),
		WithClientSendBuffer(opts.ClientSendBuffer),
		WithWriteTimeout(opts.WriteTimeout),
		WithReadTimeout(opts.ReadTimeout),
		WithPingInterval(opts.PingInterval),
		WithServerRequestTimeout(opts.ServerRequestTimeout),
	}
	optionFns = append(optionFns, extraOpts...)
	return New(optionFns...)
}
