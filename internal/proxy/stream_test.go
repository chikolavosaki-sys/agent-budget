package proxy

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/chikolavosaki-sys/agent-budget/internal/meter"
)

func TestPrepareRequest(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		want       string // expected JSON (compared as data, not bytes); "" = unchanged bytes
		wantStream bool
		wantErr    bool
	}{
		{name: "not json", in: `hello`},
		{name: "json array", in: `[1,2]`},
		{name: "json null", in: `null`},
		{
			name: "normal request is re-encoded but equal",
			in:   `{"model":"m","messages":[{"role":"user","content":"hi"}]}`,
			want: `{"model":"m","messages":[{"role":"user","content":"hi"}]}`,
		},
		{
			name:       "stream gets include_usage",
			in:         `{"model":"m","stream":true}`,
			want:       `{"model":"m","stream":true,"stream_options":{"include_usage":true}}`,
			wantStream: true,
		},
		{
			name:       "include_usage false is turned on, other options kept",
			in:         `{"stream":true,"stream_options":{"include_usage":false,"x":1}}`,
			want:       `{"stream":true,"stream_options":{"include_usage":true,"x":1}}`,
			wantStream: true,
		},
		{
			name:       "stream_options null",
			in:         `{"stream":true,"stream_options":null}`,
			want:       `{"stream":true,"stream_options":{"include_usage":true}}`,
			wantStream: true,
		},
		{
			name: "stream false is left alone",
			in:   `{"stream":false}`,
			want: `{"stream":false}`,
		},
		{
			name: "stream that is not a bool is not a stream",
			in:   `{"stream":"yes"}`,
			want: `{"stream":"yes"}`,
		},
		{
			name:       "duplicate key: provider gets what we saw",
			in:         `{"stream":false,"stream":true}`,
			want:       `{"stream":true,"stream_options":{"include_usage":true}}`,
			wantStream: true,
		},
		{
			name:       "bad stream_options",
			in:         `{"stream":true,"stream_options":"please"}`,
			wantStream: true,
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, stream, err := prepareRequest([]byte(tt.in))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if stream != tt.wantStream {
				t.Errorf("stream = %v, want %v", stream, tt.wantStream)
			}
			if tt.wantErr {
				return
			}
			if tt.want == "" {
				if string(out) != tt.in {
					t.Errorf("body changed: %s", out)
				}
				return
			}
			assertSameJSON(t, string(out), tt.want)
		})
	}
}

func TestPrepareRequestKeepsSpecialCharacters(t *testing.T) {
	out, _, err := prepareRequest([]byte(`{"content":"<b>a & b</b>"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `<b>a & b</b>`) {
		t.Errorf("special characters were escaped: %s", out)
	}
}

func assertSameJSON(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("output is not JSON: %s", got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("bad test JSON: %s", want)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("JSON differs:\n got %s\nwant %s", got, want)
	}
}

// sseStream builds an OpenAI-style stream: content chunks with "usage": null,
// then the usage chunk, then [DONE].
func sseStream(lineEnd string, withUsage bool) string {
	var b strings.Builder
	for _, word := range []string{"Hello", " world"} {
		b.WriteString(`data: {"model":"fake-model","choices":[{"delta":{"content":"` + word + `"}}],"usage":null}` + lineEnd + lineEnd)
	}
	if withUsage {
		b.WriteString(`data: {"model":"fake-model","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":34,"total_tokens":46}}` + lineEnd + lineEnd)
	}
	b.WriteString("data: [DONE]" + lineEnd + lineEnd)
	return b.String()
}

func TestUsageStreamReader(t *testing.T) {
	wantUsage := Usage{Model: "fake-model", InputTokens: 12, OutputTokens: 34}
	huge := "data: " + strings.Repeat("x", maxSSELine+10) + "\n\n"

	tests := []struct {
		name      string
		stream    string
		oneByte   bool // read one byte at a time: every line is split across reads
		wantFound bool
	}{
		{name: "normal", stream: sseStream("\n", true), wantFound: true},
		{name: "one byte at a time", stream: sseStream("\n", true), oneByte: true, wantFound: true},
		{name: "CRLF line endings", stream: sseStream("\r\n", true), wantFound: true},
		{name: "no usage chunk", stream: sseStream("\n", false), wantFound: false},
		{name: "huge line is skipped", stream: huge + sseStream("\n", true), wantFound: true},
		{
			name:      "last line without newline",
			stream:    `data: {"model":"fake-model","usage":{"prompt_tokens":12,"completion_tokens":34}}`,
			wantFound: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var src io.Reader = strings.NewReader(tt.stream)
			if tt.oneByte {
				src = iotest.OneByteReader(src)
			}
			calls := 0
			var got Usage
			var found bool
			r := newUsageStreamReader(io.NopCloser(src), func(u Usage, f bool) {
				calls++
				got, found = u, f
			})

			out, err := io.ReadAll(r)
			if err != nil {
				t.Fatal(err)
			}
			_ = r.Close() // Close after EOF must not report twice

			if string(out) != tt.stream {
				t.Error("stream bytes were changed")
			}
			if calls != 1 {
				t.Fatalf("onDone called %d times, want 1", calls)
			}
			if found != tt.wantFound {
				t.Fatalf("found = %v, want %v", found, tt.wantFound)
			}
			if found && got != wantUsage {
				t.Errorf("usage = %+v, want %+v", got, wantUsage)
			}
		})
	}
}

func TestUsageStreamReaderBrokenConnection(t *testing.T) {
	// The connection breaks after the first chunk: no usage arrives.
	src := io.MultiReader(
		strings.NewReader(`data: {"choices":[],"usage":null}`+"\n\n"),
		iotest.ErrReader(errors.New("connection reset")),
	)
	calls := 0
	r := newUsageStreamReader(io.NopCloser(src), func(_ Usage, found bool) {
		calls++
		if found {
			t.Error("found usage in a broken stream")
		}
	})
	if _, err := io.ReadAll(r); err == nil {
		t.Fatal("expected the read error to reach the caller")
	}
	_ = r.Close()
	if calls != 1 {
		t.Fatalf("onDone called %d times, want 1", calls)
	}
}

// streamingProvider is a fake LLM that streams. It checks that the proxy
// asked for usage, and it only sends the rest of the stream after the test
// has received the first chunk — so a proxy that buffers the whole stream
// would fail this test.
func streamingProvider(t *testing.T, firstChunkSeen <-chan struct{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream        bool `json:"stream"`
			StreamOptions struct {
				IncludeUsage bool `json:"include_usage"`
			} `json:"stream_options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("provider got invalid JSON: %v", err)
		}
		if !req.Stream || !req.StreamOptions.IncludeUsage {
			t.Errorf("provider got stream=%v include_usage=%v, want both true", req.Stream, req.StreamOptions.IncludeUsage)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		parts := strings.SplitAfter(sseStream("\n", true), "\n\n")
		_, _ = io.WriteString(w, parts[0])
		flusher.Flush()

		if firstChunkSeen != nil {
			select {
			case <-firstChunkSeen:
			case <-time.After(5 * time.Second):
				t.Error("first chunk never reached the agent: the proxy is buffering the stream")
				return
			}
		}
		for _, part := range parts[1:] {
			_, _ = io.WriteString(w, part)
			flusher.Flush()
		}
	}
}

const streamRequest = `{"model":"fake-model","stream":true,"messages":[{"role":"user","content":"` + testPrompt + `"}]}`

func TestProxyStreamingIsLiveAndCounted(t *testing.T) {
	firstChunkSeen := make(chan struct{})
	m := meter.New(map[string]int64{DefaultTeam: 1000})
	proxyURL, logs := newTestProxyWithBudget(t, streamingProvider(t, firstChunkSeen), m)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		proxyURL+"/v1/chat/completions", strings.NewReader(streamRequest))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	br := bufio.NewReader(resp.Body)
	first, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	close(firstChunkSeen) // let the provider send the rest
	rest, err := io.ReadAll(br)
	if err != nil {
		t.Fatal(err)
	}

	if first+string(rest) != sseStream("\n", true) {
		t.Errorf("agent got a changed stream:\n%s%s", first, rest)
	}
	if got := usedBy(m, DefaultTeam); got != tokensPerCall {
		t.Errorf("used = %d, want %d", got, tokensPerCall)
	}
	log := logs.String()
	for _, want := range []string{`"stream":true`, `"input_tokens":12`, `"output_tokens":34`} {
		if !strings.Contains(log, want) {
			t.Errorf("log is missing %s\nlog: %s", want, log)
		}
	}
	assertNoSecrets(t, log)
}

// TestProxyStreamingCannotBypassBudget is the bug IDEA-1 fixes: before, a
// team could stream forever without being charged.
func TestProxyStreamingCannotBypassBudget(t *testing.T) {
	m := meter.New(map[string]int64{DefaultTeam: 50})
	proxyURL, _ := newTestProxyWithBudget(t, streamingProvider(t, nil), m)

	stream := func() int {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
			proxyURL+"/v1/chat/completions", strings.NewReader(streamRequest))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	for i, want := range []int{http.StatusOK, http.StatusOK, http.StatusTooManyRequests} {
		if got := stream(); got != want {
			t.Fatalf("stream %d: status = %d, want %d", i+1, got, want)
		}
	}
}

func TestProxyRejectsBadRequests(t *testing.T) {
	provider := &countingProvider{response: fakeAnswer}
	proxyURL, _ := newTestProxy(t, provider.ServeHTTP)

	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "bad stream_options", body: `{"stream":true,"stream_options":"x"}`, want: http.StatusBadRequest},
		{name: "too large", body: strings.Repeat("a", maxRequestBytes+1), want: http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
				proxyURL+"/v1/chat/completions", strings.NewReader(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != tt.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
	if got := provider.calls.Load(); got != 0 {
		t.Errorf("provider called %d times, want 0", got)
	}
}
