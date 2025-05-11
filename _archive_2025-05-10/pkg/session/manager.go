// cmd/rpcserver/session/manager.go
package session

import (
	"context"
	"sync"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker" // For broker.Broker interface and constants
	"github.com/lightforgemedia/go-websocketmq/pkg/model"  // For model.Message
)

// Manager handles the mapping between user-defined PageSessionIDs and internal BrokerClientIDs.
// It listens to broker events to keep its mappings synchronized with client connections.
type Manager struct {
	logger broker.Logger // Use logger interface from the library's broker package
	brk    broker.Broker // Use broker.Broker interface

	// pageToBroker maps PageSessionID (string) to BrokerClientID (string)
	pageToBroker sync.Map
	// brokerToPage maps BrokerClientID (string) to PageSessionID (string)
	brokerToPage sync.Map
}

// NewManager creates a new session manager.
func NewManager(logger broker.Logger, brk broker.Broker) *Manager {
	if logger == nil {
		panic("logger cannot be nil for session.Manager")
	}
	if brk == nil {
		panic("broker cannot be nil for session.Manager")
	}
	m := &Manager{
		logger: logger,
		brk:    brk,
	}
	m.startEventListeners()
	return m
}

// startEventListeners subscribes to relevant broker events for session management.
func (m *Manager) startEventListeners() {
	// Subscribe to client registration events from the broker
	// These events are published by server.Handler after a client sends its PageSessionID.
	err := m.brk.Subscribe(context.Background(), broker.TopicClientRegistered, m.handleClientRegistered)
	if err != nil {
		m.logger.Error("SessionManager: Failed to subscribe to %s: %v", broker.TopicClientRegistered, err)
		// This is a critical failure for session management. Consider panic or retry in a real app.
	} else {
		m.logger.Info("SessionManager: Subscribed to %s", broker.TopicClientRegistered)
	}

	// Subscribe to client deregistration events from the broker
	// These events are published by broker.DeregisterConnection.
	err = m.brk.Subscribe(context.Background(), broker.TopicClientDeregistered, m.handleClientDeregistered)
	if err != nil {
		m.logger.Error("SessionManager: Failed to subscribe to %s: %v", broker.TopicClientDeregistered, err)
	} else {
		m.logger.Info("SessionManager: Subscribed to %s", broker.TopicClientDeregistered)
	}
}

// handleClientRegistered processes client registration events from the broker.
// Expecting Body to be map[string]string{"pageSessionID": "...", "brokerClientID": "..."}
func (m *Manager) handleClientRegistered(ctx context.Context, msg *model.Message, sourceBrokerClientID string) (*model.Message, error) {
	// sourceBrokerClientID is not typically relevant for these internal events, but available.
	
	var pageSessionID, brokerClientID string
	
	// The event body should be map[string]string as published by server.Handler
	bodyMap, ok := msg.Body.(map[string]string)
	if !ok {
		// Handle case where it might be unmarshaled as map[string]interface{} if not strictly typed by intermediate layers
		if genericBodyMap, ok := msg.Body.(map[string]interface{}); ok {
			pageSessionIDStr, psOk := genericBodyMap["pageSessionID"].(string)
			brokerClientIDStr, bcOk := genericBodyMap["brokerClientID"].(string)
			if psOk && bcOk {
				pageSessionID = pageSessionIDStr
				brokerClientID = brokerClientIDStr
			} else {
				m.logger.Error("SessionManager: Invalid body type/content for %s. Expected pageSessionID and brokerClientID strings. Body: %+v", broker.TopicClientRegistered, msg.Body)
				return nil, nil // No response needed for internal event
			}
		} else {
			m.logger.Error("SessionManager: Invalid body type for %s: not map[string]string or map[string]interface{}. Body: %+v", broker.TopicClientRegistered, msg.Body)
			return nil, nil 
		}
	} else {
		pageSessionID = bodyMap["pageSessionID"]
		brokerClientID = bodyMap["brokerClientID"]
	}


	if pageSessionID == "" || brokerClientID == "" {
		m.logger.Error("SessionManager: Malformed %s event: missing pageSessionID or brokerClientID. Body: %+v", broker.TopicClientRegistered, msg.Body)
		return nil, nil
	}

	m.logger.Debug("SessionManager: Processing %s event: PageID=%s, BrokerID=%s", broker.TopicClientRegistered, pageSessionID, brokerClientID)

	// Atomically clean up old associations and store new ones.
	// If PageSessionID was previously mapped to an old BrokerClientID, remove that old BrokerClientID's reverse mapping.
	if oldBrokerIDVal, loaded := m.pageToBroker.Load(pageSessionID); loaded {
		if oldBrokerID, ok := oldBrokerIDVal.(string); ok && oldBrokerID != brokerClientID {
			m.brokerToPage.Delete(oldBrokerID)
			m.logger.Info("SessionManager: Cleaned old brokerToPage mapping for PageSessionID %s (was BrokerID %s, now %s)", pageSessionID, oldBrokerID, brokerClientID)
		}
	}
	// If BrokerClientID was previously mapped to an old PageSessionID (less likely but possible if events are out of order), remove that.
	if oldPageIDVal, loaded := m.brokerToPage.Load(brokerClientID); loaded {
		if oldPageID, ok := oldPageIDVal.(string); ok && oldPageID != pageSessionID {
			m.pageToBroker.Delete(oldPageID)
			m.logger.Info("SessionManager: Cleaned old pageToBroker mapping for BrokerID %s (was PageID %s, now %s)", brokerClientID, oldPageID, pageSessionID)
		}
	}
	
	m.pageToBroker.Store(pageSessionID, brokerClientID)
	m.brokerToPage.Store(brokerClientID, pageSessionID)
	m.logger.Info("SessionManager: Registered session. PageSessionID: %s <-> BrokerClientID: %s", pageSessionID, brokerClientID)
	return nil, nil // No response needed for internal event
}

// handleClientDeregistered processes client deregistration events from the broker.
// Expecting Body to be map[string]string{"brokerClientID": "..."}
func (m *Manager) handleClientDeregistered(ctx context.Context, msg *model.Message, sourceBrokerClientID string) (*model.Message, error) {
	var brokerClientID string

	bodyMap, ok := msg.Body.(map[string]string)
	if !ok {
		if genericBodyMap, ok := msg.Body.(map[string]interface{}); ok {
			brokerClientIDStr, bcOk := genericBodyMap["brokerClientID"].(string)
			if bcOk {
				brokerClientID = brokerClientIDStr
			} else {
				m.logger.Error("SessionManager: Invalid body type/content for %s. Expected brokerClientID string. Body: %+v", broker.TopicClientDeregistered, msg.Body)
				return nil, nil
			}
		} else {
			m.logger.Error("SessionManager: Invalid body type for %s: not map[string]string or map[string]interface{}. Body: %+v", broker.TopicClientDeregistered, msg.Body)
			return nil, nil
		}
	} else {
		brokerClientID = bodyMap["brokerClientID"]
	}

	if brokerClientID == "" {
		m.logger.Error("SessionManager: Malformed %s event: missing brokerClientID. Body: %+v", broker.TopicClientDeregistered, msg.Body)
		return nil, nil
	}

	m.logger.Debug("SessionManager: Processing %s event for BrokerID: %s", broker.TopicClientDeregistered, brokerClientID)

	// Remove the BrokerClientID and its associated PageSessionID from mappings.
	if pageSessionIDVal, loaded := m.brokerToPage.LoadAndDelete(brokerClientID); loaded {
		if pageSessionID, ok := pageSessionIDVal.(string); ok {
			m.pageToBroker.Delete(pageSessionID) // Delete the reverse mapping too
			m.logger.Info("SessionManager: Deregistered session for BrokerClientID: %s (was PageSessionID: %s)", brokerClientID, pageSessionID)
		} else {
			m.logger.Warn("SessionManager: Value for BrokerClientID %s in brokerToPage was not a string: %T", brokerClientID, pageSessionIDVal)
		}
	} else {
		// This can happen if deregister is called multiple times or if registration never fully completed in manager.
		m.logger.Warn("SessionManager: Received deregistration for unknown or already removed BrokerClientID: %s", brokerClientID)
	}
	return nil, nil // No response needed for internal event
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
	if !ok {
		// This would indicate an internal issue with stored type.
		m.logger.Error("SessionManager: Value for PageSessionID %s in pageToBroker was not a string: %T", pageSessionID, val)
		return "", false
	}
	return brokerID, true
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
	if !ok {
		m.logger.Error("SessionManager: Value for BrokerClientID %s in brokerToPage was not a string: %T", brokerClientID, val)
		return "", false
	}
	return pageID, true
}