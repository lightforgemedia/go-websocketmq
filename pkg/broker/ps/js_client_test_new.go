// pkg/broker/ps/js_client_test_new.go
//go:build js_client
// +build js_client

// This file contains tests for the JavaScript client integration.
// Run with: go test -tags=js_client

package ps_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps"
	"github.com/lightforgemedia/go-websocketmq/pkg/broker/ps/testutil"
	"github.com/lightforgemedia/go-websocketmq/pkg/model"
	"github.com/lightforgemedia/go-websocketmq/pkg/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	jsTestdataRoot string
	jsTestOnce     sync.Once
)

func setupJSTestdataRoot() {
	_, filename, _, _ := runtime.Caller(0)
	// Assumes js_client_test.go is in pkg/broker/ps/
	// testdata is ../../../testdata/
	// assets are ../../../assets/
	jsTestdataRoot = filepath.Join(filepath.Dir(filename), "..", "..", "..") // Project root
}

func getJSTestdataRoot() string {
	jsTestOnce.Do(setupJSTestdataRoot)
	return jsTestdataRoot
}

// serveTestFiles serves files from the project's testdata and assets directories.
func serveTestFiles(t *testing.T, w http.ResponseWriter, r *http.Request) {
	projectRoot := getJSTestdataRoot()
	path := r.URL.Path
	t.Logf("File server request for: %s", path)

	var servePath string
	if strings.HasPrefix(path, "/wsmq/") { // For websocketmq.js from assets
		// This matches how cmd/rpcserver/main.go serves it via assets.ScriptHandler
		// We need to serve assets/dist/websocketmq.js when /wsmq/websocketmq.js is requested.
		assetName := strings.TrimPrefix(path, "/wsmq/")
		servePath = filepath.Join(projectRoot, "assets", "dist", assetName)
	} else if strings.HasSuffix(path, ".js") && !strings.Contains(path, "testdata") {
		// General JS files might be from static mocks
		servePath = filepath.Join(projectRoot, "cmd", "rpcserver", "static", strings.TrimPrefix(path, "/"))
	} else { // HTML, CSS from testdata or cmd/rpcserver/static
		// Prioritize testdata for test-specific HTML like simple_test.html
		testdataFilePath := filepath.Join(projectRoot, "testdata", strings.TrimPrefix(path, "/"))
		if _, err := os.Stat(testdataFilePath); err == nil {
			servePath = testdataFilePath
		} else {
			// Fallback to cmd/rpcserver/static for general static assets like style.css
			servePath = filepath.Join(projectRoot, "cmd", "rpcserver", "static", strings.TrimPrefix(path, "/"))
		}
	}

	t.Logf("Attempting to serve file: %s", servePath)
	http.ServeFile(w, r, servePath)
}

// Helper to log browser console output for debugging failed tests
func logBrowserConsole(t *testing.T, page *rod.Page) {
	t.Helper()
	t.Logf("--- Browser Console Logs for URL: %s ---", page.MustInfo().URL)
	entries, err := page.Eval("() => { const logs = []; window.console.history = window.console.history || []; window.console.history.forEach(l => logs.push(l)); return logs; }")
	if err != nil {
		t.Logf("Error getting console logs: %v", err)
		return
	}

	if entries.Value.Str() == "null" || entries.Value.Str() == "" {
		t.Log("(No console logs captured via console.history or array is empty)")
		
		// Try to get console logs via JavaScript evaluation
		consoleLogsJS, err := page.Eval(`() => {
			if (window.console && window.console.logs) {
				return window.console.logs;
			}
			return [];
		}`)

		if err == nil {
			t.Logf("Console logs from JavaScript: %v", consoleLogsJS.Value)
		} else {
			t.Log("Could not retrieve console logs via JavaScript")
		}

		// Trigger a console log for debugging
		page.MustEvaluate(rod.Eval(`() => console.log("--- Go test requesting browser logs ---")`))
		return
	}

	var logs []map[string]interface{}
	if err := json.Unmarshal([]byte(entries.Value.Str()), &logs); err == nil {
		for _, logEntry := range logs {
			t.Logf("Browser Log: [%s] %v", logEntry["type"], logEntry["args"])
		}
	} else {
		t.Logf("Could not parse console.history: %s", entries.Value.Str())
	}
	t.Logf("--- End Browser Console Logs ---")
}

// MockSessionManager for JS tests that don't need full session manager logic
type MockSessionManager struct {
	logger       broker.Logger
	mu           sync.RWMutex
	pageToBroker map[string]string
	brokerToPage map[string]string
}

func NewMockSessionManager(logger broker.Logger) *MockSessionManager {
	return &MockSessionManager{
		logger:       logger,
		pageToBroker: make(map[string]string),
		brokerToPage: make(map[string]string),
	}
}

func (m *MockSessionManager) Store(pageID, brokerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pageToBroker[pageID] = brokerID
	m.brokerToPage[brokerID] = pageID
	m.logger.Debug("MockSessionManager: Stored %s -> %s", pageID, brokerID)
}

func (m *MockSessionManager) GetBrokerClientID(pageSessionID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, found := m.pageToBroker[pageSessionID]
	m.logger.Debug("MockSessionManager: GetBrokerClientID for %s -> (%s, %v)", pageSessionID, id, found)
	return id, found
}

func (m *MockSessionManager) GetPageSessionID(brokerClientID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, found := m.brokerToPage[brokerClientID]
	return id, found
}
