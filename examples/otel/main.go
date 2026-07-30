package main

import (
	"context"
	"log"

	claudeagentsdk "github.com/gly-hub/claude-agent-sdk-go"
	"go.opentelemetry.io/otel"
	stdouttrace "go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func main() {
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		log.Fatal(err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("claude-agent-sdk-go-example"),
		)),
	)
	defer func() {
		_ = tp.Shutdown(context.Background())
	}()

	otel.SetTracerProvider(tp)

	ctx := context.Background()
	tracer := otel.Tracer("example/claude")
	ctx, span := tracer.Start(ctx, "interactive-query")
	defer span.End()

	stream, err := claudeagentsdk.Query(ctx, "Say hello in one sentence.", &claudeagentsdk.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer stream.Close()

	for range stream.ReceiveResponseStream(ctx) {
	}
}
