package proxy

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/chikolavosaki-sys/agent-budget/internal/agents"
	"github.com/chikolavosaki-sys/agent-budget/internal/meter"
)

// fakeAnswer uses 12 input + 34 output tokens.
const tokensPerCall = 12 + 34

// Virtual keys for the test agents, and the real provider key. They contain
// SECRET so assertNoSecrets can check none of them reach the logs.
const (
	keyBot      = "ab-test-bot-SECRET"
	keyHelper   = "ab-test-helper-SECRET"
	keyOther    = "ab-test-other-SECRET"
	upstreamKey = "sk-upstream-SECRET" //nolint:gosec // fake key for tests
)

// budgetSetup builds the test world:
//
//	team "pay"    (100 tokens): agent "bot" (own budget 50), agent "helper" (no own budget)
//	team "search" (huge):       agent "other"
func budgetSetup(t *testing.T) (Options, *meter.Meter) {
	t.Helper()
	agentJSON := func(name, team, key, budget string) string {
		b := ""
		if budget != "" {
			b = `"budget":` + budget + `,`
		}
		return `{"name":"` + name + `","team":"` + team + `","owner":"test",` + b +
			`"key_sha256":"` + agents.HashKey(key) + `"}`
	}
	registry, err := agents.Parse([]byte(`{
		"teams":[{"name":"pay","budget":100},{"name":"search","budget":1000000000}],
		"agents":[` +
		agentJSON("bot", "pay", keyBot, "50") + "," +
		agentJSON("helper", "pay", keyHelper, "") + "," +
		agentJSON("other", "search", keyOther, "") + `]}`))
	if err != nil {
		t.Fatal(err)
	}

	cfg := meter.Config{Teams: map[string]int64{}, Agents: map[string]meter.AgentBudget{}}
	for _, tm := range registry.Teams() {
		cfg.Teams[tm.Name] = tm.Budget
	}
	for _, a := range registry.Agents() {
		cfg.Agents[a.Name] = meter.AgentBudget{Team: a.Team, Limit: a.Budget}
	}
	m, err := meter.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return Options{Agents: registry, Budget: m, UpstreamKey: upstreamKey}, m
}

func bearer(key string) http.Header {
	return http.Header{"Authorization": {"Bearer " + key}}
}

func agentUsed(m *meter.Meter, name string) int64 { return usedIn(m.Snapshot().Agents, name) }
func teamUsed(m *meter.Meter, name string) int64  { return usedIn(m.Snapshot().Teams, name) }

func usedIn(us []meter.Usage, name string) int64 {
	for _, u := range us {
		if u.Name == name {
			return u.Used
		}
	}
	return -1
}

// countingProvider is a fake LLM that counts how often it was called and
// remembers the last Authorization header it saw.
type countingProvider struct {
	calls    atomic.Int32
	sawAuth  atomic.Value // string
	status   int
	response string
}

func (c *countingProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.calls.Add(1)
	c.sawAuth.Store(r.Header.Get("Authorization"))
	w.Header().Set("Content-Type", "application/json")
	if c.status != 0 {
		w.WriteHeader(c.status)
	}
	_, _ = io.WriteString(w, c.response)
}

func TestVirtualKeyRequired(t *testing.T) {
	provider := &countingProvider{response: fakeAnswer}
	opts, _ := budgetSetup(t)
	proxyURL, logs := newTestProxyWithOptions(t, provider.ServeHTTP, opts)

	tests := []struct {
		name   string
		header http.Header
	}{
		{name: "no Authorization", header: http.Header{"Authorization": nil}},
		{name: "empty bearer", header: http.Header{"Authorization": {"Bearer "}}},
		{name: "wrong key", header: bearer("ab-wrong-SECRET")},
		{name: "real provider key is not a virtual key", header: bearer(testAPIKey)},
		{name: "hash instead of key", header: bearer(agents.HashKey(keyBot))},
		{name: "not a bearer token", header: http.Header{"Authorization": {"Basic " + keyBot}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := postChat(t, proxyURL, tt.header)
			if status != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", status)
			}
			if !strings.Contains(body, "invalid_api_key") {
				t.Errorf("body should say invalid_api_key, got %s", body)
			}
		})
	}
	if got := provider.calls.Load(); got != 0 {
		t.Errorf("provider called %d times, want 0", got)
	}
	if !strings.Contains(logs.String(), "unknown virtual key") {
		t.Errorf("expected an 'unknown virtual key' log\nlog: %s", logs.String())
	}
	assertNoSecrets(t, logs.String())
}

func TestVirtualKeySwappedForUpstreamKey(t *testing.T) {
	tests := []struct {
		name        string
		upstreamKey string
		auth        string
		want        string
	}{
		{name: "upstream key set", upstreamKey: upstreamKey, auth: "Bearer " + keyBot, want: "Bearer " + upstreamKey},
		{name: "no upstream key (Ollama)", upstreamKey: "", auth: "Bearer " + keyBot, want: ""},
		{name: "lowercase bearer accepted", upstreamKey: upstreamKey, auth: "bearer " + keyBot, want: "Bearer " + upstreamKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &countingProvider{response: fakeAnswer}
			opts, _ := budgetSetup(t)
			opts.UpstreamKey = tt.upstreamKey
			proxyURL, logs := newTestProxyWithOptions(t, provider.ServeHTTP, opts)

			if status, _ := postChat(t, proxyURL, http.Header{"Authorization": {tt.auth}}); status != http.StatusOK {
				t.Fatalf("status = %d, want 200", status)
			}
			if got, _ := provider.sawAuth.Load().(string); got != tt.want {
				t.Errorf("provider saw Authorization %q, want %q", got, tt.want)
			}
			if !strings.Contains(logs.String(), `"agent":"bot","team":"pay"`) {
				t.Errorf("log should name agent and team\nlog: %s", logs.String())
			}
			assertNoSecrets(t, logs.String())
		})
	}
}

func TestAgentBudgetBlocks(t *testing.T) {
	provider := &countingProvider{response: fakeAnswer}
	opts, m := budgetSetup(t)
	proxyURL, logs := newTestProxyWithOptions(t, provider.ServeHTTP, opts)

	// bot's own budget is 50: 0 → 46 → 92 (soft limit), then blocked.
	for i, want := range []int{http.StatusOK, http.StatusOK, http.StatusTooManyRequests} {
		status, body := postChat(t, proxyURL, bearer(keyBot))
		if status != want {
			t.Fatalf("call %d: status = %d, want %d (body %s)", i+1, status, want, body)
		}
		if want == http.StatusTooManyRequests && !strings.Contains(body, `agent \"bot\"`) {
			t.Errorf("429 should name the agent, got %s", body)
		}
	}
	if got := provider.calls.Load(); got != 2 {
		t.Errorf("provider called %d times, want 2 (blocked call must not reach it)", got)
	}
	if got := agentUsed(m, "bot"); got != 2*tokensPerCall {
		t.Errorf("bot used = %d, want %d", got, 2*tokensPerCall)
	}
	if got := teamUsed(m, "pay"); got != 2*tokensPerCall {
		t.Errorf("team pay used = %d, want %d", got, 2*tokensPerCall)
	}
	if !strings.Contains(logs.String(), "request blocked") {
		t.Errorf("expected a 'request blocked' log\nlog: %s", logs.String())
	}
	assertNoSecrets(t, logs.String())
}

func TestTeamBudgetSharedByAgents(t *testing.T) {
	provider := &countingProvider{response: fakeAnswer}
	opts, m := budgetSetup(t)
	proxyURL, _ := newTestProxyWithOptions(t, provider.ServeHTTP, opts)

	steps := []struct {
		key  string
		want int
	}{
		{keyHelper, http.StatusOK},              // team 0 → 46
		{keyHelper, http.StatusOK},              // team 46 → 92
		{keyBot, http.StatusOK},                 // bot 0 < 50, team 92 < 100 → team 138
		{keyHelper, http.StatusTooManyRequests}, // team is used up
		{keyBot, http.StatusTooManyRequests},    // bot has budget left, but its team does not
		{keyOther, http.StatusOK},               // another team is not affected
	}
	for i, s := range steps {
		status, body := postChat(t, proxyURL, bearer(s.key))
		if status != s.want {
			t.Fatalf("step %d: status = %d, want %d (body %s)", i+1, status, s.want, body)
		}
		if s.want == http.StatusTooManyRequests && !strings.Contains(body, `team \"pay\"`) {
			t.Errorf("step %d: 429 should name the team, got %s", i+1, body)
		}
	}
	if got := teamUsed(m, "pay"); got != 3*tokensPerCall {
		t.Errorf("team pay used = %d, want %d", got, 3*tokensPerCall)
	}
}

func TestBudgetNotChargedOnUpstreamError(t *testing.T) {
	provider := &countingProvider{status: http.StatusInternalServerError, response: `{"error":"boom"}`}
	opts, m := budgetSetup(t)
	proxyURL, _ := newTestProxyWithOptions(t, provider.ServeHTTP, opts)

	postChat(t, proxyURL, bearer(keyBot))
	if got := agentUsed(m, "bot"); got != 0 {
		t.Errorf("used = %d, want 0 for a failed call", got)
	}
}

// TestConcurrentRequestsCountedExactly sends many requests at the same time
// through the real proxy and checks that no tokens are lost. Run with -race.
func TestConcurrentRequestsCountedExactly(t *testing.T) {
	const (
		workers  = 20
		requests = 10
	)
	provider := &countingProvider{response: fakeAnswer}
	opts, m := budgetSetup(t)
	proxyURL, _ := newTestProxyWithOptions(t, provider.ServeHTTP, opts)

	var wg sync.WaitGroup
	for range workers {
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
				req.Header.Set("Authorization", "Bearer "+keyOther)
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

	want := int64(workers * requests * tokensPerCall)
	if got := agentUsed(m, "other"); got != want {
		t.Errorf("agent used = %d, want %d (lost updates!)", got, want)
	}
	if got := teamUsed(m, "search"); got != want {
		t.Errorf("team used = %d, want %d (lost updates!)", got, want)
	}
}
