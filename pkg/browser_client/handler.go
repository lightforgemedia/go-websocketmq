// pkg/browser_client/handler.go
package browser_client

import (
	"net/http"
	"path"
	"strconv"
	"strings"
)

// ClientScriptOptions configures the JavaScript client handler
type ClientScriptOptions struct {
	// Path is the URL path where the script will be served
	// Default: "/websocketmq.js"
	Path string

	// UseMinified determines whether to serve the minified version by default
	// Default: true
	UseMinified bool

	// CacheMaxAge sets the Cache-Control max-age directive in seconds
	// Default: 3600 (1 hour)
	CacheMaxAge int
}

// DefaultClientScriptOptions returns the default options for the client script handler
func DefaultClientScriptOptions() ClientScriptOptions {
	return ClientScriptOptions{
		Path:        "/websocketmq.js",
		UseMinified: true,
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
		if h.options.UseMinified {
			filePath = "websocketmq.min.js"
		} else {
			filePath = "websocketmq.js"
		}
	}

	// Map the requested path to the embedded file path
	embeddedPath := path.Join("dist", filePath)

	data, err := clientFiles.ReadFile(embeddedPath)
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

// GetClientScript returns the raw JavaScript client code
func GetClientScript(minified bool) ([]byte, error) {
	filename := "dist/websocketmq.js"
	if minified {
		filename = "dist/websocketmq.min.js"
	}
	return clientFiles.ReadFile(filename)
}
