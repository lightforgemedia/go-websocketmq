// pkg/browser_client/embed.go
package browser_client

import (
	"embed"
)

//go:generate go run build.go

//go:embed dist/websocketmq.js
var clientFiles embed.FS
