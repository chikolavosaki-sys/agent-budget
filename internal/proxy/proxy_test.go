package proxy

import (
	"bytes"
	"compress/gzip"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/chikolavosaki-sys/agent-budget/internal/agents"
)

const (
	testPrompt = "SECRET_PROMPT_TEXT"
	testAPIKey = "sk-test-SECRET_KEY" //nolint:gosec // fake key, used only to check it never appears in logs
	fakeAnswer = `{"id":"chatcmpl-1","model":"fake-model",` +
		`"choices":[{"message":{"role":"assistant","content":"SECRET_ANSWER_TEXT"}}],` +
		`"usage":{"prompt_tokens":12,"completion_tokens":34,"total_tokens":46}}`
)

// newTestProxy starts a fake LLM provider and our proxy in front of it.
// It returns the proxy's URL and a buffer that collects the proxy's logs.
func newTestProxy(t *testing.T, provider http.HandlerFunc) (string, *bytes.Buffer) {
	t.Helper()
	return newTestProxyWithOptions(t, provider, Options{})
}

// newTestProxyWithOptions is newTestProxy with options (keys, budgets). The
// logger in opts is replaced by one that writes into the returned buffer.
func newTestProxyWithOptions(t *testing.T, provider http.HandlerFunc, opts Options) (string, *bytes.Buffer) {
	t.Helper()
	upstream := httptest.NewServer(provider)
	t.Cleanup(upstream.Close)

	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	opts.Logger = slog.New(slog.NewJSONHandler(logs, nil))
	return startProxy(t, u, opts), logs
}

func startProxy(t *testing.T, upstream *url.URL, opts Options) string {
	t.Helper()
	p, err := New(upstream, opts)
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(p)
	t.Cleanup(front.Close)
	return front.URL
}

// postChat sends one chat request through the proxy like an agent would, and
// returns the status code and body the agent received.
func postChat(t *testing.T, proxyURL string, header http.Header) (int, string) {
	t.Helper()
	body := `{"model":"fake-model","messages":[{"role":"user","content":"` + testPrompt + `"}]}`
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		proxyURL+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := http.DefaultTransport.RoundTrip(req) // raw transport: no automatic gzip handling
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(got)
}

func TestProxyForwardsAndLogsUsage(t *testing.T) {
	var gotPath, gotAuth string
	proxyURL, logs := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fakeAnswer)
	})

	status, got := postChat(t, proxyURL, nil)

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if got != fakeAnswer {
		t.Errorf("agent got a changed body:\n got %s\nwant %s", got, fakeAnswer)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("provider saw path %q", gotPath)
	}
	if gotAuth != "Bearer "+testAPIKey {
		t.Errorf("Authorization header was not forwarded")
	}

	log := logs.String()
	for _, want := range []string{`"input_tokens":12`, `"output_tokens":34`, `"model":"fake-model"`, `"status":200`} {
		if !strings.Contains(log, want) {
			t.Errorf("log is missing %s\nlog: %s", want, log)
		}
	}
	assertNoSecrets(t, log)
}

func TestProxyHandlesGzipFromProvider(t *testing.T) {
	proxyURL, logs := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			_, _ = io.WriteString(w, fakeAnswer)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		_, _ = io.WriteString(zw, fakeAnswer)
		_ = zw.Close()
	})

	// The agent asks for gzip; the proxy must still be able to count tokens.
	postChat(t, proxyURL, http.Header{"Accept-Encoding": {"gzip"}})

	if !strings.Contains(logs.String(), `"input_tokens":12`) {
		t.Errorf("tokens not counted for gzip response\nlog: %s", logs.String())
	}
}

func TestProxyUpstreamError(t *testing.T) {
	proxyURL, logs := newTestProxy(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
	})

	status, _ := postChat(t, proxyURL, nil)
	if status != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 passed through", status)
	}
	if !strings.Contains(logs.String(), "failed upstream") {
		t.Errorf("expected a 'failed upstream' log\nlog: %s", logs.String())
	}
}

func TestProxyStreamingIsPassedThrough(t *testing.T) {
	const stream = "data: {\"choices\":[]}\n\ndata: [DONE]\n\n"
	proxyURL, logs := newTestProxy(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	})

	_, got := postChat(t, proxyURL, nil)
	if got != stream {
		t.Errorf("stream body changed: %q", got)
	}
	if !strings.Contains(logs.String(), "stream ended without usage") {
		t.Errorf("expected a 'stream ended without usage' log\nlog: %s", logs.String())
	}
}

func TestNewRejectsHalfConfiguredOptions(t *testing.T) {
	u, _ := url.Parse("http://localhost:1")
	if _, err := New(u, Options{Agents: &agents.Registry{}}); err == nil {
		t.Error("Agents without Budget should be an error")
	}
}

func TestProxyUpstreamDown(t *testing.T) {
	// Point the proxy at a server that is already closed.
	dead := httptest.NewServer(http.NotFoundHandler())
	u, _ := url.Parse(dead.URL)
	dead.Close()

	logs := &bytes.Buffer{}
	frontURL := startProxy(t, u, Options{Logger: slog.New(slog.NewJSONHandler(logs, nil))})

	status, _ := postChat(t, frontURL, nil)
	if status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", status)
	}
	assertNoSecrets(t, logs.String())
}

// assertNoSecrets fails if prompts, answers or API keys leaked into the logs.
func assertNoSecrets(t *testing.T, log string) {
	t.Helper()
	for _, secret := range []string{testPrompt, testAPIKey, "SECRET_ANSWER_TEXT", keyBot, keyHelper, keyOther, upstreamKey} {
		if strings.Contains(log, secret) {
			t.Errorf("log leaked %q", secret)
		}
	}
}
