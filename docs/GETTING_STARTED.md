# Getting started

This guide runs agent-budget three ways. Start with **Option A** — it needs nothing but Go.

## Prerequisites

| Tool | Why | Check |
|---|---|---|
| Go 1.26+ | build the code | `go version` |
| make | run the shortcuts | `make --version` |
| curl | pretend to be an agent | `curl --version` |
| golangci-lint 2.15+ *(optional)* | `make lint` | `golangci-lint --version` |

> **golangci-lint and Go 1.27:** older golangci-lint builds (for example 2.10) crash on Go 1.27 with
> `export data version 5 is greater than maximum supported version 2`. Install a newer one:
> `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`

Build once:

```bash
make build      # creates bin/proxy and bin/fakellm
```

---

## Option A — fake LLM (no key, no GPU, no internet)

`fakellm` is a tiny server that pretends to be an OpenAI-compatible LLM. It always gives the same answer and estimates tokens as *characters ÷ 4*.

```bash
# Terminal 1
make run-fake          # fake LLM on :9090

# Terminal 2
make run-proxy         # proxy on :8080 → forwards to :9090
```

```bash
# Terminal 3 — you are the agent
curl -s localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"demo-model","messages":[{"role":"user","content":"Hello there!"}]}'
```

Terminal 2 shows:

```text
level=INFO msg="llm request" method=POST path=/v1/chat/completions status=200 team=default duration_ms=1 model=demo-model input_tokens=3 output_tokens=11
```

`make run-proxy` starts with demo budgets `default=100,team-a=50`. Change them with
`make run-proxy BUDGETS="default=500,team-b=200"`.

Press **Ctrl+C** in Terminal 2 — the proxy finishes any running request, prints `shutting down`, and exits cleanly.

---

## Option B — a real model with Ollama (free, local)

[Ollama](https://ollama.com) runs open models on your own laptop and serves an
[OpenAI-compatible API](https://docs.ollama.com/api/openai-compatibility) on port `11434`.

```bash
ollama pull llama3.2           # one-time download
./bin/proxy                    # UPSTREAM_URL defaults to http://localhost:11434
```

```bash
curl -s localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"llama3.2","messages":[{"role":"user","content":"What is a proxy? One sentence."}]}'
```

---

## Option C — OpenAI (or any OpenAI-compatible cloud API)

```bash
UPSTREAM_URL=https://api.openai.com ./bin/proxy
```

```bash
curl -s localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Hi"}]}'
```

The proxy passes your `Authorization` header through unchanged and **never logs it**.
Keep keys in environment variables — never commit them.

---

## Budgets

### Setting budgets

`BUDGETS` is a comma-separated list of `team=tokens`:

```bash
BUDGETS="default=1000,payments=5000,search=200" UPSTREAM_URL=http://localhost:9090 ./bin/proxy
```

- A budget counts **input + output tokens**, for the lifetime of the proxy process.
- `BUDGETS` empty → **watch-only mode**: everything is counted and logged, nothing is blocked. The proxy prints a warning at startup.
- A wrong value (`team-a=lots`, `=100`, duplicates) stops the proxy at startup with a clear error.

### Saying which team you are

The agent adds a header:

```bash
-H 'X-Budget-Team: payments'
```

No header → team `default`. The proxy removes the header before forwarding, so the provider never sees it.

### What the agent gets back

| Situation | Status | `error.code` |
|---|---|---|
| Team is under its budget | the provider's answer | — |
| Team has used its budget | `429 Too Many Requests` | `budget_exceeded` |
| Team has no budget at all | `403 Forbidden` | `no_budget` |

The error body uses the same shape as OpenAI errors, so agent SDKs show the message:

```json
{"error":{"code":"budget_exceeded","message":"team \"team-a\" used 66 of 50 tokens: team is over its token budget","type":"budget_exceeded"}}
```

### Watching spend live

```bash
watch -n1 curl -s localhost:8080/budgets
```

```json
[
  { "team": "default", "used": 22, "limit": 100, "remaining": 78 },
  { "team": "team-a",  "used": 66, "limit": 50,  "remaining": 0 }
]
```

## JSON logs

For log tools (Loki, Elasticsearch, `jq`):

```bash
LOG_FORMAT=json UPSTREAM_URL=http://localhost:9090 ./bin/proxy
```

```json
{"time":"…","level":"INFO","msg":"llm request","method":"POST","path":"/v1/chat/completions","status":200,"duration_ms":1,"model":"demo-model","input_tokens":3,"output_tokens":11}
```

## What the log messages mean

| Message | Level | Meaning |
|---|---|---|
| `budgets loaded` | INFO | Startup: how many teams have a budget |
| `BUDGETS is empty: watch-only mode…` | WARN | Startup: nothing will be blocked |
| `llm request` | INFO | Success; tokens counted (and added to the team's budget) |
| `request blocked` | WARN | Team over budget (429) or without budget (403); provider not called |
| `llm request failed upstream` | WARN | Provider returned a non-200 status (passed back to the agent as-is) |
| `llm request: could not read token usage` | WARN | 200 OK but no readable `usage` in the body |
| `streaming response: tokens not counted` | WARN | `"stream": true` — passed through, not counted (M0 limit) |
| `response too large: tokens not counted` | WARN | Body over 10 MiB — passed through, not counted |
| `upstream unreachable` | ERROR | Provider is down; agent receives `502 Bad Gateway` |

## Troubleshooting

| Symptom | Fix |
|---|---|
| `502 Bad Gateway` | The provider is not running. Is `fakellm` / Ollama up? Check `UPSTREAM_URL`. |
| `bind: address already in use` | Something else uses the port. Try `LISTEN_ADDR=:8081`. |
| `UPSTREAM_URL must look like http://host:port` | Add the scheme: `http://localhost:9090`, not `localhost:9090`. |
| Every request gets `403 no_budget` | The team (or `default`) is missing from `BUDGETS`. Add it, or send `X-Budget-Team`. |
| `GET /budgets` returns 404 `watch-only mode` | `BUDGETS` is empty — there are no budgets to show. |
