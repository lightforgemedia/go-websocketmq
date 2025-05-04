// Package assets contains embedded assets for the websocketmq package.
package assets

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed dist
var jsFiles embed.FS

// GetFS returns a filesystem containing the embedded JavaScript files.
func GetFS() fs.FS {
	return jsFiles
}

// JSFiles returns a map of filenames to their contents.
func JSFiles() map[string][]byte {
	files := map[string][]byte{}

	// Read the full JS file
	fullJS, err := jsFiles.ReadFile("dist/websocketmq.js")
	if err == nil {
		files["websocketmq.js"] = fullJS
	} else {
		// Log error for debugging
		fmt.Printf("Error reading websocketmq.js: %v\n", err)
	}

	// Read the minified JS file
	minJS, err := jsFiles.ReadFile("dist/websocketmq.min.js")
	if err == nil {
		files["websocketmq.min.js"] = minJS
	} else {
		// Log error for debugging
		fmt.Printf("Error reading websocketmq.min.js: %v\n", err)
	}

	return files
}
