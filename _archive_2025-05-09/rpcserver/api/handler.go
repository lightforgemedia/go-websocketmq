// cmd/rpcserver/api/handler.go
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/lightforgemedia/go-websocketmq" // Import the updated library
	"github.com/lightforgemedia/go-websocketmq/cmd/rpcserver/session"
)

// RequestPayload is the expected JSON structure for API requests.
type RequestPayload struct {
	SessionID string                 `json:"sessionId"`
	Selector  string                 `json:"selector,omitempty"`
	Text      string                 `json:"text,omitempty"`
	URL       string                 `json:"url,omitempty"`
	Params    map[string]interface{} `json:"params,omitempty"` // For generic actions
}

// Response is a generic JSON response structure.
type Response struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// Handler provides HTTP handlers for API endpoints.
type Handler struct {
	logger         websocketmq.Logger
	broker         websocketmq.Broker
	sessionManager *session.Manager
	requestTimeout time.Duration
}

// NewHandler creates a new API handler.
func NewHandler(logger websocketmq.Logger, broker websocketmq.Broker, sm *session.Manager) *Handler {
	return &Handler{
		logger:         logger,
		broker:         broker,
		sessionManager: sm,
		requestTimeout: 15 * time.Second, // Default timeout for browser actions
	}
}

func (h *Handler) sendJSONResponse(w http.ResponseWriter, statusCode int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		h.logger.Error("API Handler: Failed to encode JSON response: %v", err)
	}
}

// HandleAction is a generic handler for various browser actions.
// The action name (e.g., "browser.click", "browser.input") is passed as a parameter.
func (h *Handler) HandleAction(actionName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			h.sendJSONResponse(w, http.StatusMethodNotAllowed, Response{Success: false, Error: "Method not allowed"})
			return
		}

		var payload RequestPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			h.sendJSONResponse(w, http.StatusBadRequest, Response{Success: false, Error: "Invalid JSON payload: " + err.Error()})
			return
		}
		defer r.Body.Close()

		if payload.SessionID == "" {
			h.sendJSONResponse(w, http.StatusBadRequest, Response{Success: false, Error: "sessionId is required"})
			return
		}

		brokerClientID, found := h.sessionManager.GetBrokerClientID(payload.SessionID)
		if !found {
			h.logger.Warn("API Handler: No active client found for SessionID: %s", payload.SessionID)
			h.sendJSONResponse(w, http.StatusNotFound, Response{Success: false, Error: "Client session not found or not active"})
			return
		}

		// Prepare parameters for the browser action
		actionParams := make(map[string]interface{})
		if payload.Selector != "" {
			actionParams["selector"] = payload.Selector
		}
		if payload.Text != "" {
			actionParams["text"] = payload.Text
		}
		if payload.URL != "" {
			actionParams["url"] = payload.URL
		}
		// Merge generic params if any
		if payload.Params != nil {
			for k, v := range payload.Params {
				actionParams[k] = v
			}
		}
		if len(actionParams) == 0 && actionName != "browser.screenshot" && actionName != "browser.getPageSource" { // Some actions might not need params
			// For actions like click, selector is usually required. This is a basic check.
			// Specific validation should happen based on actionName.
			// h.sendJSONResponse(w, http.StatusBadRequest, Response{Success: false, Error: "Missing required parameters for action: " + actionName})
			// return
			h.logger.Debug("API Handler: Action %s called with no specific params, using empty map.", actionName)
		}


		// Create the request message for the client
		// The Topic is the action name the client is subscribed to.
		requestMsg := websocketmq.NewRequest(actionName, actionParams, int64(h.requestTimeout/time.Millisecond))

		h.logger.Info("API Handler: Sending action '%s' to BrokerClientID: %s (PageSessionID: %s) with params: %+v",
			actionName, brokerClientID, payload.SessionID, actionParams)

		responseMsg, err := h.broker.RequestToClient(r.Context(), brokerClientID, requestMsg, int64(h.requestTimeout/time.Millisecond))

		if err != nil {
			h.logger.Error("API Handler: Error calling RequestToClient for action '%s' on client %s: %v", actionName, brokerClientID, err)
			errMsg := "Failed to execute action on client"
			if err == websocketmq.ErrClientNotFound {
				errMsg = "Client disconnected or not found"
				// Proactively try to clean up session if client is not found by broker
				mngPageID, _ := h.sessionManager.GetPageSessionID(brokerClientID)
				if mngPageID == payload.SessionID { // ensure it's the same session we are trying to remove
					// This might be redundant if broker's DeregisterConnection already published an event
					// but serves as a fallback.
					h.logger.Info("API Handler: Client %s not found by broker, attempting to remove from session manager.", brokerClientID)
					// Directly removing here. Ideally, this is handled by broker disconnect events.
					// m.sessionManager.RemoveByBrokerClientID(brokerClientID)
				}
			} else if err == websocketmq.ErrRequestTimeout {
				errMsg = "Action timed out on client"
			} else if err == websocketmq.ErrConnectionWrite {
				errMsg = "Failed to send action to client (connection issue)"
			}
			h.sendJSONResponse(w, http.StatusInternalServerError, Response{Success: false, Error: errMsg, Data: fmt.Sprintf("Details: %v", err)})
			return
		}

		// Check if the response from the client indicates an error
		if responseMsg.Header.Type == "error" {
			h.logger.Warn("API Handler: Action '%s' on client %s resulted in an error: %+v", actionName, brokerClientID, responseMsg.Body)
			var clientErrorMsg string
			if errMap, ok := responseMsg.Body.(map[string]interface{}); ok {
				if errMsg, ok := errMap["error"].(string); ok {
					clientErrorMsg = errMsg
				} else if errStr, ok := errMap["message"].(string); ok { // some errors might use "message"
					clientErrorMsg = errStr
				}
			}
			if clientErrorMsg == "" {
				clientErrorMsg = "Unknown error from client execution"
			}
			h.sendJSONResponse(w, http.StatusOK, Response{Success: false, Message: "Action resulted in an error on the client", Error: clientErrorMsg, Data: responseMsg.Body})
			return
		}

		h.logger.Info("API Handler: Action '%s' successful for client %s. Response: %+v", actionName, brokerClientID, responseMsg.Body)
		h.sendJSONResponse(w, http.StatusOK, Response{Success: true, Message: "Action executed successfully", Data: responseMsg.Body})
	}
}