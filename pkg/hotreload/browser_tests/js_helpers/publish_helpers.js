// publish_helpers.js
// Helper functions for publishing messages to topics

// Wait for client to be connected before publishing
function waitForConnection(callback, interval = 100) {
  if (window.client && window.client.isConnected) {
    callback();
  } else {
    setTimeout(() => waitForConnection(callback, interval), interval);
  }
}

// Publish a message to a topic
function publishMessage(topic, payload) {
  console.log(`Publishing message to ${topic} with payload:`, payload);
  
  // Update UI to show publish is in progress
  const statusElement = document.getElementById('publish-status');
  if (statusElement) {
    statusElement.textContent = 'Publishing...';
  }
  
  // Make sure client is connected
  waitForConnection(() => {
    // Publish the message
    window.client.publish(topic, payload)
      .then(() => {
        console.log(`Published message to ${topic}`);
        
        // Update UI with success
        if (statusElement) {
          statusElement.textContent = 'Published successfully';
          statusElement.style.color = 'green';
        }
        
        // Dispatch a custom event for test detection
        const event = new CustomEvent('publishComplete', { 
          detail: { success: true, topic, payload } 
        });
        document.dispatchEvent(event);
      })
      .catch(error => {
        console.error(`Error publishing message to ${topic}:`, error);
        
        // Update UI with error
        if (statusElement) {
          statusElement.textContent = `Error: ${error.message || error}`;
          statusElement.style.color = 'red';
        }
        
        // Dispatch a custom event for test detection
        const event = new CustomEvent('publishComplete', { 
          detail: { success: false, topic, error } 
        });
        document.dispatchEvent(event);
      });
  });
}

// Publish a test message
function publishTestMessage() {
  publishMessage('test:broadcast', {
    messageId: 'client-' + Date.now(),
    timestamp: new Date().toISOString(),
    content: 'Hello from the client!'
  });
}

// Publish multiple messages
function publishMultipleMessages(count = 3) {
  console.log(`Publishing ${count} messages`);
  
  for (let i = 0; i < count; i++) {
    setTimeout(() => {
      publishMessage('test:broadcast', {
        messageId: 'client-' + Date.now() + '-' + i,
        timestamp: new Date().toISOString(),
        content: `Message ${i+1} from the client`
      });
    }, i * 100); // Small delay between messages
  }
}

// Initialize publish buttons if they exist
document.addEventListener('DOMContentLoaded', () => {
  const publishBtn = document.getElementById('publish-btn');
  if (publishBtn) {
    publishBtn.addEventListener('click', publishTestMessage);
  }
  
  const multiPublishBtn = document.getElementById('multi-publish-btn');
  if (multiPublishBtn) {
    multiPublishBtn.addEventListener('click', () => publishMultipleMessages());
  }
});
