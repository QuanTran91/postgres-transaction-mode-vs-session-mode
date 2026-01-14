package main

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

func TestTraceCollector(t *testing.T) {
	// Initialize tracer
	collector, cleanup, err := InitTracer("test-service")
	if err != nil {
		t.Fatalf("Failed to initialize tracer: %v", err)
	}
	defer cleanup()

	// Get tracer
	tracer := GetTracer("test")

	// Create some test spans
	ctx := context.Background()
	ctx, span1 := tracer.Start(ctx, "test.operation")
	span1.SetAttributes(attribute.String("test.key", "value1"))
	time.Sleep(10 * time.Millisecond)
	span1.End()

	ctx, span2 := tracer.Start(ctx, "test.operation")
	span2.SetAttributes(attribute.String("test.key", "value2"))
	time.Sleep(20 * time.Millisecond)
	span2.End()

	// Force flush
	time.Sleep(100 * time.Millisecond)

	// Check collected spans
	spans := collector.GetSpans()
	if len(spans) < 2 {
		t.Errorf("Expected at least 2 spans, got %d", len(spans))
	}

	t.Logf("Collected %d spans", len(spans))
	for i, span := range spans {
		t.Logf("Span %d: %s (duration: %v)", i, span.Name(), span.EndTime().Sub(span.StartTime()))
	}
}

func TestFindSlowestTraces(t *testing.T) {
	// Initialize tracer
	collector, cleanup, err := InitTracer("test-service")
	if err != nil {
		t.Fatalf("Failed to initialize tracer: %v", err)
	}
	defer cleanup()

	tracer := GetTracer("test")
	ctx := context.Background()

	// Create traces with different durations
	for i := 0; i < 5; i++ {
		ctx, span := tracer.Start(ctx, "worker.request")
		duration := time.Duration(i*10) * time.Millisecond
		time.Sleep(duration)
		span.End()
	}

	// Force flush
	time.Sleep(100 * time.Millisecond)

	// Find slowest traces
	slowest := FindSlowestTraces(collector, 3)
	if len(slowest) != 3 {
		t.Errorf("Expected 3 slowest traces, got %d", len(slowest))
	}

	t.Logf("Found %d slowest traces", len(slowest))
	for i, trace := range slowest {
		t.Logf("Trace %d: duration=%v, spans=%d", i, trace.Duration, len(trace.Spans))
	}
}
