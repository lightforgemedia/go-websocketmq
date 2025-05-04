// pkg/broker/broker.go
package broker

import (
	"context"

	"github.com/lightforgemedia/go-websocketmq/pkg/model"
)

type Handler func(ctx context.Context, m *model.Message) (*model.Message, error)

type Broker interface {
	Publish(ctx context.Context, m *model.Message) error
	Subscribe(ctx context.Context, topic string, fn Handler) error
	Request(ctx context.Context, req *model.Message, timeoutMs int64) (*model.Message, error)
	Close() error
}
