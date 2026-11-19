# Concepts

Every term used in this project, in plain words with an everyday analogy. Useful for learning — and for viva questions.

## AI and LLMs

**LLM (Large Language Model)** — A program that reads text and writes text (ChatGPT, Llama…).
*Analogy:* a very well-read friend who answers any question, but charges per word.

**Token** — The unit LLMs count and bill in. Roughly ¾ of an English word; "Kubernetes" may be 2–3 tokens.
*Analogy:* the meter in an auto-rickshaw — you pay per unit, not per trip.

**Input tokens / output tokens** — Tokens in your question (prompt) vs. tokens in the answer (completion). Providers return both in a `usage` object; output tokens are usually more expensive.

**AI agent** — A program that calls an LLM *in a loop*: think → use a tool → think again. Many calls per task, so costs add up quickly.
*Analogy:* an intern who keeps phoning the expert for every small step.

**OpenAI-compatible API** — The request/response format first used by OpenAI (`POST /v1/chat/completions`). Many others copy it (Ollama, vLLM, Groq…), so one proxy works with all of them.

**Streaming (SSE)** — The answer arrives word by word instead of all at once, using *Server-Sent Events* (`Content-Type: text/event-stream`).
*Analogy:* live cricket commentary vs. reading the match report afterwards.

## Networking

**Proxy / reverse proxy** — A server that receives requests on behalf of another server and forwards them. A *reverse* proxy sits in front of the real server; the client doesn't know it's there.
*Analogy:* the canteen counter between you and the kitchen.

**HTTP status codes** — Three-digit result codes. `200` OK · `429` Too Many Requests (we'll use this to say "over budget") · `502` Bad Gateway (the proxy couldn't reach the server behind it).

**Headers** — Labels on a request or response (`Content-Type`, `Authorization`).
*Analogy:* what's written on the outside of an envelope.

**gzip / Content-Encoding** — Compression for HTTP bodies. The proxy must unzip before it can read tokens.
*Analogy:* a vacuum-packed parcel — smaller to send, must be opened to read.

**TCP connection / port** — TCP is the reliable "phone line" between two programs; a port (like `8080`) is the extension number that picks which program answers.

## Go

**Goroutine** — A lightweight thread managed by Go. Costs a few KB; you can run thousands.
*Analogy:* waiters in a restaurant — one per table, and hiring one is cheap.

**`context.Context`** — A value passed along with a request that carries deadlines, cancel signals and small values (like our start time).
*Analogy:* the order slip clipped to the plate — it travels with the order, and the kitchen can see "cancelled".

**`io.Reader` / stream** — Data you read piece by piece; once read, it's gone.
*Analogy:* water through a pipe — that's why we collect the body in a bucket (`[]byte`) and pour it again.

**`httptest.Server`** — A real HTTP server started inside a test, on a random port. Lets us test with a *fake* LLM.
*Analogy:* a mock exam before the real one.

**Race detector (`-race`)** — A Go tool that catches two goroutines touching the same memory at the same time without coordination.
*Analogy:* two people writing on the same register page at once — the race detector is the teacher who notices.

**Table-driven test** — One test function with a list (table) of input → expected output cases. Adding a case = adding one row.

**Structured logging (`slog`)** — Logs as key=value pairs instead of free text, so machines can search them (`input_tokens=11`).

**Graceful shutdown** — On Ctrl+C, stop taking new work but finish current work before exiting.
*Analogy:* the shop pulls down the shutter halfway — no new customers, but those inside finish shopping.

**Linter (golangci-lint)** — A tool that reads code without running it and points out bugs and style problems.
*Analogy:* a strict teacher checking your answer sheet before submission.

## Kubernetes (coming in later milestones)

**Kubernetes** — Software that runs and manages many containers across many machines.
*Analogy:* a hostel warden who assigns rooms, restarts anyone who falls sick, and keeps the head-count right.

**CRD (Custom Resource Definition)** — A way to teach Kubernetes a new kind of object. We will add `TokenBudget`, so a budget is just YAML.

**Controller / reconcile loop** — A program that keeps comparing "what YAML says" with "what is real" and fixes the difference, again and again.
*Analogy:* a thermostat — set 24 °C, it keeps checking and adjusting.

**kind** — "Kubernetes in Docker": a full cluster on your laptop, free.

**OpenTelemetry, Jaeger, Prometheus, Grafana** — Standard tools for traces (the story of one request), metrics (numbers over time) and dashboards (charts).

**kagent** — An open-source project for running AI agents on Kubernetes; our planned first integration.
