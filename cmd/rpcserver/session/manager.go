// cmd/rpcserver/session/manager.go
package session

import (
	"context"
	"sync"

	"github.com/lightforgemedia/go-websocketmq" // Import the updated library
)

// Manager handles the mapping between user-defined PageSessionIDs and internal BrokerClientIDs.
type Manager struct {
	logger websocketmq.Logger
	broker websocketmq.Broker // To subscribe to broker events

	// pageToBroker maps PageSessionID (string) to BrokerClientID (string)
	pageToBroker sync.Map
	// brokerToPage maps BrokerClientID (string) to PageSessionID (string)
	brokerToPage sync.Map
}

// NewManager creates a new session manager.
func NewManager(logger websocketmq.Logger, broker websocketmq.Broker) *Manager {
	m := &Manager{
		logger: logger,
		broker: broker,
	}
	m.startEventListeners()
	return m
}

// startEventListeners subscribes to relevant broker events for session management.
func (m *Manager) startEventListeners() {
	// Subscribe to client registration events
	err := m.broker.Subscribe(context.Background(), websocketmq.TopicClientRegistered, m.handleClientRegistered)
	if err != nil {
		m.logger.Error("SessionManager: Failed to subscribe to TopicClientRegistered: %v", err)
		// This is a critical failure for session management. Consider panic or retry.
	} else {
		m.logger.Info("SessionManager: Subscribed to TopicClientRegistered")
	}

	// Subscribe to client deregistration events
	err = m.broker.Subscribe(context.Background(), websocketmq.TopicClientDeregistered, m.handleClientDeregistered)
	if err != nil {
		m.logger.Error("SessionManager: Failed to subscribe to TopicClientDeregistered: %v", err)
	} else {
		m.logger.Info("SessionManager: Subscribed to TopicClientDeregistered")
	}
}

// handleClientRegistered processes client registration events from the broker.
func (m *Manager) handleClientRegistered(ctx context.Context, msg *websocketmq.Message, sourceBrokerClientID string) (*websocketmq.Message, error) {
	bodyMap, ok := msg.Body.(map[string]string) // Expecting map[string]string from broker event
	if !ok {
		m.logger.Error("SessionManager: Invalid body for TopicClientRegistered: not map[string]string. Body: %+v", msg.Body)
		return nil, nil // No response needed for internal event
	}

	pageSessionID, pExists := bodyMap["pageSessionID"]
	brokerClientID, bExists := bodyMap["brokerClientID"]

	if !pExists || !bExists || pageSessionID == "" || brokerClientID == "" {
		m.logger.Error("SessionManager: Malformed TopicClientRegistered event: missing pageSessionID or brokerClientID. Body: %+v", bodyMap)
		return nil, nil
	}

	// Clean up any old associations for this pageSessionID or brokerClientID
	if oldBrokerID, loaded := m.pageToBroker.Load(pageSessionID); loaded {
		m.brokerToPage.Delete(oldBrokerID.(string))
	}
	if oldPageID, loaded := m.brokerToPage.Load(brokerClientID); loaded {
		m.pageToBroker.Delete(oldPageID.(string))
	}
	
	m.pageToBroker.Store(pageSessionID, brokerClientID)
	m.brokerToPage.Store(brokerClientID, pageSessionID)
	m.logger.Info("SessionManager: Registered session. PageSessionID: %s <-> BrokerClientID: %s", pageSessionID, brokerClientID)
	return nil, nil
}

// handleClientDeregistered processes client deregistration events from the broker.
func (m *Manager) handleClientDeregistered(ctx context.Context, msg *websocketmq.Message, sourceBrokerClientID string) (*websocketmq.Message, error) {
	bodyMap, ok := msg.Body.(map[string]string)
	if !ok {
		m.logger.Error("SessionManager: Invalid body for TopicClientDeregistered: not map[string]string. Body: %+v", msg.Body)
		return nil, nil
	}

	brokerClientID, exists := bodyMap["brokerClientID"]
	if !exists || brokerClientID == "" {
		m.logger.Error("SessionManager: Malformed TopicClientDeregistered event: missing brokerClientID. Body: %+v", bodyMap)
		return nil, nil
	}

	if pageSessionIDVal, loaded := m.brokerToPage.LoadAndDelete(brokerClientID); loaded {
		if pageSessionID, ok := pageSessionIDVal.(string); ok {
			m.pageToBroker.Delete(pageSessionID)
			m.logger.Info("SessionManager: Deregistered session for BrokerClientID: %s (was PageSessionID: %s)", brokerClientID, pageSessionID)
		}
	} else {
		m.logger.Warn("SessionManager: Received deregistration for unknown BrokerClientID: %s", brokerClientID)
	}
	return nil, nil
}

// GetBrokerClientID retrieves the BrokerClientID for a given PageSessionID.
func (m *Manager) GetBrokerClientID(pageSessionID string) (string, bool) {
	if pageSessionID == "" {
		return "", false
	}
	val, ok := m.pageToBroker.Load(pageSessionID)
	if !ok {
		return "", false
	}
	brokerID, ok := val.(string)
	return brokerID, ok
}

// GetPageSessionID retrieves the PageSessionID for a given BrokerClientID.
func (m *Manager) GetPageSessionID(brokerClientID string) (string, bool) {
	if brokerClientID == "" {
		return "", false
	}
	val, ok := m.brokerToPage.Load(brokerClientID)
	if !ok {
		return "", false
	}
	pageID, ok := val.(string)
	return pageID, ok
}