// Package trace provides a small concurrency-safe in-memory span recorder.
package trace

import (
	"sync"
	"time"

	"github.com/Aurelia-Zhang/Prism/internal/provider"
)

// Status is the lifecycle state of a span.
type Status string

const (
	StatusRunning Status = "running"
	StatusOK      Status = "ok"
	StatusError   Status = "error"
)

// Clock supplies timestamps to the recorder.
type Clock func() time.Time

// Options configures the recorder without introducing a dependency-injection framework.
type Options struct {
	Clock       Clock
	IDGenerator func() string
}

// Attributes are small string-valued span annotations.
type Attributes map[string]string

type spanState struct {
	id            string
	name          string
	parentSpanID  string
	status        Status
	startTime     time.Time
	endTime       time.Time
	startSequence uint64
	endSequence   uint64
	usage         provider.Usage
	err           *provider.Error
	attributes    Attributes
}

// Recorder stores spans for one or more runs in memory.
type Recorder struct {
	mu          sync.RWMutex
	clock       Clock
	idGenerator func() string
	nextStart   uint64
	nextEnd     uint64
	spans       map[string]*spanState
}

// NewRecorder creates an in-memory recorder.
func NewRecorder(options Options) *Recorder {
	clock := options.Clock
	if clock == nil {
		clock = time.Now
	}
	idGenerator := options.IDGenerator
	if idGenerator == nil {
		var next uint64
		idGenerator = func() string {
			next++
			return "span-" + formatUint(next)
		}
	}
	return &Recorder{
		clock:       clock,
		idGenerator: idGenerator,
		spans:       make(map[string]*spanState),
	}
}

// Run is a handle to a single root span and its descendants.
type Run struct {
	recorder *Recorder
	rootID   string
}

// Span is a handle used to create children and finish one span.
type Span struct {
	recorder *Recorder
	id       string
}

// SpanSnapshot is an immutable copy of a recorded span.
type SpanSnapshot struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	ParentSpanID  string          `json:"parent_span_id,omitempty"`
	Status        Status          `json:"status"`
	StartTime     time.Time       `json:"start_time"`
	EndTime       time.Time       `json:"end_time,omitempty"`
	StartSequence uint64          `json:"start_sequence"`
	EndSequence   uint64          `json:"end_sequence,omitempty"`
	Usage         provider.Usage  `json:"usage"`
	Error         *provider.Error `json:"error,omitempty"`
	Attributes    Attributes      `json:"attributes,omitempty"`
}

// Snapshot is a deep copy of all spans in one run, ordered by start sequence.
type Snapshot struct {
	TraceID string         `json:"trace_id,omitempty"`
	Spans   []SpanSnapshot `json:"spans"`
}

// StartRun creates the agent.run root span.
func (r *Recorder) StartRun() *Run {
	root := r.startSpan("agent.run", "", nil)
	return &Run{recorder: r, rootID: root.id}
}

// Root returns the root span for this run.
func (r *Run) Root() *Span {
	return &Span{recorder: r.recorder, id: r.rootID}
}

// Snapshot returns a deep copy of this run's spans.
func (r *Run) Snapshot() Snapshot {
	return r.recorder.snapshot(r.rootID)
}

func (r *Recorder) startSpan(name, parentID string, attributes Attributes) *Span {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextStart++
	id := r.idGenerator()
	state := &spanState{
		id:            id,
		name:          name,
		parentSpanID:  parentID,
		status:        StatusRunning,
		startTime:     r.clock(),
		startSequence: r.nextStart,
		attributes:    cloneAttributes(attributes),
	}
	r.spans[id] = state
	return &Span{recorder: r, id: id}
}

// StartChild creates a running child span.
func (s *Span) StartChild(name string) *Span {
	return s.StartChildWithAttributes(name, nil)
}

// StartChildWithAttributes creates a running child span with detached attributes.
func (s *Span) StartChildWithAttributes(name string, attributes Attributes) *Span {
	return s.recorder.startSpan(name, s.id, attributes)
}

// SetAttribute records or replaces an attribute while the span is running.
func (s *Span) SetAttribute(key, value string) {
	s.recorder.mu.Lock()
	defer s.recorder.mu.Unlock()
	state, ok := s.recorder.spans[s.id]
	if !ok || state.status != StatusRunning {
		return
	}
	if state.attributes == nil {
		state.attributes = make(Attributes)
	}
	state.attributes[key] = value
}

// DeleteAttribute removes an attribute while the span is running.
func (s *Span) DeleteAttribute(key string) {
	s.recorder.mu.Lock()
	defer s.recorder.mu.Unlock()
	state, ok := s.recorder.spans[s.id]
	if !ok || state.status != StatusRunning || state.attributes == nil {
		return
	}
	delete(state.attributes, key)
}

// End finishes a span. A repeated call is ignored, preserving the first terminal state.
func (s *Span) End(status Status, err *provider.Error, usage provider.Usage) {
	s.recorder.mu.Lock()
	defer s.recorder.mu.Unlock()
	state, ok := s.recorder.spans[s.id]
	if !ok || state.status != StatusRunning {
		return
	}
	if status != StatusOK && status != StatusError {
		status = StatusError
	}
	s.recorder.nextEnd++
	state.status = status
	state.endSequence = s.recorder.nextEnd
	state.endTime = s.recorder.clock()
	state.usage = usage
	state.err = cloneError(err)
}

func cloneAttributes(attributes Attributes) Attributes {
	if attributes == nil {
		return nil
	}
	cloned := make(Attributes, len(attributes))
	for key, value := range attributes {
		cloned[key] = value
	}
	return cloned
}

func (r *Recorder) snapshot(rootID string) Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	states := make([]*spanState, 0)
	for _, state := range r.spans {
		if belongsToRun(state, rootID, r.spans) {
			states = append(states, state)
		}
	}
	for i := 1; i < len(states); i++ {
		for j := i; j > 0 && states[j].startSequence < states[j-1].startSequence; j-- {
			states[j], states[j-1] = states[j-1], states[j]
		}
	}
	snapshot := Snapshot{TraceID: rootID, Spans: make([]SpanSnapshot, 0, len(states))}
	for _, state := range states {
		snapshot.Spans = append(snapshot.Spans, SpanSnapshot{
			ID:            state.id,
			Name:          state.name,
			ParentSpanID:  state.parentSpanID,
			Status:        state.status,
			StartTime:     state.startTime,
			EndTime:       state.endTime,
			StartSequence: state.startSequence,
			EndSequence:   state.endSequence,
			Usage:         state.usage,
			Error:         cloneError(state.err),
			Attributes:    cloneAttributes(state.attributes),
		})
	}
	return snapshot
}

func belongsToRun(state *spanState, rootID string, spans map[string]*spanState) bool {
	for current := state; current != nil; {
		if current.id == rootID {
			return true
		}
		if current.parentSpanID == "" {
			return false
		}
		current = spans[current.parentSpanID]
	}
	return false
}

func cloneError(err *provider.Error) *provider.Error {
	if err == nil {
		return nil
	}
	copy := *err
	return &copy
}

func formatUint(value uint64) string {
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = byte('0' + value%10)
		value /= 10
	}
	return string(buffer[index:])
}
