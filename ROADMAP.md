# Roadmap

## Next

- [ ] Start M2: decide how to identify the agent and team (header vs. virtual key vs. Kubernetes service account), record it in DECISIONS, add per-agent counters.

## Milestones

Each milestone must work end to end, with tests, before the next one starts.

- [x] **M0 — Hello proxy.** Go module, Makefile (`build`, `test`, `lint`), golangci-lint config. A proxy forwards one LLM request and logs `input_tokens` / `output_tokens` from the response. Unit test with a fake provider. *(done 2026-11-19 — also ships `fakellm` for demos and GitHub Actions CI)*
- [x] **M1 — Count and block.** In-memory per-team counter; return HTTP 429 when over the limit. Concurrency test with `-race`. *(done 2026-11-19 — `X-Budget-Team` header, `BUDGETS` env, `GET /budgets`, 403 for teams without a budget; see D-005. Streaming counted too — IDEA-1, D-006)*
- [ ] **M2 — Know the agent.** Identify agent + team per request (header, virtual key, or Kubernetes service account — decide and record in DECISIONS). Per-agent counters.
- [ ] **M3 — Budgets in Kubernetes.** `TokenBudget` CRD (`v1alpha1`) + controller on kind. Changing the YAML changes the live limit. envtest coverage.
- [ ] **M4 — See it.** OTel GenAI traces into Jaeger, metrics into Prometheus, one Grafana dashboard: spend by team and by agent.
- [ ] **M5 — Survive restarts and scale out.** Durable counters (Redis or Postgres — decide and record in DECISIONS); correct behaviour with two proxy replicas and concurrent requests.
- [ ] **M6 — kagent integration.** Real kagent agents run through the proxy end to end; e2e test on kind.
- [ ] **M7+ — From research.** Top-scored ideas from `research/IDEAS.md` (for example: downgrade model instead of block, burn-rate alerts, "no budget, no deploy" policy).

## Project hygiene

- [x] Apache-2.0 `LICENSE`
- [x] `README.md` with a quick start
- [x] GitHub Actions: lint, test, build on every push and PR
- [ ] Demo video / screenshots for the college submission
