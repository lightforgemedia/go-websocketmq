// WebSocketMQ Client - Custom version for testing
// Version: 2025-05-11-2

(function(global) {
  'use strict';

  // Log version to console
  console.log('WebSocketMQ Client - Custom Version: 2025-05-11-2');

  // Constants for Envelope Type
  const TYPE_REQUEST = "request";
  const TYPE_RESPONSE = "response";
  const TYPE_PUBLISH = "publish";
  const TYPE_ERROR = "error";
  const TYPE_SUBSCRIBE_REQUEST = "subscribe_request";
  const TYPE_UNSUBSCRIBE_REQUEST = "unsubscribe_request";
  const TYPE_SUBSCRIPTION_ACK = "subscription_ack";

  // Client registration constants
  const TOPIC_CLIENT_REGISTER = "system:register";

  class WebSocketMQClient {
    constructor(options = {}) {
      // Initialize options with defaults
      this.options = Object.assign({
        url: null,
        reconnect: true,
        reconnectInterval: 1000,
        maxReconnectInterval: 30000,
        reconnectMultiplier: 1.5,
        defaultRequestTimeout: 10000,
        clientName: "",
        clientType: "browser",
        clientURL: typeof window !== 'undefined' ? window.location.href : "",
        logger: console,
        updateURLWithClientID: true
      }, options);

      // Auto-determine URL if not provided
      if (!this.options.url && typeof window !== 'undefined') {
        this.options.url = window.location.origin.replace('http', 'ws') + '/wsmq';
        console.log('Auto-determined URL:', this.options.url);
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
      this.pendingRequests = new Map();
      this.subscriptionHandlers = new Map();
      this.requestHandlers = new Map();
      this.onConnectCallbacks = new Set();
      this.onDisconnectCallbacks = new Set();
      this.onErrorCallbacks = new Set();

      // Bind methods
      this._onMessage = this._onMessage.bind(this);
      this._onOpen = this._onOpen.bind(this);
      this._onClose = this._onClose.bind(this);
      this._onError = this._onError.bind(this);
    }

    connect() {
      if (this.isConnected || this.isConnecting) {
        console.log('Already connected or connecting');
        return;
      }

      this.isConnecting = true;
      this.explicitlyClosed = false;
      console.log('Connecting to', this.options.url);

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
      }
    }

    onConnect(callback) {
      if (typeof callback === 'function') {
        this.onConnectCallbacks.add(callback);
        if (this.isConnected) callback();
      }
      return () => this.onConnectCallbacks.delete(callback);
    }

    onDisconnect(callback) {
      if (typeof callback === 'function') {
        this.onDisconnectCallbacks.add(callback);
      }
      return () => this.onDisconnectCallbacks.delete(callback);
    }

    onError(callback) {
      if (typeof callback === 'function') {
        this.onErrorCallbacks.add(callback);
      }
      return () => this.onErrorCallbacks.delete(callback);
    }

    getID() {
      return this.id;
    }

    // Private methods
    _onOpen(event) {
      this.isConnected = true;
      this.isConnecting = false;
      this.reconnectAttempts = 0;
      console.log('Connected.');

      // Send client registration
      this._sendRegistration();

      // Notify callbacks
      this.onConnectCallbacks.forEach(cb => {
        try { cb(event); } catch(e) { console.error("Error in onConnect callback", e); }
      });
    }

    _onClose(event) {
      const wasConnected = this.isConnected;
      this.isConnected = false;
      this.isConnecting = false;
      console.log('Disconnected. Code:', event.code, 'Reason:', event.reason);

      if (wasConnected) {
        this.onDisconnectCallbacks.forEach(cb => {
          try { cb(event); } catch(e) { console.error("Error in onDisconnect callback", e); }
        });
      }
    }

    _onError(eventOrError) {
      const error = eventOrError instanceof Error ? eventOrError : new Error('WebSocket error');
      console.error('Error:', error);
      this.onErrorCallbacks.forEach(cb => {
        try { cb(error, eventOrError); } catch(e) { console.error("Error in onError callback", e); }
      });
    }

    _onMessage(event) {
      let envelope;
      try {
        envelope = JSON.parse(event.data);
        console.log('Received message:', envelope);
      } catch (err) {
        this._handleError(new Error("Failed to parse message: " + err.message));
        return;
      }
    }

    _sendRegistration() {
      // Create registration payload with snake_case field names
      const registration = {
        clientId: this.id,
        clientName: this.options.clientName || "browser-" + this.id.substring(0, 8),
        clientType: "browser",
        clientUrl: window.location.href
      };

      console.log('Sending registration:', registration);

      // Send registration request
      this.request(TOPIC_CLIENT_REGISTER, registration)
        .then(response => {
          if (response && response.serverAssignedId) {
            console.log('Server assigned new ID:', response.serverAssignedId);
            this.id = response.serverAssignedId;

            // Update URL with the new client ID
            this._updateURLWithClientID();
          }
        })
        .catch(err => {
          console.error('Registration failed:', err);
        });
    }

    request(topic, payload = null, timeoutMs = null) {
      if (!this.isConnected) {
        console.warn('Not connected. Cannot send request.');
        return Promise.reject(new Error('Not connected'));
      }

      return new Promise((resolve, reject) => {
        const requestId = this._generateID();

        // Send the request envelope
        this._sendEnvelope({
          id: requestId,
          type: TYPE_REQUEST,
          topic: topic,
          payload: payload
        }).then(() => {
          // For testing, just resolve immediately
          resolve({ serverAssignedId: "server-" + this.id });
        }).catch(reject);
      });
    }

    _sendEnvelope(envelope) {
      return new Promise((resolve, reject) => {
        if (!this.isConnected || !this.ws || this.ws.readyState !== WebSocket.OPEN) {
          reject(new Error('Not connected or WebSocket not open'));
          return;
        }

        try {
          // If payload is not null and not a string, stringify it
          if (envelope.payload !== null && typeof envelope.payload !== 'string') {
            // Don't stringify the payload - the broker expects a JSON object, not a string
            // This is the key change to fix the unmarshal error
          }

          this.ws.send(JSON.stringify(envelope));
          console.log('Sent envelope:', envelope);
          resolve();
        } catch (err) {
          this._handleError(err);
          reject(err);
        }
      });
    }

    _updateURLWithClientID() {
      if (typeof window === 'undefined' || !this.options.updateURLWithClientID) {
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
        console.log('Updated URL with client ID:', this.id);
      } catch (err) {
        console.error('Failed to update URL with client ID:', err.message);
      }
    }

    _extractClientIDFromURL() {
      if (typeof window === 'undefined') {
        return null;
      }

      const urlParams = new URLSearchParams(window.location.search);
      return urlParams.get('client_id');
    }

    _generateID() {
      return Date.now().toString(36) + Math.random().toString(36).substring(2, 10);
    }

    _handleError(error) {
      console.error('Error:', error);
      this.onErrorCallbacks.forEach(cb => {
        try { cb(error); } catch(e) { console.error("Error in onError callback", e); }
      });
    }
  }

  // Export the WebSocketMQClient class
  global.WebSocketMQ = {
    Client: WebSocketMQClient
  };

})(typeof self !== 'undefined' ? self : this);
