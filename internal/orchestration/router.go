package orchestration

import "fmt"

// ModelProfile is a small local routing profile, not a provider or price claim.
type ModelProfile struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	MaxTokens   int64  `json:"max_tokens"`
}

var profiles = map[string]ModelProfile{
	"fast":     {Name: "fast", Description: "short local task", MaxTokens: 2048},
	"balanced": {Name: "balanced", Description: "general tool-capable task", MaxTokens: 8192},
	"strong":   {Name: "strong", Description: "complex or retry task", MaxTokens: 16384},
}

type RouteRequest struct {
	Complexity   int
	ToolRequired bool
	TokenBudget  int64
	RetryHistory int
}

type Router interface {
	Route(RouteRequest) (ModelProfile, string)
}

// RuleRouter keeps routing explainable and deterministic for a portfolio demo.
type RuleRouter struct{}

func (RuleRouter) Route(request RouteRequest) (ModelProfile, string) {
	if request.RetryHistory > 0 {
		return profiles["strong"], fmt.Sprintf("retry history=%d requires a stronger profile", request.RetryHistory)
	}
	if request.Complexity >= 8 {
		return profiles["strong"], fmt.Sprintf("complexity=%d is high", request.Complexity)
	}
	if request.ToolRequired || request.Complexity >= 4 {
		return profiles["balanced"], fmt.Sprintf("tool_required=%t and complexity=%d fit the balanced profile", request.ToolRequired, request.Complexity)
	}
	if request.TokenBudget > 0 && request.TokenBudget <= profiles["fast"].MaxTokens {
		return profiles["fast"], fmt.Sprintf("token budget=%d fits the fast profile", request.TokenBudget)
	}
	return profiles["balanced"], "defaulted to balanced for an ordinary task"
}
