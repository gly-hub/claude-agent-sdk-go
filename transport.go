package claudeagentsdk

import "context"

type Transport interface {
	Connect(ctx context.Context) error
	Write(ctx context.Context, payload []byte) error
	ReadMessages() <-chan transportMessage
	Close() error
	EndInput() error
	IsReady() bool
}

type transportMessage struct {
	Data map[string]any
	Err  error
}
