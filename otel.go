package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// TraceCollector collects spans in memory for later export
type TraceCollector struct {
	mu     sync.Mutex
	spans  []sdktrace.ReadOnlySpan
	tracer trace.Tracer
}

// NewTraceCollector creates a new in-memory trace collector
func NewTraceCollector() *TraceCollector {
	return &TraceCollector{
		spans: make([]sdktrace.ReadOnlySpan, 0),
	}
}

// ExportSpans implements the SpanExporter interface
func (tc *TraceCollector) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.spans = append(tc.spans, spans...)
	return nil
}

// Shutdown implements the SpanExporter interface
func (tc *TraceCollector) Shutdown(ctx context.Context) error {
	return nil
}

// GetSpans returns all collected spans
func (tc *TraceCollector) GetSpans() []sdktrace.ReadOnlySpan {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.spans
}

// InitTracer initializes OpenTelemetry tracer with in-memory collector
func InitTracer(serviceName string) (*TraceCollector, func(), error) {
	collector := NewTraceCollector()

	// Create resource with service information
	res, err := resource.New(
		context.Background(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion("1.0.0"),
			attribute.String("environment", "benchmark"),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Create trace provider with our collector
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(collector),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	// Set global tracer provider
	otel.SetTracerProvider(tp)

	// Create cleanup function
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tp.Shutdown(ctx); err != nil {
			fmt.Printf("Error shutting down tracer provider: %v\n", err)
		}
	}

	return collector, cleanup, nil
}

// GetTracer returns a tracer instance
func GetTracer(name string) trace.Tracer {
	return otel.Tracer(name)
}

// OTLPSpan represents a span in OTLP JSON format
type OTLPSpan struct {
	TraceID                string          `json:"traceId"`
	SpanID                 string          `json:"spanId"`
	ParentSpanID           string          `json:"parentSpanId,omitempty"`
	TraceState             string          `json:"traceState"`
	Name                   string          `json:"name"`
	Kind                   string          `json:"kind"`
	StartTimeUnixNano      int64           `json:"startTimeUnixNano"`
	EndTimeUnixNano        int64           `json:"endTimeUnixNano"`
	Attributes             []OTLPAttribute `json:"attributes,omitempty"`
	DroppedAttributesCount int             `json:"droppedAttributesCount"`
	DroppedEventsCount     int             `json:"droppedEventsCount"`
	DroppedLinksCount      int             `json:"droppedLinksCount"`
	Status                 OTLPStatus      `json:"status"`
}

// OTLPAttribute represents a span attribute
type OTLPAttribute struct {
	Key   string    `json:"key"`
	Value OTLPValue `json:"value"`
}

// OTLPValue represents an attribute value in OTLP format
type OTLPValue struct {
	StringValue *string  `json:"stringValue,omitempty"`
	IntValue    *int64   `json:"intValue,omitempty"`
	DoubleValue *float64 `json:"doubleValue,omitempty"`
	BoolValue   *bool    `json:"boolValue,omitempty"`
}

// OTLPStatus represents span status
type OTLPStatus struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

// OTLPTrace represents a complete trace in Tempo's expected format
type OTLPTrace struct {
	Batches []OTLPBatch `json:"batches"`
}

// OTLPBatch represents a batch of spans
type OTLPBatch struct {
	Resource                    OTLPResource                     `json:"resource"`
	InstrumentationLibrarySpans []OTLPInstrumentationLibrarySpan `json:"instrumentationLibrarySpans"`
}

// OTLPResource represents resource attributes
type OTLPResource struct {
	Attributes             []OTLPAttribute `json:"attributes"`
	DroppedAttributesCount int             `json:"droppedAttributesCount"`
}

// OTLPInstrumentationLibrarySpan groups spans by instrumentation library
type OTLPInstrumentationLibrarySpan struct {
	InstrumentationLibrary OTLPInstrumentationLibrary `json:"instrumentationLibrary"`
	Spans                  []OTLPSpan                 `json:"spans"`
}

// OTLPInstrumentationLibrary represents instrumentation library info
type OTLPInstrumentationLibrary struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ConvertSpanToOTLP converts a ReadOnlySpan to OTLP format
func ConvertSpanToOTLP(span sdktrace.ReadOnlySpan) OTLPSpan {
	otlpSpan := OTLPSpan{
		TraceID:                span.SpanContext().TraceID().String(),
		SpanID:                 span.SpanContext().SpanID().String(),
		TraceState:             "",
		Name:                   span.Name(),
		Kind:                   convertSpanKind(span.SpanKind()),
		StartTimeUnixNano:      span.StartTime().UnixNano(),
		EndTimeUnixNano:        span.EndTime().UnixNano(),
		Attributes:             make([]OTLPAttribute, 0),
		DroppedAttributesCount: 0,
		DroppedEventsCount:     0,
		DroppedLinksCount:      0,
		Status: OTLPStatus{
			Code:    convertStatusCode(span.Status().Code),
			Message: span.Status().Description,
		},
	}

	// Add parent span ID if exists
	if span.Parent().SpanID().IsValid() {
		otlpSpan.ParentSpanID = span.Parent().SpanID().String()
	}

	// Convert attributes
	for _, attr := range span.Attributes() {
		otlpAttr := OTLPAttribute{
			Key: string(attr.Key),
		}

		// Convert value to proper OTLP format based on type
		switch attr.Value.Type() {
		case attribute.STRING:
			strVal := attr.Value.AsString()
			otlpAttr.Value.StringValue = &strVal
		case attribute.INT64:
			intVal := attr.Value.AsInt64()
			otlpAttr.Value.IntValue = &intVal
		case attribute.FLOAT64:
			floatVal := attr.Value.AsFloat64()
			otlpAttr.Value.DoubleValue = &floatVal
		case attribute.BOOL:
			boolVal := attr.Value.AsBool()
			otlpAttr.Value.BoolValue = &boolVal
		default:
			// Fallback to string representation
			strVal := attr.Value.AsString()
			otlpAttr.Value.StringValue = &strVal
		}

		otlpSpan.Attributes = append(otlpSpan.Attributes, otlpAttr)
	}

	return otlpSpan
}

// convertSpanKind converts trace.SpanKind to OTLP string format
func convertSpanKind(kind trace.SpanKind) string {
	switch kind {
	case trace.SpanKindInternal:
		return "SPAN_KIND_INTERNAL"
	case trace.SpanKindServer:
		return "SPAN_KIND_SERVER"
	case trace.SpanKindClient:
		return "SPAN_KIND_CLIENT"
	case trace.SpanKindProducer:
		return "SPAN_KIND_PRODUCER"
	case trace.SpanKindConsumer:
		return "SPAN_KIND_CONSUMER"
	default:
		return "SPAN_KIND_UNSPECIFIED"
	}
}

// convertStatusCode converts codes.Code to integer
func convertStatusCode(code codes.Code) int {
	switch code {
	case codes.Ok:
		return 1
	case codes.Error:
		return 2
	default:
		return 0 // Unset
	}
}

// ExportTraceToJSON exports a trace (group of spans with same trace ID) to JSON file
func ExportTraceToJSON(spans []sdktrace.ReadOnlySpan, filename string) error {
	if len(spans) == 0 {
		return fmt.Errorf("no spans to export")
	}

	// Convert spans to OTLP format
	otlpSpans := make([]OTLPSpan, 0, len(spans))
	for _, span := range spans {
		otlpSpans = append(otlpSpans, ConvertSpanToOTLP(span))
	}

	// Create OTLP trace structure in Tempo format
	serviceName := "pgx-benchmark"
	serviceVersion := "1.0.0"

	trace := OTLPTrace{
		Batches: []OTLPBatch{
			{
				Resource: OTLPResource{
					Attributes: []OTLPAttribute{
						{
							Key:   "service.name",
							Value: OTLPValue{StringValue: &serviceName},
						},
						{
							Key:   "service.version",
							Value: OTLPValue{StringValue: &serviceVersion},
						},
					},
					DroppedAttributesCount: 0,
				},
				InstrumentationLibrarySpans: []OTLPInstrumentationLibrarySpan{
					{
						InstrumentationLibrary: OTLPInstrumentationLibrary{
							Name:    "pgx-benchmark",
							Version: "1.0.0",
						},
						Spans: otlpSpans,
					},
				},
			},
		},
	}

	// Marshal to JSON with indentation
	jsonData, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal trace to JSON: %w", err)
	}

	// Write to file
	if err := os.WriteFile(filename, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write trace file: %w", err)
	}

	return nil
}
