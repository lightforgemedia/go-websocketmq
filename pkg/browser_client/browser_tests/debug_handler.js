// Debug script to test handler registration
console.log('Debug script loaded');

function testHandlerRegistration() {
    console.log('Testing handler registration');
    
    // Create a simple handler function
    const handler = function(payload) {
        console.log('Handler called with payload:', payload);
        return { status: 'ok' };
    };
    
    console.log('Handler type:', typeof handler);
    console.log('Handler is function:', handler instanceof Function);
    console.log('Handler.constructor:', handler.constructor);
    console.log('Handler.toString():', handler.toString());
    
    // Try to register the handler
    try {
        if (window.client) {
            console.log('Client exists:', window.client);
            console.log('Client.handleServerRequest type:', typeof window.client.handleServerRequest);
            
            // Try to register the handler
            window.client.handleServerRequest('test:topic', handler);
            console.log('Handler registered successfully');
        } else {
            console.log('Client not found');
        }
    } catch (error) {
        console.error('Error registering handler:', error);
        console.error('Error stack:', error.stack);
    }
}

// Export the test function
window.testHandlerRegistration = testHandlerRegistration;