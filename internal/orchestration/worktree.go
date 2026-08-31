package orchestration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// WorktreeInfo is the inspectable state of one isolated checkout.
type WorktreeInfo struct {
	Path         string   `json:"path"`
	Branch       string   `json:"branch"`
	HEAD         string   `json:"head"`
	ChangedFiles []string `json:"changed_files"`
	DiffSummary  string   `json:"diff_summary"`
}

// WorktreeManager creates and inspects only worktrees it created itself.
type WorktreeManager struct {
	repo    string
	root    string
	mu      sync.Mutex
	created map[string]string
}

func NewWorktreeManager(repo, root string) (*WorktreeManager, error) {
	if strings.TrimSpace(repo) == "" || strings.TrimSpace(root) == "" {
		return nil, errors.New("repository and worktree root are required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &WorktreeManager{repo: repo, root: root, created: make(map[string]string)}, nil
}

// Create requires an explicit base ref and never merges the resulting branch.
func (m *WorktreeManager) Create(ctx context.Context, taskID string, attempt int, baseRef string) (WorktreeInfo, error) {
	if m == nil {
		return WorktreeInfo{}, errors.New("worktree manager is nil")
	}
	if strings.TrimSpace(taskID) == "" || attempt <= 0 || strings.TrimSpace(baseRef) == "" {
		return WorktreeInfo{}, errors.New("task ID, attempt, and base ref are required")
	}
	name := safeName(fmt.Sprintf("%s-%d", taskID, attempt))
	path := filepath.Join(m.root, name)
	branch := "prism/" + name
	if _, err := runGit(ctx, m.repo, "worktree", "add", "-b", branch, path, baseRef); err != nil {
		return WorktreeInfo{}, fmt.Errorf("create worktree: %w", err)
	}
	m.mu.Lock()
	m.created[path] = branch
	m.mu.Unlock()
	info, err := m.Inspect(ctx, path)
	if err != nil {
		return WorktreeInfo{}, err
	}
	return info, nil
}

// Inspect records branch, HEAD, tracked/untracked changes, and a diff summary.
func (m *WorktreeManager) Inspect(ctx context.Context, path string) (WorktreeInfo, error) {
	branch, err := runGit(ctx, path, "branch", "--show-current")
	if err != nil {
		return WorktreeInfo{}, fmt.Errorf("inspect branch: %w", err)
	}
	head, err := runGit(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return WorktreeInfo{}, fmt.Errorf("inspect HEAD: %w", err)
	}
	status, err := runGit(ctx, path, "status", "--porcelain=v1")
	if err != nil {
		return WorktreeInfo{}, fmt.Errorf("inspect status: %w", err)
	}
	diff, err := runGit(ctx, path, "diff", "--stat", "HEAD", "--")
	if err != nil {
		return WorktreeInfo{}, fmt.Errorf("inspect diff: %w", err)
	}
	files := parseChangedFiles(status)
	diffSummary := strings.TrimSpace(diff)
	if diffSummary == "" && len(files) > 0 {
		diffSummary = fmt.Sprintf("%d changed file(s); untracked changes are included", len(files))
	}
	return WorktreeInfo{Path: path, Branch: strings.TrimSpace(branch), HEAD: strings.TrimSpace(head), ChangedFiles: files, DiffSummary: diffSummary}, nil
}

// Cleanup removes only a worktree created by this manager and unregisters it.
func (m *WorktreeManager) Cleanup(ctx context.Context, path string) error {
	if m == nil {
		return errors.New("worktree manager is nil")
	}
	m.mu.Lock()
	branch, ok := m.created[path]
	if ok {
		delete(m.created, path)
	}
	m.mu.Unlock()
	if !ok {
		return errors.New("worktree was not created by this manager")
	}
	if _, err := runGit(ctx, m.repo, "worktree", "remove", "--force", path); err != nil {
		m.mu.Lock()
		m.created[path] = branch
		m.mu.Unlock()
		return fmt.Errorf("remove worktree: %w", err)
	}
	return nil
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	commandArgs := append([]string{"-C", dir}, args...)
	command := exec.CommandContext(ctx, "git", commandArgs...)
	output, err := command.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("git %v: %w: %s", args, err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func parseChangedFiles(status string) []string {
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(status), "\n") {
		if len(line) < 4 {
			continue
		}
		file := strings.TrimSpace(line[3:])
		if index := strings.Index(file, " -> "); index >= 0 {
			file = strings.TrimSpace(file[index+4:])
		}
		if file != "" {
			files = append(files, file)
		}
	}
	return files
}

func safeName(value string) string {
	value = strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(value)
	return value
}
