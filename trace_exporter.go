package main

import (
	"fmt"
	"sort"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// TraceInfo holds information about a trace for sorting
type TraceInfo struct {
	TraceID  trace.TraceID
	Duration time.Duration
	Spans    []sdktrace.ReadOnlySpan
}

// FindSlowestTraces identifies the N slowest traces from collected spans
func FindSlowestTraces(collector *TraceCollector, n int) []TraceInfo {
	allSpans := collector.GetSpans()

	// Group spans by trace ID
	traceMap := make(map[trace.TraceID][]sdktrace.ReadOnlySpan)
	for _, span := range allSpans {
		traceID := span.SpanContext().TraceID()
		traceMap[traceID] = append(traceMap[traceID], span)
	}

	// Calculate duration for each trace (using root span duration)
	traces := make([]TraceInfo, 0, len(traceMap))
	for traceID, spans := range traceMap {
		// Find the root span (worker.request span) to get total duration
		var maxDuration time.Duration
		for _, span := range spans {
			if span.Name() == "worker.request" {
				duration := span.EndTime().Sub(span.StartTime())
				if duration > maxDuration {
					maxDuration = duration
				}
			}
		}

		traces = append(traces, TraceInfo{
			TraceID:  traceID,
			Duration: maxDuration,
			Spans:    spans,
		})
	}

	// Sort by duration (slowest first)
	sort.Slice(traces, func(i, j int) bool {
		return traces[i].Duration > traces[j].Duration
	})

	fmt.Printf("Debug: Found %d total traces, returning top %d\n", len(traces), n)

	// Return top N
	if len(traces) > n {
		traces = traces[:n]
	}

	return traces
}

// ExportSlowestTraces exports the slowest traces to a single JSON file
func ExportSlowestTraces(collector *TraceCollector, connType ConnectionType, numToExport int) error {
	slowestTraces := FindSlowestTraces(collector, numToExport)

	if len(slowestTraces) == 0 {
		return fmt.Errorf("no traces found to export")
	}

	fmt.Printf("\nExporting %d slowest traces for %s...\n", len(slowestTraces), connType)

	// Collect all spans from all slowest traces
	allSpans := make([]sdktrace.ReadOnlySpan, 0)
	for _, traceInfo := range slowestTraces {
		allSpans = append(allSpans, traceInfo.Spans...)
	}

	// Create single filename for all traces
	timestamp := time.Now().Format("20060102150405")
	filename := fmt.Sprintf("trace_slowest_%s_top%d_%s.json", connType, len(slowestTraces), timestamp)

	err := ExportTraceToJSON(allSpans, filename)
	if err != nil {
		return fmt.Errorf("failed to export traces: %w", err)
	}

	// Show summary of exported traces
	fmt.Printf("  ✓ Exported %d traces to %s\n", len(slowestTraces), filename)
	fmt.Printf("  Top %d slowest durations:\n", numToExport)
	for i := 0; i < numToExport && i < len(slowestTraces); i++ {
		fmt.Printf("    %d. %v\n", i+1, slowestTraces[i].Duration)
	}

	return nil
}
