package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/chikolavosaki-sys/agent-budget/internal/meter"
)

// maxBodyBytes is the largest response body we read into memory to count
// tokens. Bigger bodies are still forwarded, just not counted.
const maxBodyBytes = 10 << 20 // 10 MiB

// TeamHeader is the request header an agent uses to say which team it belongs
// to. The proxy removes it before forwarding, so the provider never sees it.
const TeamHeader = "X-Budget-Team"

// DefaultTeam is used when a request has no TeamHeader.
const DefaultTeam = "default"

// Budget decides whether a team may send a request and records what it spent.
// *meter.Meter implements it. Implementations must be safe for concurrent use.
type Budget interface {
	// Allow returns nil if the team may send a request. Otherwise it returns
	// an error wrapping meter.ErrNoBudget or meter.ErrOverBudget.
	Allow(team string) error
	// Record adds the tokens a finished request used.
	Record(team string, tokens int64)
}

// Proxy sits between an agent and an LLM provider. It checks the team's budget,
// forwards the request unchanged, and counts the tokens in the response.
type Proxy struct {
	rp     *httputil.ReverseProxy
	logger *slog.Logger
	budget Budget // nil = watch-only: count and log, never block
}

// New returns a Proxy that sends all requests to upstream
// (for example http://localhost:11434 or https://api.openai.com).
// If budget is nil, the proxy only logs usage and never blocks.
func New(upstream *url.URL, logger *slog.Logger, budget Budget) *Proxy {
	p := &Proxy{logger: logger, budget: budget}
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			// Point the request at the provider: scheme, host and path prefix.
			r.SetURL(upstream)
			// Remove the agent's Accept-Encoding. Go's HTTP client then asks the
			// provider for gzip itself and unzips it for us, so we always get
			// plain JSON to read tokens from.
			r.Out.Header.Del("Accept-Encoding")
			// The team is our business, not the provider's.
			r.Out.Header.Del(TeamHeader)
		},
		ModifyResponse: p.recordUsage,
		ErrorHandler:   p.handleError,
	}
	return p
}

// requestInfo travels with each request in its context, from ServeHTTP to
// recordUsage.
type requestInfo struct {
	team  string
	start time.Time
}

type requestInfoKey struct{}

// ServeHTTP implements http.Handler. It is the "gate": budget check first,
// then forward.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	team := r.Header.Get(TeamHeader)
	if team == "" {
		team = DefaultTeam
	}

	if p.budget != nil {
		if err := p.budget.Allow(team); err != nil {
			p.reject(w, r, team, err)
			return
		}
	}

	info := requestInfo{team: team, start: time.Now()}
	ctx := context.WithValue(r.Context(), requestInfoKey{}, info)
	p.rp.ServeHTTP(w, r.WithContext(ctx))
}

// reject answers without contacting the provider: 429 if the team used up its
// budget, 403 if it has none. The body uses the OpenAI error shape so agent
// SDKs can show the message.
func (p *Proxy) reject(w http.ResponseWriter, r *http.Request, team string, err error) {
	status, code := http.StatusForbidden, "no_budget"
	if errors.Is(err, meter.ErrOverBudget) {
		status, code = http.StatusTooManyRequests, "budget_exceeded"
	}
	p.logger.WarnContext(r.Context(), "request blocked",
		slog.String("team", team), slog.Int("status", status), slog.String("reason", err.Error()))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"message": err.Error(), "type": code, "code": code},
	})
}

// recordUsage runs after the provider answers and before the answer goes back
// to the agent. It reads the body, logs token usage, and puts the exact same
// bytes back so the agent sees no difference.
func (p *Proxy) recordUsage(resp *http.Response) error {
	ctx := resp.Request.Context()
	attrs := []any{
		slog.String("method", resp.Request.Method),
		slog.String("path", resp.Request.URL.Path),
		slog.Int("status", resp.StatusCode),
	}
	info, _ := ctx.Value(requestInfoKey{}).(requestInfo)
	attrs = append(attrs, slog.String("team", info.team))
	if !info.start.IsZero() {
		attrs = append(attrs, slog.Int64("duration_ms", time.Since(info.start).Milliseconds()))
	}

	// Streaming answers (Server-Sent Events) arrive in many small pieces.
	// M0 does not count them yet; we pass them straight through.
	if mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mediaType == "text/event-stream" {
		p.logger.WarnContext(ctx, "streaming response: tokens not counted", attrs...)
		return nil
	}

	orig := resp.Body
	body, err := io.ReadAll(io.LimitReader(orig, maxBodyBytes+1))
	if err != nil {
		_ = orig.Close()
		return fmt.Errorf("read upstream response: %w", err)
	}
	if len(body) > maxBodyBytes {
		// Too big to count: glue the part we read back onto the rest.
		resp.Body = readCloser{io.MultiReader(bytes.NewReader(body), orig), orig}
		p.logger.WarnContext(ctx, "response too large: tokens not counted", attrs...)
		return nil
	}
	_ = orig.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))

	if resp.StatusCode != http.StatusOK {
		p.logger.WarnContext(ctx, "llm request failed upstream", attrs...)
		return nil
	}

	usage, err := ParseUsage(body)
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
		p.logger.WarnContext(ctx, "llm request: could not read token usage", attrs...)
		return nil
	}
	if p.budget != nil {
		p.budget.Record(info.team, int64(usage.InputTokens)+int64(usage.OutputTokens))
	}
	attrs = append(attrs,
		slog.String("model", usage.Model),
		slog.Int("input_tokens", usage.InputTokens),
		slog.Int("output_tokens", usage.OutputTokens),
	)
	p.logger.InfoContext(ctx, "llm request", attrs...)
	return nil
}

// handleError answers 502 Bad Gateway when the provider cannot be reached.
func (p *Proxy) handleError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		// The agent hung up; nobody is waiting for an answer.
		return
	}
	p.logger.ErrorContext(r.Context(), "upstream unreachable",
		slog.String("path", r.URL.Path), slog.String("error", err.Error()))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	_, _ = io.WriteString(w, `{"error":{"message":"upstream LLM provider unreachable","type":"bad_gateway"}}`)
}

// readCloser joins a Reader with a separate Closer.
type readCloser struct {
	io.Reader
	io.Closer
}
