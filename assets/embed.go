// assets/embed.go
package assets

import (
	"embed"
)

//go:embed dist/websocketmq.js dist/websocketmq.min.js
var clientFiles embed.FS