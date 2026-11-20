package meter

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func ptr(n int64) *int64 { return &n }

// testMeter: team "pay" (100 tokens) with agents "bot" (own limit 30) and
// "free" (no own limit); team "search" (1000) with agent "finder".
func testMeter(t *testing.T) *Meter {
	t.Helper()
	m, err := New(Config{
		Teams: map[string]int64{"pay": 100, "search": 1000},
		Agents: map[string]AgentBudget{
			"bot":    {Team: "pay", Limit: ptr(30)},
			"free":   {Team: "pay"},
			"finder": {Team: "search"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNewRejectsAgentWithoutTeamBudget(t *testing.T) {
	_, err := New(Config{
		Teams:  map[string]int64{"pay": 100},
		Agents: map[string]AgentBudget{"lost": {Team: "nowhere"}},
	})
	if !errors.Is(err, ErrNoBudget) {
		t.Fatalf("err = %v, want ErrNoBudget", err)
	}
}

func TestAllow(t *testing.T) {
	tests := []struct {
		name    string
		spend   map[string]int64 // agent → tokens recorded before the check
		agent   string
		wantErr error // nil = allowed
	}{
		{name: "fresh agent", agent: "bot"},
		{name: "agent just under own limit", spend: map[string]int64{"bot": 29}, agent: "bot"},
		{name: "agent at own limit", spend: map[string]int64{"bot": 30}, agent: "bot", wantErr: ErrOverBudget},
		{name: "agent without own limit uses team budget", spend: map[string]int64{"free": 99}, agent: "free"},
		{name: "team limit reached by teammates", spend: map[string]int64{"bot": 20, "free": 80}, agent: "free", wantErr: ErrOverBudget},
		{name: "team limit blocks agent under own limit", spend: map[string]int64{"free": 100}, agent: "bot", wantErr: ErrOverBudget},
		{name: "other team is not affected", spend: map[string]int64{"free": 500}, agent: "finder"},
		{name: "unknown agent fails closed", agent: "ghost", wantErr: ErrUnknownAgent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testMeter(t)
			for agent, tokens := range tt.spend {
				m.Record(agent, tokens)
			}
			err := m.Allow(tt.agent)
			if tt.wantErr == nil && err != nil {
				t.Fatalf("Allow() = %v, want allowed", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("Allow() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestRecordIgnoresBadInput(t *testing.T) {
	m := testMeter(t)
	m.Record("bot", 5)
	m.Record("bot", -100)
	m.Record("bot", 0)
	m.Record("ghost", 50) // unknown agent: ignored, not charged to anyone

	s := m.Snapshot()
	if got := find(s.Agents, "bot").Used; got != 5 {
		t.Errorf("bot used = %d, want 5", got)
	}
	if got := find(s.Teams, "pay").Used; got != 5 {
		t.Errorf("pay used = %d, want 5", got)
	}
}

func TestSnapshot(t *testing.T) {
	m := testMeter(t)
	m.Record("bot", 40) // over its own limit of 30
	m.Record("free", 10)

	s := m.Snapshot()
	wantTeams := []Usage{
		{Name: "pay", Used: 50, Limit: ptr(100), Remaining: ptr(50)},
		{Name: "search", Used: 0, Limit: ptr(1000), Remaining: ptr(1000)},
	}
	wantAgents := []Usage{
		{Name: "bot", Team: "pay", Used: 40, Limit: ptr(30), Remaining: ptr(0)},
		{Name: "finder", Team: "search"},
		{Name: "free", Team: "pay", Used: 10},
	}
	assertUsages(t, "teams", s.Teams, wantTeams)
	assertUsages(t, "agents", s.Agents, wantAgents)
}

func TestNewCopiesConfig(t *testing.T) {
	cfg := Config{
		Teams:  map[string]int64{"pay": 10},
		Agents: map[string]AgentBudget{"bot": {Team: "pay"}},
	}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Teams["pay"] = 999 // changing the caller's maps must not change the meter
	delete(cfg.Agents, "bot")
	if got := *find(m.Snapshot().Teams, "pay").Limit; got != 10 {
		t.Errorf("limit = %d, want 10", got)
	}
	if err := m.Allow("bot"); err != nil {
		t.Errorf("bot should still be known: %v", err)
	}
}

// TestConcurrentRecord runs many goroutines at once. Without the mutex,
// `go test -race` reports a data race and the totals come out wrong.
func TestConcurrentRecord(t *testing.T) {
	const (
		workers   = 100
		perWorker = 1000
	)
	m, err := New(Config{
		Teams: map[string]int64{"pay": 1 << 40},
		Agents: map[string]AgentBudget{
			"a": {Team: "pay", Limit: ptr(1 << 40)},
			"b": {Team: "pay"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := range workers {
		agent := "a"
		if i%2 == 1 {
			agent = "b"
		}
		wg.Go(func() {
			for range perWorker {
				_ = m.Allow(agent)
				m.Record(agent, 1)
				_ = m.Snapshot()
			}
		})
	}
	wg.Wait()

	s := m.Snapshot()
	for _, agent := range []string{"a", "b"} {
		if got, want := find(s.Agents, agent).Used, int64(workers/2*perWorker); got != want {
			t.Errorf("agent %s used = %d, want %d (lost updates!)", agent, got, want)
		}
	}
	if got, want := find(s.Teams, "pay").Used, int64(workers*perWorker); got != want {
		t.Errorf("team used = %d, want %d (lost updates!)", got, want)
	}
}

func find(us []Usage, name string) Usage {
	for _, u := range us {
		if u.Name == name {
			return u
		}
	}
	return Usage{}
}

func assertUsages(t *testing.T, what string, got, want []Usage) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %d entries, want %d", what, len(got), len(want))
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Name != w.Name || g.Team != w.Team || g.Used != w.Used ||
			!eqPtr(g.Limit, w.Limit) || !eqPtr(g.Remaining, w.Remaining) {
			t.Errorf("%s[%d] = %s, want %s", what, i, fmtUsage(g), fmtUsage(w))
		}
	}
}

func eqPtr(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func fmtUsage(u Usage) string {
	s := func(p *int64) any {
		if p == nil {
			return "nil"
		}
		return *p
	}
	return fmt.Sprint(u.Name, u.Team, u.Used, s(u.Limit), s(u.Remaining))
}
