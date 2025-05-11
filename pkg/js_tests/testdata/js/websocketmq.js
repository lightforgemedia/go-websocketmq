/**
 * WebSocketMQ JavaScript Client
 * A client library for the WebSocketMQ Go server
 */

class WebSocketMQ {
    /**
     * Create a new WebSocketMQ client
     * @param {string} url - WebSocket URL to connect to
     * @param {Object} options - Configuration options
     */
    constructor(url, options = {}) {
        this.url = url;
        this.options = Object.assign({
            reconnect: true,
            reconnectAttempts: 5,
            reconnectDelay: 1000,
            maxReconnectDelay: 15000,
            requestTimeout: 5000,
            debug: false
        }, options);

        this.socket = null;
        this.connected = false;
        this.connecting = false;
        this.reconnectAttempt = 0;
        this.clientId = null;
        this.requestMap = new Map();
        this.subscriptions = new Map();
        this.requestHandlers = new Map();
        this.eventListeners = {
            'connect': [],
            'disconnect': [],
            'error': [],
            'message': []
        };

        this.connect();
    }

    /**
     * Connect to the WebSocket server
     */
    connect() {
        if (this.connected || this.connecting) return;
        
        this.connecting = true;
        this.log('Connecting to', this.url);
        
        try {
            this.socket = new WebSocket(this.url);
            
            this.socket.onopen = () => {
                this.connected = true;
                this.connecting = false;
                this.reconnectAttempt = 0;
                this.log('Connected to', this.url);
                this._triggerEvent('connect');
            };
            
            this.socket.onclose = (event) => {
                this.connected = false;
                this.connecting = false;
                this.log('Disconnected from', this.url, event.code, event.reason);
                this._triggerEvent('disconnect', event);
                
                if (this.options.reconnect && this.reconnectAttempt < this.options.reconnectAttempts) {
                    const delay = Math.min(
                        this.options.reconnectDelay * Math.pow(1.5, this.reconnectAttempt),
                        this.options.maxReconnectDelay
                    );
                    this.reconnectAttempt++;
                    this.log(`Reconnecting in ${delay}ms (attempt ${this.reconnectAttempt})`);
                    setTimeout(() => this.connect(), delay);
                }
            };
            
            this.socket.onerror = (error) => {
                this.log('WebSocket error:', error);
                this._triggerEvent('error', error);
            };
            
            this.socket.onmessage = (event) => {
                try {
                    const envelope = JSON.parse(event.data);
                    this._handleEnvelope(envelope);
                } catch (error) {
                    this.log('Error parsing message:', error, event.data);
                    this._triggerEvent('error', error);
                }
            };
        } catch (error) {
            this.connecting = false;
            this.log('Connection error:', error);
            this._triggerEvent('error', error);
            
            if (this.options.reconnect && this.reconnectAttempt < this.options.reconnectAttempts) {
                const delay = Math.min(
                    this.options.reconnectDelay * Math.pow(1.5, this.reconnectAttempt),
                    this.options.maxReconnectDelay
                );
                this.reconnectAttempt++;
                this.log(`Reconnecting in ${delay}ms (attempt ${this.reconnectAttempt})`);
                setTimeout(() => this.connect(), delay);
            }
        }
    }

    /**
     * Close the WebSocket connection
     */
    close() {
        if (this.socket) {
            this.options.reconnect = false; // Prevent reconnection
            this.socket.close();
        }
    }

    /**
     * Get the client ID
     * @returns {string} Client ID
     */
    id() {
        return this.clientId;
    }

    /**
     * Send a request to the server and wait for a response
     * @param {string} topic - Topic to send the request to
     * @param {Object} payload - Request payload
     * @returns {Promise<Object>} - Response payload
     */
    request(topic, payload = null) {
        return new Promise((resolve, reject) => {
            if (!this.connected) {
                reject(new Error('Not connected'));
                return;
            }
            
            const id = this._generateId();
            const envelope = {
                id: id,
                type: 'request',
                topic: topic,
                payload: payload
            };
            
            const timeoutId = setTimeout(() => {
                if (this.requestMap.has(id)) {
                    this.requestMap.delete(id);
                    reject(new Error('Request timeout'));
                }
            }, this.options.requestTimeout);
            
            this.requestMap.set(id, { resolve, reject, timeoutId });
            
            this._sendEnvelope(envelope);
        });
    }

    /**
     * Subscribe to a topic
     * @param {string} topic - Topic to subscribe to
     * @param {Function} handler - Handler function for messages on this topic
     * @returns {Function} - Unsubscribe function
     */
    subscribe(topic, handler) {
        if (!this.connected) {
            throw new Error('Not connected');
        }
        
        const id = this._generateId();
        const envelope = {
            id: id,
            type: 'subscribe_request',
            topic: topic
        };
        
        this.subscriptions.set(topic, handler);
        
        this._sendEnvelope(envelope);
        
        return () => {
            this.unsubscribe(topic);
        };
    }

    /**
     * Unsubscribe from a topic
     * @param {string} topic - Topic to unsubscribe from
     */
    unsubscribe(topic) {
        if (!this.connected) {
            throw new Error('Not connected');
        }
        
        if (this.subscriptions.has(topic)) {
            const id = this._generateId();
            const envelope = {
                id: id,
                type: 'unsubscribe_request',
                topic: topic
            };
            
            this.subscriptions.delete(topic);
            
            this._sendEnvelope(envelope);
        }
    }

    /**
     * Register a request handler for a topic
     * @param {string} topic - Topic to handle requests for
     * @param {Function} handler - Handler function for requests on this topic
     */
    onRequest(topic, handler) {
        this.requestHandlers.set(topic, handler);
    }

    /**
     * Add an event listener
     * @param {string} event - Event name
     * @param {Function} listener - Event listener
     */
    on(event, listener) {
        if (this.eventListeners[event]) {
            this.eventListeners[event].push(listener);
        }
    }

    /**
     * Remove an event listener
     * @param {string} event - Event name
     * @param {Function} listener - Event listener
     */
    off(event, listener) {
        if (this.eventListeners[event]) {
            this.eventListeners[event] = this.eventListeners[event].filter(l => l !== listener);
        }
    }

    /**
     * Handle an envelope from the server
     * @private
     * @param {Object} envelope - Envelope from the server
     */
    _handleEnvelope(envelope) {
        this.log('Received envelope:', envelope);
        this._triggerEvent('message', envelope);
        
        switch (envelope.type) {
            case 'response':
                this._handleResponse(envelope);
                break;
            case 'publish':
                this._handlePublish(envelope);
                break;
            case 'request':
                this._handleRequest(envelope);
                break;
            case 'subscription_ack':
                this._handleSubscriptionAck(envelope);
                break;
            case 'error':
                this._handleError(envelope);
                break;
            default:
                this.log('Unknown envelope type:', envelope.type);
        }
    }

    /**
     * Handle a response envelope
     * @private
     * @param {Object} envelope - Response envelope
     */
    _handleResponse(envelope) {
        const request = this.requestMap.get(envelope.id);
        if (request) {
            clearTimeout(request.timeoutId);
            this.requestMap.delete(envelope.id);
            
            if (envelope.error) {
                request.reject(new Error(envelope.error.message));
            } else {
                request.resolve(envelope.payload);
            }
        }
    }

    /**
     * Handle a publish envelope
     * @private
     * @param {Object} envelope - Publish envelope
     */
    _handlePublish(envelope) {
        const handler = this.subscriptions.get(envelope.topic);
        if (handler) {
            try {
                handler(envelope.payload);
            } catch (error) {
                this.log('Error in subscription handler:', error);
                this._triggerEvent('error', error);
            }
        }
    }

    /**
     * Handle a request envelope
     * @private
     * @param {Object} envelope - Request envelope
     */
    _handleRequest(envelope) {
        const handler = this.requestHandlers.get(envelope.topic);
        if (handler) {
            try {
                Promise.resolve(handler(envelope.payload))
                    .then(response => {
                        const responseEnvelope = {
                            id: envelope.id,
                            type: 'response',
                            topic: envelope.topic,
                            payload: response
                        };
                        this._sendEnvelope(responseEnvelope);
                    })
                    .catch(error => {
                        const errorEnvelope = {
                            id: envelope.id,
                            type: 'response',
                            topic: envelope.topic,
                            error: {
                                message: error.message
                            }
                        };
                        this._sendEnvelope(errorEnvelope);
                    });
            } catch (error) {
                const errorEnvelope = {
                    id: envelope.id,
                    type: 'response',
                    topic: envelope.topic,
                    error: {
                        message: error.message
                    }
                };
                this._sendEnvelope(errorEnvelope);
            }
        } else {
            const errorEnvelope = {
                id: envelope.id,
                type: 'response',
                topic: envelope.topic,
                error: {
                    message: `No handler for topic: ${envelope.topic}`
                }
            };
            this._sendEnvelope(errorEnvelope);
        }
    }

    /**
     * Handle a subscription acknowledgement envelope
     * @private
     * @param {Object} envelope - Subscription acknowledgement envelope
     */
    _handleSubscriptionAck(envelope) {
        // Store client ID if provided
        if (envelope.payload && envelope.payload.clientId) {
            this.clientId = envelope.payload.clientId;
            this.log('Client ID set to', this.clientId);
        }
    }

    /**
     * Handle an error envelope
     * @private
     * @param {Object} envelope - Error envelope
     */
    _handleError(envelope) {
        const error = new Error(envelope.error ? envelope.error.message : 'Unknown error');
        this._triggerEvent('error', error);
        
        // If this is a response to a request, reject the promise
        if (envelope.id && this.requestMap.has(envelope.id)) {
            const request = this.requestMap.get(envelope.id);
            clearTimeout(request.timeoutId);
            this.requestMap.delete(envelope.id);
            request.reject(error);
        }
    }

    /**
     * Send an envelope to the server
     * @private
     * @param {Object} envelope - Envelope to send
     */
    _sendEnvelope(envelope) {
        if (!this.connected) {
            throw new Error('Not connected');
        }
        
        try {
            const json = JSON.stringify(envelope);
            this.log('Sending envelope:', envelope);
            this.socket.send(json);
        } catch (error) {
            this.log('Error sending envelope:', error);
            this._triggerEvent('error', error);
            throw error;
        }
    }

    /**
     * Generate a random ID
     * @private
     * @returns {string} Random ID
     */
    _generateId() {
        return Math.random().toString(36).substring(2, 15) + Math.random().toString(36).substring(2, 15);
    }

    /**
     * Trigger an event
     * @private
     * @param {string} event - Event name
     * @param {*} data - Event data
     */
    _triggerEvent(event, data) {
        if (this.eventListeners[event]) {
            for (const listener of this.eventListeners[event]) {
                try {
                    listener(data);
                } catch (error) {
                    console.error('Error in event listener:', error);
                }
            }
        }
    }

    /**
     * Log a message if debug is enabled
     * @private
     * @param {...*} args - Arguments to log
     */
    log(...args) {
        if (this.options.debug) {
            console.log('[WebSocketMQ]', ...args);
        }
    }
}

// Export for both browser and Node.js
if (typeof module !== 'undefined' && typeof module.exports !== 'undefined') {
    module.exports = WebSocketMQ;
} else {
    window.WebSocketMQ = WebSocketMQ;
}
