/**
 * WebSocketMQ JavaScript Client
 * A client library for communicating with a WebSocketMQ server.
 */
class WebSocketMQClient {
    /**
     * Create a new WebSocketMQ client
     * @param {string} url - The WebSocket URL to connect to
     * @param {Object} options - Configuration options
     */
    constructor(url, options = {}) {
        this.url = url;
        this.options = Object.assign({
            reconnectDelay: 1000,
            maxReconnectAttempts: 5,
            debug: false
        }, options);
        
        this.socket = null;
        this.connected = false;
        this.reconnectAttempts = 0;
        this.pageSessionID = this.generateID();
        this.brokerClientID = null;
        this.messageID = 0;
        this.pendingRequests = new Map();
        this.handlers = new Map();
        
        // Event callbacks
        this.onConnect = null;
        this.onDisconnect = null;
        this.onError = null;
        this.onMessage = null;
        
        // Register internal handlers
        this.registerHandler('_internal.client.registered', this._handleRegistration.bind(this));
        
        // Connect immediately
        this.connect();
    }
    
    /**
     * Connect to the WebSocketMQ server
     */
    connect() {
        this.log('Connecting to', this.url);
        
        try {
            this.socket = new WebSocket(this.url);
            
            this.socket.onopen = () => {
                this.log('WebSocket connection established');
                this._sendRegistration();
            };
            
            this.socket.onmessage = (event) => {
                this._handleMessage(event.data);
            };
            
            this.socket.onclose = (event) => {
                this.connected = false;
                this.log('WebSocket connection closed:', event.code, event.reason);
                
                if (this.onDisconnect) {
                    this.onDisconnect(event);
                }
                
                // Attempt to reconnect
                if (this.reconnectAttempts < this.options.maxReconnectAttempts) {
                    this.reconnectAttempts++;
                    setTimeout(() => this.connect(), this.options.reconnectDelay);
                }
            };
            
            this.socket.onerror = (error) => {
                this.log('WebSocket error:', error);
                
                if (this.onError) {
                    this.onError(error);
                }
            };
        } catch (error) {
            this.log('Error creating WebSocket:', error);
            
            if (this.onError) {
                this.onError(error);
            }
        }
    }
    
    /**
     * Close the WebSocket connection
     */
    close() {
        if (this.socket) {
            this.socket.close();
            this.socket = null;
            this.connected = false;
        }
    }
    
    /**
     * Send an RPC request to the server
     * @param {string} topic - The topic to send the request to
     * @param {any} body - The request body
     * @param {number} timeoutMs - Timeout in milliseconds
     * @returns {Promise} - A promise that resolves with the response
     */
    sendRPC(topic, body, timeoutMs = 5000) {
        return new Promise((resolve, reject) => {
            if (!this.connected) {
                reject(new Error('Not connected to server'));
                return;
            }
            
            const messageID = this.generateID();
            const correlationID = messageID;
            
            const message = {
                header: {
                    messageID: messageID,
                    correlationID: correlationID,
                    type: 'request',
                    topic: topic,
                    timestamp: Date.now(),
                    ttl: timeoutMs
                },
                body: body
            };
            
            // Set up timeout
            const timeoutId = setTimeout(() => {
                if (this.pendingRequests.has(correlationID)) {
                    this.pendingRequests.delete(correlationID);
                    reject(new Error('Request timed out'));
                }
            }, timeoutMs);
            
            // Store the pending request
            this.pendingRequests.set(correlationID, {
                resolve,
                reject,
                timeoutId
            });
            
            // Send the message
            this._sendMessage(message);
        });
    }
    
    /**
     * Send an event to the server
     * @param {string} topic - The topic to send the event to
     * @param {any} body - The event body
     */
    sendEvent(topic, body) {
        if (!this.connected) {
            this.log('Cannot send event: Not connected to server');
            return;
        }
        
        const message = {
            header: {
                messageID: this.generateID(),
                type: 'event',
                topic: topic,
                timestamp: Date.now()
            },
            body: body
        };
        
        this._sendMessage(message);
    }
    
    /**
     * Register a handler for a specific topic
     * @param {string} topic - The topic to handle
     * @param {Function} handler - The handler function
     */
    registerHandler(topic, handler) {
        this.handlers.set(topic, handler);
        this.log('Registered handler for topic:', topic);
    }
    
    /**
     * Unregister a handler for a specific topic
     * @param {string} topic - The topic to unregister
     */
    unregisterHandler(topic) {
        this.handlers.delete(topic);
        this.log('Unregistered handler for topic:', topic);
    }
    
    /**
     * Generate a unique ID
     * @returns {string} - A unique ID
     */
    generateID() {
        return Date.now() + '-' + Math.random().toString(36).substr(2, 9);
    }
    
    /**
     * Log a message if debugging is enabled
     * @param {...any} args - The arguments to log
     */
    log(...args) {
        if (this.options.debug) {
            console.log('[WebSocketMQ]', ...args);
        }
    }
    
    /**
     * Send a message to the server
     * @param {Object} message - The message to send
     * @private
     */
    _sendMessage(message) {
        if (!this.socket || this.socket.readyState !== WebSocket.OPEN) {
            this.log('Cannot send message: Socket not open');
            return;
        }
        
        const messageStr = JSON.stringify(message);
        this.socket.send(messageStr);
        this.log('Sent message:', message);
    }
    
    /**
     * Handle an incoming message
     * @param {string} data - The raw message data
     * @private
     */
    _handleMessage(data) {
        try {
            const message = JSON.parse(data);
            this.log('Received message:', message);
            
            // Call the general message handler if provided
            if (this.onMessage) {
                this.onMessage(message);
            }
            
            // Handle responses to pending requests
            if (message.header.correlationID && this.pendingRequests.has(message.header.correlationID)) {
                const { resolve, reject, timeoutId } = this.pendingRequests.get(message.header.correlationID);
                this.pendingRequests.delete(message.header.correlationID);
                clearTimeout(timeoutId);
                
                if (message.header.type === 'error') {
                    reject(new Error(JSON.stringify(message.body)));
                } else {
                    resolve(message);
                }
                return;
            }
            
            // Handle topic-specific handlers
            const topic = message.header.topic;
            if (this.handlers.has(topic)) {
                const handler = this.handlers.get(topic);
                
                // Call the handler
                try {
                    const response = handler(message);
                    
                    // If this is a request and the handler returned a response, send it back
                    if (message.header.type === 'request' && response) {
                        const responseMessage = {
                            header: {
                                messageID: this.generateID(),
                                correlationID: message.header.correlationID,
                                type: 'response',
                                topic: message.header.correlationID,
                                timestamp: Date.now()
                            },
                            body: response
                        };
                        
                        this._sendMessage(responseMessage);
                    }
                } catch (error) {
                    this.log('Error in handler for topic', topic, ':', error);
                    
                    // If this is a request, send back an error response
                    if (message.header.type === 'request') {
                        const errorMessage = {
                            header: {
                                messageID: this.generateID(),
                                correlationID: message.header.correlationID,
                                type: 'error',
                                topic: message.header.correlationID,
                                timestamp: Date.now()
                            },
                            body: {
                                error: error.message || 'Error in handler'
                            }
                        };
                        
                        this._sendMessage(errorMessage);
                    }
                }
            } else {
                this.log('No handler registered for topic:', topic);
            }
        } catch (error) {
            this.log('Error handling message:', error);
        }
    }
    
    /**
     * Send a registration message to the server
     * @private
     */
    _sendRegistration() {
        const message = {
            header: {
                messageID: this.generateID(),
                type: 'event',
                topic: '_client.register',
                timestamp: Date.now()
            },
            body: {
                pageSessionID: this.pageSessionID
            }
        };
        
        this._sendMessage(message);
    }
    
    /**
     * Handle the registration response from the server
     * @param {Object} message - The registration message
     * @private
     */
    _handleRegistration(message) {
        if (message.body && message.body.brokerClientID) {
            this.brokerClientID = message.body.brokerClientID;
            this.connected = true;
            this.reconnectAttempts = 0;
            
            this.log('Registered with server, brokerClientID:', this.brokerClientID);
            
            // Call the connect callback if provided
            if (this.onConnect) {
                this.onConnect();
            }
        }
    }
}

// Export for CommonJS/ES modules compatibility
if (typeof module !== 'undefined' && module.exports) {
    module.exports = WebSocketMQClient;
} else if (typeof define === 'function' && define.amd) {
    define([], function() { return WebSocketMQClient; });
} else {
    window.WebSocketMQClient = WebSocketMQClient;
}
