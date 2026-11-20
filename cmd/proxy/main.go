// Command proxy runs the agent-budget proxy: it finds out which agent sends
// each LLM request, enforces the agent's and its team's token budgets, and
// forwards the request to the provider.
//
// Usage:
//
//	proxy          run the proxy
//	proxy keygen   print a new virtual key and the hash to put in the agents file
//
// Configuration (environment variables):
//
//	LISTEN_ADDR       address to listen on   (default ":8080")
//	UPSTREAM_URL      LLM provider base URL  (default "http://localhost:11434", Ollama)
//	UPSTREAM_API_KEY  real provider key, sent instead of the agent's virtual key
//	                  (empty = no Authorization sent, fine for Ollama / fakellm)
//	AGENTS_FILE       JSON file with teams, agents and key hashes
//	                  (empty = watch-only: no keys checked, nothing blocked)
//	LOG_FORMAT        "text" or "json"       (default "text")
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chikolavosaki-sys/agent-budget/internal/agents"
	"github.com/chikolavosaki-sys/agent-budget/internal/meter"
	"github.com/chikolavosaki-sys/agent-budget/internal/proxy"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "keygen" {
		keygen(os.Stdout)
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// keygen prints a fresh virtual key and its hash.
func keygen(w io.Writer) {
	key := agents.NewKey()
	_, _ = fmt.Fprintf(w, "key:        %s\n", key)
	_, _ = fmt.Fprintf(w, "key_sha256: %s\n\n", agents.HashKey(key))
	_, _ = fmt.Fprintln(w, "Give the key to the agent (it is shown only this once).")
	_, _ = fmt.Fprintln(w, "Put key_sha256 in the agents file.")
}

func run() error {
	logger := newLogger(os.Stdout, os.Getenv("LOG_FORMAT"))
	listenAddr := envOr("LISTEN_ADDR", ":8080")

	upstream, err := parseUpstream(envOr("UPSTREAM_URL", "http://localhost:11434"))
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok\n")
	})

	opts := proxy.Options{Logger: logger, UpstreamKey: os.Getenv("UPSTREAM_API_KEY")}
	if path := os.Getenv("AGENTS_FILE"); path == "" {
		logger.Warn("AGENTS_FILE is empty: watch-only mode, no keys checked, no request blocked")
		mux.HandleFunc("GET /budgets", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"message":"watch-only mode: AGENTS_FILE is empty"}}`+"\n")
		})
	} else {
		registry, err := agents.Load(path)
		if err != nil {
			return err
		}
		m, err := meter.New(meterConfig(registry))
		if err != nil {
			return fmt.Errorf("build meter: %w", err)
		}
		opts.Agents, opts.Budget = registry, m
		mux.Handle("GET /budgets", budgetsHandler(m))
		logger.Info("agents loaded",
			"teams", len(registry.Teams()), "agents", len(registry.Agents()),
			"upstream_key_set", opts.UpstreamKey != "")
	}

	p, err := proxy.New(upstream, opts)
	if err != nil {
		return err
	}
	mux.Handle("/", p)

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: LLM answers can legitimately take minutes.
	}

	// ctx is cancelled when you press Ctrl+C (SIGINT) or Kubernetes stops the pod (SIGTERM).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("proxy listening", "addr", listenAddr, "upstream", upstream.Redacted())
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	// Graceful shutdown: stop taking new requests, let running ones finish.
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// meterConfig turns the agents file into the limits the meter needs.
func meterConfig(r *agents.Registry) meter.Config {
	cfg := meter.Config{
		Teams:  make(map[string]int64),
		Agents: make(map[string]meter.AgentBudget),
	}
	for _, t := range r.Teams() {
		cfg.Teams[t.Name] = t.Budget
	}
	for _, a := range r.Agents() {
		cfg.Agents[a.Name] = meter.AgentBudget{Team: a.Team, Limit: a.Budget}
	}
	return cfg
}

// budgetsHandler shows every team's and agent's live usage as JSON.
func budgetsHandler(m *meter.Meter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(m.Snapshot())
	})
}

func parseUpstream(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse UPSTREAM_URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("UPSTREAM_URL must look like http://host:port, got %q", raw)
	}
	return u, nil
}

func newLogger(w io.Writer, format string) *slog.Logger {
	if format == "json" {
		return slog.New(slog.NewJSONHandler(w, nil))
	}
	return slog.New(slog.NewTextHandler(w, nil))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
