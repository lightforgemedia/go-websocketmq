// Initialize the WebSocketMQ client when the page loads
// Make client a global variable
window.client = null;

// Set up the client and connect automatically when the page loads
window.addEventListener('DOMContentLoaded', () => {
    console.log('Page loaded, connecting with default settings');

    // Create a new WebSocketMQ client with default settings
    // The client should automatically determine the WebSocket URL
    try {
        console.log('Creating client with explicit WebSocket URL');
        const wsUrl = window.location.origin.replace('http', 'ws') + '/wsmq';
        console.log('Using WebSocket URL:', wsUrl);

        window.client = new WebSocketMQ.Client({});
        console.log('Client created successfully');
    } catch (err) {
        console.error('Error creating client:', err);
    }

    window.client.onConnect(() => {
        console.log('Connected to WebSocket server with ID:', window.client.getID());
        document.getElementById('status').textContent = 'Connected';
        document.getElementById('status').style.color = 'green';
    });

    window.client.onDisconnect(() => {
        console.log('Disconnected from WebSocket server');
        document.getElementById('status').textContent = 'Disconnected';
        document.getElementById('status').style.color = 'red';
    });

    // Add error handler
    window.client.onError((error) => {
        console.error('WebSocket error:', error);
    });

    // Connect automatically with error handling
    try {
        console.log('Calling client.connect()');
        window.client.connect();
        console.log('client.connect() called successfully');
    } catch (err) {
        console.error('Error connecting to WebSocket:', err);
    }
});