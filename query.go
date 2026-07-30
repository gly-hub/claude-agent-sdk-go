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
	return queryWithClient(ctx, prompt, resolved, client)
}

func QueryWithTransport(ctx context.Context, prompt string, opts *Options, transport Transport) (*MessageStream, error) {
	var resolved Options
	if opts != nil {
		resolved = *opts
	}
	if transport == nil {
		transport = NewSubprocessTransport(resolved)
	}
	client := NewClientWithTransport(resolved, transport)
	return queryWithClient(ctx, prompt, resolved, client)
}

func queryWithClient(ctx context.Context, prompt string, opts Options, client *Client) (*MessageStream, error) {
	if err := client.Connect(ctx); err != nil {
		return nil, err
	}
	sessionID := opts.SessionID
	payload := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": prompt,
		},
		"parent_tool_use_id": nil,
		"session_id":         sessionID,
	}
	if err := client.Send(ctx, payload); err != nil {
		_ = client.Close()
		return nil, err
	}
	client.startQueryInputClosure()
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

func (s *MessageStream) ReceiveResponseStream(ctx context.Context) <-chan Message {
	out := make(chan Message)
	inner := s.client.ReceiveResponseStream(ctx)
	go func() {
		defer close(out)
		for msg := range inner {
			out <- msg
			if _, ok := msg.(*ResultMessage); ok {
				return
			}
		}
	}()
	return out
}

func (s *MessageStream) Close() error {
	return s.client.Close()
}
