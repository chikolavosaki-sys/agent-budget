# Architecture decisions

Newest first. One entry per real decision. Keep each entry short; link to a proposal for detail.

Format:

```
## D-<number>: <title> (<YYYY-MM-DD>)
- Context: what problem forced a choice
- Options considered: A / B / C
- Decision: the option chosen
- Why: the deciding reasons and the trade-off we accept
- Status: accepted | superseded by D-<n>
```

## D-006: Count streaming responses by turning on include_usage (2026-11-19)
- Context: with M1 enforcement, `"stream": true` requests were not counted, so a team could bypass its budget by streaming.
- Options considered: block streaming when budgets are on / estimate tokens from the streamed text / ask the provider for usage with `stream_options.include_usage` and read the final chunk.
- Decision: the proxy sets `stream_options.include_usage = true` on streamed requests and wraps the SSE response in a reader that passes bytes through immediately and records the usage chunk at the end. Every JSON-object request body is re-encoded so the provider sees exactly what the proxy inspected. Request bodies are capped at 32 MiB (`413`).
- Why: exact provider numbers, no added latency, agents keep streaming. Trade-offs we accept: the agent receives one extra chunk it did not ask for (empty `choices`, per the OpenAI spec: https://github.com/openai/openai-openapi); providers that reject `stream_options` will fail streamed requests; a stream that breaks before the usage chunk is not counted (logged as WARN); key order of the request JSON may change.
- Status: accepted

## D-005: M1 budget enforcement — team header, in-memory soft limit, fail closed (2026-11-19)
- Context: M1 must block a team that has used its token budget, before M2 (real agent identity) and M5 (durable storage) exist.
- Options considered:
  - Team identity: `X-Budget-Team` header / API key per team / Kubernetes service account. → **header** (M2 decides the real one).
  - Limit type: soft (check before, count after) / hard (reserve `max_tokens` before, settle after). → **soft**.
  - Unknown team: fail open (allow) / fail closed (block). → **fail closed, `403 no_budget`**.
  - `BUDGETS` empty: refuse to start / watch-only. → **watch-only**, with a startup WARN.
  - Storage: Go map + `sync.Mutex` / `sync.Map` / atomics per team. → **map + mutex**.
- Decision: as marked above. Over budget → `429 budget_exceeded` in the OpenAI error shape.
- Why: smallest design that blocks real overspending and is easy to reason about. Trade-offs we accept: overshoot up to (in-flight requests × tokens per request); any agent can claim any team; counters reset on restart; streaming requests are not counted (IDEA-1). Mutex chosen over `sync.Map` because `Allow` must read the limit and the usage together, consistently.
- Status: accepted

## D-004: Pin `go 1.26` in go.mod and require golangci-lint ≥ 2.15 (2026-11-19)
- Context: the dev machine has Go 1.27.4; golangci-lint 2.10.1 panics on Go 1.27 standard-library export data.
- Options considered: downgrade Go / upgrade golangci-lint / skip lint.
- Decision: keep the `go 1.26.0` directive (works on any Go ≥ 1.26), upgrade golangci-lint to 2.15.0; CI pins the same version.
- Why: no feature from 1.27 is needed; upgrading the linter is cheap. Makefile accepts `GOLANGCI_LINT=path` for a non-default binary.
- Status: accepted

## D-003: College project scope — drop CNCF-style process (2026-11-19)
- Context: the project was first set up to CNCF standards (DCO sign-off, mandatory design proposals, community files).
- Options considered: keep CNCF process / college-project process.
- Decision: college project. Keep code quality (tests, lint, CI, docs); drop DCO sign-off, mandatory proposals, CONTRIBUTING / CODE_OF_CONDUCT / SECURITY.
- Why: the goal is learning and a working demo, not donation to a foundation; the process overhead does not pay off.
- Status: accepted

## D-002: Support the OpenAI-compatible response format first (2026-11-19)
- Context: each provider names token usage differently; M0 must pick one to parse.
- Options considered: OpenAI-compatible (`usage.prompt_tokens` / `usage.completion_tokens`) / Anthropic (`usage.input_tokens` / `usage.output_tokens`) / several at once.
- Decision: OpenAI-compatible only. Field names verified against the `CompletionUsage` schema in https://github.com/openai/openai-openapi.
- Why: many providers speak it, including free local ones like Ollama (https://docs.ollama.com/api/openai-compatibility), so it can be tested at zero cost. Other formats can be added later as adapters.
- Status: accepted

## D-001: Telemetry explains spend; it never enforces it (2026-11-13)
- Context: Traces (Jaeger) and metrics carry token counts, so they are tempting as the source of truth for budgets.
- Options considered: enforce from traces / enforce from our own counters on the request path.
- Decision: budgets are enforced only from counters updated on the request path; OpenTelemetry data is for explanation and dashboards.
- Why: traces are often sampled and arrive after the request; a budget must see every token before the next request.
- Status: accepted
