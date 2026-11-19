# Ideas backlog

Ideas from the cloud-native ecosystem that could become features. Added by `/research`. Best ideas move into `ROADMAP.md`.

## Idea format

```
## IDEA-<number>: <short name>
- Inspired by: <project + link>
- The idea in one line:
- How it works there (plain words + analogy):
- How it would work here:
- Impact / Effort / Learning (1–5 each): x / x / x
- Already solved elsewhere?: no | yes — <where; integrate instead?>
- Concept learned: <e.g. token bucket, reconciliation loop, consistent hashing>
- Status: new | spike | building | done | rejected (<reason>)
```

Scoring guide: **Impact** = how much it closes our gap (per-agent attribution, request-time enforcement, budgets as Kubernetes resources, agent ownership). **Effort** = 5 means hardest. **Learning** = how much system design / distributed systems it teaches.

## Research candidates (not yet studied)

Verify each one's current status before relying on it. Remove an entry once it has a log entry in `RESEARCH_LOG.md`.

- **kagent** — how agents, sessions and model calls flow; where a budget hook fits.
- **Kubernetes ResourceQuota / LimitRange** — Kubernetes already budgets CPU and memory per namespace; reuse that API design for tokens.
- **OpenCost** — cost allocation by namespace and label; allocate token cost the same way.
- **OpenTelemetry Collector + GenAI semantic conventions** — receivers → processors → exporters; a processor that turns tokens into cost.
- **Jaeger** — show per-agent token cost along a trace.
- **Prometheus** — counters, recording rules, burn-rate alerts (borrowed from SRE error budgets).
- **Envoy and Envoy AI Gateway** — token-based rate limiting at the proxy; learn the design, integrate rather than duplicate.
- **KEDA** — scale agents down as a budget runs out.
- **OPA / Kyverno** — policy as code: no agent runs without an owner and a budget.
- **Dapr** — state store and pub/sub building blocks for counters and budget events.
- **Cilium / eBPF** — wild idea: observe LLM traffic at the kernel level.
- **FinOps Foundation FOCUS spec / Tokenomics Foundation** — standard formats for AI cost data; make our exports compatible.

## Ideas

## IDEA-1: Count tokens in streaming answers
- Inspired by: M0 limitation; OpenAI-compatible APIs can send usage in the last stream chunk when `stream_options.include_usage` is set (Ollama lists it as supported: https://docs.ollama.com/api/openai-compatibility)
- The idea in one line: read the SSE stream as it flows, find the final chunk that carries `usage`, and count it — without delaying the stream.
- How it works there (plain words + analogy): the provider sends the answer word by word and, at the very end, a "bill" chunk with the token totals — like a waiter bringing the bill after the last dish.
- How it would work here: wrap the response body in a reader that copies bytes to the agent immediately and scans each `data:` line for `usage`; optionally add `include_usage` to the request if the agent did not.
- Impact / Effort / Learning (1–5 each): 5 / 3 / 4
- Already solved elsewhere?: yes — LLM gateways do this; we need it for our own counters anyway.
- Concept learned: streaming parsers, io.Reader wrapping, backpressure
- Status: new
