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
	"strings"
	"time"

	"github.com/chikolavosaki-sys/agent-budget/internal/agents"
	"github.com/chikolavosaki-sys/agent-budget/internal/meter"
)

// maxBodyBytes is the largest non-streaming response body we read into memory
// to count tokens. Bigger bodies are still forwarded, just not counted.
const maxBodyBytes = 10 << 20 // 10 MiB

// maxRequestBytes is the largest request body we accept. We must read the
// whole request to see whether it asks for streaming.
const maxRequestBytes = 32 << 20 // 32 MiB

// Identifier finds the agent that owns a virtual key.
// *agents.Registry implements it. Implementations must be safe for concurrent use.
type Identifier interface {
	Lookup(key string) (agents.Agent, bool)
}

// Budget decides whether an agent may send a request and records what it
// spent. *meter.Meter implements it. Implementations must be safe for
// concurrent use.
type Budget interface {
	// Allow returns nil if the agent may send a request. Otherwise it returns
	// an error wrapping meter.ErrOverBudget, meter.ErrUnknownAgent or
	// meter.ErrNoBudget.
	Allow(agent string) error
	// Record adds the tokens a finished request used to the agent and its team.
	Record(agent string, tokens int64)
}

// Options configures a Proxy. With Agents and Budget nil, the proxy runs in
// watch-only mode: no keys are checked, the agent's Authorization header is
// passed through, and nothing is blocked — usage is only logged.
type Options struct {
	Logger *slog.Logger
	Agents Identifier // who is calling (from the virtual key)
	Budget Budget     // may they spend more
	// UpstreamKey is sent to the provider as "Authorization: Bearer <key>"
	// in place of the agent's virtual key. Empty = send no Authorization
	// (fine for local providers like Ollama).
	UpstreamKey string
}

// Proxy sits between an agent and an LLM provider. It finds out which agent is
// calling, checks the budgets, forwards the request, and counts the tokens in
// the response.
type Proxy struct {
	rp          *httputil.ReverseProxy
	logger      *slog.Logger
	agents      Identifier
	budget      Budget
	upstreamKey string
}

// New returns a Proxy that sends all requests to upstream
// (for example http://localhost:11434 or https://api.openai.com).
// Agents and Budget must both be set, or both be nil (watch-only mode).
func New(upstream *url.URL, opts Options) (*Proxy, error) {
	if (opts.Agents == nil) != (opts.Budget == nil) {
		return nil, errors.New("proxy: Agents and Budget must both be set or both be nil")
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	p := &Proxy{logger: opts.Logger, agents: opts.Agents, budget: opts.Budget, upstreamKey: opts.UpstreamKey}
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			// Point the request at the provider: scheme, host and path prefix.
			r.SetURL(upstream)
			// Remove the agent's Accept-Encoding. Go's HTTP client then asks the
			// provider for gzip itself and unzips it for us, so we always get
			// plain JSON to read tokens from.
			r.Out.Header.Del("Accept-Encoding")
			// Swap the agent's virtual key for the real provider key. The
			// agent never sees the real key; the provider never sees ours.
			if p.agents != nil {
				r.Out.Header.Del("Authorization")
				if p.upstreamKey != "" {
					r.Out.Header.Set("Authorization", "Bearer "+p.upstreamKey)
				}
			}
		},
		ModifyResponse: p.recordUsage,
		ErrorHandler:   p.handleError,
	}
	return p, nil
}

// requestInfo travels with each request in its context, from ServeHTTP to
// recordUsage.
type requestInfo struct {
	agent  string // "" in watch-only mode
	team   string
	start  time.Time
	stream bool
}

type requestInfoKey struct{}

// ServeHTTP implements http.Handler. It is the "gate": who are you, may you
// spend more — and only then forward.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var info requestInfo

	if p.agents != nil {
		agent, ok := p.agents.Lookup(bearerToken(r.Header.Get("Authorization")))
		if !ok {
			// Never log the key itself — not even a wrong one.
			p.logger.WarnContext(r.Context(), "request rejected: missing or unknown virtual key",
				slog.String("path", r.URL.Path))
			writeError(w, http.StatusUnauthorized, "invalid_api_key", "missing or unknown virtual key")
			return
		}
		info.agent, info.team = agent.Name, agent.Team

		if err := p.budget.Allow(agent.Name); err != nil {
			p.reject(w, r, info, err)
			return
		}
	}

	stream, ok := p.prepareBody(w, r)
	if !ok {
		return
	}

	info.start, info.stream = time.Now(), stream
	ctx := context.WithValue(r.Context(), requestInfoKey{}, info)
	p.rp.ServeHTTP(w, r.WithContext(ctx))
}

// prepareBody reads the request body, lets prepareRequest adjust it, and puts
// the result back on r. It returns ok=false after writing an error response.
func (p *Proxy) prepareBody(w http.ResponseWriter, r *http.Request) (stream, ok bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	_ = r.Body.Close()
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "could not read request body")
		return false, false
	}
	if len(body) > maxRequestBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "request_too_large",
			fmt.Sprintf("request body is larger than %d bytes", maxRequestBytes))
		return false, false
	}
	if len(body) == 0 {
		r.Body, r.ContentLength = http.NoBody, 0
		return false, true
	}

	body, stream, err = prepareRequest(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return false, false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Del("Content-Length") // the transport sets it from r.ContentLength
	return stream, true
}

// writeError sends an error in the OpenAI error shape, which agent SDKs understand.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"message": message, "type": code, "code": code},
	})
}

// bearerToken returns the token from an "Authorization: Bearer <token>" header.
func bearerToken(header string) string {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// reject answers without contacting the provider: 429 if the agent or its team
// used up its budget, 403 otherwise. The body uses the OpenAI error shape so
// agent SDKs can show the message.
func (p *Proxy) reject(w http.ResponseWriter, r *http.Request, info requestInfo, err error) {
	status, code := http.StatusForbidden, "no_budget"
	if errors.Is(err, meter.ErrOverBudget) {
		status, code = http.StatusTooManyRequests, "budget_exceeded"
	}
	p.logger.WarnContext(r.Context(), "request blocked",
		slog.String("agent", info.agent), slog.String("team", info.team),
		slog.Int("status", status), slog.String("reason", err.Error()))
	writeError(w, status, code, err.Error())
}

// recordUsage runs after the provider answers and before the answer goes back
// to the agent. It reads the body, logs token usage, and puts the exact same
// bytes back so the agent sees no difference.
func (p *Proxy) recordUsage(resp *http.Response) error {
	ctx := resp.Request.Context()
	info, _ := ctx.Value(requestInfoKey{}).(requestInfo)
	attrs := []any{
		slog.String("method", resp.Request.Method),
		slog.String("path", resp.Request.URL.Path),
		slog.Int("status", resp.StatusCode),
		slog.Bool("stream", info.stream),
	}
	if info.agent != "" {
		attrs = append(attrs, slog.String("agent", info.agent), slog.String("team", info.team))
	}

	// Streaming answers (Server-Sent Events) arrive in many small pieces.
	// We pass every byte on immediately and count the usage chunk at the end.
	if mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mediaType == "text/event-stream" {
		resp.Body = newUsageStreamReader(resp.Body, func(u Usage, found bool) {
			attrs = append(attrs, durationAttr(info))
			if !found {
				p.logger.WarnContext(ctx, "stream ended without usage: tokens not counted", attrs...)
				return
			}
			p.account(ctx, info, u, attrs)
		})
		return nil
	}
	attrs = append(attrs, durationAttr(info))

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
	p.account(ctx, info, usage, attrs)
	return nil
}

// account charges the tokens to the team and writes the "llm request" log line.
func (p *Proxy) account(ctx context.Context, info requestInfo, u Usage, attrs []any) {
	if p.budget != nil && info.agent != "" {
		p.budget.Record(info.agent, int64(u.InputTokens)+int64(u.OutputTokens))
	}
	attrs = append(attrs,
		slog.String("model", u.Model),
		slog.Int("input_tokens", u.InputTokens),
		slog.Int("output_tokens", u.OutputTokens),
	)
	p.logger.InfoContext(ctx, "llm request", attrs...)
}

func durationAttr(info requestInfo) slog.Attr {
	if info.start.IsZero() {
		return slog.Attr{}
	}
	return slog.Int64("duration_ms", time.Since(info.start).Milliseconds())
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
