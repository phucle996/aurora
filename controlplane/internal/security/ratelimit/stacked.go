package ratelimit

import (
	"context"
)

// Rule defines a single limiter rule in a stacked policy.
type Rule struct {
	Name string
	Key  string
	Rate Rate
	Cost int64
}

// Decision captures the outcome for a single rule.
type Decision struct {
	Name string
	Key  string
	Result
}

// StackedResult aggregates decisions for multiple rules.
type StackedResult struct {
	Allowed bool
	Results []Decision
	Blocked *Decision
}

// Stacked applies multiple token buckets in sequence.
type Stacked struct {
	bucket *Bucket
}

// NewStacked constructs a stacked limiter.
func NewStacked(bucket *Bucket) *Stacked {
	return &Stacked{bucket: bucket}
}

// Allow checks all rules and returns the aggregated result.
// Empty keys are skipped (useful for optional scopes like user/tenant).
func (s *Stacked) Allow(ctx context.Context, rules []Rule) (StackedResult, error) {
	if s == nil || s.bucket == nil {
		return StackedResult{Allowed: true}, nil
	}

	out := StackedResult{Allowed: true}
	blockedIdx := -1
	for _, rule := range rules {
		if rule.Key == "" {
			continue
		}
		cost := rule.Cost
		if cost <= 0 {
			cost = 1
		}
		res, err := s.bucket.Allow(ctx, rule.Key, rule.Rate, cost)
		if err != nil {
			return out, err
		}
		decision := Decision{
			Name:   rule.Name,
			Key:    rule.Key,
			Result: res,
		}
		out.Results = append(out.Results, decision)
		if !res.Allowed {
			out.Allowed = false
			if blockedIdx == -1 {
				blockedIdx = len(out.Results) - 1
			}
		}
	}

	if blockedIdx >= 0 {
		out.Blocked = &out.Results[blockedIdx]
	}

	return out, nil
}
