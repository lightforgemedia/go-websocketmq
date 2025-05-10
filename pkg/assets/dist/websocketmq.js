// assets/dist/websocketmq.js
// WebSocketMQ Client v2 (RPC Enhancements)

window.WebSocketMQ = window.WebSocketMQ || {};

WebSocketMQ.Client = class {
  constructor(options = {}) {
    this.options = Object.assign({
      url: null,
      reconnect: true,
      reconnectInterval: 1000,
      maxReconnectInterval: 30000,
      reconnectMultiplier: 1.5,
      devMode: false,
      logger: console, // Allow custom logger
      // New option for registration
      pageSessionID: null, // User-defined session ID for this client instance
      clientRegisterTopic: "_client.register", // Topic to publish registration
    }, options);

    if (!this.options.url) {
      throw new Error('WebSocketMQ: URL is required');
    }
    this.logger = this.options.logger;

    this.ws = null;
    this.isConnected = false;
    this.isConnecting = false;
    this.reconnectAttempts = 0;
    this.reconnectTimer = null;
    this.explicitlyClosed = false;
    
    this.subscriptions = new Map(); // topic -> Set of handlers
    this.onConnectCallbacks = new Set();
    this.onDisconnectCallbacks = new Set();
    this.onErrorCallbacks = new Set();
    
    this._onMessage = this._onMessage.bind(this);
    this._onOpen = this._onOpen.bind(this);
    this._onClose = this._onClose.bind(this);
    this._onError = this._onError.bind(this);
    
    if (this.options.devMode) {
      this._setupDevMode();
    }
  }
  
  connect() {
    if (this.isConnected || this.isConnecting) {
      this.logger.debug('WebSocketMQ: Already connected or connecting.');
      return;
    }
    
    this.isConnecting = true;
    this.explicitlyClosed = false;
    this.logger.debug('WebSocketMQ: Connecting to', this.options.url);
    
    try {
      this.ws = new WebSocket(this.options.url);
      this.ws.addEventListener('open', this._onOpen);
      this.ws.addEventListener('message', this._onMessage);
      this.ws.addEventListener('close', this._onClose);
      this.ws.addEventListener('error', this._onError);
    } catch (err) {
      this.isConnecting = false;
      this._handleError(err);
      if (!this.explicitlyClosed) this._scheduleReconnect();
    }
  }
  
  disconnect() {
    this.logger.debug('WebSocketMQ: Disconnect called.');
    this.explicitlyClosed = true;
    clearTimeout(this.reconnectTimer);
    this.reconnectTimer = null;
    
    if (this.ws) {
      try {
        this.ws.close(1000, 'Client disconnected');
      } catch (err) { /* Ignore */ }
    }
    // _onClose will handle state changes and callbacks
  }
  
  _registerClientSession() {
    if (this.options.pageSessionID && this.options.clientRegisterTopic) {
      this.logger.info(`WebSocketMQ: Registering PageSessionID: ${this.options.pageSessionID}`);
      try {
        this.publish(this.options.clientRegisterTopic, { pageSessionID: this.options.pageSessionID });
      } catch (err) {
        this.logger.error('WebSocketMQ: Failed to send registration message:', err);
      }
    }
  }

  publish(topic, body) {
    if (!this.isConnected) {
      this.logger.warn('WebSocketMQ: Not connected. Cannot publish.');
      throw new Error('WebSocketMQ: Not connected');
    }
    
    const message = {
      header: {
        messageID: this._generateID(),
        type: 'event',
        topic: topic,
        timestamp: Date.now()
      },
      body: body
    };
    this._sendMessage(message);
    this.logger.debug('WebSocketMQ: Published to', topic, body);
    return true;
  }
  
  // Subscribe to an action name (for server-initiated RPC) or a general topic
  subscribe(topic, handler) {
    if (!handler || typeof handler !== 'function') {
      throw new Error('WebSocketMQ: Handler must be a function');
    }
    
    if (!this.subscriptions.has(topic)) {
      this.subscriptions.set(topic, new Set());
    }
    this.subscriptions.get(topic).add(handler);
    this.logger.debug('WebSocketMQ: Subscribed to topic:', topic);

    // For general pub/sub (not RPC action names), client might need to inform server.
    // For RPC action names, server initiates, so client doesn't need to send "subscribe" message.
    // We are simplifying: client manages its subscriptions locally for RPC.
    // If general pub/sub from server to client groups is needed, client would send a "subscribe" type message.

    return () => {
      if (this.subscriptions.has(topic)) {
        const handlers = this.subscriptions.get(topic);
        handlers.delete(handler);
        if (handlers.size === 0) {
          this.subscriptions.delete(topic);
          this.logger.debug('WebSocketMQ: Unsubscribed from topic (last handler removed):', topic);
          // Optionally send "unsubscribe" message to server if it was for general pub/sub
        }
      }
    };
  }
  
  // Client-initiated request to a server-side handler
  request(topic, body, timeoutMs = 5000) {
    if (!this.isConnected) {
      this.logger.warn('WebSocketMQ: Not connected. Cannot send request.');
      return Promise.reject(new Error('WebSocketMQ: Not connected'));
    }
    
    return new Promise((resolve, reject) => {
      const correlationID = this._generateID();
      let timeoutId = null;

      const responseHandler = (responseBody, responseMessage) => {
        clearTimeout(timeoutId);
        // Clean up this specific one-time subscription
        if (this.subscriptions.has(correlationID)) {
            const handlers = this.subscriptions.get(correlationID);
            handlers.delete(responseHandler);
            if (handlers.size === 0) this.subscriptions.delete(correlationID);
        }

        if (responseMessage.header.type === 'error') {
          this.logger.warn('WebSocketMQ: Request to', topic, 'failed with error:', responseBody);
          reject(responseBody.error ? new Error(responseBody.error.message || responseBody.error) : new Error('Request failed'));
        } else {
          this.logger.debug('WebSocketMQ: Response for', topic, '(CorrID', correlationID, '):', responseBody);
          resolve(responseBody);
        }
      };
      
      // Subscribe to the correlationID for the response
      if (!this.subscriptions.has(correlationID)) {
        this.subscriptions.set(correlationID, new Set());
      }
      this.subscriptions.get(correlationID).add(responseHandler);
      
      timeoutId = setTimeout(() => {
        if (this.subscriptions.has(correlationID)) {
            const handlers = this.subscriptions.get(correlationID);
            handlers.delete(responseHandler);
            if (handlers.size === 0) this.subscriptions.delete(correlationID);
        }
        this.logger.warn('WebSocketMQ: Request to', topic, 'timed out for CorrID', correlationID);
        reject(new Error('WebSocketMQ: Request timed out'));
      }, timeoutMs);
      
      const message = {
        header: {
          messageID: this._generateID(),
          correlationID: correlationID,
          type: 'request',
          topic: topic, // Topic the server handler is listening on
          timestamp: Date.now(),
          ttl: timeoutMs
        },
        body: body
      };
      
      this._sendMessage(message);
      this.logger.debug('WebSocketMQ: Sent request to', topic, '(CorrID', correlationID, '):', body);
    });
  }
  
  onConnect(callback) { this.onConnectCallbacks.add(callback); if (this.isConnected) callback(); }
  onDisconnect(callback) { this.onDisconnectCallbacks.add(callback); }
  onError(callback) { this.onErrorCallbacks.add(callback); }
  
  _onOpen(event) {
    this.isConnected = true;
    this.isConnecting = false;
    this.reconnectAttempts = 0;
    this.logger.info('WebSocketMQ: Connected.');
    
    this._registerClientSession(); // Attempt to register if pageSessionID is set

    this.onConnectCallbacks.forEach(cb => { try { cb(event); } catch(e) { this.logger.error("Error in onConnect callback", e)} });
  }
  
  _onClose(event) {
    const wasConnected = this.isConnected;
    this.isConnected = false;
    this.isConnecting = false;
    this.logger.info('WebSocketMQ: Disconnected. Code:', event.code, 'Reason:', event.reason, 'WasClean:', event.wasClean);
    
    if (wasConnected) { // Only call disconnect if it was previously connected
        this.onDisconnectCallbacks.forEach(cb => { try { cb(event); } catch(e) { this.logger.error("Error in onDisconnect callback", e)} });
    }
    
    if (!this.explicitlyClosed && this.options.reconnect) {
      this._scheduleReconnect();
    }
  }
  
  _onError(eventOrError) {
    // WebSocket 'error' events are simple events, not Error objects.
    // Actual Error objects might come from other parts of the code.
    const error = eventOrError instanceof Error ? eventOrError : new Error('WebSocket error');
    this.logger.error('WebSocketMQ: Error:', error, 'Raw event:', eventOrError);
    this.onErrorCallbacks.forEach(cb => { try { cb(error, eventOrError); } catch(e) { this.logger.error("Error in onError callback", e)} });
    // Note: A WebSocket 'error' event is typically followed by a 'close' event.
    // Reconnection logic is handled in _onClose.
  }
  
  _onMessage(event) {
    let message;
    try {
      message = JSON.parse(event.data);
      this.logger.debug('WebSocketMQ: Received message:', message);
    } catch (err) {
      this._handleError(new Error(`WebSocketMQ: Failed to parse message: ${err.message}. Data: ${event.data}`));
      return;
    }

    const topic = message.header.topic; // For events, requests, or responses (where topic=correlationID)
    const handlers = this.subscriptions.get(topic);

    if (handlers && handlers.size > 0) {
      handlers.forEach(async (handler) => {
        try {
          if (message.header.type === 'request') {
            this.logger.debug('WebSocketMQ: Handling server-initiated request for action:', topic);
            const result = await handler(message.body, message); // Handler for server-initiated RPC
            
            if (result !== undefined && message.header.correlationID) { // Check undefined explicitly
              const response = {
                header: {
                  messageID: this._generateID(),
                  correlationID: message.header.correlationID,
                  type: 'response',
                  topic: message.header.correlationID, // Response topic is the original request's CorrID
                  timestamp: Date.now()
                },
                body: result
              };
              this._sendMessage(response);
              this.logger.debug('WebSocketMQ: Sent response for server-initiated request on action', topic, 'Result:', result);
            }
          } else {
            // Handler for general events or client-initiated request responses
             handler(message.body, message);
          }
        } catch (err) {
          this.logger.error(`WebSocketMQ: Error in message handler for topic ${topic}:`, err);
          if (message.header.type === 'request' && message.header.correlationID) {
            const errorResponse = {
              header: {
                messageID: this._generateID(),
                correlationID: message.header.correlationID,
                type: 'error',
                topic: message.header.correlationID,
                timestamp: Date.now()
              },
              body: { error: { message: err.message || 'Handler error' } }
            };
            this._sendMessage(errorResponse);
          }
        }
      });
    } else {
        this.logger.debug('WebSocketMQ: No handlers for topic:', topic, 'Message type:', message.header.type);
    }
  }
  
  _sendMessage(message) {
    if (!this.isConnected || !this.ws || this.ws.readyState !== WebSocket.OPEN) {
      this.logger.warn('WebSocketMQ: Not connected or WebSocket not open. Cannot send message.');
      // Do not throw here, as it might be a normal race condition during disconnect.
      return false;
    }
    try {
      this.ws.send(JSON.stringify(message));
      return true;
    } catch (err) {
      this._handleError(err); // This will call onError callbacks
      return false;
    }
  }
  
  _handleError(error) {
    this.logger.error('WebSocketMQ: Handling error:', error);
    this.onErrorCallbacks.forEach(cb => { try { cb(error); } catch(e) { this.logger.error("Error in _handleError's onError callback", e)} });
  }
  
  _scheduleReconnect() {
    if (this.explicitlyClosed || !this.options.reconnect || this.reconnectTimer) {
      return;
    }
    
    this.reconnectAttempts++;
    const interval = Math.min(
      this.options.reconnectInterval * Math.pow(this.options.reconnectMultiplier, this.reconnectAttempts -1),
      this.options.maxReconnectInterval
    );
    
    this.logger.info(`WebSocketMQ: Scheduling reconnect attempt ${this.reconnectAttempts} in ${interval}ms.`);
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      if (!this.explicitlyClosed) this.connect();
    }, interval);
  }
  
  _generateID() {
    return Date.now().toString(36) + Math.random().toString(36).substring(2, 10);
  }
  
  _setupDevMode() {
    this.onConnect(() => {
      this.subscribe('_dev.hotreload', () => {
        this.logger.info('WebSocketMQ: Hot reload triggered, refreshing page...');
        window.location.reload();
      });
      this.logger.info('WebSocketMQ: Development mode active - JS error reporting enabled.');
    });
    
    const reportError = (errorData) => {
      if (!this.isConnected) {
        this.logger.warn('WebSocketMQ: Cannot report JS error - not connected', errorData);
        return;
      }
      this.logger.debug('WebSocketMQ: Reporting JS error to server:', errorData);
      try {
        this.publish('_dev.js-error', errorData);
      } catch (err) {
        this.logger.error('WebSocketMQ: Failed to report JS error to server:', err);
      }
    };

    window.addEventListener('error', (event) => {
      reportError({
        message: event.message,
        source: event.filename,
        lineno: event.lineno,
        colno: event.colno,
        stack: event.error ? event.error.stack : null,
        timestamp: new Date().toISOString(),
        type: "window.onerror"
      });
    });
    
    window.addEventListener('unhandledrejection', (event) => {
      reportError({
        message: event.reason ? (event.reason.message || String(event.reason)) : 'Unhandled Promise Rejection',
        stack: event.reason && event.reason.stack ? event.reason.stack : null,
        timestamp: new Date().toISOString(),
        type: "unhandledrejection"
      });
    });
  }
};