package main

import (
	"context"
	"fmt"
	"io"
	"log"

	claudeagentsdk "github.com/gly-hub/claude-agent-sdk-go"
)

func main() {
	ctx := context.Background()

	stream, err := claudeagentsdk.Query(ctx, "What is 2 + 2?", &claudeagentsdk.Options{
		SystemPrompt: "You are a concise assistant.",
		MaxTurns:     1,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer stream.Close()

	for {
		msg, err := stream.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}

		switch m := msg.(type) {
		case *claudeagentsdk.AssistantMessage:
			for _, block := range m.Content {
				if text, ok := block.(claudeagentsdk.TextBlock); ok {
					fmt.Println(text.Text)
				}
			}
		}
	}
}
