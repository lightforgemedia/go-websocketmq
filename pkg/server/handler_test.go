// pkg/server/handler_test.go
package server

import (
"context"
"net/http/httptest"
"strings"
"testing"
"time"

"github.com/lightforgemedia/go-websocketmq/pkg/broker"
"github.com/lightforgemedia/go-websocketmq/pkg/model"
"nhooyr.io/websocket"
)

// MockBroker is a mock implementation of the broker.Broker interface
type MockBroker struct {
PublishFunc   func(ctx context.Context, m *model.Message) error
SubscribeFunc func(ctx context.Context, topic string, fn broker.Handler) error
RequestFunc   func(ctx context.Context, req *model.Message, timeoutMs int64) (*model.Message, error)
CloseFunc     func() error
}

func (b *MockBroker) Publish(ctx context.Context, m *model.Message) error {
return b.PublishFunc(ctx, m)
}

func (b *MockBroker) Subscribe(ctx context.Context, topic string, fn broker.Handler) error {
return b.SubscribeFunc(ctx, topic, fn)
}

func (b *MockBroker) Request(ctx context.Context, req *model.Message, timeoutMs int64) (*model.Message, error) {
return b.RequestFunc(ctx, req, timeoutMs)
}

func (b *MockBroker) Close() error {
if b.CloseFunc != nil {
return b.CloseFunc()
}
return nil
}

func TestHandler_ServeHTTP(t *testing.T) {
// Create a mock broker
mockBroker := &MockBroker{
PublishFunc: func(ctx context.Context, m *model.Message) error {
return nil
},
SubscribeFunc: func(ctx context.Context, topic string, fn broker.Handler) error {
return nil
},
RequestFunc: func(ctx context.Context, req *model.Message, timeoutMs int64) (*model.Message, error) {
return nil, nil
},
CloseFunc: func() error {
return nil
},
}

// Create a handler
handler := New(mockBroker)

// Create a test server
server := httptest.NewServer(handler)
defer server.Close()

// Create a WebSocket URL
wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

// Test connecting to the WebSocket
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

conn, _, err := websocket.Dial(ctx, wsURL, nil)
if err != nil {
t.Fatalf("Error connecting to WebSocket: %v", err)
}
defer conn.Close(websocket.StatusNormalClosure, "")
}

func TestHandler_Integration(t *testing.T) {
// We're using the integration_test.go file for integration tests now
t.Skip("Integration tests moved to integration_test.go")
}
