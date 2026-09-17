package agent

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// IterationBudgetToolName is the model-facing renewal tool for the main loop's
// iteration budget. It is registered only for the session's conversational lead;
// sub-agents and expert members never receive it (their iteration count is a
// capability ceiling that may only narrow).
const IterationBudgetToolName = "extend_budget"

const (
	defaultIterationBudgetSoft      = 200
	defaultRenewFactor              = 0.5
	defaultMaxRenewals              = 2
	defaultIterationBudgetWallClock = 16 * time.Hour
)

// IterationBudgetPolicy bounds the main loop's iteration count and governs
// model-requested renewals. The zero value disables renewal: the loop keeps the
// legacy fixed-MaxIterations behavior.
//
// Soft is the initial effective limit, Hard the ceiling renewal may never pass,
// RenewFactor the fraction of Soft granted per renewal, MaxRenewals the number
// of grants allowed per run, MinInterval the minimum number of iterations between
// two grants, and MaxWallClock the total wall-clock cap for one run.
type IterationBudgetPolicy struct {
	Soft         int
	Hard         int
	RenewFactor  float64
	MaxRenewals  int
	MinInterval  int
	MaxWallClock time.Duration
}

// Normalize applies defaults relative to the supplied soft limit (used when the
// policy leaves Soft unset). A normalized policy is always Enabled.
func (p IterationBudgetPolicy) Normalize(soft int) IterationBudgetPolicy {
	if soft <= 0 {
		soft = defaultIterationBudgetSoft
	}
	out := p
	if out.Soft <= 0 {
		out.Soft = soft
	}
	if out.Hard < out.Soft {
		out.Hard = out.Soft * 2
	}
	if out.RenewFactor <= 0 {
		out.RenewFactor = defaultRenewFactor
	}
	if out.RenewFactor > 1 {
		out.RenewFactor = 1
	}
	if out.MaxRenewals <= 0 {
		out.MaxRenewals = defaultMaxRenewals
	}
	if out.MinInterval < 0 {
		out.MinInterval = 0
	}
	if out.MinInterval == 0 {
		out.MinInterval = out.Soft / 10
		if out.MinInterval < 1 {
			out.MinInterval = 1
		}
	}
	if out.MaxWallClock <= 0 {
		out.MaxWallClock = defaultIterationBudgetWallClock
	}
	return out
}

// Enabled reports whether renewal is configured (a Hard ceiling above Soft).
func (p IterationBudgetPolicy) Enabled() bool { return p.Soft > 0 && p.Hard > p.Soft }

// iterationBudget is the per-run, Runtime-owned budget handle shared between the
// agent loop and the extend_budget tool. The loop owns the turn counter and the
// limit; the tool may only request a clamped increase.
type iterationBudget struct {
	mu sync.Mutex

	soft        int
	hard        int
	limit       int
	renewFactor float64
	maxRenewals int
	minInterval int

	turn        int
	renewals    int
	lastRenewal int
}

func newIterationBudget(policy IterationBudgetPolicy, soft int) *iterationBudget {
	p := policy.Normalize(soft)
	return &iterationBudget{
		soft:        p.Soft,
		hard:        p.Hard,
		limit:       p.Soft,
		renewFactor: p.RenewFactor,
		maxRenewals: p.MaxRenewals,
		minInterval: p.MinInterval,
		lastRenewal: -1,
	}
}

// Limit returns the current effective iteration limit.
func (b *iterationBudget) Limit() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.limit
}

// Soft returns the initial (pre-renewal) iteration limit.
func (b *iterationBudget) Soft() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.soft
}

// Hard returns the ceiling renewal may never pass.
func (b *iterationBudget) Hard() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hard
}

// Renewals returns the number of granted renewals.
func (b *iterationBudget) Renewals() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.renewals
}

// setTurn records the current iteration index so the tool can enforce the
// minimum interval between grants.
func (b *iterationBudget) setTurn(i int) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.turn = i
	b.mu.Unlock()
}

// defaultGrant returns the number of turns granted by one renewal when the model
// does not request a specific amount.
func (b *iterationBudget) defaultGrant() int {
	grant := int(math.Round(float64(b.soft) * b.renewFactor))
	if grant < 1 {
		grant = 1
	}
	return grant
}

// Request clamps and applies a renewal. It returns the granted turn count, the
// remaining turns after the grant, and the number of renewals used. A zero grant
// with a nil error means the request was valid but the ceiling is already reached.
func (b *iterationBudget) Request(additional int, reason string) (granted, remaining, renewals int, err error) {
	if b == nil {
		return 0, 0, 0, fmt.Errorf("iteration budget renewal is not available for this run")
	}
	if strings.TrimSpace(reason) == "" {
		return 0, 0, 0, fmt.Errorf("reason is required to request more turns")
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.renewals >= b.maxRenewals {
		return 0, b.limit - b.turn, b.renewals, fmt.Errorf("iteration budget renewal limit reached (%d/%d)", b.renewals, b.maxRenewals)
	}
	if b.lastRenewal >= 0 && b.turn-b.lastRenewal < b.minInterval {
		return 0, b.limit - b.turn, b.renewals, fmt.Errorf("iteration budget was renewed too recently (wait %d more turns)", b.minInterval-(b.turn-b.lastRenewal))
	}
	if b.limit >= b.hard {
		return 0, b.limit - b.turn, b.renewals, fmt.Errorf("iteration budget is already at its hard ceiling (%d)", b.hard)
	}

	want := additional
	if want <= 0 {
		want = b.defaultGrant()
	}
	next := b.limit + want
	if next > b.hard {
		next = b.hard
	}
	granted = next - b.limit
	if granted <= 0 {
		return 0, b.limit - b.turn, b.renewals, nil
	}
	b.limit = next
	b.renewals++
	b.lastRenewal = b.turn
	return granted, b.limit - b.turn, b.renewals, nil
}

type iterationBudgetContextKey struct{}

// contextWithIterationBudget attaches the per-run budget handle to the run
// context so the extend_budget tool can reach it without owning run state.
func contextWithIterationBudget(ctx context.Context, b *iterationBudget) context.Context {
	if ctx == nil || b == nil {
		return ctx
	}
	return context.WithValue(ctx, iterationBudgetContextKey{}, b)
}

// iterationBudgetFromContext extracts the per-run budget handle.
func iterationBudgetFromContext(ctx context.Context) (*iterationBudget, bool) {
	if ctx == nil {
		return nil, false
	}
	b, ok := ctx.Value(iterationBudgetContextKey{}).(*iterationBudget)
	return b, ok && b != nil
}
