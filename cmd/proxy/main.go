// Command proxy runs the agent-budget proxy: it forwards LLM requests from
// agents to a provider and logs how many tokens each request used.
//
// Configuration (environment variables):
//
//	LISTEN_ADDR   address to listen on        (default ":8080")
//	UPSTREAM_URL  LLM provider base URL       (default "http://localhost:11434", Ollama)
//	LOG_FORMAT    "text" or "json"            (default "text")
//	BUDGETS       token limit per team, e.g. "default=1000,team-a=500"
//	              (empty = watch-only: count and log, never block)
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

	"github.com/chikolavosaki-sys/agent-budget/internal/meter"
	"github.com/chikolavosaki-sys/agent-budget/internal/proxy"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	logger := newLogger(os.Stdout, os.Getenv("LOG_FORMAT"))
	listenAddr := envOr("LISTEN_ADDR", ":8080")

	upstream, err := parseUpstream(envOr("UPSTREAM_URL", "http://localhost:11434"))
	if err != nil {
		return err
	}

	limits, err := meter.ParseBudgets(os.Getenv("BUDGETS"))
	if err != nil {
		return fmt.Errorf("parse BUDGETS: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok\n")
	})

	if len(limits) == 0 {
		logger.Warn("BUDGETS is empty: watch-only mode, no request will be blocked")
		mux.HandleFunc("GET /budgets", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"message":"watch-only mode: BUDGETS is empty"}}`+"\n")
		})
		mux.Handle("/", proxy.New(upstream, logger, nil))
	} else {
		m := meter.New(limits)
		logger.Info("budgets loaded", "teams", len(limits))
		mux.Handle("GET /budgets", budgetsHandler(m))
		mux.Handle("/", proxy.New(upstream, logger, m))
	}

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

// budgetsHandler shows every team's live usage as JSON.
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
