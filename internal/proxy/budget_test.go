package proxy

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/chikolavosaki-sys/agent-budget/internal/meter"
)

// fakeAnswer uses 12 input + 34 output tokens.
const tokensPerCall = 12 + 34

// countingProvider is a fake LLM that counts how often it was called and
// remembers the last team header it saw.
type countingProvider struct {
	calls    atomic.Int32
	sawTeam  atomic.Value // string
	status   int
	response string
}

func (c *countingProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.calls.Add(1)
	c.sawTeam.Store(r.Header.Get(TeamHeader))
	w.Header().Set("Content-Type", "application/json")
	if c.status != 0 {
		w.WriteHeader(c.status)
	}
	_, _ = io.WriteString(w, c.response)
}

func teamHeader(team string) http.Header {
	return http.Header{TeamHeader: {team}}
}

func usedBy(m *meter.Meter, team string) int64 {
	for _, u := range m.Snapshot() {
		if u.Team == team {
			return u.Used
		}
	}
	return -1
}

func TestBudgetBlocksWhenOver(t *testing.T) {
	provider := &countingProvider{response: fakeAnswer}
	m := meter.New(map[string]int64{DefaultTeam: 50})
	proxyURL, logs := newTestProxyWithBudget(t, provider.ServeHTTP, m)

	// Call 1: used 0 < 50 → allowed, used becomes 46.
	// Call 2: used 46 < 50 → allowed (soft limit), used becomes 92.
	// Call 3: used 92 >= 50 → blocked before reaching the provider.
	wantStatus := []int{http.StatusOK, http.StatusOK, http.StatusTooManyRequests}
	for i, want := range wantStatus {
		status, body := postChat(t, proxyURL, nil)
		if status != want {
			t.Fatalf("call %d: status = %d, want %d (body %s)", i+1, status, want, body)
		}
		if want == http.StatusTooManyRequests && !strings.Contains(body, "budget_exceeded") {
			t.Errorf("429 body should say budget_exceeded, got %s", body)
		}
	}
	if got := provider.calls.Load(); got != 2 {
		t.Errorf("provider called %d times, want 2 (blocked call must not reach it)", got)
	}
	if got := usedBy(m, DefaultTeam); got != 2*tokensPerCall {
		t.Errorf("used = %d, want %d", got, 2*tokensPerCall)
	}
	if !strings.Contains(logs.String(), "request blocked") {
		t.Errorf("expected a 'request blocked' log\nlog: %s", logs.String())
	}
	assertNoSecrets(t, logs.String())
}

func TestBudgetUnknownTeamFailsClosed(t *testing.T) {
	provider := &countingProvider{response: fakeAnswer}
	m := meter.New(map[string]int64{"team-a": 1000})
	proxyURL, _ := newTestProxyWithBudget(t, provider.ServeHTTP, m)

	for _, team := range []string{"ghost", ""} { // "" falls back to DefaultTeam, which has no budget here
		status, body := postChat(t, proxyURL, teamHeader(team))
		if status != http.StatusForbidden {
			t.Errorf("team %q: status = %d, want 403", team, status)
		}
		if !strings.Contains(body, "no_budget") {
			t.Errorf("team %q: body should say no_budget, got %s", team, body)
		}
	}
	if got := provider.calls.Load(); got != 0 {
		t.Errorf("provider called %d times, want 0", got)
	}
}

func TestBudgetCountsPerTeamAndHidesHeader(t *testing.T) {
	provider := &countingProvider{response: fakeAnswer}
	m := meter.New(map[string]int64{DefaultTeam: 1000, "team-a": 1000})
	proxyURL, _ := newTestProxyWithBudget(t, provider.ServeHTTP, m)

	if status, _ := postChat(t, proxyURL, teamHeader("team-a")); status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if got := usedBy(m, "team-a"); got != tokensPerCall {
		t.Errorf("team-a used = %d, want %d", got, tokensPerCall)
	}
	if got := usedBy(m, DefaultTeam); got != 0 {
		t.Errorf("default used = %d, want 0", got)
	}
	if saw, _ := provider.sawTeam.Load().(string); saw != "" {
		t.Errorf("provider saw %s: %q; the header must be removed", TeamHeader, saw)
	}
}

func TestBudgetNotChargedOnUpstreamError(t *testing.T) {
	provider := &countingProvider{status: http.StatusInternalServerError, response: `{"error":"boom"}`}
	m := meter.New(map[string]int64{DefaultTeam: 1000})
	proxyURL, _ := newTestProxyWithBudget(t, provider.ServeHTTP, m)

	postChat(t, proxyURL, nil)
	if got := usedBy(m, DefaultTeam); got != 0 {
		t.Errorf("used = %d, want 0 for a failed call", got)
	}
}

// TestConcurrentRequestsCountedExactly sends many requests at the same time
// through the real proxy and checks that no tokens are lost. Run with -race.
func TestConcurrentRequestsCountedExactly(t *testing.T) {
	const (
		agents   = 20
		requests = 10
	)
	provider := &countingProvider{response: fakeAnswer}
	m := meter.New(map[string]int64{DefaultTeam: 1 << 40})
	proxyURL, _ := newTestProxyWithBudget(t, provider.ServeHTTP, m)

	var wg sync.WaitGroup
	for range agents {
		wg.Go(func() {
			for range requests {
				// t.Fatal may only be called from the test goroutine, so use
				// a plain request and t.Error here.
				req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
					proxyURL+"/v1/chat/completions", strings.NewReader(`{}`))
				if err != nil {
					t.Error(err)
					return
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					t.Errorf("status = %d", resp.StatusCode)
				}
			}
		})
	}
	wg.Wait()

	if want := int64(agents * requests * tokensPerCall); usedBy(m, DefaultTeam) != want {
		t.Errorf("used = %d, want %d (lost updates!)", usedBy(m, DefaultTeam), want)
	}
}
