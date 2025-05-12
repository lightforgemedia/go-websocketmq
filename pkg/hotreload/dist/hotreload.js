// Hot Reload Client - Extends WebSocketMQ client with hot reload capabilities
// Version: 2025-05-11-1

(function(global) {
  'use strict';
  
  // Log version to console
  console.log('Hot Reload Client - Version: 2025-05-11-1');
  
  // Make sure WebSocketMQ is loaded
  if (!global.WebSocketMQ || !global.WebSocketMQ.Client) {
    console.error('WebSocketMQ client not found. Make sure to load websocketmq.js before hotreload.js');
    return;
  }
  
  // Constants
  const TOPIC_HOT_RELOAD = "system:hot_reload";
  const TOPIC_CLIENT_ERROR = "system:client_error";
  const TOPIC_HOTRELOAD_READY = "system:hotreload_ready";
  
  // Create the hot reload client
  class HotReloadClient {
    constructor(options = {}) {
      // Store options with defaults
      this.options = Object.assign({
        client: null,
        autoReload: true,
        reportErrors: true,
        maxErrors: 100
      }, options);
      
      // Create or use existing WebSocketMQ client
      this.client = this.options.client;
      if (!this.client) {
        console.log('Creating new WebSocketMQ client');
        this.client = new WebSocketMQ.Client(options);
      }
      
      // Initialize error tracking
      this.errors = [];
      this.fileLoadStatus = {};
      
      // Set up error handlers if enabled
      if (this.options.reportErrors) {
        this._setupErrorHandlers();
      }
      
      // Set up hot reload handler
      this.client.subscribe(TOPIC_HOT_RELOAD, () => {
        console.log('Hot reload requested by server');
        if (this.options.autoReload) {
          this._performReload();
        }
        return { success: true };
      });
      
      // Report client ready when connected
      this.client.onConnect(() => {
        console.log('Connected, sending hot reload ready notification');
        this._reportReady();
      });
      
      // Connect if the client isn't already connected
      if (!this.client.isConnected) {
        console.log('Connecting WebSocketMQ client');
        this.client.connect();
      } else {
        // If already connected, report ready immediately
        this._reportReady();
      }
    }
    
    _setupErrorHandlers() {
      // Capture window errors
      window.addEventListener('error', (event) => {
        const error = {
          message: event.message,
          filename: event.filename,
          lineno: event.lineno,
          colno: event.colno,
          stack: event.error ? event.error.stack : '',
          timestamp: new Date().toISOString()
        };
        
        this._addError(error);
      });
      
      // Capture unhandled promise rejections
      window.addEventListener('unhandledrejection', (event) => {
        const error = {
          message: 'Unhandled Promise Rejection: ' + (event.reason.message || event.reason),
          stack: event.reason.stack || '',
          timestamp: new Date().toISOString()
        };
        
        this._addError(error);
      });
      
      // Capture resource load errors
      window.addEventListener('error', (event) => {
        if (event.target && (event.target.tagName === 'SCRIPT' || event.target.tagName === 'LINK')) {
          const error = {
            message: 'Resource load error: ' + event.target.src || event.target.href,
            filename: event.target.src || event.target.href,
            timestamp: new Date().toISOString()
          };
          
          this._addError(error);
        }
      }, true); // Use capture phase to catch resource errors
    }
    
    _addError(error) {
      // Add to local error list
      this.errors.push(error);
      
      // Limit the number of stored errors
      if (this.errors.length > this.options.maxErrors) {
        this.errors.shift();
      }
      
      // Report to server if connected
      this._reportError(error);
      
      // Log to console
      console.error('Error captured by hot reload client:', error);
    }
    
    _reportError(error) {
      if (this.client && this.client.isConnected) {
        this.client.publish(TOPIC_CLIENT_ERROR, error)
          .catch(err => {
            console.error('Failed to report error to server:', err);
          });
      }
    }
    
    _reportReady() {
      if (this.client && this.client.isConnected) {
        this.client.publish(TOPIC_HOTRELOAD_READY, {
          url: window.location.href,
          userAgent: navigator.userAgent,
          timestamp: new Date().toISOString()
        }).catch(err => {
          console.error('Failed to report ready status to server:', err);
        });
      }
    }
    
    _performReload() {
      console.log('Reloading page...');
      window.location.reload();
    }
    
    // Public API
    
    // Manually trigger a reload
    reload() {
      this._performReload();
    }
    
    // Get all captured errors
    getErrors() {
      return [...this.errors];
    }
    
    // Clear all captured errors
    clearErrors() {
      this.errors = [];
    }
    
    // Set auto reload option
    setAutoReload(enabled) {
      this.options.autoReload = !!enabled;
    }
  }
  
  // Export the HotReloadClient
  global.HotReload = {
    Client: HotReloadClient
  };
  
})(typeof self !== 'undefined' ? self : this);
