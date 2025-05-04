/**
 * WebSocketMQ Client
 *
 * A lightweight client for WebSocketMQ, providing real-time messaging
 * with publish/subscribe and request/response patterns.
 */

// Wrap in a UMD pattern for universal usage (browser, CommonJS, AMD)
(function(root, factory) {
  if (typeof define === 'function' && define.amd) {
    // AMD. Register as an anonymous module
    define([], factory);
  } else if (typeof module === 'object' && module.exports) {
    // Node. Does not work with strict CommonJS, but
    // only CommonJS-like environments that support module.exports
    module.exports = factory();
  } else {
    // Browser globals (root is window)
    root.WebSocketMQ = factory();
  }
}(typeof self !== 'undefined' ? self : this, function() {

  /**
   * Client for WebSocketMQ
   */
  class Client {
    /**
     * Create a new WebSocketMQ client
     *
     * @param {Object} options - Configuration options
     * @param {string} options.url - WebSocket URL to connect to
     * @param {boolean} [options.reconnect=true] - Whether to automatically reconnect
     * @param {number} [options.reconnectInterval=1000] - Initial reconnect interval in ms
     * @param {number} [options.maxReconnectInterval=30000] - Maximum reconnect interval in ms
     * @param {number} [options.reconnectMultiplier=1.5] - Backoff multiplier for reconnect attempts
     * @param {boolean} [options.devMode=false] - Enable development mode features
     */
    constructor(options) {
      this.url = options.url;
      this.reconnect = options.reconnect !== false; // Default to true
      this.reconnectInterval = options.reconnectInterval || 1000;
      this.maxReconnectInterval = options.maxReconnectInterval || 30000;
      this.reconnectMultiplier = options.reconnectMultiplier || 1.5;
      this.devMode = options.devMode || false;

      this.ws = null;
      this.subs = new Map();
      this.connectCallbacks = [];
      this.disconnectCallbacks = [];
      this.errorCallbacks = [];
      this.currentReconnectInterval = this.reconnectInterval;
      this.reconnectTimer = null;
      this.isConnecting = false;
      this.isConnected = false;

      // Setup dev mode features
      if (this.devMode) {
        this._setupDevMode();
      }
    }

    /**
     * Connect to the WebSocket server
     */
    connect() {
      if (this.isConnecting || this.isConnected) return;

      this.isConnecting = true;
      this.ws = new WebSocket(this.url);

      this.ws.onopen = (event) => {
        this.isConnecting = false;
        this.isConnected = true;
        this.currentReconnectInterval = this.reconnectInterval; // Reset reconnect interval
        this._notifyCallbacks(this.connectCallbacks, event);
      };

      this.ws.onclose = (event) => {
        this.isConnecting = false;
        this.isConnected = false;
        this._notifyCallbacks(this.disconnectCallbacks, event);

        // Attempt to reconnect if enabled
        if (this.reconnect && !event.wasClean) {
          this._scheduleReconnect();
        }
      };

      this.ws.onerror = (event) => {
        this._notifyCallbacks(this.errorCallbacks, event);
      };

      this.ws.onmessage = (event) => {
        try {
          const msg = JSON.parse(event.data);
          const topic = msg.header.topic || msg.header.correlationID;

          if (this.subs.has(topic)) {
            const handlers = this.subs.get(topic);
            for (const handler of handlers) {
              // Call the handler with the message body and full message
              const result = handler(msg.body, msg);

              // If this is a request and the handler returned a value or Promise,
              // send a response back
              if (msg.header.type === 'request' && msg.header.correlationID && result !== undefined) {
                Promise.resolve(result).then(responseBody => {
                  this._sendResponse(msg.header.correlationID, responseBody);
                }).catch(err => {
                  console.error('Error in request handler:', err);
                });
              }
            }
          }
        } catch (err) {
          console.error('Error processing message:', err);
        }
      };
    }

    /**
     * Disconnect from the WebSocket server
     */
    disconnect() {
      if (this.ws) {
        this.reconnect = false; // Disable reconnection
        this.ws.close();
        this.ws = null;
      }

      if (this.reconnectTimer) {
        clearTimeout(this.reconnectTimer);
        this.reconnectTimer = null;
      }
    }

    /**
     * Subscribe to a topic
     *
     * @param {string} topic - Topic to subscribe to
     * @param {Function} handler - Function to call when a message is received
     * @returns {Function} Unsubscribe function
     */
    subscribe(topic, handler) {
      if (!this.subs.has(topic)) {
        this.subs.set(topic, []);
      }

      const handlers = this.subs.get(topic);
      handlers.push(handler);

      // Return unsubscribe function
      return () => {
        const index = handlers.indexOf(handler);
        if (index !== -1) {
          handlers.splice(index, 1);
          if (handlers.length === 0) {
            this.subs.delete(topic);
          }
        }
      };
    }

    /**
     * Publish a message to a topic
     *
     * @param {string} topic - Topic to publish to
     * @param {*} body - Message body
     */
    publish(topic, body) {
      if (!this.isConnected) {
        throw new Error('Not connected');
      }

      const message = {
        header: {
          messageID: this._generateID(),
          type: 'event',
          topic,
          timestamp: Date.now()
        },
        body
      };

      this.ws.send(JSON.stringify(message));
    }

    /**
     * Send a request and wait for a response
     *
     * @param {string} topic - Topic to send the request to
     * @param {*} body - Request body
     * @param {number} [timeout=5000] - Timeout in milliseconds
     * @returns {Promise<*>} Response body
     */
    request(topic, body, timeout = 5000) {
      if (!this.isConnected) {
        return Promise.reject(new Error('Not connected'));
      }

      return new Promise((resolve, reject) => {
        const correlationID = this._generateID();

        // Set up timeout
        const timer = setTimeout(() => {
          // Clean up subscription
          if (this.subs.has(correlationID)) {
            this.subs.delete(correlationID);
          }
          reject(new Error(`Request timed out after ${timeout}ms`));
        }, timeout);

        // Subscribe to response
        this.subscribe(correlationID, (responseBody) => {
          clearTimeout(timer);
          // Clean up subscription
          if (this.subs.has(correlationID)) {
            this.subs.delete(correlationID);
          }
          resolve(responseBody);
        });

        // Send request
        const message = {
          header: {
            messageID: correlationID,
            correlationID,
            type: 'request',
            topic,
            timestamp: Date.now(),
            ttl: timeout
          },
          body
        };

        this.ws.send(JSON.stringify(message));
      });
    }

    /**
     * Register a callback for when the connection is established
     *
     * @param {Function} callback - Function to call when connected
     */
    onConnect(callback) {
      this.connectCallbacks.push(callback);
      // If already connected, call immediately
      if (this.isConnected) {
        callback();
      }
    }

    /**
     * Register a callback for when the connection is closed
     *
     * @param {Function} callback - Function to call when disconnected
     */
    onDisconnect(callback) {
      this.disconnectCallbacks.push(callback);
    }

    /**
     * Register a callback for when an error occurs
     *
     * @param {Function} callback - Function to call when an error occurs
     */
    onError(callback) {
      this.errorCallbacks.push(callback);
    }

    /**
     * Generate a unique ID
     *
     * @private
     * @returns {string} Unique ID
     */
    _generateID() {
      // Use crypto.randomUUID if available, otherwise fallback to timestamp-based ID
      if (typeof crypto !== 'undefined' && crypto.randomUUID) {
        return crypto.randomUUID();
      }
      return Date.now().toString(36) + Math.random().toString(36).substring(2);
    }

    /**
     * Send a response to a request
     *
     * @private
     * @param {string} correlationID - Correlation ID from the request
     * @param {*} body - Response body
     */
    _sendResponse(correlationID, body) {
      const message = {
        header: {
          messageID: this._generateID(),
          correlationID,
          type: 'response',
          timestamp: Date.now()
        },
        body
      };

      this.ws.send(JSON.stringify(message));
    }

    /**
     * Schedule a reconnection attempt
     *
     * @private
     */
    _scheduleReconnect() {
      if (this.reconnectTimer) {
        clearTimeout(this.reconnectTimer);
      }

      this.reconnectTimer = setTimeout(() => {
        this.reconnectTimer = null;
        this.connect();
      }, this.currentReconnectInterval);

      // Apply exponential backoff for next attempt
      this.currentReconnectInterval = Math.min(
        this.currentReconnectInterval * this.reconnectMultiplier,
        this.maxReconnectInterval
      );
    }

    /**
     * Notify all callbacks in a list
     *
     * @private
     * @param {Function[]} callbacks - List of callbacks to notify
     * @param {*} data - Data to pass to callbacks
     */
    _notifyCallbacks(callbacks, data) {
      for (const callback of callbacks) {
        try {
          callback(data);
        } catch (err) {
          console.error('Error in callback:', err);
        }
      }
    }

    /**
     * Set up development mode features
     *
     * @private
     */
    _setupDevMode() {
      // Subscribe to hot reload events
      this.subscribe('_dev.hotreload', (data) => {
        console.log('[WebSocketMQ] Hot reload triggered:', data);
        // Reload the page
        window.location.reload();
      });

      // Capture JavaScript errors and report them back to the server
      window.addEventListener('error', (event) => {
        if (!this.isConnected) return;

        this.publish('_dev.js-error', {
          type: 'error',
          message: event.message,
          filename: event.filename,
          lineno: event.lineno,
          colno: event.colno,
          stack: event.error ? event.error.stack : null,
          timestamp: Date.now()
        });
      });

      // Capture unhandled promise rejections
      window.addEventListener('unhandledrejection', (event) => {
        if (!this.isConnected) return;

        this.publish('_dev.js-error', {
          type: 'unhandledrejection',
          message: event.reason ? (event.reason.message || String(event.reason)) : 'Unknown promise rejection',
          stack: event.reason && event.reason.stack ? event.reason.stack : null,
          timestamp: Date.now()
        });
      });

      console.log('[WebSocketMQ] Development mode enabled');
    }
  }

  // Return the public API
  return {
    Client
  };
}));
