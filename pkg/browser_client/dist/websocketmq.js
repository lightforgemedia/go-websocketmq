// pkg/assets/dist/websocketmq.js
// WebSocketMQ Client - Compatible with Go WebSocketMQ protocol

(function(global) {
  'use strict';

  // Constants for Envelope Type - matching Go client
  const TYPE_REQUEST = "request";
  const TYPE_RESPONSE = "response";
  const TYPE_PUBLISH = "publish";
  const TYPE_ERROR = "error";
  const TYPE_SUBSCRIBE_REQUEST = "subscribe_request";
  const TYPE_UNSUBSCRIBE_REQUEST = "unsubscribe_request";
  const TYPE_SUBSCRIPTION_ACK = "subscription_ack";

  // Client registration constants
  const TOPIC_CLIENT_REGISTER = "system:register"; // Matches shared_types.TopicClientRegister

  /**
   * WebSocketMQ Client - JavaScript implementation compatible with Go WebSocketMQ protocol
   */
  class WebSocketMQClient {
    /**
     * Create a new WebSocketMQ client
     * @param {Object} options - Configuration options
     * @param {string} options.url - WebSocket server URL
     * @param {boolean} [options.reconnect=true] - Whether to automatically reconnect
     * @param {number} [options.reconnectInterval=1000] - Initial reconnect interval in ms
     * @param {number} [options.maxReconnectInterval=30000] - Maximum reconnect interval in ms
     * @param {number} [options.reconnectMultiplier=1.5] - Multiplier for exponential backoff
     * @param {number} [options.defaultRequestTimeout=10000] - Default timeout for requests in ms
     * @param {string} [options.clientName=""] - Client name
     * @param {string} [options.clientType="browser"] - Client type
     * @param {string} [options.clientURL=""] - Client URL
     * @param {Object} [options.logger=console] - Logger object
     * @param {boolean} [options.updateURLWithClientID=true] - Whether to update URL with client ID parameter
     */
    constructor(options = {}) {
      // Initialize options with defaults
      this.options = Object.assign({
        url: null,
        reconnect: true,
        reconnectInterval: 1000,
        maxReconnectInterval: 30000,
        reconnectMultiplier: 1.5,
        defaultRequestTimeout: 10000, // 10 seconds, matching Go client
        clientName: "",
        clientType: "browser",
        clientURL: typeof window !== 'undefined' ? window.location.href : "",
        logger: console,
        updateURLWithClientID: true // Whether to update URL with client ID
      }, options);

      if (!this.options.url) {
        throw new Error('WebSocketMQ: URL is required');
      }

      // Initialize client state
      this.ws = null;
      this.isConnected = false;
      this.isConnecting = false;
      this.reconnectAttempts = 0;
      this.reconnectTimer = null;
      this.explicitlyClosed = false;

      // Generate a client ID or extract from URL
      this.id = this._extractClientIDFromURL() || this._generateID();

      // Initialize handlers and callbacks
      this.pendingRequests = new Map(); // id -> {resolve, reject}
      this.subscriptionHandlers = new Map(); // topic -> handler function
      this.requestHandlers = new Map(); // topic -> handler function for server-initiated requests
      this.onConnectCallbacks = new Set();
      this.onDisconnectCallbacks = new Set();
      this.onErrorCallbacks = new Set();

      // Bind methods
      this._onMessage = this._onMessage.bind(this);
      this._onOpen = this._onOpen.bind(this);
      this._onClose = this._onClose.bind(this);
      this._onError = this._onError.bind(this);
    }

    /**
     * Connect to the WebSocket server
     */
    connect() {
      if (this.isConnected || this.isConnecting) {
        this.options.logger.debug('WebSocketMQ: Already connected or connecting.');
        return;
      }

      this.isConnecting = true;
      this.explicitlyClosed = false;
      this.options.logger.debug('WebSocketMQ: Connecting to', this.options.url);

      try {
        // Add client ID to the URL as a query parameter
        let urlWithID = this.options.url;
        const url = new URL(urlWithID, window.location.href);
        url.searchParams.set('client_id', this.id);

        this.ws = new WebSocket(url.toString());
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

    /**
     * Disconnect from the WebSocket server
     */
    disconnect() {
      this.options.logger.debug('WebSocketMQ: Disconnect called.');
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

    /**
     * Send a request to the server and wait for a response
     * @param {string} topic - Topic to send the request to
     * @param {any} payload - Request payload
     * @param {number} [timeoutMs] - Request timeout in ms
     * @returns {Promise<any>} - Response payload
     */
    request(topic, payload = null, timeoutMs = null) {
      if (!this.isConnected) {
        this.options.logger.warn('WebSocketMQ: Not connected. Cannot send request.');
        return Promise.reject(new Error('WebSocketMQ: Not connected'));
      }

      // Use default timeout if not specified
      const effectiveTimeout = timeoutMs || this.options.defaultRequestTimeout;

      return new Promise((resolve, reject) => {
        const requestId = this._generateID();

        // Create timeout handler
        const timeoutId = setTimeout(() => {
          if (this.pendingRequests.has(requestId)) {
            this.pendingRequests.delete(requestId);
            reject(new Error(`WebSocketMQ: Request to '${topic}' timed out after ${effectiveTimeout}ms`));
          }
        }, effectiveTimeout);

        // Store the request handlers
        this.pendingRequests.set(requestId, {
          resolve: (result) => {
            clearTimeout(timeoutId);
            resolve(result);
          },
          reject: (error) => {
            clearTimeout(timeoutId);
            reject(error);
          }
        });

        // Send the request envelope
        this._sendEnvelope({
          id: requestId,
          type: TYPE_REQUEST,
          topic: topic,
          payload: payload
        }).catch(err => {
          clearTimeout(timeoutId);
          this.pendingRequests.delete(requestId);
          reject(err);
        });
      });
    }

    /**
     * Publish a message to a topic
     * @param {string} topic - Topic to publish to
     * @param {any} payload - Message payload
     * @returns {Promise<void>}
     */
    publish(topic, payload = null) {
      if (!this.isConnected) {
        this.options.logger.warn('WebSocketMQ: Not connected. Cannot publish.');
        return Promise.reject(new Error('WebSocketMQ: Not connected'));
      }

      return this._sendEnvelope({
        id: "", // No ID for publish messages
        type: TYPE_PUBLISH,
        topic: topic,
        payload: payload
      });
    }

    /**
     * Subscribe to a topic
     * @param {string} topic - Topic to subscribe to
     * @param {Function} handler - Handler function for received messages
     * @returns {Function} - Unsubscribe function
     */
    subscribe(topic, handler) {
      if (typeof handler !== 'function') {
        throw new Error('WebSocketMQ: Handler must be a function');
      }

      // Store the subscription handler
      this.subscriptionHandlers.set(topic, handler);

      // Send subscribe request if connected
      if (this.isConnected) {
        this._sendSubscribeRequest(topic).catch(err => {
          this.options.logger.error(`WebSocketMQ: Error subscribing to topic '${topic}':`, err);
        });
      }

      // Return unsubscribe function
      return () => {
        this.subscriptionHandlers.delete(topic);

        // Send unsubscribe request if connected
        if (this.isConnected) {
          this._sendUnsubscribeRequest(topic).catch(err => {
            this.options.logger.error(`WebSocketMQ: Error unsubscribing from topic '${topic}':`, err);
          });
        }
      };
    }

    /**
     * Register a handler for server-initiated requests
     * @param {string} topic - Topic to handle requests for
     * @param {Function} handler - Handler function for requests
     * @returns {Function} - Function to remove the handler
     */
    onRequest(topic, handler) {
      if (typeof handler !== 'function') {
        throw new Error('WebSocketMQ: Handler must be a function');
      }

      // Store the request handler
      this.requestHandlers.set(topic, handler);

      // Return a function to remove the handler
      return () => {
        this.requestHandlers.delete(topic);
      };
    }

    /**
     * Register a connect callback
     * @param {Function} callback - Callback function
     * @returns {Function} - Function to remove the callback
     */
    onConnect(callback) {
      if (typeof callback === 'function') {
        this.onConnectCallbacks.add(callback);
        if (this.isConnected) callback();
      }
      return () => this.onConnectCallbacks.delete(callback);
    }

    /**
     * Register a disconnect callback
     * @param {Function} callback - Callback function
     * @returns {Function} - Function to remove the callback
     */
    onDisconnect(callback) {
      if (typeof callback === 'function') {
        this.onDisconnectCallbacks.add(callback);
      }
      return () => this.onDisconnectCallbacks.delete(callback);
    }

    /**
     * Register an error callback
     * @param {Function} callback - Callback function
     * @returns {Function} - Function to remove the callback
     */
    onError(callback) {
      if (typeof callback === 'function') {
        this.onErrorCallbacks.add(callback);
      }
      return () => this.onErrorCallbacks.delete(callback);
    }

    /**
     * Get client ID
     * @returns {string} - Client ID
     */
    getID() {
      return this.id;
    }

    // Private methods

    /**
     * Handle WebSocket open event
     * @private
     */
    _onOpen(event) {
      this.isConnected = true;
      this.isConnecting = false;
      this.reconnectAttempts = 0;
      this.options.logger.info('WebSocketMQ: Connected.');

      // Send client registration
      this._sendRegistration();

      // Notify callbacks
      this.onConnectCallbacks.forEach(cb => {
        try { cb(event); } catch(e) { this.options.logger.error("Error in onConnect callback", e); }
      });
    }

    /**
     * Handle WebSocket close event
     * @private
     */
    _onClose(event) {
      const wasConnected = this.isConnected;
      this.isConnected = false;
      this.isConnecting = false;
      this.options.logger.info('WebSocketMQ: Disconnected. Code:', event.code, 'Reason:', event.reason, 'WasClean:', event.wasClean);

      if (wasConnected) {
        // Only call disconnect callbacks if it was previously connected
        this.onDisconnectCallbacks.forEach(cb => {
          try { cb(event); } catch(e) { this.options.logger.error("Error in onDisconnect callback", e); }
        });
      }

      if (!this.explicitlyClosed && this.options.reconnect) {
        this._scheduleReconnect();
      }
    }

    /**
     * Handle WebSocket error event
     * @private
     */
    _onError(eventOrError) {
      const error = eventOrError instanceof Error ? eventOrError : new Error('WebSocket error');
      this.options.logger.error('WebSocketMQ: Error:', error, 'Raw event:', eventOrError);
      this.onErrorCallbacks.forEach(cb => {
        try { cb(error, eventOrError); } catch(e) { this.options.logger.error("Error in onError callback", e); }
      });
    }

    /**
     * Handle WebSocket message event
     * @private
     */
    _onMessage(event) {
      let envelope;
      try {
        envelope = JSON.parse(event.data);
        this.options.logger.debug('WebSocketMQ: Received message:', envelope);
      } catch (err) {
        this._handleError(new Error(`WebSocketMQ: Failed to parse message: ${err.message}. Data: ${event.data}`));
        return;
      }

      // Handle different message types
      switch (envelope.type) {
        case TYPE_RESPONSE:
        case TYPE_ERROR:
          // Handle response to a client-initiated request
          this._handleResponseEnvelope(envelope);
          break;

        case TYPE_PUBLISH:
          // Handle published message
          this._handlePublishEnvelope(envelope);
          break;

        case TYPE_REQUEST:
          // Handle server-initiated request
          this._handleRequestEnvelope(envelope);
          break;

        case TYPE_SUBSCRIPTION_ACK:
          // Handle subscription acknowledgment
          this.options.logger.debug(`WebSocketMQ: Received subscription ack for topic '${envelope.topic}'`);
          break;

        default:
          this.options.logger.warn(`WebSocketMQ: Received unknown message type: ${envelope.type}`);
      }
    }

    /**
     * Handle response envelope
     * @private
     */
    _handleResponseEnvelope(envelope) {
      const requestId = envelope.id;
      if (!requestId) {
        this.options.logger.warn('WebSocketMQ: Received response without ID:', envelope);
        return;
      }

      const pendingRequest = this.pendingRequests.get(requestId);
      if (!pendingRequest) {
        this.options.logger.warn(`WebSocketMQ: Received response for unknown request ID: ${requestId}`);
        return;
      }

      this.pendingRequests.delete(requestId);

      if (envelope.type === TYPE_ERROR) {
        pendingRequest.reject(new Error(envelope.error ? envelope.error.message : 'Unknown error'));
      } else {
        let payload = null;
        try {
          payload = envelope.payload ? JSON.parse(envelope.payload) : null;
        } catch (err) {
          pendingRequest.reject(new Error(`Failed to parse response payload: ${err.message}`));
          return;
        }
        pendingRequest.resolve(payload);
      }
    }

    /**
     * Handle publish envelope
     * @private
     */
    _handlePublishEnvelope(envelope) {
      const topic = envelope.topic;
      const handler = this.subscriptionHandlers.get(topic);

      if (handler) {
        let payload = null;
        try {
          payload = envelope.payload ? JSON.parse(envelope.payload) : null;
        } catch (err) {
          this.options.logger.error(`WebSocketMQ: Failed to parse publish payload for topic '${topic}':`, err);
          return;
        }

        try {
          handler(payload);
        } catch (err) {
          this.options.logger.error(`WebSocketMQ: Error in subscription handler for topic '${topic}':`, err);
        }
      } else {
        this.options.logger.debug(`WebSocketMQ: No handler for published message on topic: ${topic}`);
      }
    }

    /**
     * Handle request envelope
     * @private
     */
    _handleRequestEnvelope(envelope) {
      const topic = envelope.topic;
      const handler = this.requestHandlers.get(topic);

      if (handler) {
        let payload = null;
        try {
          payload = envelope.payload ? JSON.parse(envelope.payload) : null;
        } catch (err) {
          this.options.logger.error(`WebSocketMQ: Failed to parse request payload for topic '${topic}':`, err);

          // Send error response
          this._sendEnvelope({
            id: envelope.id,
            type: TYPE_ERROR,
            topic: envelope.topic,
            error: {
              code: 400,
              message: `Failed to parse request payload: ${err.message}`
            }
          });
          return;
        }

        try {
          // Call handler and handle response
          Promise.resolve(handler(payload))
            .then(result => {
              // Send response
              this._sendEnvelope({
                id: envelope.id,
                type: TYPE_RESPONSE,
                topic: envelope.topic,
                payload: result
              });
            })
            .catch(err => {
              // Send error response
              this._sendEnvelope({
                id: envelope.id,
                type: TYPE_ERROR,
                topic: envelope.topic,
                error: {
                  code: 500,
                  message: err.message || 'Handler error'
                }
              });
            });
        } catch (err) {
          this.options.logger.error(`WebSocketMQ: Error in request handler for topic '${topic}':`, err);

          // Send error response
          this._sendEnvelope({
            id: envelope.id,
            type: TYPE_ERROR,
            topic: envelope.topic,
            error: {
              code: 500,
              message: err.message || 'Handler error'
            }
          });
        }
      } else {
        this.options.logger.warn(`WebSocketMQ: No handler for server-initiated request on topic: ${topic}`);

        // Send error response
        this._sendEnvelope({
          id: envelope.id,
          type: TYPE_ERROR,
          topic: envelope.topic,
          error: {
            code: 404,
            message: `No handler registered for topic: ${topic}`
          }
        });
      }
    }

    /**
     * Send client registration to the server
     * @private
     */
    _sendRegistration() {
      // Create registration payload
      const registration = {
        clientID: this.id,
        clientName: this.options.clientName || `browser-${this.id.substring(0, 8)}`,
        clientType: this.options.clientType || "browser",
        clientURL: this.options.clientURL || window.location.href
      };

      // Send registration request
      this.request(TOPIC_CLIENT_REGISTER, registration)
        .then(response => {
          if (response && response.serverAssignedID) {
            this.options.logger.info(`WebSocketMQ: Server assigned new ID: ${response.serverAssignedID}`);
            this.id = response.serverAssignedID;

            // Update URL with the new client ID
            this._updateURLWithClientID();

            // Re-subscribe to all topics
            this._resubscribeAll();
          }
        })
        .catch(err => {
          this.options.logger.error('WebSocketMQ: Registration failed:', err);
        });
    }

    /**
     * Re-subscribe to all topics after reconnection
     * @private
     */
    _resubscribeAll() {
      if (this.subscriptionHandlers.size > 0) {
        this.options.logger.info(`WebSocketMQ: Re-subscribing to ${this.subscriptionHandlers.size} topics...`);
        for (const topic of this.subscriptionHandlers.keys()) {
          this._sendSubscribeRequest(topic).catch(err => {
            this.options.logger.error(`WebSocketMQ: Error re-subscribing to topic '${topic}':`, err);
          });
        }
      }
    }

    /**
     * Send a subscribe request to the server
     * @private
     */
    _sendSubscribeRequest(topic) {
      return this._sendEnvelope({
        id: this._generateID(),
        type: TYPE_SUBSCRIBE_REQUEST,
        topic: topic
      });
    }

    /**
     * Send an unsubscribe request to the server
     * @private
     */
    _sendUnsubscribeRequest(topic) {
      return this._sendEnvelope({
        id: this._generateID(),
        type: TYPE_UNSUBSCRIBE_REQUEST,
        topic: topic
      });
    }

    /**
     * Send an envelope to the server
     * @private
     */
    _sendEnvelope(envelope) {
      return new Promise((resolve, reject) => {
        if (!this.isConnected || !this.ws || this.ws.readyState !== WebSocket.OPEN) {
          reject(new Error('WebSocketMQ: Not connected or WebSocket not open'));
          return;
        }

        try {
          // If payload is not null and not a string, stringify it
          if (envelope.payload !== null && typeof envelope.payload !== 'string') {
            envelope.payload = JSON.stringify(envelope.payload);
          }

          this.ws.send(JSON.stringify(envelope));
          this.options.logger.debug('WebSocketMQ: Sent envelope:', envelope);
          resolve();
        } catch (err) {
          this._handleError(err);
          reject(err);
        }
      });
    }

    /**
     * Schedule a reconnect attempt
     * @private
     */
    _scheduleReconnect() {
      if (this.explicitlyClosed || !this.options.reconnect || this.reconnectTimer) {
        return;
      }

      this.reconnectAttempts++;
      const interval = Math.min(
        this.options.reconnectInterval * Math.pow(this.options.reconnectMultiplier, this.reconnectAttempts - 1),
        this.options.maxReconnectInterval
      );

      this.options.logger.info(`WebSocketMQ: Scheduling reconnect attempt ${this.reconnectAttempts} in ${interval}ms.`);
      this.reconnectTimer = setTimeout(() => {
        this.reconnectTimer = null;
        if (!this.explicitlyClosed) this.connect();
      }, interval);
    }

    /**
     * Generate a unique ID
     * @private
     */
    _generateID() {
      return Date.now().toString(36) + Math.random().toString(36).substring(2, 10);
    }

    /**
     * Extract client ID from URL if present
     * @private
     * @returns {string|null} - Client ID from URL or null if not found
     */
    _extractClientIDFromURL() {
      if (typeof window === 'undefined') {
        return null;
      }

      const urlParams = new URLSearchParams(window.location.search);
      return urlParams.get('client_id');
    }

    /**
     * Update URL with client ID
     * @private
     */
    _updateURLWithClientID() {
      if (typeof window === 'undefined' || !this.options.updateURLWithClientID) {
        return;
      }

      // Don't modify URL if we're not in a browser context or feature is disabled
      if (!window.history || !window.location) {
        return;
      }

      try {
        const url = new URL(window.location.href);
        const params = new URLSearchParams(url.search);

        // Update or add client_id parameter
        params.set('client_id', this.id);
        url.search = params.toString();

        // Update URL without reloading the page
        window.history.replaceState({}, '', url.toString());
        this.options.logger.debug(`WebSocketMQ: Updated URL with client ID: ${this.id}`);
      } catch (err) {
        this.options.logger.error(`WebSocketMQ: Failed to update URL with client ID: ${err.message}`);
      }
    }

    /**
     * Handle errors
     * @private
     */
    _handleError(error) {
      this.options.logger.error('WebSocketMQ: Error:', error);
      this.onErrorCallbacks.forEach(cb => {
        try { cb(error); } catch(e) { this.options.logger.error("Error in onError callback", e); }
      });
    }
  }

  // Export the WebSocketMQClient class
  if (typeof module !== 'undefined' && typeof module.exports !== 'undefined') {
    // CommonJS/Node.js
    module.exports = {
      WebSocketMQClient,
      TYPE_REQUEST,
      TYPE_RESPONSE,
      TYPE_PUBLISH,
      TYPE_ERROR,
      TYPE_SUBSCRIBE_REQUEST,
      TYPE_UNSUBSCRIBE_REQUEST,
      TYPE_SUBSCRIPTION_ACK,
      TOPIC_CLIENT_REGISTER
    };
  } else {
    // Browser global
    global.WebSocketMQ = {
      Client: WebSocketMQClient,
      TYPE_REQUEST,
      TYPE_RESPONSE,
      TYPE_PUBLISH,
      TYPE_ERROR,
      TYPE_SUBSCRIBE_REQUEST,
      TYPE_UNSUBSCRIBE_REQUEST,
      TYPE_SUBSCRIPTION_ACK,
      TOPIC_CLIENT_REGISTER
    };
  }

})(typeof self !== 'undefined' ? self : this);