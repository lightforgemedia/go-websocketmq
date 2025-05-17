package client

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// Options contains configuration values for ConnectWithOptions.
type Options struct {
	Logger                *slog.Logger
	DialOptions           *websocket.DialOptions
	DefaultRequestTimeout time.Duration
	WriteTimeout          time.Duration
	ReadTimeout           time.Duration
	PingInterval          time.Duration
	AutoReconnect         bool
	ReconnectAttempts     int
	ReconnectDelayMin     time.Duration
	ReconnectDelayMax     time.Duration
	ClientName            string
	ClientType            string
	ClientURL             string
}

// DefaultOptions returns a Options struct populated with library defaults.
func DefaultOptions() Options {
	return Options{
		Logger:                slog.Default(),
		DialOptions:           &websocket.DialOptions{HTTPClient: http.DefaultClient},
		DefaultRequestTimeout: defaultClientReqTimeout,
		WriteTimeout:          defaultWriteClientTimeout,
		ReadTimeout:           defaultReadClientTimeout,
		PingInterval:          libraryDefaultClientPingInterval,
		AutoReconnect:         false,
		ReconnectAttempts:     defaultReconnectAttempts,
		ReconnectDelayMin:     defaultReconnectDelayMin,
		ReconnectDelayMax:     defaultReconnectDelayMax,
	}
}

// ConnectWithOptions establishes a connection using an Options struct.
// Additional Option functions may be provided and override struct values.
func ConnectWithOptions(urlStr string, optsStruct Options, extraOpts ...Option) (*Client, error) {
	optionFns := []Option{
		WithLogger(optsStruct.Logger),
		WithDialOptions(optsStruct.DialOptions),
		WithDefaultRequestTimeout(optsStruct.DefaultRequestTimeout),
		WithWriteTimeout(optsStruct.WriteTimeout),
		WithReadTimeout(optsStruct.ReadTimeout),
		WithClientPingInterval(optsStruct.PingInterval),
	}
	if optsStruct.AutoReconnect {
		optionFns = append(optionFns, WithAutoReconnect(optsStruct.ReconnectAttempts, optsStruct.ReconnectDelayMin, optsStruct.ReconnectDelayMax))
	}
	if optsStruct.ClientName != "" {
		optionFns = append(optionFns, WithClientName(optsStruct.ClientName))
	}
	if optsStruct.ClientType != "" {
		optionFns = append(optionFns, WithClientType(optsStruct.ClientType))
	}
	if optsStruct.ClientURL != "" {
		optionFns = append(optionFns, WithClientURL(optsStruct.ClientURL))
	}

	optionFns = append(optionFns, extraOpts...)
	return Connect(urlStr, optionFns...)
}
