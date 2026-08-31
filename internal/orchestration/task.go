// Package orchestration provides a local, persistent multi-agent task runner.
package orchestration

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Aurelia-Zhang/Prism/internal/provider"
)

// Status is the lifecycle state of a task and its current attempt.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

var ErrInvalidTransition = errors.New("invalid task status transition")

// TaskSpec describes a task before it is persisted.
type TaskSpec struct {
	ID           string
	ParentID     string
	Strategy     string
	Goal         string
	Constraints  []string
	BaseRef      string
	Complexity   int
	ToolRequired bool
	TokenBudget  int64
	MaxRetries   int
}

// Task is the durable task and current-attempt snapshot.
type Task struct {
	ID           string          `json:"id"`
	ParentID     string          `json:"parent_id,omitempty"`
	Strategy     string          `json:"strategy"`
	Status       Status          `json:"status"`
	Progress     int             `json:"progress"`
	Goal         string          `json:"goal"`
	Constraints  []string        `json:"constraints,omitempty"`
	BaseRef      string          `json:"base_ref"`
	Complexity   int             `json:"complexity"`
	ToolRequired bool            `json:"tool_required"`
	TokenBudget  int64           `json:"token_budget"`
	Worktree     string          `json:"worktree,omitempty"`
	Branch       string          `json:"branch,omitempty"`
	HEAD         string          `json:"head,omitempty"`
	ChangedFiles []string        `json:"changed_files,omitempty"`
	DiffSummary  string          `json:"diff_summary,omitempty"`
	ModelProfile string          `json:"model_profile,omitempty"`
	RouteReason  string          `json:"route_reason,omitempty"`
	Result       json.RawMessage `json:"result,omitempty"`
	Error        *provider.Error `json:"error,omitempty"`
	Usage        provider.Usage  `json:"usage"`
	Attempt      int             `json:"attempt"`
	MaxRetries   int             `json:"max_retries"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	StartedAt    time.Time       `json:"started_at,omitempty"`
	CompletedAt  time.Time       `json:"completed_at,omitempty"`
	Trace        json.RawMessage `json:"trace,omitempty"`
}

// Attempt is an immutable historical execution slot except for its final snapshot fields.
type Attempt struct {
	ID           string          `json:"id"`
	TaskID       string          `json:"task_id"`
	Number       int             `json:"number"`
	Status       Status          `json:"status"`
	Progress     int             `json:"progress"`
	Worktree     string          `json:"worktree,omitempty"`
	Branch       string          `json:"branch,omitempty"`
	HEAD         string          `json:"head,omitempty"`
	ChangedFiles []string        `json:"changed_files,omitempty"`
	DiffSummary  string          `json:"diff_summary,omitempty"`
	ModelProfile string          `json:"model_profile,omitempty"`
	RouteReason  string          `json:"route_reason,omitempty"`
	Result       json.RawMessage `json:"result,omitempty"`
	Error        *provider.Error `json:"error,omitempty"`
	Usage        provider.Usage  `json:"usage"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	StartedAt    time.Time       `json:"started_at,omitempty"`
	CompletedAt  time.Time       `json:"completed_at,omitempty"`
	Trace        json.RawMessage `json:"trace,omitempty"`
}

// TaskStore is a fixed-schema SQLite store. One connection keeps :memory: demos
// and concurrent Manager updates deterministic, matching the C1 memory store.
type TaskStore struct {
	db *sql.DB
}

var nextID uint64

func newID(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), atomic.AddUint64(&nextID, 1))
}

func validateSpec(spec TaskSpec) error {
	if strings.TrimSpace(spec.Goal) == "" {
		return errors.New("task goal must be non-empty")
	}
	if strings.TrimSpace(spec.BaseRef) == "" {
		return errors.New("task base ref must be non-empty")
	}
	if spec.Complexity < 0 {
		return errors.New("task complexity must not be negative")
	}
	if spec.TokenBudget < 0 {
		return errors.New("task token budget must not be negative")
	}
	if spec.MaxRetries < 0 {
		return errors.New("task max retries must not be negative")
	}
	return nil
}

func canTransition(from, to Status) bool {
	switch from {
	case StatusQueued:
		return to == StatusRunning || to == StatusCancelled
	case StatusRunning:
		return to == StatusSucceeded || to == StatusFailed || to == StatusCancelled
	case StatusFailed, StatusCancelled:
		return to == StatusQueued
	default:
		return false
	}
}

func validateTransition(from, to Status) error {
	if !canTransition(from, to) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
	}
	return nil
}
