// WebSocketMQ Client
// A lightweight client for communicating with WebSocketMQ server

// Global namespace
window.WebSocketMQ = window.WebSocketMQ || {};

// Client class
WebSocketMQ.Client = class {
  constructor(options = {}) {
    this.options = Object.assign({
      url: null,                // WebSocket URL (required)
      reconnect: true,          // Auto-reconnect on disconnection
      reconnectInterval: 1000,  // Initial reconnect interval in ms
      maxReconnectInterval: 30000, // Maximum reconnect interval in ms
      reconnectMultiplier: 1.5, // Backoff multiplier for reconnect attempts
      devMode: false,           // Enable development features
    }, options);

    if (!this.options.url) {
      throw new Error('WebSocketMQ: URL is required');
    }

    // Internal state
    this.ws = null;
    this.isConnected = false;
    this.isConnecting = false;
    this.reconnectAttempts = 0;
    this.reconnectTimer = null;
    
    // Subscription storage
    this.subscriptions = new Map();
    
    // Event callbacks
    this.onConnectCallbacks = [];
    this.onDisconnectCallbacks = [];
    this.onErrorCallbacks = [];
    
    // Bind methods to maintain 'this' context
    this._onMessage = this._onMessage.bind(this);
    this._onOpen = this._onOpen.bind(this);
    this._onClose = this._onClose.bind(this);
    this._onError = this._onError.bind(this);
    
    // Set up dev mode error reporting if enabled
    if (this.options.devMode) {
      this._setupDevMode();
    }
  }
  
  // Connect to the WebSocket server
  connect() {
    if (this.isConnected || this.isConnecting) {
      return;
    }
    
    this.isConnecting = true;
    
    try {
      this.ws = new WebSocket(this.options.url);
      this.ws.addEventListener('open', this._onOpen);
      this.ws.addEventListener('message', this._onMessage);
      this.ws.addEventListener('close', this._onClose);
      this.ws.addEventListener('error', this._onError);
    } catch (err) {
      this.isConnecting = false;
      this._handleError(err);
      this._scheduleReconnect();
    }
  }
  
  // Disconnect from the WebSocket server
  disconnect() {
    if (!this.ws) {
      return;
    }
    
    clearTimeout(this.reconnectTimer);
    this.reconnectTimer = null;
    this.options.reconnect = false; // Disable auto-reconnect
    
    if (this.isConnected || this.isConnecting) {
      try {
        this.ws.close(1000, 'Client disconnected');
      } catch (err) {
        // Ignore close errors
      }
    }
  }
  
  // Publish a message to a topic
  publish(topic, body) {
    if (!this.isConnected) {
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
    return true;
  }
  
  // Subscribe to a topic
  subscribe(topic, handler) {
    if (!handler || typeof handler !== 'function') {
      throw new Error('WebSocketMQ: Handler must be a function');
    }
    
    // Get or create subscription array for this topic
    if (!this.subscriptions.has(topic)) {
      this.subscriptions.set(topic, []);
      
      // If we're already connected, send a subscribe message
      if (this.isConnected) {
        const message = {
          header: {
            messageID: this._generateID(),
            type: 'subscribe',
            topic: 'subscribe',
            timestamp: Date.now()
          },
          body: topic
        };
        
        this._sendMessage(message);
      }
    }
    
    // Add the handler to the subscription list
    const handlers = this.subscriptions.get(topic);
    handlers.push(handler);
    
    // Return an unsubscribe function
    return () => {
      const index = handlers.indexOf(handler);
      if (index !== -1) {
        handlers.splice(index, 1);
        
        // If this was the last handler, send an unsubscribe message
        if (handlers.length === 0) {
          this.subscriptions.delete(topic);
          
          if (this.isConnected) {
            const message = {
              header: {
                messageID: this._generateID(),
                type: 'unsubscribe',
                topic: 'unsubscribe',
                timestamp: Date.now()
              },
              body: topic
            };
            
            this._sendMessage(message);
          }
        }
      }
    };
  }
  
  // Make a request and wait for a response
  request(topic, body, timeoutMs = 5000) {
    if (!this.isConnected) {
      return Promise.reject(new Error('WebSocketMQ: Not connected'));
    }
    
    return new Promise((resolve, reject) => {
      // Generate a unique correlation ID for this request
      const correlationID = this._generateID();
      
      // Create a timeout for the request
      const timeoutId = setTimeout(() => {
        // Clean up subscription
        const handlers = this.subscriptions.get(correlationID) || [];
        const index = handlers.indexOf(onResponse);
        if (index !== -1) {
          handlers.splice(index, 1);
        }
        
        if (handlers.length === 0) {
          this.subscriptions.delete(correlationID);
        }
        
        reject(new Error('WebSocketMQ: Request timed out'));
      }, timeoutMs);
      
      // Handler for the response
      const onResponse = (body, message) => {
        clearTimeout(timeoutId);
        
        // Clean up subscription
        const handlers = this.subscriptions.get(correlationID) || [];
        const index = handlers.indexOf(onResponse);
        if (index !== -1) {
          handlers.splice(index, 1);
        }
        
        if (handlers.length === 0) {
          this.subscriptions.delete(correlationID);
        }
        
        // Handle error response
        if (message.header.type === 'error') {
          reject(new Error(body.error || 'WebSocketMQ: Request failed'));
          return;
        }
        
        // Resolve with the response body
        resolve(body);
      };
      
      // Subscribe to the correlation ID topic
      if (!this.subscriptions.has(correlationID)) {
        this.subscriptions.set(correlationID, []);
      }
      
      const handlers = this.subscriptions.get(correlationID);
      handlers.push(onResponse);
      
      // Send the request message
      const message = {
        header: {
          messageID: this._generateID(),
          correlationID: correlationID,
          type: 'request',
          topic: topic,
          timestamp: Date.now(),
          ttl: timeoutMs
        },
        body: body
      };
      
      this._sendMessage(message);
    });
  }
  
  // Add a connection event handler
  onConnect(callback) {
    if (typeof callback === 'function') {
      this.onConnectCallbacks.push(callback);
      
      // If already connected, call immediately
      if (this.isConnected) {
        try {
          callback();
        } catch (err) {
          console.error('WebSocketMQ: Error in connect callback', err);
        }
      }
    }
  }
  
  // Add a disconnection event handler
  onDisconnect(callback) {
    if (typeof callback === 'function') {
      this.onDisconnectCallbacks.push(callback);
    }
  }
  
  // Add an error event handler
  onError(callback) {
    if (typeof callback === 'function') {
      this.onErrorCallbacks.push(callback);
    }
  }
  
  // Private: Handle WebSocket open event
  _onOpen(event) {
    this.isConnected = true;
    this.isConnecting = false;
    this.reconnectAttempts = 0;
    
    // Send subscribe messages for all current subscriptions
    for (const [topic, handlers] of this.subscriptions.entries()) {
      // Skip response topics (correlation IDs) which should not be auto-subscribed
      if (handlers.length > 0 && !topic.match(/^[0-9a-f]{8}-/)) {
        const message = {
          header: {
            messageID: this._generateID(),
            type: 'subscribe',
            topic: 'subscribe',
            timestamp: Date.now()
          },
          body: topic
        };
        
        this._sendMessage(message);
      }
    }
    
    // Call connect callbacks
    for (const callback of this.onConnectCallbacks) {
      try {
        callback(event);
      } catch (err) {
        console.error('WebSocketMQ: Error in connect callback', err);
      }
    }
  }
  
  // Private: Handle WebSocket close event
  _onClose(event) {
    const wasConnected = this.isConnected;
    this.isConnected = false;
    this.isConnecting = false;
    
    // Call disconnect callbacks if we were previously connected
    if (wasConnected) {
      for (const callback of this.onDisconnectCallbacks) {
        try {
          callback(event);
        } catch (err) {
          console.error('WebSocketMQ: Error in disconnect callback', err);
        }
      }
    }
    
    // Schedule reconnect if enabled
    if (this.options.reconnect) {
      this._scheduleReconnect();
    }
  }
  
  // Private: Handle WebSocket error event
  _onError(event) {
    this._handleError(event);
  }
  
  // Private: Handle WebSocket message event
  _onMessage(event) {
    try {
      const message = JSON.parse(event.data);
      
      // Get the topic from either the topic or correlation ID
      const topic = message.header.topic || message.header.correlationID;
      
      // Find and call handlers for this topic
      const handlers = this.subscriptions.get(topic) || [];
      for (const handler of handlers) {
        try {
          // If this is a request, call the handler and send back a response
          if (message.header.type === 'request') {
            console.log(`WebSocketMQ: Handling request for topic ${topic}`, message);
            
            // Call the handler and get the response
            const result = handler(message.body, message);
            
            // If the result is a promise, handle it asynchronously
            if (result && typeof result.then === 'function') {
              result.then(responseBody => {
                if (responseBody !== undefined) {
                  // Create a response message
                  const response = {
                    header: {
                      messageID: this._generateID(),
                      correlationID: message.header.correlationID,
                      type: 'response',
                      topic: message.header.correlationID,
                      timestamp: Date.now()
                    },
                    body: responseBody
                  };
                  
                  console.log(`WebSocketMQ: Sending async response for ${topic}`, response);
                  this._sendMessage(response);
                }
              }).catch(err => {
                console.error(`WebSocketMQ: Error in async handler for ${topic}`, err);
                
                // Send an error response
                const errorResponse = {
                  header: {
                    messageID: this._generateID(),
                    correlationID: message.header.correlationID,
                    type: 'error',
                    topic: message.header.correlationID,
                    timestamp: Date.now()
                  },
                  body: { error: err.message || 'Unknown error' }
                };
                
                this._sendMessage(errorResponse);
              });
            } else if (result !== undefined) {
              // Create a response message for synchronous result
              const response = {
                header: {
                  messageID: this._generateID(),
                  correlationID: message.header.correlationID,
                  type: 'response',
                  topic: message.header.correlationID,
                  timestamp: Date.now()
                },
                body: result
              };
              
              console.log(`WebSocketMQ: Sending sync response for ${topic}`, response);
              this._sendMessage(response);
            }
          } else {
            // For non-request messages, just call the handler
            handler(message.body, message);
          }
        } catch (err) {
          console.error(`WebSocketMQ: Error in message handler for topic ${topic}`, err);
          
          // If this was a request and the handler threw an error, send an error response
          if (message.header.type === 'request' && message.header.correlationID) {
            const errorResponse = {
              header: {
                messageID: this._generateID(),
                correlationID: message.header.correlationID,
                type: 'error',
                topic: message.header.correlationID,
                timestamp: Date.now()
              },
              body: { error: err.message || 'Unknown error' }
            };
            
            this._sendMessage(errorResponse);
          }
        }
      }
    } catch (err) {
      this._handleError(new Error(`WebSocketMQ: Failed to parse message: ${err.message}`));
    }
  }
  
  // Private: Send a message over the WebSocket
  _sendMessage(message) {
    if (!this.isConnected) {
      throw new Error('WebSocketMQ: Not connected');
    }
    
    try {
      const json = JSON.stringify(message);
      this.ws.send(json);
      return true;
    } catch (err) {
      this._handleError(err);
      return false;
    }
  }
  
  // Private: Handle an error by logging and notifying error callbacks
  _handleError(error) {
    // Call error callbacks
    for (const callback of this.onErrorCallbacks) {
      try {
        callback(error);
      } catch (err) {
        console.error('WebSocketMQ: Error in error callback', err);
      }
    }
  }
  
  // Private: Schedule a reconnection attempt
  _scheduleReconnect() {
    if (!this.options.reconnect || this.reconnectTimer) {
      return;
    }
    
    // Calculate backoff interval
    const interval = Math.min(
      this.options.reconnectInterval * Math.pow(this.options.reconnectMultiplier, this.reconnectAttempts),
      this.options.maxReconnectInterval
    );
    
    this.reconnectAttempts++;
    
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, interval);
  }
  
  // Private: Generate a unique ID
  _generateID() {
    // Simple ID generation - in a real implementation you might want a UUID library
    return Date.now().toString(36) + Math.random().toString(36).substring(2);
  }
  
  // Private: Set up development mode features
  _setupDevMode() {
    // Subscribe to hot reload topic
    this.onConnect(() => {
      this.subscribe('_dev.hotreload', () => {
        console.log('WebSocketMQ: Hot reload triggered, refreshing page...');
        window.location.reload();
      });
      
      // Log a message that dev mode is active
      console.log('WebSocketMQ: Development mode active - JS error reporting enabled');
    });
    
    // Report JavaScript errors back to the server
    window.addEventListener('error', (event) => {
      if (!this.isConnected) {
        console.warn('WebSocketMQ: Cannot report error - not connected');
        return;
      }
      
      console.log('WebSocketMQ: Caught JS error, reporting to server');
      
      const errorInfo = {
        message: event.message,
        source: event.filename,
        lineno: event.lineno,
        colno: event.colno,
        stack: event.error ? event.error.stack : null,
        timestamp: new Date().toISOString()
      };
      
      try {
        this.publish('_dev.js-error', errorInfo);
        console.log('WebSocketMQ: Error reported to server');
      } catch (err) {
        console.error('WebSocketMQ: Failed to report error to server:', err);
      }
    });
    
    // Report unhandled promise rejections
    window.addEventListener('unhandledrejection', (event) => {
      if (!this.isConnected) {
        console.warn('WebSocketMQ: Cannot report rejection - not connected');
        return;
      }
      
      console.log('WebSocketMQ: Caught unhandled rejection, reporting to server');
      
      const errorInfo = {
        message: event.reason ? (event.reason.message || String(event.reason)) : 'Unhandled Promise Rejection',
        stack: event.reason && event.reason.stack ? event.reason.stack : null,
        timestamp: new Date().toISOString()
      };
      
      try {
        this.publish('_dev.js-error', errorInfo);
        console.log('WebSocketMQ: Rejection reported to server');
      } catch (err) {
        console.error('WebSocketMQ: Failed to report rejection to server:', err);
      }
    });
  }
};