// Package meter keeps per-team token counters in memory and decides whether a
// team may send another request.
package meter

import (
	"errors"
	"fmt"
	"slices"
	"sync"
)

var (
	// ErrNoBudget means the team has no budget configured. We fail closed:
	// no budget, no access.
	ErrNoBudget = errors.New("team has no budget")
	// ErrOverBudget means the team has already used its whole budget.
	ErrOverBudget = errors.New("team is over its token budget")
)

// TeamUsage is a point-in-time view of one team's budget.
type TeamUsage struct {
	Team      string `json:"team"`
	Used      int64  `json:"used"`
	Limit     int64  `json:"limit"`
	Remaining int64  `json:"remaining"`
}

// Meter counts tokens per team. It is safe for concurrent use: every read and
// write of the maps happens while holding mu.
type Meter struct {
	mu     sync.Mutex
	limits map[string]int64 // set once in New, never changed afterwards
	used   map[string]int64
}

// New returns a Meter with the given per-team token limits.
func New(limits map[string]int64) *Meter {
	m := &Meter{
		limits: make(map[string]int64, len(limits)),
		used:   make(map[string]int64, len(limits)),
	}
	for team, limit := range limits {
		m.limits[team] = limit
	}
	return m
}

// Allow reports whether team may send another request. It returns an error
// wrapping ErrNoBudget or ErrOverBudget when it may not.
//
// The check happens before the request and tokens are added after the
// response, so a team can go slightly over its limit: every request that was
// already allowed still finishes. This is a soft limit.
func (m *Meter) Allow(team string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	limit, ok := m.limits[team]
	if !ok {
		return fmt.Errorf("team %q: %w", team, ErrNoBudget)
	}
	if used := m.used[team]; used >= limit {
		return fmt.Errorf("team %q used %d of %d tokens: %w", team, used, limit, ErrOverBudget)
	}
	return nil
}

// Record adds tokens to the team's counter. Negative values are ignored.
func (m *Meter) Record(team string, tokens int64) {
	if tokens <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.used[team] += tokens
}

// Snapshot returns the usage of every team with a budget, sorted by team name.
func (m *Meter) Snapshot() []TeamUsage {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]TeamUsage, 0, len(m.limits))
	for team, limit := range m.limits {
		used := m.used[team]
		out = append(out, TeamUsage{
			Team:      team,
			Used:      used,
			Limit:     limit,
			Remaining: max(0, limit-used),
		})
	}
	slices.SortFunc(out, func(a, b TeamUsage) int {
		switch {
		case a.Team < b.Team:
			return -1
		case a.Team > b.Team:
			return 1
		}
		return 0
	})
	return out
}
