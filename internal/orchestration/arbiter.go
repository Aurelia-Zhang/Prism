package orchestration

import (
	"context"
	"fmt"
	"sort"

	"github.com/Aurelia-Zhang/Prism/internal/provider"
)

type Candidate struct {
	TaskID       string
	Strategy     string
	Status       Status
	Summary      string
	ChangedFiles []string
	Usage        provider.Usage
}

type CandidateScore struct {
	TaskID string  `json:"task_id"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

type Decision struct {
	WinnerID string           `json:"winner_id"`
	Scores   []CandidateScore `json:"scores"`
	Reason   string           `json:"reason"`
}

type Arbiter interface {
	Arbitrate(context.Context, string, []Candidate) (Decision, error)
}

// DeterministicArbiter prefers successful candidates, then more reported changes,
// then lexical task ID. It never merges or modifies a winner worktree.
type DeterministicArbiter struct{}

func (DeterministicArbiter) Arbitrate(_ context.Context, _ string, candidates []Candidate) (Decision, error) {
	if len(candidates) == 0 {
		return Decision{}, fmt.Errorf("no candidates to arbitrate")
	}
	sorted := append([]Candidate(nil), candidates...)
	sort.Slice(sorted, func(i, j int) bool {
		left, right := score(sorted[i]), score(sorted[j])
		if left != right {
			return left > right
		}
		return sorted[i].TaskID < sorted[j].TaskID
	})
	decision := Decision{WinnerID: sorted[0].TaskID, Reason: "selected the highest deterministic score; ties use task ID"}
	for _, candidate := range sorted {
		decision.Scores = append(decision.Scores, CandidateScore{TaskID: candidate.TaskID, Score: score(candidate), Reason: scoreReason(candidate)})
	}
	return decision, nil
}

func score(candidate Candidate) float64 {
	if candidate.Status != StatusSucceeded {
		return 0
	}
	return 100 + float64(len(candidate.ChangedFiles))
}

func scoreReason(candidate Candidate) string {
	if candidate.Status != StatusSucceeded {
		return "non-succeeded candidate is not eligible"
	}
	return fmt.Sprintf("succeeded candidate base=100 plus changed_files=%d", len(candidate.ChangedFiles))
}
