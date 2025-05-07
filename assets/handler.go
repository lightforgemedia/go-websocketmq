// assets/handler.go
package assets

import (
	"net/http"
	"path"
	"strings"
)

// ScriptHandler returns an HTTP handler that serves the embedded JavaScript client
func ScriptHandler() http.Handler {
	return &scriptHandler{}
}

type scriptHandler struct{}

func (h *scriptHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Get the requested file path
	filePath := strings.TrimPrefix(r.URL.Path, "/")
	if filePath == "" {
		filePath = "websocketmq.min.js" // Default to minified version
	}

	// Map the requested path to the embedded file path
	embeddedPath := path.Join("dist", filePath)

	data, err := clientFiles.ReadFile(embeddedPath)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	contentType := "application/javascript"
	if strings.HasSuffix(filePath, ".map") {
		contentType = "application/json" // Source maps if you generate them
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "max-age=3600") 
	
	w.Write(data)
}

func GetClientScript(minified bool) ([]byte, error) {
	var filename string
	if minified {
		filename = "dist/websocketmq.min.js"
	} else {
		filename = "dist/websocketmq.js"
	}
	return clientFiles.ReadFile(filename)
}