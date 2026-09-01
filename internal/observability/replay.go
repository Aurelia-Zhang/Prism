package observability

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Aurelia-Zhang/Prism/internal/provider"
	"github.com/Aurelia-Zhang/Prism/internal/trace"
)

// Timeline is the stable, read-only representation used by replay and export.
type Timeline struct {
	TraceID string         `json:"trace_id"`
	Spans   []TimelineSpan `json:"spans"`
}

// TimelineSpan contains only fields needed to inspect an execution timeline.
type TimelineSpan struct {
	ID            string         `json:"id"`
	ParentSpanID  string         `json:"parent_span_id,omitempty"`
	Depth         int            `json:"depth"`
	Name          string         `json:"name"`
	StartTime     time.Time      `json:"start_time"`
	EndTime       time.Time      `json:"end_time,omitempty"`
	Duration      string         `json:"duration"`
	StartSequence uint64         `json:"start_sequence"`
	EndSequence   uint64         `json:"end_sequence,omitempty"`
	Status        trace.Status   `json:"status"`
	Usage         provider.Usage `json:"usage"`
	ErrorCode     string         `json:"error_code,omitempty"`
}

// Replay rebuilds a timeline from a persisted snapshot. It does not call a
// Provider or Tool and keeps the original start-sequence ordering.
func Replay(snapshot trace.Snapshot) Timeline {
	spans := append([]trace.SpanSnapshot(nil), snapshot.Spans...)
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].StartSequence == spans[j].StartSequence {
			return spans[i].ID < spans[j].ID
		}
		return spans[i].StartSequence < spans[j].StartSequence
	})
	depths := make(map[string]int, len(spans))
	timeline := Timeline{TraceID: snapshot.TraceID, Spans: make([]TimelineSpan, 0, len(spans))}
	if timeline.TraceID == "" {
		timeline.TraceID = traceIDFromSpans(spans)
	}
	for _, span := range spans {
		depth := 0
		if span.ParentSpanID != "" {
			depth = depths[span.ParentSpanID] + 1
		}
		depths[span.ID] = depth
		duration := time.Duration(0)
		if !span.StartTime.IsZero() && !span.EndTime.IsZero() && span.EndTime.After(span.StartTime) {
			duration = span.EndTime.Sub(span.StartTime)
		}
		entry := TimelineSpan{
			ID: span.ID, ParentSpanID: span.ParentSpanID, Depth: depth, Name: span.Name,
			StartTime: span.StartTime, EndTime: span.EndTime, Duration: duration.String(),
			StartSequence: span.StartSequence, EndSequence: span.EndSequence,
			Status: span.Status, Usage: span.Usage,
		}
		if span.Error != nil {
			entry.ErrorCode = span.Error.Code
		}
		timeline.Spans = append(timeline.Spans, entry)
	}
	return timeline
}

// RenderTimeline renders the stable human-readable replay format.
func RenderTimeline(timeline Timeline) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "trace %s\n", timeline.TraceID)
	for _, span := range timeline.Spans {
		fmt.Fprintf(&builder, "%s%s status=%s duration=%s usage=%s", strings.Repeat("  ", span.Depth), span.Name, span.Status, span.Duration, formatUsage(span.Usage))
		if span.ErrorCode != "" {
			fmt.Fprintf(&builder, " error=%s", span.ErrorCode)
		}
		builder.WriteByte('\n')
	}
	return builder.String()
}

// MarshalTimeline returns the JSON form used by `prism trace export`.
func MarshalTimeline(timeline Timeline) ([]byte, error) {
	return json.MarshalIndent(timeline, "", "  ")
}

func formatUsage(usage provider.Usage) string {
	return fmt.Sprintf("input=%d output=%d cache_read=%d cache_write=%d", usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens)
}
