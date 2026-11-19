# Architecture

## The big picture

agent-budget will grow into four parts. Today the **proxy** and a simple in-memory **meter** exist (M0 + M1).

```mermaid
flowchart TB
    subgraph K8s["Kubernetes cluster (later milestones)"]
        Agents["🤖 AI agents<br/>(kagent)"]
        Proxy["🛡️ Proxy — enforce<br/>M0 ✅ forward + log<br/>M1 ✅ block with 429"]
        Meter["🧮 Meter — account<br/>M1 ✅ in-memory counters<br/>M5 ⬜ durable"]
        Ctrl["⚙️ Controller — configure<br/>M3 ⬜ TokenBudget CRD"]
        Tel["📈 Telemetry — explain<br/>M4 ⬜ OTel → Jaeger / Prometheus"]
    end
    LLM["🧠 LLM provider"]

    Agents --> Proxy --> LLM
    Proxy --> Meter
    Ctrl -. "pushes limits" .-> Proxy
    Proxy -. "traces + metrics" .-> Tel
```

| Part | Job | Analogy |
|---|---|---|
| **Proxy** | Stands between agents and the LLM; allows or blocks each request | Canteen counter |
| **Meter** | Adds up tokens after each answer | The register book |
| **Controller** | Reads budgets written as Kubernetes YAML and tells the proxy | The manager who sets each student's monthly limit |
| **Telemetry** | Charts and traces that *explain* spending | The monthly report on the notice board |

One rule from [D-001](DECISIONS.md): **telemetry explains spend; it never enforces it.** Budgets are enforced only from counters on the request path, because traces can be sampled and arrive late.

---

## One request, layer by layer

### 1. The agent sends a request (network)

The agent sends an HTTP `POST` to `http://localhost:8080/v1/chat/completions`.
The operating system delivers it over a **TCP connection** to the process listening on port 8080 — our proxy.

### 2. Go gives it a worker (process)

`cmd/proxy/main.go` started an `http.Server`. For every incoming connection, Go's `net/http` starts a **goroutine** — a very cheap worker (a few KB of memory), so thousands of agents can be served at once.

The server's router (`http.ServeMux`) looks at the path:

- `GET /healthz` → answer `ok` (used by Kubernetes to check the proxy is alive)
- `GET /budgets` → live usage per team as JSON
- everything else → `proxy.ServeHTTP`

### 3. The budget gate (M1)

`ServeHTTP` (`internal/proxy/proxy.go`) reads the `X-Budget-Team` header (missing → `default`) and asks the meter:

```go
if err := p.budget.Allow(team); err != nil {
    p.reject(w, r, team, err) // 429 or 403 — the LLM is never called
    return
}
```

```mermaid
flowchart TD
    Q["Allow(team)"] --> K{"team in BUDGETS?"}
    K -- no --> F["403 no_budget<br/>(fail closed)"]
    K -- yes --> U{"used ≥ limit?"}
    U -- yes --> T["429 budget_exceeded"]
    U -- no --> G["✅ forward to the LLM"]
```

Blocking here, **before** forwarding, is the whole point: a blocked request costs zero tokens.

### 3b. Stamp team and start time

`ServeHTTP` then puts a small `requestInfo{team, start}` into the request's **context** — a bag of values that travels with the request — so the response side knows which team to charge and how long the LLM took.

### 3c. Prepare the body

`prepareBody` reads the whole request body (max 32 MiB, else `413`) and calls `prepareRequest` (`internal/proxy/stream.go`):

- If the body is a JSON object, it is **re-encoded**, so the provider receives exactly what we inspected. (Example attack it stops: `{"stream":false,"stream":true}` — if we read the last key but the provider read the first, a stream could slip through uncounted. This class of bug is called a *parser differential*.)
- If `"stream": true`, it sets `stream_options.include_usage = true`. Without this, the provider sends **no** usage in a stream and we could not count it.

### 4. Rewrite and forward

Go's standard library `httputil.ReverseProxy` does the forwarding. Our `Rewrite` function:

1. `SetURL(upstream)` — changes scheme + host to the provider (e.g. `http://localhost:9090`), keeps the path `/v1/chat/completions`.
2. Deletes `Accept-Encoding` — so Go's HTTP client asks for gzip *itself* and unzips it for us. Otherwise the body could arrive compressed and we couldn't read the tokens.
3. Deletes `X-Budget-Team` — the team is our business, not the provider's.

Headers (including `Authorization`) and body go through unchanged.

### 5. The provider answers

```json
{
  "model": "demo-model",
  "choices": [{ "message": { "content": "…the answer…" } }],
  "usage": { "prompt_tokens": 11, "completion_tokens": 11, "total_tokens": 22 }
}
```

Field names come from the `CompletionUsage` schema in the
[official OpenAI OpenAPI spec](https://github.com/openai/openai-openapi).

### 6. Read the usage (memory)

`ModifyResponse` → `recordUsage` runs **before** the answer is sent to the agent:

```mermaid
flowchart TD
    R["Response arrives"] --> S{"Content-Type is<br/>text/event-stream?"}
    S -- yes --> W1["Wrap body in usageStreamReader<br/>(see Streaming below)"]
    S -- no --> B["Read body into memory<br/>(max 10 MiB)"]
    B --> T{"Bigger than 10 MiB?"}
    T -- yes --> W2["WARN: too large<br/>glue bytes back, pass through"]
    T -- no --> C["Put the same bytes back<br/>into resp.Body"]
    C --> O{"Status 200?"}
    O -- no --> W3["WARN: failed upstream"]
    O -- yes --> P["ParseUsage(body)"]
    P --> R2["Record(team, input + output)"]
    R2 --> OK["INFO: team, model, input_tokens,<br/>output_tokens, duration_ms"]
```

Why "put the same bytes back"? An HTTP body is a **stream** — like water through a pipe, once you read it, it's gone. We read it all into a byte slice (`[]byte`) in RAM, look inside, then hand the agent a fresh reader over the same bytes.

`ParseUsage` (`internal/proxy/usage.go`) decodes **only** `model` and `usage`. The Go struct simply has no field for `choices`, so the answer text is never even stored in a variable we use — and never logged.

### 6b. Streaming answers (Server-Sent Events)

With `"stream": true`, the answer arrives in pieces over a few seconds:

```text
data: {"choices":[{"delta":{"content":"Hello "}}],"usage":null}

data: {"choices":[{"delta":{"content":"world"}}],"usage":null}

data: {"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":11,"total_tokens":22}}

data: [DONE]
```

Per the [OpenAI spec](https://github.com/openai/openai-openapi) (`stream_options.include_usage`), every chunk has `"usage": null` **except one extra chunk just before `[DONE]`**, which holds the usage for the whole request.

We cannot read the whole stream first — the agent would wait seconds for the first word. Instead `recordUsage` swaps the body for a **`usageStreamReader`** that *peeks while passing through*:

```mermaid
sequenceDiagram
    participant LLM as LLM provider
    participant R as usageStreamReader
    participant RP as ReverseProxy
    participant A as Agent
    loop every piece
        LLM->>R: bytes
        R->>R: split into lines, look for "usage"
        R->>RP: same bytes, immediately
        RP->>A: flush
    end
    LLM->>R: end of stream (EOF)
    R->>R: onDone(last usage) → Record(team, tokens)
```

Details that matter:

- **Lines can be cut in half.** One `Read` may end in the middle of a line. The unfinished part waits in `pending` until its `\n` arrives. A test reads the stream **one byte at a time** to prove this works.
- **Memory is bounded.** A line longer than 1 MiB is passed through but not inspected.
- **Counted exactly once.** `onDone` runs on end-of-stream, on a read error or on `Close` — whichever comes first — guarded by `sync.Once`.
- **Broken streams.** If the agent hangs up or the connection breaks before the usage chunk, nothing is counted and the proxy logs `WARN stream ended without usage`. The spec warns about this case too.

### 7. Back to the agent

The proxy writes the identical status, headers and body back on the agent's TCP connection. The agent cannot tell the proxy was there.

### 8. Memory, not a database

The meter keeps two Go maps in RAM: `limits[team]` and `used[team]`. Nothing is written to disk, so **restarting the proxy resets every counter**. A database (Redis or Postgres) arrives in M5.

---

## The meter and the mutex

Go's `net/http` runs every request in its own goroutine. With 20 agents calling at once, 20 goroutines may run `used[team] += tokens` **at the same moment**.

`+=` is really three steps: *read* the old value, *add*, *write* it back. If two goroutines interleave:

| Time | Goroutine A | Goroutine B | `used` in memory |
|---|---|---|---|
| 1 | read 100 | | 100 |
| 2 | | read 100 | 100 |
| 3 | write 100 + 46 = 146 | | 146 |
| 4 | | write 100 + 46 = 146 | **146** ❌ (should be 192) |

B's tokens are **lost** — a *lost update*. With Go maps it is even worse: the runtime crashes with `fatal error: concurrent map read and map write`.

The fix is a **mutex** (`sync.Mutex`) — a lock that only one goroutine can hold at a time:

```go
func (m *Meter) Record(team string, tokens int64) {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.used[team] += tokens
}
```

Every method that touches the maps (`Allow`, `Record`, `Snapshot`) takes the same lock.

**Proof:** `TestConcurrentRecord` runs 100 goroutines × 1000 updates. With the lock removed, `go test -race` reports 8 data races and the runtime crashes. With the lock, the total is exact every time.

## Why the limit is soft

The gate checks **before** the request; tokens are added **after** the answer. In between, the meter does not know how many tokens the request will use.

```text
limit 50 │ request 1: used 0  → allowed → +22 → used 22
         │ request 2: used 22 → allowed → +22 → used 44
         │ request 3: used 44 → allowed → +22 → used 66   ← over by 16
         │ request 4: used 66 → 429
```

Also, if 10 requests from one team arrive at the same time, all 10 may pass the gate before any of them records tokens. So the overshoot is at most *(requests in flight) × (tokens per request)*.

This is the classic **check-then-act** problem. It is accepted for now (D-005). A fix — *reserve* tokens before the call using the request's `max_tokens`, then settle the real amount after — is [IDEA-2](../research/IDEAS.md).

---

## Failure cases

| What goes wrong | What the agent sees | What the proxy logs |
|---|---|---|
| Provider is down | `502` with a JSON error | `ERROR upstream unreachable` |
| Provider returns 429 / 500 | the same status and body | `WARN failed upstream` |
| Agent disconnects mid-request | — | nothing (not an error) |
| Streaming answer | the stream, live and unchanged | `INFO llm request stream=true …` |
| Stream breaks before the usage chunk | whatever arrived | `WARN stream ended without usage` |
| Request body > 32 MiB | `413` | — |
| `stream_options` is not an object | `400` | — |
| Team over budget | `429` `budget_exceeded` | `WARN request blocked` |
| Team has no budget | `403` `no_budget` | `WARN request blocked` |

## Shutdown

`signal.NotifyContext` turns **Ctrl+C** (`SIGINT`) or a Kubernetes pod stop (`SIGTERM`) into a cancelled context.
`srv.Shutdown` then stops accepting new connections and waits up to 15 s for running requests to finish — so no agent gets cut off mid-answer.

## Testing strategy

`internal/proxy/proxy_test.go` starts **two in-process servers** with `httptest`:

```mermaid
flowchart LR
    T["Test<br/>(acts as agent)"] --> PX["Proxy<br/>httptest.Server"] --> FK["Fake provider<br/>httptest.Server"]
```

| Test | Checks |
|---|---|
| `TestProxyForwardsAndLogsUsage` | body unchanged, path + auth forwarded, tokens logged, **no secrets in logs** |
| `TestProxyHandlesGzipFromProvider` | tokens still counted when the provider gzips |
| `TestProxyUpstreamError` | a 429 from the provider passes through |
| `TestProxyStreamingIsPassedThrough` | stream without usage passes through, warning logged |
| `TestProxyStreamingIsLiveAndCounted` | provider got `include_usage`; first chunk reaches the agent **before** the provider sends the rest; tokens charged |
| `TestProxyStreamingCannotBypassBudget` | 200, 200, then 429 with streams only |
| `TestProxyRejectsBadRequests` | 400 and 413; provider never called |
| `TestPrepareRequest` (table) | include_usage added, other options kept, duplicate keys, non-JSON untouched |
| `TestUsageStreamReader` (table) | one-byte reads, CRLF, no usage, 1 MiB+ line, no trailing newline; `onDone` exactly once |
| `TestProxyUpstreamDown` | 502 returned, no secrets logged |
| `TestParseUsage` (table) | normal, zero tokens, missing usage, broken JSON, empty body |
| `TestBudgetBlocksWhenOver` | 200, 200, then 429; the blocked call never reaches the provider |
| `TestBudgetUnknownTeamFailsClosed` | unknown team → 403, provider never called |
| `TestBudgetCountsPerTeamAndHidesHeader` | tokens charged to the right team; header not forwarded |
| `TestBudgetNotChargedOnUpstreamError` | a failed call costs nothing |
| `TestConcurrentRequestsCountedExactly` | 20 agents × 10 requests through the real proxy, exact total |
| `TestAllow`, `TestSnapshot`, `TestParseBudgets` (tables) | meter rules and `BUDGETS` parsing |
| `TestConcurrentRecord` | 100 goroutines × 1000 updates, no lost updates |

All tests run with Go's **race detector** (`go test -race`).

**Testing the tests.** For the two most important tests we checked that they fail when the code is broken on purpose (*mutation testing*): removing the meter's mutex makes `TestConcurrentRecord` report data races, and buffering the whole stream makes `TestProxyStreamingIsLiveAndCounted` fail with "the proxy is buffering the stream".
