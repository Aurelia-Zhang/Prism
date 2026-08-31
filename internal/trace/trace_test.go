package trace

import (
	"sync"
	"testing"
	"time"

	"github.com/Aurelia-Zhang/Prism/internal/provider"
)

func TestSpanParentLifecycleAndFirstWins(t *testing.T) {
	recorder := NewRecorder(Options{
		Clock: func() time.Time { return time.Unix(100, 0).UTC() },
		IDGenerator: func() func() string {
			n := 0
			return func() string { n++; return string(rune('a' + n - 1)) }
		}(),
	})
	run := recorder.StartRun()
	child := run.Root().StartChild("model.call")
	grandchild := child.StartChild("tool.call")
	child.End(StatusOK, nil, provider.Usage{InputTokens: 7, OutputTokens: 3})
	child.End(StatusError, provider.NewError("late", "must be ignored"), provider.Usage{InputTokens: 99})
	grandchild.End(StatusError, provider.NewError("bad_args", "invalid"), provider.Usage{})
	run.Root().End(StatusOK, nil, provider.Usage{InputTokens: 7, OutputTokens: 3})

	snapshot := run.Snapshot()
	if len(snapshot.Spans) != 3 {
		t.Fatalf("got %d spans, want 3", len(snapshot.Spans))
	}
	if snapshot.Spans[1].ParentSpanID != snapshot.Spans[0].ID || snapshot.Spans[2].ParentSpanID != snapshot.Spans[1].ID {
		t.Fatalf("unexpected parent chain: %#v", snapshot.Spans)
	}
	if snapshot.Spans[1].Status != StatusOK || snapshot.Spans[1].Usage.InputTokens != 7 {
		t.Fatalf("first terminal state was overwritten: %#v", snapshot.Spans[1])
	}
	for _, span := range snapshot.Spans {
		if span.Status == StatusRunning || span.StartSequence == 0 || span.EndSequence == 0 {
			t.Fatalf("unfinished or unsequenced span: %#v", span)
		}
	}
	for i := 1; i < len(snapshot.Spans); i++ {
		if snapshot.Spans[i].StartSequence <= snapshot.Spans[i-1].StartSequence {
			t.Fatalf("start sequence is not increasing: %#v", snapshot.Spans)
		}
	}
}

func TestSnapshotIsDeepCopy(t *testing.T) {
	recorder := NewRecorder(Options{})
	run := recorder.StartRun()
	run.Root().End(StatusError, provider.NewError("failure", "original"), provider.Usage{InputTokens: 1})

	snapshot := run.Snapshot()
	snapshot.Spans[0].Error.Message = "changed"
	snapshot.Spans[0].Usage.InputTokens = 99
	snapshot.Spans[0].Status = StatusOK
	again := run.Snapshot()
	if again.Spans[0].Error.Message != "original" || again.Spans[0].Usage.InputTokens != 1 || again.Spans[0].Status != StatusError {
		t.Fatalf("snapshot mutation changed recorder state: %#v", again.Spans[0])
	}
}

func TestRecorderConcurrentReadsAndWrites(t *testing.T) {
	recorder := NewRecorder(Options{})
	run := recorder.StartRun()
	spans := make([]*Span, 64)
	for i := range spans {
		spans[i] = run.Root().StartChild("tool.call")
	}

	var wait sync.WaitGroup
	wait.Add(len(spans) + 1)
	for _, span := range spans {
		go func(span *Span) {
			defer wait.Done()
			snapshot := run.Snapshot()
			if len(snapshot.Spans) == 0 {
				t.Error("snapshot unexpectedly empty")
			}
			span.End(StatusOK, nil, provider.Usage{})
		}(span)
	}
	go func() {
		defer wait.Done()
		for i := 0; i < 20; i++ {
			_ = run.Snapshot()
		}
	}()
	wait.Wait()
	run.Root().End(StatusOK, nil, provider.Usage{})
	for _, span := range run.Snapshot().Spans {
		if span.Status == StatusRunning {
			t.Fatalf("span remained running: %#v", span)
		}
	}
}
