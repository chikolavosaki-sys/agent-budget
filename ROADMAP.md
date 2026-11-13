# Roadmap

## Next

- [ ] Start M0: create the Go module, Makefile and a proxy that forwards one LLM request and logs its token usage.

## Milestones

Each milestone must work end to end, with tests, before the next one starts.

- [ ] **M0 — Hello proxy.** Go module, Makefile (`build`, `test`, `lint`), golangci-lint config. A proxy forwards one LLM request and logs `input_tokens` / `output_tokens` from the response. Unit test with a fake provider.
- [ ] **M1 — Count and block.** In-memory per-team counter; return HTTP 429 when over the limit. Concurrency test with `-race`.
- [ ] **M2 — Know the agent.** Identify agent + team per request (header, virtual key, or Kubernetes service account — decide in a proposal). Per-agent counters.
- [ ] **M3 — Budgets in Kubernetes.** `TokenBudget` CRD (`v1alpha1`) + controller on kind. Changing the YAML changes the live limit. envtest coverage. *Needs a design proposal.*
- [ ] **M4 — See it.** OTel GenAI traces into Jaeger, metrics into Prometheus, one Grafana dashboard: spend by team and by agent.
- [ ] **M5 — Survive restarts and scale out.** Durable counters (Redis or Postgres — decide in a proposal); correct behaviour with two proxy replicas and concurrent requests. *Needs a design proposal.*
- [ ] **M6 — kagent integration.** Real kagent agents run through the proxy end to end; e2e test on kind.
- [ ] **M7+ — From research.** Top-scored ideas from `research/IDEAS.md` (for example: downgrade model instead of block, burn-rate alerts, "no budget, no deploy" policy).

## Before going public

- [ ] Apache-2.0 `LICENSE`
- [ ] `README.md` with a 5-minute quick start on kind
- [ ] `CONTRIBUTING.md` (DCO sign-off, Conventional Commits, how to run tests)
- [ ] `CODE_OF_CONDUCT.md` (CNCF Code of Conduct)
- [ ] `SECURITY.md`
- [ ] GitHub Actions: lint, test, build on every PR
