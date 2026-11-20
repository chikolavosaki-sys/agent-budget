// Package meter keeps per-agent and per-team token counters in memory and
// decides whether an agent may send another request.
package meter

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"sync"
)

var (
	// ErrUnknownAgent means the meter has never heard of this agent. We fail
	// closed: unknown agent, no access.
	ErrUnknownAgent = errors.New("unknown agent")
	// ErrNoBudget means the agent's team has no budget configured.
	ErrNoBudget = errors.New("team has no budget")
	// ErrOverBudget means the agent or its team has used its whole budget.
	ErrOverBudget = errors.New("over token budget")
)

// AgentBudget says which team an agent belongs to and, optionally, how many
// tokens the agent itself may use.
type AgentBudget struct {
	Team  string
	Limit *int64 // nil = no budget of its own; only the team budget applies
}

// Config is everything the meter needs to know at start.
type Config struct {
	Teams  map[string]int64       // team name → token limit
	Agents map[string]AgentBudget // agent name → team and optional own limit
}

// Usage is a point-in-time view of one budget holder (a team or an agent).
type Usage struct {
	Name      string `json:"name"`
	Team      string `json:"team,omitempty"` // set for agents
	Used      int64  `json:"used"`
	Limit     *int64 `json:"limit,omitempty"`     // nil for agents without their own budget
	Remaining *int64 `json:"remaining,omitempty"` // nil when Limit is nil
}

// Snapshot is the usage of every team and every agent.
type Snapshot struct {
	Teams  []Usage `json:"teams"`
	Agents []Usage `json:"agents"`
}

// Meter counts tokens per agent and per team. It is safe for concurrent use:
// every read and write of the maps happens while holding mu.
type Meter struct {
	mu sync.Mutex

	// Set once in New, never changed afterwards.
	teamLimit  map[string]int64
	agentTeam  map[string]string
	agentLimit map[string]int64 // only agents with their own budget

	teamUsed  map[string]int64
	agentUsed map[string]int64
}

// New returns a Meter for cfg. Every agent's team must have a budget.
func New(cfg Config) (*Meter, error) {
	m := &Meter{
		teamLimit:  make(map[string]int64, len(cfg.Teams)),
		agentTeam:  make(map[string]string, len(cfg.Agents)),
		agentLimit: make(map[string]int64),
		teamUsed:   make(map[string]int64, len(cfg.Teams)),
		agentUsed:  make(map[string]int64, len(cfg.Agents)),
	}
	for team, limit := range cfg.Teams {
		m.teamLimit[team] = limit
	}
	for agent, b := range cfg.Agents {
		if _, ok := m.teamLimit[b.Team]; !ok {
			return nil, fmt.Errorf("agent %q: team %q: %w", agent, b.Team, ErrNoBudget)
		}
		m.agentTeam[agent] = b.Team
		if b.Limit != nil {
			m.agentLimit[agent] = *b.Limit
		}
	}
	return m, nil
}

// Allow reports whether agent may send another request. Both the agent's own
// budget (if it has one) and its team's budget must have tokens left.
//
// The check happens before the request and tokens are added after the
// response, so a budget can go slightly over its limit: every request that was
// already allowed still finishes. This is a soft limit.
func (m *Meter) Allow(agent string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	team, ok := m.agentTeam[agent]
	if !ok {
		return fmt.Errorf("agent %q: %w", agent, ErrUnknownAgent)
	}
	if limit, ok := m.agentLimit[agent]; ok {
		if used := m.agentUsed[agent]; used >= limit {
			return fmt.Errorf("agent %q used %d of %d tokens: %w", agent, used, limit, ErrOverBudget)
		}
	}
	if used, limit := m.teamUsed[team], m.teamLimit[team]; used >= limit {
		return fmt.Errorf("team %q used %d of %d tokens: %w", team, used, limit, ErrOverBudget)
	}
	return nil
}

// Record adds tokens to the agent's counter and to its team's counter.
// Unknown agents and non-positive values are ignored.
func (m *Meter) Record(agent string, tokens int64) {
	if tokens <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	team, ok := m.agentTeam[agent]
	if !ok {
		return
	}
	m.agentUsed[agent] += tokens
	m.teamUsed[team] += tokens
}

// Snapshot returns the usage of every team and every agent, sorted by name.
func (m *Meter) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := Snapshot{
		Teams:  make([]Usage, 0, len(m.teamLimit)),
		Agents: make([]Usage, 0, len(m.agentTeam)),
	}
	for team, limit := range m.teamLimit {
		s.Teams = append(s.Teams, usage(team, "", m.teamUsed[team], &limit))
	}
	for agent, team := range m.agentTeam {
		var limit *int64
		if l, ok := m.agentLimit[agent]; ok {
			limit = &l
		}
		s.Agents = append(s.Agents, usage(agent, team, m.agentUsed[agent], limit))
	}
	byName := func(a, b Usage) int { return cmp.Compare(a.Name, b.Name) }
	slices.SortFunc(s.Teams, byName)
	slices.SortFunc(s.Agents, byName)
	return s
}

func usage(name, team string, used int64, limit *int64) Usage {
	u := Usage{Name: name, Team: team, Used: used, Limit: limit}
	if limit != nil {
		remaining := max(0, *limit-used)
		u.Remaining = &remaining
	}
	return u
}
