// cmd/rpcserver/api/handler.go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/lightforgemedia/go-websocketmq/pkg/broker" // For broker.Broker interface, error types, and constants
	"github.com/lightforgemedia/go-websocketmq/pkg/model"  // For model.Message and factory functions
	"github.com/lightforgemedia/go-websocketmq/pkg/session"
)

// RequestPayload is the expected JSON structure for API requests.
type RequestPayload struct {
	SessionID string                 `json:"sessionId"`
	Selector  string                 `json:"selector,omitempty"`
	Text      string                 `json:"text,omitempty"`
	URL       string                 `json:"url,omitempty"`
	Params    map[string]interface{} `json:"params,omitempty"` // For generic actions or additional parameters
}

// Response is a generic JSON response structure for API calls.
type Response struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"` // General message, e.g., "Action executed successfully"
	Data    interface{} `json:"data,omitempty"`    // Payload from the client's RPC response
	Error   string      `json:"error,omitempty"`   // Error message if Success is false
}

// Handler provides HTTP handlers for API endpoints that trigger RPC calls to browser clients.
type Handler struct {
	logger         broker.Logger // Use logger interface from the library
	broker         broker.Broker // Use broker interface from the library
	sessionManager *session.Manager
	requestTimeout time.Duration // Default timeout for RPC calls to browser clients
}

// NewHandler creates a new API handler.
func NewHandler(logger broker.Logger, brk broker.Broker, sm *session.Manager) *Handler {
	if logger == nil || brk == nil || sm == nil {
		panic("logger, broker, and sessionManager must not be nil for api.Handler")
	}
	return &Handler{
		logger:         logger,
		broker:         brk,
		sessionManager: sm,
		requestTimeout: 20 * time.Second, // Default timeout for browser actions, can be overridden
	}
}

// sendJSONResponse is a helper to marshal and send JSON responses.
func (h *Handler) sendJSONResponse(w http.ResponseWriter, statusCode int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// This error occurs if writing to ResponseWriter fails, client might have disconnected.
		h.logger.Error("API Handler: Failed to encode or write JSON response: %v", err)
	}
}

// HandleAction is a generic handler for various browser actions.
// The action name (e.g., "browser.click", "browser.input") is the RPC topic the client listens to.
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
			h.logger.Warn("API Handler: No active client (BrokerClientID) found for PageSessionID: %s", payload.SessionID)
			h.sendJSONResponse(w, http.StatusNotFound, Response{Success: false, Error: "Client session not found or not active for PageSessionID: " + payload.SessionID})
			return
		}

		// Prepare parameters for the browser action RPC
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
		// Merge generic params if any, allowing overrides of specific params if keys match
		if payload.Params != nil {
			for k, v := range payload.Params {
				actionParams[k] = v
			}
		}

		// Some actions might not require specific parameters beyond the action name itself.
		// Example: browser.screenshot, browser.getPageSource
		if len(actionParams) == 0 {
			h.logger.Debug("API Handler: Action '%s' called with no specific parameters in payload (using empty map for RPC body).", actionName)
		}

		// Create the request message for the client using library's model.NewRequest
		// The Topic for the RPC is the actionName.
		// Use a context for the RPC call, derived from the HTTP request's context.
		// This allows cancellation to propagate if the HTTP client disconnects.
		rpcCtx, rpcCancel := context.WithTimeout(r.Context(), h.requestTimeout)
		defer rpcCancel()

		requestMsg := model.NewRequest(actionName, actionParams, int64(h.requestTimeout/time.Millisecond))

		h.logger.Info("API Handler: Sending action '%s' (RPC Topic) to BrokerClientID: %s (PageSessionID: %s) with params: %+v",
			actionName, brokerClientID, payload.SessionID, actionParams)

		responseMsg, err := h.broker.RequestToClient(rpcCtx, brokerClientID, requestMsg, int64(h.requestTimeout/time.Millisecond))

		if err != nil {
			h.logger.Error("API Handler: Error from broker.RequestToClient for action '%s' on client %s: %v", actionName, brokerClientID, err)
			errMsg := "Failed to execute action on client"
			httpStatus := http.StatusInternalServerError

			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, broker.ErrRequestTimeout) {
				errMsg = "Action timed out on client"
				httpStatus = http.StatusGatewayTimeout // 504 might be more appropriate for timeout
			} else if errors.Is(err, broker.ErrClientNotFound) {
				errMsg = "Client disconnected or not found by broker"
				httpStatus = http.StatusNotFound // Or 503 Service Unavailable if client is temporarily unavailable
				// SessionManager should react to broker.TopicClientDeregistered for cleanup.
			} else if errors.Is(err, broker.ErrConnectionWrite) {
				errMsg = "Failed to send action to client (connection issue)"
				// This also implies client might be gone.
			}
			h.sendJSONResponse(w, httpStatus, Response{Success: false, Error: errMsg, Data: fmt.Sprintf("Details: %v", err)})
			return
		}

		// Check if the response from the client indicates an error within its execution
		if responseMsg.Header.Type == model.KindError {
			h.logger.Warn("API Handler: Action '%s' on client %s resulted in an error response: %+v", actionName, brokerClientID, responseMsg.Body)
			var clientErrorMsg string
			if errMap, ok := responseMsg.Body.(map[string]interface{}); ok {
				if errMsg, ok := errMap["error"].(string); ok { // Standard error field
					clientErrorMsg = errMsg
				} else if msgStr, ok := errMap["message"].(string); ok { // Alternative error field
					clientErrorMsg = msgStr
				}
			}
			if clientErrorMsg == "" {
				// If body is just a string, use that.
				if errStr, ok := responseMsg.Body.(string); ok {
					clientErrorMsg = errStr
				} else {
					clientErrorMsg = "Unknown error from client execution"
				}
			}
			// Client executed, but reported an error. This is often a 200 OK from HTTP perspective,
			// with success:false in the JSON body.
			h.sendJSONResponse(w, http.StatusOK, Response{Success: false, Message: "Action resulted in an error on the client", Error: clientErrorMsg, Data: responseMsg.Body})
			return
		}

		h.logger.Info("API Handler: Action '%s' successful for client %s. Response Body: %+v", actionName, brokerClientID, responseMsg.Body)
		h.sendJSONResponse(w, http.StatusOK, Response{Success: true, Message: "Action executed successfully", Data: responseMsg.Body})
	}
}
