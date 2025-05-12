// Package hotreload provides hot reload functionality for web applications.
package hotreload

import (
	"embed"
)

//go:embed dist/hotreload.js
var clientFiles embed.FS
