package provider

import (
	"context"
	"time"
)

// RetryConfig bounds transient provider retries. A zero value uses conservative
// defaults and a custom Sleeper can make tests deterministic.
type RetryConfig struct {
	MaxAttempts  int
	MaxTotalWait time.Duration
	Backoff      func(attempt int) time.Duration
	Sleeper      func(context.Context, time.Duration) error
}

func (c RetryConfig) withDefaults() RetryConfig {
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 3
	}
	if c.MaxTotalWait <= 0 {
		c.MaxTotalWait = 10 * time.Second
	}
	if c.Backoff == nil {
		c.Backoff = func(attempt int) time.Duration {
			return time.Duration(1<<min(attempt-1, 6)) * 100 * time.Millisecond
		}
	}
	if c.Sleeper == nil {
		c.Sleeper = sleepContext
	}
	return c
}

// Retry runs fn until it succeeds, the error is non-retryable, output has
// already been emitted, or the attempt/wait budget is exhausted.
func Retry(ctx context.Context, config RetryConfig, fn func(attempt int) (err error, outputEmitted bool)) error {
	config = config.withDefaults()
	started := time.Now()
	for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return ErrorFrom(err, "network")
		}
		err, outputEmitted := fn(attempt)
		if err == nil {
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ErrorFrom(ctxErr, "network")
		}
		structured := ErrorFrom(err, "network")
		if !structured.Retryable || outputEmitted || attempt == config.MaxAttempts {
			return structured
		}
		wait := config.Backoff(attempt)
		if structured.RetryAfter > 0 {
			wait = structured.RetryAfter
		}
		remaining := config.MaxTotalWait - time.Since(started)
		if wait > remaining {
			return structured
		}
		if err := config.Sleeper(ctx, wait); err != nil {
			return ErrorFrom(err, "network")
		}
	}
	return nil
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
