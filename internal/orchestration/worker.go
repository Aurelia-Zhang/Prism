package orchestration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Aurelia-Zhang/Prism/internal/provider"
)

// Worker is the minimal execution contract used by Manager.
type Worker interface {
	Run(context.Context, WorkerRequest) (WorkerResult, error)
}

type WorkerRequest struct {
	TaskID       string   `json:"task_id"`
	Goal         string   `json:"goal"`
	Constraints  []string `json:"constraints,omitempty"`
	WorktreePath string   `json:"worktree_path"`
	ModelProfile string   `json:"model_profile"`
	Budget       int64    `json:"budget"`
	Strategy     string   `json:"strategy"`
	Attempt      int      `json:"attempt"`
}

type WorkerResult struct {
	Status       Status          `json:"status"`
	Summary      string          `json:"summary"`
	ChangedFiles []string        `json:"changed_files,omitempty"`
	Validation   []string        `json:"validation,omitempty"`
	Usage        provider.Usage  `json:"usage"`
	Error        *provider.Error `json:"error,omitempty"`
}

// CommandWorker exchanges exactly one JSON request and one JSON result with a local command.
type CommandWorker struct {
	Command []string
	Env     []string
	Dir     string
}

func (w CommandWorker) Run(ctx context.Context, request WorkerRequest) (WorkerResult, error) {
	if len(w.Command) == 0 || strings.TrimSpace(w.Command[0]) == "" {
		return WorkerResult{}, errors.New("worker command is required")
	}
	input, err := json.Marshal(request)
	if err != nil {
		return WorkerResult{}, fmt.Errorf("encode worker request: %w", err)
	}
	command := exec.CommandContext(ctx, w.Command[0], w.Command[1:]...)
	command.Stdin = bytes.NewReader(input)
	command.Dir = w.Dir
	command.Env = append(os.Environ(), w.Env...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	if ctx.Err() != nil {
		return WorkerResult{}, provider.ErrorFrom(ctx.Err(), "worker_canceled")
	}
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return WorkerResult{}, provider.NewError("worker_process_error", message)
	}
	var result WorkerResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return WorkerResult{}, provider.NewError("worker_invalid_json", err.Error())
	}
	if result.Status != StatusSucceeded && result.Status != StatusFailed && result.Status != StatusCancelled {
		return WorkerResult{}, provider.NewError("worker_invalid_status", "worker result has invalid status")
	}
	return result, nil
}
