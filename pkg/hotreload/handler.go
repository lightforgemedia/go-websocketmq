package hotreload

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ClientScriptOptions configures the JavaScript client handler
type ClientScriptOptions struct {
	// Path is the URL path where the script will be served
	// Default: "/hotreload.js"
	Path string

	// CacheMaxAge sets the Cache-Control max-age directive in seconds
	// Default: 3600 (1 hour)
	CacheMaxAge int
}

// DefaultClientScriptOptions returns the default options for the client script handler
func DefaultClientScriptOptions() ClientScriptOptions {
	return ClientScriptOptions{
		Path:        "/hotreload.js",
		CacheMaxAge: 3600,
	}
}

// ScriptHandler returns an HTTP handler that serves the embedded JavaScript client
func ScriptHandler(options ClientScriptOptions) http.Handler {
	return &scriptHandler{
		options: options,
	}
}

type scriptHandler struct {
	options ClientScriptOptions
}

func (h *scriptHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Get the requested file path
	filePath := strings.TrimPrefix(r.URL.Path, "/")

	// If no specific file is requested, use the default
	if filePath == "" || filePath == strings.TrimPrefix(h.options.Path, "/") {
		filePath = "hotreload.js"
	}

	data, err := GetClientScript()
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	contentType := "application/javascript"
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "max-age="+strconv.Itoa(h.options.CacheMaxAge))

	w.Write(data)
}

// RegisterHandler registers the JavaScript client handler with the provided ServeMux
func RegisterHandler(mux *http.ServeMux, options ClientScriptOptions) {
	handler := ScriptHandler(options)
	mux.Handle(options.Path, handler)
}

// RegisterHandlers registers all handlers for the hot reload service
func (hr *HotReload) RegisterHandlers(mux *http.ServeMux) {
	// Register the broker's handlers with default options
	hr.broker.RegisterHandlersWithDefaults(mux)

	// Register the hot reload JavaScript client handler
	RegisterHandler(mux, DefaultClientScriptOptions())

	// Register status API endpoint
	mux.HandleFunc("/api/hotreload/status", hr.StatusHandler)
}

// StatusHandler handles requests for hot reload status
func (hr *HotReload) StatusHandler(w http.ResponseWriter, r *http.Request) {
	// Get client status
	hr.clientsMu.RLock()
	defer hr.clientsMu.RUnlock()

	// Create status response
	type clientErrorResponse struct {
		Message   string `json:"message"`
		Filename  string `json:"filename,omitempty"`
		Line      int    `json:"line,omitempty"`
		Column    int    `json:"column,omitempty"`
		Stack     string `json:"stack,omitempty"`
		Timestamp string `json:"timestamp"`
	}

	type clientResponse struct {
		ID       string                `json:"id"`
		Status   string                `json:"status"`
		URL      string                `json:"url,omitempty"`
		LastSeen string                `json:"lastSeen"`
		Errors   []clientErrorResponse `json:"errors,omitempty"`
	}

	type statusResponse struct {
		Clients []clientResponse `json:"clients"`
	}

	response := statusResponse{
		Clients: make([]clientResponse, 0, len(hr.clients)),
	}

	for _, client := range hr.clients {
		clientResp := clientResponse{
			ID:       client.id,
			Status:   client.status,
			URL:      client.url,
			LastSeen: client.lastSeen.Format(time.RFC3339),
			Errors:   make([]clientErrorResponse, 0, len(client.errors)),
		}

		for _, err := range client.errors {
			clientResp.Errors = append(clientResp.Errors, clientErrorResponse{
				Message:   err.message,
				Filename:  err.filename,
				Line:      err.lineno,
				Column:    err.colno,
				Stack:     err.stack,
				Timestamp: err.timestamp,
			})
		}

		response.Clients = append(response.Clients, clientResp)
	}

	// Write response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetClientScript returns the raw JavaScript client code
func GetClientScript() ([]byte, error) {
	filename := "dist/hotreload.js"
	return clientFiles.ReadFile(filename)
}
