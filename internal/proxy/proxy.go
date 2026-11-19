package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// maxBodyBytes is the largest response body we read into memory to count
// tokens. Bigger bodies are still forwarded, just not counted.
const maxBodyBytes = 10 << 20 // 10 MiB

// Proxy sits between an agent and an LLM provider. It forwards every request
// unchanged and logs the token usage found in the response.
type Proxy struct {
	rp     *httputil.ReverseProxy
	logger *slog.Logger
}

// New returns a Proxy that sends all requests to upstream
// (for example http://localhost:11434 or https://api.openai.com).
func New(upstream *url.URL, logger *slog.Logger) *Proxy {
	p := &Proxy{logger: logger}
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			// Point the request at the provider: scheme, host and path prefix.
			r.SetURL(upstream)
			// Remove the agent's Accept-Encoding. Go's HTTP client then asks the
			// provider for gzip itself and unzips it for us, so we always get
			// plain JSON to read tokens from.
			r.Out.Header.Del("Accept-Encoding")
		},
		ModifyResponse: p.recordUsage,
		ErrorHandler:   p.handleError,
	}
	return p
}

type startTimeKey struct{}

// ServeHTTP implements http.Handler.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := context.WithValue(r.Context(), startTimeKey{}, time.Now())
	p.rp.ServeHTTP(w, r.WithContext(ctx))
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
	if start, ok := ctx.Value(startTimeKey{}).(time.Time); ok {
		attrs = append(attrs, slog.Int64("duration_ms", time.Since(start).Milliseconds()))
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
