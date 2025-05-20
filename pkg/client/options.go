package client

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/lightforgemedia/go-websocketmq/pkg/ergosockets"
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
// This function directly initializes a Client without converting to functional options.
func ConnectWithOptions(urlStr string, opts Options) (*Client, error) {
	clientCtx, clientCancel := context.WithCancel(context.Background())
	
	// Initialize the client with options from the struct
	cli := &Client{
		config: clientConfig{
			logger:                opts.Logger,
			dialOptions:           opts.DialOptions,
			defaultRequestTimeout: opts.DefaultRequestTimeout,
			writeTimeout:          opts.WriteTimeout,
			readTimeout:           opts.ReadTimeout,
			pingInterval:          opts.PingInterval,
			autoReconnect:         opts.AutoReconnect,
			reconnectAttempts:     opts.ReconnectAttempts,
			reconnectDelayMin:     opts.ReconnectDelayMin,
			reconnectDelayMax:     opts.ReconnectDelayMax,
			clientName:            opts.ClientName,
			clientType:            opts.ClientType,
			clientURL:             opts.ClientURL,
		},
		urlStr:               urlStr,
		id:                   ergosockets.GenerateID(),
		clientCtx:            clientCtx,
		clientCancel:         clientCancel,
		send:                 make(chan *ergosockets.Envelope, defaultClientSendBuffer),
		pendingRequests:      make(map[string]chan *ergosockets.Envelope),
		subscriptionHandlers: make(map[string]*ergosockets.HandlerWrapper),
		requestHandlers:      make(map[string]*ergosockets.HandlerWrapper),
	}
	
	// Apply defaults for zero values
	if cli.config.logger == nil {
		cli.config.logger = slog.Default()
	}
	if cli.config.dialOptions == nil {
		cli.config.dialOptions = &websocket.DialOptions{HTTPClient: http.DefaultClient}
	}
	if cli.config.defaultRequestTimeout <= 0 {
		cli.config.defaultRequestTimeout = defaultClientReqTimeout
	}
	if cli.config.writeTimeout <= 0 {
		cli.config.writeTimeout = defaultWriteClientTimeout
	}
	if cli.config.readTimeout <= 0 {
		cli.config.readTimeout = defaultReadClientTimeout
	}
	
	// Handle ping interval - special case
	if cli.config.pingInterval < 0 {
		cli.config.pingInterval = 0 // Disable ping
	}
	
	// Handle reconnect delays
	if cli.config.reconnectDelayMin <= 0 {
		cli.config.reconnectDelayMin = defaultReconnectDelayMin
	}
	if cli.config.reconnectDelayMax <= 0 {
		cli.config.reconnectDelayMax = defaultReconnectDelayMax
	}
	if cli.config.reconnectDelayMax < cli.config.reconnectDelayMin {
		cli.config.reconnectDelayMax = cli.config.reconnectDelayMin
	}
	
	// Establish initial connection
	err := cli.establishConnection(cli.clientCtx)
	if err != nil {
		cli.config.logger.Info(fmt.Sprintf("Client %s: Initial connection failed: %v", cli.id, err))
		if !cli.config.autoReconnect {
			cli.Close() // Clean up if not reconnecting
			return nil, fmt.Errorf("client initial connection failed and auto-reconnect disabled: %w", err)
		}
		// Auto-reconnect is enabled, start the loop
		go cli.reconnectLoop()
	}
	
	return cli, nil
}
