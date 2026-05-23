package claudeagentsdk

import (
	"context"
	"io"
)

type MessageStream struct {
	client *Client
}

func Query(ctx context.Context, prompt string, opts *Options) (*MessageStream, error) {
	var resolved Options
	if opts != nil {
		resolved = *opts
	}
	client := NewClient(resolved)
	if err := client.Connect(ctx); err != nil {
		return nil, err
	}
	if err := client.SendUser(ctx, prompt, resolved.SessionID); err != nil {
		_ = client.Close()
		return nil, err
	}
	if err := client.EndInput(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return &MessageStream{client: client}, nil
}

func (s *MessageStream) Next(ctx context.Context) (Message, error) {
	msg, err := s.client.Next(ctx)
	if err == io.EOF {
		_ = s.client.Close()
	}
	return msg, err
}

func (s *MessageStream) ReceiveResponse(ctx context.Context) ([]Message, error) {
	messages, err := s.client.ReceiveResponse(ctx)
	if err == io.EOF {
		_ = s.client.Close()
	}
	return messages, err
}

func (s *MessageStream) Close() error {
	return s.client.Close()
}
