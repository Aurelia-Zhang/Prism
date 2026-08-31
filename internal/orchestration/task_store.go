package orchestration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Aurelia-Zhang/Prism/internal/provider"
	"github.com/Aurelia-Zhang/Prism/internal/trace"
)

// OpenTaskStore opens and initializes the fixed O1 schema.
func OpenTaskStore(path string) (*TaskStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &TaskStore{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// Open is the concise constructor used by local demos.
func Open(path string) (*TaskStore, error) { return OpenTaskStore(path) }

// DB exposes the single SQLite connection for local demos and inspection.
func (s *TaskStore) DB() *sql.DB {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db
}

func (s *TaskStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *TaskStore) init() error {
	if s == nil || s.db == nil {
		return errors.New("task store is nil")
	}
	_, err := s.db.Exec(`
		PRAGMA foreign_keys = ON;
		CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			parent_id TEXT NOT NULL DEFAULT '',
			strategy TEXT NOT NULL,
			status TEXT NOT NULL,
			progress INTEGER NOT NULL DEFAULT 0,
			goal TEXT NOT NULL,
			constraints_json TEXT NOT NULL DEFAULT '[]',
			base_ref TEXT NOT NULL,
			complexity INTEGER NOT NULL DEFAULT 0,
			tool_required INTEGER NOT NULL DEFAULT 0,
			token_budget INTEGER NOT NULL DEFAULT 0,
			worktree TEXT NOT NULL DEFAULT '',
			branch TEXT NOT NULL DEFAULT '',
			head TEXT NOT NULL DEFAULT '',
			changed_files_json TEXT NOT NULL DEFAULT '[]',
			diff_summary TEXT NOT NULL DEFAULT '',
			model_profile TEXT NOT NULL DEFAULT '',
			route_reason TEXT NOT NULL DEFAULT '',
			result_json TEXT NOT NULL DEFAULT '',
			error_json TEXT NOT NULL DEFAULT '',
			usage_json TEXT NOT NULL DEFAULT '{}',
			attempt INTEGER NOT NULL DEFAULT 1,
			max_retries INTEGER NOT NULL DEFAULT 0,
			trace_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT '',
			completed_at TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS attempts (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL REFERENCES tasks(id),
			number INTEGER NOT NULL,
			status TEXT NOT NULL,
			progress INTEGER NOT NULL DEFAULT 0,
			worktree TEXT NOT NULL DEFAULT '',
			branch TEXT NOT NULL DEFAULT '',
			head TEXT NOT NULL DEFAULT '',
			changed_files_json TEXT NOT NULL DEFAULT '[]',
			diff_summary TEXT NOT NULL DEFAULT '',
			model_profile TEXT NOT NULL DEFAULT '',
			route_reason TEXT NOT NULL DEFAULT '',
			result_json TEXT NOT NULL DEFAULT '',
			error_json TEXT NOT NULL DEFAULT '',
			usage_json TEXT NOT NULL DEFAULT '{}',
			trace_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT '',
			completed_at TEXT NOT NULL DEFAULT '',
			UNIQUE(task_id, number)
		);
		CREATE INDEX IF NOT EXISTS tasks_parent_idx ON tasks(parent_id);
		CREATE INDEX IF NOT EXISTS attempts_task_idx ON attempts(task_id, number);`)
	return err
}

func (s *TaskStore) Create(ctx context.Context, spec TaskSpec) (Task, error) {
	if err := validateSpec(spec); err != nil {
		return Task{}, err
	}
	if s == nil || s.db == nil {
		return Task{}, errors.New("task store is nil")
	}
	if spec.ID == "" {
		spec.ID = newID("task")
	}
	if spec.Strategy == "" {
		spec.Strategy = "default"
	}
	now := time.Now().UTC()
	task := Task{ID: spec.ID, ParentID: spec.ParentID, Strategy: spec.Strategy, Status: StatusQueued, Goal: spec.Goal, Constraints: cloneStrings(spec.Constraints), BaseRef: spec.BaseRef, Complexity: spec.Complexity, ToolRequired: spec.ToolRequired, TokenBudget: spec.TokenBudget, Attempt: 1, MaxRetries: spec.MaxRetries, CreatedAt: now, UpdatedAt: now}
	constraints, _ := json.Marshal(task.Constraints)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO tasks(id,parent_id,strategy,status,progress,goal,constraints_json,base_ref,complexity,tool_required,token_budget,attempt,max_retries,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, task.ID, task.ParentID, task.Strategy, task.Status, task.Progress, task.Goal, string(constraints), task.BaseRef, task.Complexity, task.ToolRequired, task.TokenBudget, task.Attempt, task.MaxRetries, stamp(now), stamp(now))
	if err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO attempts(id,task_id,number,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, newID("attempt"), task.ID, 1, task.Status, stamp(now), stamp(now))
	}
	if err != nil {
		_ = tx.Rollback()
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (s *TaskStore) Get(ctx context.Context, id string) (Task, error) {
	if s == nil || s.db == nil {
		return Task{}, errors.New("task store is nil")
	}
	return scanTask(s.db.QueryRowContext(ctx, taskSelect+` WHERE id = ?`, id))
}

func (s *TaskStore) Attempts(ctx context.Context, taskID string) ([]Attempt, error) {
	rows, err := s.db.QueryContext(ctx, attemptSelect+` WHERE task_id = ? ORDER BY number`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var attempts []Attempt
	for rows.Next() {
		attempt, err := scanAttempt(rows)
		if err != nil {
			return nil, err
		}
		attempts = append(attempts, attempt)
	}
	return attempts, rows.Err()
}

func (s *TaskStore) Start(ctx context.Context, id string) error {
	return s.transition(ctx, id, StatusRunning, func(now time.Time) (string, []any) {
		return `status=?, progress=0, started_at=?, updated_at=?`, []any{StatusRunning, stamp(now), stamp(now)}
	})
}

func (s *TaskStore) Progress(ctx context.Context, id string, progress int) error {
	if progress < 0 || progress > 100 {
		return errors.New("task progress must be between 0 and 100")
	}
	task, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if task.Status != StatusRunning {
		return fmt.Errorf("%w: %s -> progress", ErrInvalidTransition, task.Status)
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `UPDATE tasks SET progress=?, updated_at=? WHERE id=?`, progress, stamp(now), id)
	if err == nil {
		_, err = s.db.ExecContext(ctx, `UPDATE attempts SET progress=?, updated_at=? WHERE task_id=? AND number=?`, progress, stamp(now), id, task.Attempt)
	}
	return err
}

func (s *TaskStore) Complete(ctx context.Context, id string, result any, usage provider.Usage) error {
	return s.finish(ctx, id, StatusSucceeded, result, nil, usage)
}

func (s *TaskStore) Fail(ctx context.Context, id string, taskErr *provider.Error, result any, usage provider.Usage) error {
	if taskErr == nil {
		taskErr = provider.NewError("task_failed", "worker failed")
	}
	return s.finish(ctx, id, StatusFailed, result, taskErr, usage)
}

func (s *TaskStore) Cancel(ctx context.Context, id string, taskErr *provider.Error) error {
	if taskErr == nil {
		taskErr = provider.NewError("context_canceled", "task canceled")
	}
	return s.finish(ctx, id, StatusCancelled, nil, taskErr, provider.Usage{})
}

func (s *TaskStore) Retry(ctx context.Context, id string) (int, error) {
	task, err := s.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	if task.Status != StatusFailed && task.Status != StatusCancelled {
		return 0, fmt.Errorf("%w: %s -> retry", ErrInvalidTransition, task.Status)
	}
	number := task.Attempt + 1
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE tasks SET status=?, progress=0, attempt=?, worktree='', branch='', head='', changed_files_json='[]', diff_summary='', model_profile='', route_reason='', result_json='', error_json='', usage_json='{}', trace_json='', updated_at=?, started_at='', completed_at='' WHERE id=?`, StatusQueued, number, stamp(now), id)
	if err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO attempts(id,task_id,number,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, newID("attempt"), id, number, StatusQueued, stamp(now), stamp(now))
	}
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return number, nil
}

func (s *TaskStore) SetRoute(ctx context.Context, id, profile, reason string) error {
	task, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	now := stamp(time.Now().UTC())
	_, err = s.db.ExecContext(ctx, `UPDATE tasks SET model_profile=?, route_reason=?, updated_at=? WHERE id=?`, profile, reason, now, id)
	if err == nil {
		_, err = s.db.ExecContext(ctx, `UPDATE attempts SET model_profile=?, route_reason=?, updated_at=? WHERE task_id=? AND number=?`, profile, reason, now, id, task.Attempt)
	}
	return err
}

// SetWorktree records only worktrees returned by the current Manager.
func (s *TaskStore) SetWorktree(ctx context.Context, id string, info WorktreeInfo) error {
	task, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	files, _ := json.Marshal(info.ChangedFiles)
	now := stamp(time.Now().UTC())
	_, err = s.db.ExecContext(ctx, `UPDATE tasks SET worktree=?, branch=?, head=?, changed_files_json=?, diff_summary=?, updated_at=? WHERE id=?`, info.Path, info.Branch, info.HEAD, string(files), info.DiffSummary, now, id)
	if err == nil {
		_, err = s.db.ExecContext(ctx, `UPDATE attempts SET worktree=?, branch=?, head=?, changed_files_json=?, diff_summary=?, updated_at=? WHERE task_id=? AND number=?`, info.Path, info.Branch, info.HEAD, string(files), info.DiffSummary, now, id, task.Attempt)
	}
	return err
}

func (s *TaskStore) SetTrace(ctx context.Context, id string, snapshot trace.Snapshot) error {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	task, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	now := stamp(time.Now().UTC())
	_, err = s.db.ExecContext(ctx, `UPDATE tasks SET trace_json=?, updated_at=? WHERE id=?`, string(encoded), now, id)
	if err == nil {
		_, err = s.db.ExecContext(ctx, `UPDATE attempts SET trace_json=?, updated_at=? WHERE task_id=? AND number=?`, string(encoded), now, id, task.Attempt)
	}
	return err
}

func (s *TaskStore) transition(ctx context.Context, id string, target Status, fields func(time.Time) (string, []any)) error {
	task, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := validateTransition(task.Status, target); err != nil {
		return err
	}
	now := time.Now().UTC()
	set, args := fields(now)
	args = append(args, id)
	if _, err := s.db.ExecContext(ctx, `UPDATE tasks SET `+set+` WHERE id=?`, args...); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE attempts SET `+set+` WHERE task_id=? AND number=?`, append(args[:len(args)-1], id, task.Attempt)...)
	return err
}

func (s *TaskStore) finish(ctx context.Context, id string, target Status, result any, taskErr *provider.Error, usage provider.Usage) error {
	task, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := validateTransition(task.Status, target); err != nil {
		return err
	}
	resultJSON, err := marshalOrEmpty(result)
	if err != nil {
		return err
	}
	errorJSON, err := marshalOrEmpty(taskErr)
	if err != nil {
		return err
	}
	usageJSON, _ := json.Marshal(usage)
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `UPDATE tasks SET status=?, progress=?, result_json=?, error_json=?, usage_json=?, updated_at=?, completed_at=? WHERE id=?`, target, terminalProgress(target), resultJSON, errorJSON, string(usageJSON), stamp(now), stamp(now), id)
	if err == nil {
		_, err = s.db.ExecContext(ctx, `UPDATE attempts SET status=?, progress=?, result_json=?, error_json=?, usage_json=?, updated_at=?, completed_at=? WHERE task_id=? AND number=?`, target, terminalProgress(target), resultJSON, errorJSON, string(usageJSON), stamp(now), stamp(now), id, task.Attempt)
	}
	return err
}

func terminalProgress(status Status) int {
	if status == StatusSucceeded {
		return 100
	}
	return 0
}

const taskSelect = `SELECT id,parent_id,strategy,status,progress,goal,constraints_json,base_ref,complexity,tool_required,token_budget,worktree,branch,head,changed_files_json,diff_summary,model_profile,route_reason,result_json,error_json,usage_json,attempt,max_retries,trace_json,created_at,updated_at,started_at,completed_at FROM tasks`
const attemptSelect = `SELECT id,task_id,number,status,progress,worktree,branch,head,changed_files_json,diff_summary,model_profile,route_reason,result_json,error_json,usage_json,trace_json,created_at,updated_at,started_at,completed_at FROM attempts`

type scanner interface{ Scan(...any) error }

func scanTask(row scanner) (Task, error) {
	var task Task
	var status, constraints, changed, result, encodedError, usage, traceJSON, created, updated, started, completed string
	if err := row.Scan(&task.ID, &task.ParentID, &task.Strategy, &status, &task.Progress, &task.Goal, &constraints, &task.BaseRef, &task.Complexity, &task.ToolRequired, &task.TokenBudget, &task.Worktree, &task.Branch, &task.HEAD, &changed, &task.DiffSummary, &task.ModelProfile, &task.RouteReason, &result, &encodedError, &usage, &task.Attempt, &task.MaxRetries, &traceJSON, &created, &updated, &started, &completed); err != nil {
		return Task{}, err
	}
	task.Status = Status(status)
	task.Constraints = decodeStrings(constraints)
	task.ChangedFiles = decodeStrings(changed)
	task.Result = raw(result)
	task.Error = decodeError(encodedError)
	task.Usage = decodeUsage(usage)
	task.Trace = raw(traceJSON)
	task.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	task.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	task.StartedAt = parseOptional(started)
	task.CompletedAt = parseOptional(completed)
	return task, nil
}

func scanAttempt(row scanner) (Attempt, error) {
	var attempt Attempt
	var status, changed, result, encodedError, usage, traceJSON, created, updated, started, completed string
	if err := row.Scan(&attempt.ID, &attempt.TaskID, &attempt.Number, &status, &attempt.Progress, &attempt.Worktree, &attempt.Branch, &attempt.HEAD, &changed, &attempt.DiffSummary, &attempt.ModelProfile, &attempt.RouteReason, &result, &encodedError, &usage, &traceJSON, &created, &updated, &started, &completed); err != nil {
		return Attempt{}, err
	}
	attempt.Status = Status(status)
	attempt.ChangedFiles = decodeStrings(changed)
	attempt.Result = raw(result)
	attempt.Error = decodeError(encodedError)
	attempt.Usage = decodeUsage(usage)
	attempt.Trace = raw(traceJSON)
	attempt.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	attempt.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	attempt.StartedAt = parseOptional(started)
	attempt.CompletedAt = parseOptional(completed)
	return attempt, nil
}

func marshalOrEmpty(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func raw(value string) json.RawMessage {
	if value == "" {
		return nil
	}
	return json.RawMessage(value)
}

func decodeStrings(value string) []string {
	var result []string
	if json.Unmarshal([]byte(value), &result) != nil || result == nil {
		return []string{}
	}
	return result
}

func decodeError(value string) *provider.Error {
	if value == "" {
		return nil
	}
	var result provider.Error
	if json.Unmarshal([]byte(value), &result) != nil {
		return nil
	}
	return &result
}

func decodeUsage(value string) provider.Usage {
	var result provider.Usage
	_ = json.Unmarshal([]byte(value), &result)
	return result
}

func cloneStrings(values []string) []string {
	return append([]string(nil), values...)
}

func stamp(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func parseOptional(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}
