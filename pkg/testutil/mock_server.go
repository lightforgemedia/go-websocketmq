package testutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/lightforgemedia/go-websocketmq/pkg/ergosockets"
)

// MockServer represents a mock WebSocket server for testing clients.
type MockServer struct {
	T          *testing.T
	Server     *httptest.Server
	WsURL      string
	Conn       *websocket.Conn
	ConnMu     sync.Mutex
	Handler    func(conn *websocket.Conn, ms *MockServer)
	ActiveConn context.CancelFunc // To signal mock server's read loop to stop for current conn
	wg         sync.WaitGroup     // For Expect functionality
}

// NewMockServer creates a new mock WebSocket server for testing clients.
func NewMockServer(t *testing.T, handlerFunc func(conn *websocket.Conn, ms *MockServer)) *MockServer {
	t.Helper()
	ms := &MockServer{T: t, Handler: handlerFunc}

	ms.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connCtx, connCancel := context.WithCancel(context.Background())
		ms.ActiveConn = connCancel // Store cancel func for this connection

		wsconn, err := websocket.Accept(w, r, nil)
		if err != nil {
			ms.T.Logf("MockServer: Accept error: %v", err)
			connCancel()
			return
		}

		ms.ConnMu.Lock()
		ms.Conn = wsconn
		ms.ConnMu.Unlock()

		// Run the handler in a goroutine
		go func() {
			defer connCancel()
			if ms.Handler != nil {
				ms.Handler(wsconn, ms)
			}
		}()

		// Wait for the connection to be closed
		<-connCtx.Done()
	}))

	ms.WsURL = "ws" + ms.Server.URL[4:] // Convert http:// to ws://

	// Setup cleanup
	t.Cleanup(func() {
		ms.Close()
	})

	return ms
}

// Send sends an envelope to the connected client.
func (ms *MockServer) Send(env ergosockets.Envelope) error {
	ms.ConnMu.Lock()
	defer ms.ConnMu.Unlock()

	if ms.Conn == nil {
		return nil // No connection, silently ignore
	}

	err := wsjson.Write(context.Background(), ms.Conn, env)
	if err == nil {
		ms.wg.Add(-1)
		ms.T.Logf("MockServer: Sent envelope and fulfilled expectation")
	}
	return err
}

// Handle sets a handler function for processing incoming envelopes
func (ms *MockServer) Handle(handler func(env *ergosockets.Envelope) *ergosockets.Envelope) {
	ms.Handler = func(conn *websocket.Conn, srv *MockServer) {
		for {
			var reqEnv ergosockets.Envelope
			err := wsjson.Read(context.Background(), conn, &reqEnv)
			if err != nil {
				return
			}

			respEnv := handler(&reqEnv)
			if respEnv != nil {
				srv.Send(*respEnv)
			}
		}
	}
}

// Expect sets an expectation for a number of messages to be sent
func (ms *MockServer) Expect(n int) {
	ms.wg.Add(n)
}

// CloseCurrentConnection closes the current WebSocket connection.
func (ms *MockServer) CloseCurrentConnection() {
	ms.ConnMu.Lock()
	defer ms.ConnMu.Unlock()

	if ms.Conn != nil {
		ms.Conn.Close(websocket.StatusNormalClosure, "Test closing connection")
		ms.Conn = nil
	}

	if ms.ActiveConn != nil {
		ms.ActiveConn()
		ms.ActiveConn = nil
	}
}

// Close closes the mock server.
func (ms *MockServer) Close() {
	ms.CloseCurrentConnection()
	if ms.Server != nil {
		ms.Server.Close()
	}
}
