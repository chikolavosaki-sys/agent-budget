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
make run-proxy         # proxy on :8080 → forwards to :9090, agents from examples/agents.json
```

```bash
# Terminal 3 — you are the agent "search-bot"
curl -s localhost:8080/v1/chat/completions \
  -H 'Authorization: Bearer ab-demo-search-bot' \
  -d '{"model":"demo-model","messages":[{"role":"user","content":"Hello there!"}]}'
```

Terminal 2 shows:

```text
level=INFO msg="llm request" method=POST path=/v1/chat/completions status=200 stream=false agent=search-bot team=search duration_ms=1 model=demo-model input_tokens=3 output_tokens=11
```

Use your own agents file with `make run-proxy AGENTS_FILE=my-agents.json`.

Press **Ctrl+C** in Terminal 2 — the proxy finishes any running request, prints `shutting down`, and exits cleanly.

---

## Option B — a real model with Ollama (free, local)

[Ollama](https://ollama.com) runs open models on your own laptop and serves an
[OpenAI-compatible API](https://docs.ollama.com/api/openai-compatibility) on port `11434`.

```bash
ollama pull llama3.2                              # one-time download
AGENTS_FILE=examples/agents.json ./bin/proxy      # UPSTREAM_URL defaults to http://localhost:11434
```

```bash
curl -s localhost:8080/v1/chat/completions \
  -H 'Authorization: Bearer ab-demo-search-bot' \
  -d '{"model":"llama3.2","messages":[{"role":"user","content":"What is a proxy? One sentence."}]}'
```

Ollama needs no API key, so leave `UPSTREAM_API_KEY` empty.

---

## Option C — OpenAI (or any OpenAI-compatible cloud API)

The **proxy** holds the real key; agents only get virtual keys:

```bash
AGENTS_FILE=examples/agents.json \
UPSTREAM_URL=https://api.openai.com \
UPSTREAM_API_KEY="$OPENAI_API_KEY" \
./bin/proxy
```

```bash
curl -s localhost:8080/v1/chat/completions \
  -H 'Authorization: Bearer ab-demo-search-bot' \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Hi"}]}'
```

The proxy removes the virtual key and sends `Authorization: Bearer $UPSTREAM_API_KEY` to OpenAI.
Neither key is ever logged. Keep the real key in an environment variable (or a Kubernetes Secret) — never commit it.

> **Watch-only mode:** without `AGENTS_FILE`, no keys are checked, the agent's own `Authorization`
> header is passed through unchanged, and nothing is blocked — tokens are only logged.

---

## Agents, keys and budgets

### The agents file

All agents and teams live in one JSON file ([`examples/agents.json`](../examples/agents.json)):

```json
{
  "teams": [
    { "name": "payments", "budget": 100 },
    { "name": "search",   "budget": 1000 }
  ],
  "agents": [
    {
      "name": "invoice-bot",
      "team": "payments",
      "owner": "payments-lead",
      "budget": 50,
      "key_sha256": "0349141c…"
    }
  ]
}
```

| Field | Required | Meaning |
|---|---|---|
| `teams[].name` | yes | Lowercase letters, digits, `-`, `.`, `_` (Kubernetes style) |
| `teams[].budget` | yes | Tokens the whole team may use (input + output) |
| `agents[].name` | yes | Same naming rules as teams; must be unique |
| `agents[].team` | yes | Must be listed in `teams` |
| `agents[].owner` | yes | Who is responsible for this agent. **No owner, no start.** |
| `agents[].budget` | no | The agent's own limit. Leave it out to use only the team budget |
| `agents[].key_sha256` | yes | SHA-256 of the agent's virtual key (from `make keygen`) |

Budgets count **input + output tokens** for the lifetime of the proxy process. Changing the file needs a restart.

If anything is wrong (typo in a field name, unknown team, missing owner, two agents with the same key), the proxy refuses to start and lists **every** problem at once:

```text
error: agents file bad.json: agent "x": team "nope" is not in the teams list
agent "x": owner is required — every agent needs someone responsible for it
agent "x": key_sha256 must be 64 lowercase hex characters (run: proxy keygen)
```

### Adding an agent

```bash
make keygen
```

```text
key:        ab-3OV6ZIKMVDHAE6FYBN6CIVDV6H
key_sha256: b47681de7e64503a007f9ad89dbbcec4198316a3e93f1b135a00aa30440f4820
```

1. Give the **key** to the agent (for example as its `OPENAI_API_KEY`). It is shown only once.
2. Put the **key_sha256** in the agents file, with a name, team and owner.
3. Restart the proxy.

Why only the hash? The file can be committed or put in a Kubernetes ConfigMap: someone who reads it still cannot call the proxy, because a SHA-256 hash cannot be turned back into the key.

### How the agent calls

The agent uses its virtual key exactly like a provider key:

```bash
-H 'Authorization: Bearer ab-demo-invoice-bot'
```

Most OpenAI SDKs only need two settings: base URL `http://<proxy>:8080/v1` and API key `ab-…`.

### What the agent gets back

| Situation | Status | `error.code` |
|---|---|---|
| Key is valid and budgets have tokens left | the provider's answer | — |
| No key, or a key the proxy does not know | `401 Unauthorized` | `invalid_api_key` |
| The agent's own budget is used up | `429 Too Many Requests` | `budget_exceeded` (message names the **agent**) |
| The agent's team budget is used up | `429 Too Many Requests` | `budget_exceeded` (message names the **team**) |

The error body uses the same shape as OpenAI errors, so agent SDKs show the message:

```json
{"error":{"code":"budget_exceeded","message":"team \"payments\" used 110 of 100 tokens: over token budget","type":"budget_exceeded"}}
```

### Watching spend live

```bash
watch -n1 curl -s localhost:8080/budgets
```

```json
{
  "teams": [
    { "name": "payments", "used": 110, "limit": 100,  "remaining": 0 },
    { "name": "search",   "used": 22,  "limit": 1000, "remaining": 978 }
  ],
  "agents": [
    { "name": "invoice-bot", "team": "payments", "used": 66, "limit": 50, "remaining": 0 },
    { "name": "refund-bot",  "team": "payments", "used": 44 },
    { "name": "search-bot",  "team": "search",   "used": 22 }
  ]
}
```

Agents without their own budget show only `used`.

## Streaming

Add `"stream": true` and use `curl -N` (no buffering) to watch the words arrive one by one:

```bash
curl -sN localhost:8080/v1/chat/completions \
  -H 'Authorization: Bearer ab-demo-search-bot' \
  -d '{"model":"demo","stream":true,"messages":[{"role":"user","content":"Hello there!"}]}'
```

```text
data: {"choices":[{"delta":{"content":"Hello "},"index":0}], …}
data: {"choices":[{"delta":{"content":"from "},"index":0}], …}
…
data: {"choices":[],"usage":{"completion_tokens":11,"prompt_tokens":3,"total_tokens":14}, …}
data: [DONE]
```

Streams are counted against the budget like any other request. To do that, the proxy turns on
`stream_options.include_usage` in your request, so you will see **one extra chunk** with an empty
`choices` array and the `usage` just before `[DONE]`. Agent SDKs that follow the OpenAI format handle it.

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
| `agents loaded` | INFO | Startup: number of teams and agents, and whether `UPSTREAM_API_KEY` is set |
| `AGENTS_FILE is empty: watch-only mode…` | WARN | Startup: no keys checked, nothing blocked |
| `llm request` | INFO | Success; tokens counted and added to the agent and its team |
| `request rejected: missing or unknown virtual key` | WARN | 401; the key itself is never logged |
| `request blocked` | WARN | Agent or team over budget (429); provider not called |
| `llm request failed upstream` | WARN | Provider returned a non-200 status (passed back to the agent as-is) |
| `llm request: could not read token usage` | WARN | 200 OK but no readable `usage` in the body |
| `stream ended without usage: tokens not counted` | WARN | A stream finished (or broke) without a usage chunk |
| `response too large: tokens not counted` | WARN | Body over 10 MiB — passed through, not counted |
| `upstream unreachable` | ERROR | Provider is down; agent receives `502 Bad Gateway` |

## Troubleshooting

| Symptom | Fix |
|---|---|
| `502 Bad Gateway` | The provider is not running. Is `fakellm` / Ollama up? Check `UPSTREAM_URL`. |
| `bind: address already in use` | Something else uses the port. Try `LISTEN_ADDR=:8081`. |
| `UPSTREAM_URL must look like http://host:port` | Add the scheme: `http://localhost:9090`, not `localhost:9090`. |
| Every request gets `401 invalid_api_key` | Send `Authorization: Bearer ab-…`, and check that `key_sha256` in the agents file is the hash of that exact key. |
| The provider answers `401` | `UPSTREAM_API_KEY` is missing or wrong (the agent's virtual key is never sent to the provider). |
| `413 request_too_large` | Request body over 32 MiB. |
| `400 invalid_request` about `stream_options` | `stream_options` must be a JSON object, e.g. `{"include_usage":true}`. |
| Provider rejects `stream_options` | Some older OpenAI-compatible servers do not know this field. Use a newer version, or don't stream. |
| `GET /budgets` returns 404 `watch-only mode` | `AGENTS_FILE` is empty — there are no budgets to show. |
