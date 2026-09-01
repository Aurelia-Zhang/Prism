package observability

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/Aurelia-Zhang/Prism/internal/trace"
)

// ExportSnapshot maps Prism spans to an official OpenTelemetry SDK provider
// and exporter. Only the span hierarchy and allowlisted, non-content
// attributes leave the process.
func ExportSnapshot(ctx context.Context, snapshot trace.Snapshot, exporter sdktrace.SpanExporter) error {
	if exporter == nil {
		return errors.New("OTel exporter is required")
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	tracer := provider.Tracer("github.com/Aurelia-Zhang/Prism")
	spans := append([]trace.SpanSnapshot(nil), snapshot.Spans...)
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].StartSequence == spans[j].StartSequence {
			return spans[i].ID < spans[j].ID
		}
		return spans[i].StartSequence < spans[j].StartSequence
	})
	contexts := make(map[string]context.Context, len(spans))
	for _, span := range spans {
		parentContext := ctx
		if span.ParentSpanID == "" {
			parentContext = ctx
			spanContext, otelSpan := tracer.Start(parentContext, span.Name, oteltrace.WithNewRoot(), oteltrace.WithTimestamp(span.StartTime), oteltrace.WithAttributes(otelAttributes(span)...))
			finishOTelSpan(otelSpan, span)
			contexts[span.ID] = spanContext
			continue
		}
		if stored, ok := contexts[span.ParentSpanID]; ok {
			parentContext = stored
		}
		spanContext, otelSpan := tracer.Start(parentContext, span.Name, oteltrace.WithTimestamp(span.StartTime), oteltrace.WithAttributes(otelAttributes(span)...))
		finishOTelSpan(otelSpan, span)
		contexts[span.ID] = spanContext
	}
	return provider.ForceFlush(context.Background())
}

// Export is a short alias for ExportSnapshot.
func Export(ctx context.Context, snapshot trace.Snapshot, exporter sdktrace.SpanExporter) error {
	return ExportSnapshot(ctx, snapshot, exporter)
}

func finishOTelSpan(span oteltrace.Span, source trace.SpanSnapshot) {
	switch source.Status {
	case trace.StatusOK:
		span.SetStatus(codes.Ok, "")
	case trace.StatusError:
		span.SetStatus(codes.Error, "")
	}
	if source.EndTime.IsZero() {
		span.End()
	} else {
		span.End(oteltrace.WithTimestamp(source.EndTime))
	}
}

func otelAttributes(source trace.SpanSnapshot) []attribute.KeyValue {
	attributes := make([]attribute.KeyValue, 0, len(source.Attributes)+7)
	for key, value := range source.Attributes {
		if sensitiveAttribute(key) {
			continue
		}
		attributes = append(attributes, attribute.String("prism."+key, value))
	}
	attributes = append(attributes,
		attribute.String("prism.span_id", source.ID),
		attribute.String("prism.status", string(source.Status)),
		attribute.Int64("prism.usage.input_tokens", source.Usage.InputTokens),
		attribute.Int64("prism.usage.output_tokens", source.Usage.OutputTokens),
		attribute.Int64("prism.usage.cache_read_tokens", source.Usage.CacheReadTokens),
		attribute.Int64("prism.usage.cache_write_tokens", source.Usage.CacheWriteTokens),
	)
	if source.ParentSpanID != "" {
		attributes = append(attributes, attribute.String("prism.parent_span_id", source.ParentSpanID))
	}
	if source.Error != nil {
		attributes = append(attributes, attribute.String("prism.error.code", source.Error.Code))
	}
	return attributes
}

func sensitiveAttribute(key string) bool {
	key = strings.ToLower(key)
	if key == "memory.query" {
		return true
	}
	for _, fragment := range []string{"prompt", "reasoning", "api_key", "apikey", "authorization", "secret", "password", "arguments", "tool_output", "output_content"} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return strings.HasSuffix(key, ".content") || strings.HasSuffix(key, ".output")
}

// ExportedSpanSummary is a deterministic, content-safe view for evidence.
// Span IDs are intentionally omitted because SDK IDs are process-generated.
type ExportedSpanSummary struct {
	Name       string            `json:"name"`
	ParentName string            `json:"parent_name,omitempty"`
	Status     string            `json:"status"`
	Attributes map[string]string `json:"attributes"`
}

// SummarizeSpans converts SDK spans into stable evidence data.
func SummarizeSpans(spans []sdktrace.ReadOnlySpan) []ExportedSpanSummary {
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].StartTime().Equal(spans[j].StartTime()) {
			return spans[i].Name() < spans[j].Name()
		}
		return spans[i].StartTime().Before(spans[j].StartTime())
	})
	byID := make(map[string]string, len(spans))
	for _, span := range spans {
		byID[span.SpanContext().SpanID().String()] = span.Name()
	}
	result := make([]ExportedSpanSummary, 0, len(spans))
	for _, span := range spans {
		attributes := make(map[string]string)
		for _, value := range span.Attributes() {
			if strings.HasPrefix(string(value.Key), "prism.") {
				attributes[string(value.Key)] = fmt.Sprint(value.Value.AsInterface())
			}
		}
		parentName := byID[span.Parent().SpanID().String()]
		result = append(result, ExportedSpanSummary{Name: span.Name(), ParentName: parentName, Status: span.Status().Code.String(), Attributes: attributes})
	}
	return result
}
