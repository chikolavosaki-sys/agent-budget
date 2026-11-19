package meter

import (
	"errors"
	"maps"
	"sync"
	"testing"
)

func TestAllow(t *testing.T) {
	tests := []struct {
		name    string
		limit   int64
		used    int64
		team    string
		wantErr error // nil means allowed
	}{
		{name: "fresh team", limit: 100, used: 0, team: "a"},
		{name: "just under limit", limit: 100, used: 99, team: "a"},
		{name: "exactly at limit", limit: 100, used: 100, team: "a", wantErr: ErrOverBudget},
		{name: "over limit", limit: 100, used: 150, team: "a", wantErr: ErrOverBudget},
		{name: "zero budget", limit: 0, used: 0, team: "a", wantErr: ErrOverBudget},
		{name: "unknown team fails closed", limit: 100, team: "nobody", wantErr: ErrNoBudget},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(map[string]int64{"a": tt.limit})
			m.Record("a", tt.used)

			err := m.Allow(tt.team)
			if tt.wantErr == nil && err != nil {
				t.Fatalf("Allow() = %v, want allowed", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("Allow() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestRecordIgnoresNegative(t *testing.T) {
	m := New(map[string]int64{"a": 10})
	m.Record("a", 5)
	m.Record("a", -100)
	m.Record("a", 0)
	if got := m.Snapshot()[0].Used; got != 5 {
		t.Fatalf("used = %d, want 5", got)
	}
}

func TestSnapshot(t *testing.T) {
	m := New(map[string]int64{"b": 10, "a": 100})
	m.Record("a", 30)
	m.Record("b", 25) // over budget: remaining must not go negative

	want := []TeamUsage{
		{Team: "a", Used: 30, Limit: 100, Remaining: 70},
		{Team: "b", Used: 25, Limit: 10, Remaining: 0},
	}
	got := m.Snapshot()
	if len(got) != len(want) {
		t.Fatalf("got %d teams, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Snapshot()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestNewCopiesLimits(t *testing.T) {
	limits := map[string]int64{"a": 10}
	m := New(limits)
	limits["a"] = 999 // changing the caller's map must not change the meter
	if got := m.Snapshot()[0].Limit; got != 10 {
		t.Fatalf("limit = %d, want 10", got)
	}
}

// TestConcurrentRecord runs many goroutines at once. Without the mutex,
// `go test -race` reports a data race and the total comes out wrong.
func TestConcurrentRecord(t *testing.T) {
	const (
		workers   = 100
		perWorker = 1000
	)
	m := New(map[string]int64{"a": 1 << 40, "b": 1 << 40})

	var wg sync.WaitGroup
	for i := range workers {
		team := "a"
		if i%2 == 1 {
			team = "b"
		}
		wg.Go(func() {
			for range perWorker {
				_ = m.Allow(team)
				m.Record(team, 1)
				_ = m.Snapshot()
			}
		})
	}
	wg.Wait()

	for _, u := range m.Snapshot() {
		if want := int64(workers / 2 * perWorker); u.Used != want {
			t.Errorf("team %s used = %d, want %d (lost updates!)", u.Team, u.Used, want)
		}
	}
}

func TestParseBudgets(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    map[string]int64
		wantErr bool
	}{
		{name: "empty", in: "", want: map[string]int64{}},
		{name: "one", in: "default=100", want: map[string]int64{"default": 100}},
		{name: "many with spaces", in: " a = 1 , b=2,", want: map[string]int64{"a": 1, "b": 2}},
		{name: "zero is allowed", in: "a=0", want: map[string]int64{"a": 0}},
		{name: "missing equals", in: "a100", wantErr: true},
		{name: "missing team", in: "=100", wantErr: true},
		{name: "not a number", in: "a=lots", wantErr: true},
		{name: "negative", in: "a=-5", wantErr: true},
		{name: "duplicate", in: "a=1,a=2", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseBudgets(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && !maps.Equal(got, tt.want) {
				t.Errorf("ParseBudgets(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
