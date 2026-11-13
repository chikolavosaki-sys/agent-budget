# <PROJECT_NAME> — token budgets for AI agents on Kubernetes

A Kubernetes-native controller and proxy that **tracks and enforces token/cost budgets for AI agents** — per agent, per workflow and per team — **at request time**, and shows who spent what and why. First integration target: kagent.

Personal working style and language rules live in `CLAUDE.local.md` (not committed).

## Scope

- **Do not rebuild** what LLM gateways (LiteLLM, Portkey, Envoy AI Gateway) already do for per-key budgets. Our value: per-agent/per-workflow attribution, budgets declared as Kubernetes resources, enforcement during the agent run, and an owner + budget for every agent.
- Current milestone and plan: @ROADMAP.md
- Architecture decisions: @docs/DECISIONS.md
- Research ideas backlog: `research/IDEAS.md` (read it when planning; run `/research` to add to it).

## Architecture (high level)

1. **Proxy (enforce)** — sits between agents and LLM providers. Before each request: identify agent + team, check budget, allow or return HTTP 429. Hot path: no network call per request in the common case.
2. **Meter (account)** — after each response, read real token usage, update counters. Every token counts; never use sampled telemetry for budgets.
3. **Controller (configure)** — `TokenBudget` CRD reconciled by a controller-runtime controller; pushes limits to the proxy.
4. **Telemetry (explain)** — OpenTelemetry traces and metrics using the GenAI semantic conventions (`gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens`); traces to Jaeger, metrics to Prometheus. Telemetry explains spend; it never enforces it.

## Repository layout (Kubebuilder style)

```
cmd/manager/         controller entrypoint
cmd/proxy/           proxy entrypoint
api/v1alpha1/        CRD Go types (TokenBudget)
internal/controller/ reconcilers
internal/proxy/      HTTP proxy, provider adapters
internal/meter/      counters and budget math
internal/telemetry/  OTel setup
config/              kustomize manifests (CRDs, RBAC, samples)
test/e2e/            end-to-end tests on kind
docs/proposals/      design proposals (template: 0000-template.md)
research/            research log and ideas
hack/                dev scripts
```

Create directories only when the first file for them is needed.

## Commands

The Makefile is created in milestone M0. Keep these targets working:

- `make build` — build all binaries
- `make test` — unit tests with the race detector (`go test -race ./...`)
- `make lint` — `golangci-lint run`
- `make generate manifests` — regenerate CRD code and YAML after changing `api/`
- `make kind-up` / `make kind-down` — local cluster
- `make e2e` — end-to-end tests on kind

Before saying a task is done: run `make lint test` and report the result. If a command fails, show the error; never claim success without running it.

## Engineering standards (CNCF level)

**Go**
- Go version pinned in `go.mod`; `gofmt` + `golangci-lint` clean.
- Pass `context.Context` as the first argument; wrap errors with `fmt.Errorf("...: %w", err)`; no panics in library code; no package-level mutable state.
- Structured logging (`logr` in controllers, `log/slog` elsewhere).
- Ask before adding a new dependency; prefer the standard library.

**Kubernetes APIs**
- New APIs start at `v1alpha1`. Use a `status` subresource with `metav1.Condition` conditions. Reconcilers must be idempotent.
- RBAC is least-privilege and generated from kubebuilder markers.

**Testing**
- Table-driven unit tests next to the code. `envtest` for controllers. e2e on kind for user-visible flows.
- Anything shared between goroutines (counters!) must be tested with `-race`. Add a concurrency test when touching the meter.

**Security**
- Never log prompts, completions, API keys or tokens. Provider keys come from Kubernetes Secrets or env vars, never from files in the repo.
- Fail **closed** when the budget state is unknown, unless a budget explicitly says otherwise — document this choice in each proposal that touches enforcement.

**Changes and history**
- One logical change per commit. Conventional Commits (`feat:`, `fix:`, `docs:`, `test:`, `refactor:`, `chore:`).
- Sign off every commit for DCO: `git commit -s`.
- Any change to the API, the enforcement path or storage needs a design proposal in `docs/proposals/` first (copy `0000-template.md`). Small fixes do not.
- Record every real decision in `docs/DECISIONS.md`.
- Keep `README.md` and `docs/` in sync with behaviour.

**Licensing and community** — Apache-2.0 license. Before the repo goes public, add `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md` (CNCF Code of Conduct), `SECURITY.md` and GitHub Actions CI for lint + test + build.

## Working rules

- Work milestone by milestone from `ROADMAP.md`; do not start the next milestone until the current one works end to end with tests.
- New ideas that are outside the current milestone go into `research/IDEAS.md`, not into the code.
- Never state facts about other projects (APIs, versions, status) from memory when it matters — check their docs and link the source.
- At the end of a work session, run `/wrap-up`.
