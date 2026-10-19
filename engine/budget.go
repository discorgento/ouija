package engine

import (
	"fmt"
	"sync"
)

// tracker enforces token/step/USD ceilings across concurrent agents.
type tracker struct {
	mu           sync.Mutex
	maxTokens    int
	maxSteps     int
	maxUSD       float64 // 0 = unlimited
	inCostPer1M  float64
	outCostPer1M float64

	tokens int
	steps  int
	usd    float64
}

func newTracker(maxTokens, maxSteps int, maxUSD, inCost, outCost float64) *tracker {
	return &tracker{
		maxTokens: maxTokens, maxSteps: maxSteps, maxUSD: maxUSD,
		inCostPer1M: inCost, outCostPer1M: outCost,
	}
}

// Add records usage and returns an error if a ceiling is exceeded (BudgetMeter).
// Negative token counts are rejected (defense against buggy providers).
func (t *tracker) Add(u Usage) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if u.InputTokens < 0 || u.OutputTokens < 0 {
		return fmt.Errorf("budget: negative token count (input=%d output=%d)", u.InputTokens, u.OutputTokens)
	}
	t.tokens += u.InputTokens + u.OutputTokens
	t.steps++
	t.usd += float64(u.InputTokens)/1e6*t.inCostPer1M + float64(u.OutputTokens)/1e6*t.outCostPer1M

	if t.maxTokens > 0 && t.tokens > t.maxTokens {
		return ErrBudget(fmt.Sprintf("budget: token ceiling reached (%d/%d)", t.tokens, t.maxTokens))
	}
	if t.maxSteps > 0 && t.steps > t.maxSteps {
		return ErrBudget(fmt.Sprintf("budget: step ceiling reached (%d/%d)", t.steps, t.maxSteps))
	}
	// maxUSD is only enforced when pricing is configured (otherwise usd stays 0).
	if t.maxUSD > 0 && t.usd > t.maxUSD {
		return ErrBudget(fmt.Sprintf("budget: cost ceiling reached ($%.2f/$%.2f)", t.usd, t.maxUSD))
	}
	return nil
}

func (t *tracker) spent() (tokens, steps int, usd float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.tokens, t.steps, t.usd
}
