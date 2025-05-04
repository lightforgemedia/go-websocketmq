package assets

import (
	"net/http"
	"path"
	"strings"
	"time"
)

// Handler returns an http.Handler that serves the embedded JavaScript files.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Clean the path to prevent directory traversal
		cleanPath := path.Clean(r.URL.Path)
		cleanPath = strings.TrimPrefix(cleanPath, "/")
		
		// Only serve .js files
		if !strings.HasSuffix(cleanPath, ".js") {
			http.NotFound(w, r)
			return
		}
		
		// Get the file content
		files := JSFiles()
		content, ok := files[cleanPath]
		if !ok {
			http.NotFound(w, r)
			return
		}
		
		// Set appropriate headers
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "public, max-age=86400") // 1 day
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		
		// Write the content
		w.Write(content)
	})
}
