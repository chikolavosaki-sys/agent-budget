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

## Identity and security

**Authentication vs. authorization** — *Authentication* answers "who are you?" (we check the virtual key → `401` if unknown). *Authorization* answers "may you do this?" (we check the budget → `429` if used up).
*Analogy:* at the cinema, the ticket checker first checks it's *your* ticket (authentication), then that it's for *this* show (authorization).

**Virtual key** — A key our proxy gives to each agent, looking like a normal API key (`ab-…`). It only works against our proxy, and it tells the proxy exactly which agent is calling. The real provider key stays inside the proxy.
*Analogy:* every hostel student has their own ID card; only the warden holds the master key. A lost ID card is cancelled alone — the locks don't need changing.

**Bearer token** — The format `Authorization: Bearer <token>`: "whoever *bears* (carries) this token may enter". That is why a leaked token is dangerous, and why we never log it.

**Hash (SHA-256)** — A function that turns any text into a fixed-length fingerprint (64 hex characters). Same input → same fingerprint; but you cannot get the input back from the fingerprint.
*Analogy:* a fingerprint identifies a person, but you can't rebuild the person from it.

**Why store only the hash?** — The agents file can then be committed to Git or put in a Kubernetes ConfigMap without leaking keys. To check a key, the proxy hashes it and compares fingerprints.

**Why SHA-256 and not bcrypt?** — Human passwords are short and guessable ("password123"), so they need a deliberately *slow* hash like bcrypt to make guessing expensive. Our keys are 128+ random bits from `crypto/rand` — there is nothing to guess, so a fast hash is enough.

**Owner** — Every agent must name a person or team responsible for it. When an agent overspends at 3 a.m., somebody must be reachable. The proxy refuses to start if an owner is missing.

## Go

**Goroutine** — A lightweight thread managed by Go. Costs a few KB; you can run thousands.
*Analogy:* waiters in a restaurant — one per table, and hiring one is cheap.

**`context.Context`** — A value passed along with a request that carries deadlines, cancel signals and small values (like our start time).
*Analogy:* the order slip clipped to the plate — it travels with the order, and the kitchen can see "cancelled".

**`io.Reader` / stream** — Data you read piece by piece; once read, it's gone.
*Analogy:* water through a pipe — that's why we collect the body in a bucket (`[]byte`) and pour it again.

**Wrapping a reader (decorator)** — A type that holds another `io.Reader` and adds behaviour while passing the data along. Our `usageStreamReader` wraps the provider's stream and *peeks* at every line without slowing it down.
*Analogy:* a ticket checker standing at the train door — passengers keep flowing; he just glances at each ticket.

**Flush** — Push data that is waiting in a buffer out to the network *now*. For streams, every piece must be flushed or the agent sees nothing until the end.

**Parser differential** — Two programs read the same input differently (e.g. `{"stream":false,"stream":true}`: one takes the first key, one the last). An attacker can use the gap. We avoid it by re-encoding the request, so the provider receives exactly what we checked.
*Analogy:* a cheque where the amount in words and in digits differ — the bank and you may read different amounts.

**`sync.Once`** — Runs a function at most one time, even if called from many places. Used so a stream is charged exactly once.

**Mutation testing** — Breaking the code on purpose to check that a test notices. A test that still passes on broken code is not testing anything.

**`httptest.Server`** — A real HTTP server started inside a test, on a random port. Lets us test with a *fake* LLM.
*Analogy:* a mock exam before the real one.

**Race detector (`-race`)** — A Go tool that catches two goroutines touching the same memory at the same time without coordination.
*Analogy:* two people writing on the same register page at once — the race detector is the teacher who notices.

**Table-driven test** — One test function with a list (table) of input → expected output cases. Adding a case = adding one row.

**Structured logging (`slog`)** — Logs as key=value pairs instead of free text, so machines can search them (`input_tokens=11`).

**Fail closed vs. fail open** — When the system is unsure, *fail closed* says "no", *fail open* says "yes". We fail closed: an unknown key gets `401`, and an agent the meter does not know is refused.
*Analogy:* a door with an electronic lock — in a power cut, a bank vault stays locked (closed); a fire exit opens (open).

**401 vs. 403 vs. 429** — `401 Unauthorized`: "I don't know who you are" (no key or a wrong key). `403 Forbidden`: "I know you, but you may not do this". `429 Too Many Requests`: "you may, but you've used up your share".

**Graceful shutdown** — On Ctrl+C, stop taking new work but finish current work before exiting.
*Analogy:* the shop pulls down the shutter halfway — no new customers, but those inside finish shopping.

**Data race** — Two goroutines touch the same memory at the same time, and at least one of them writes. The result depends on luck.
*Analogy:* two people updating the same bank passbook at two counters — one deposit disappears.

**Lost update** — The most common data-race symptom: two "read → add → write" steps interleave, and one addition is overwritten. See the table in [Architecture](ARCHITECTURE.md#the-meter-and-the-mutex).

**Mutex (`sync.Mutex`)** — A lock: `Lock()` waits until no one else holds it, `Unlock()` lets the next one in. Code between them runs one goroutine at a time.
*Analogy:* the single key to the hostel washroom — whoever has the key goes in; everyone else waits.

**`defer`** — Runs a line when the function returns, however it returns. `defer m.mu.Unlock()` makes sure we never forget to unlock.
*Analogy:* "switch off the lights when you leave" — written on the door, so it happens on every exit.

**Check-then-act** — Checking a condition and acting on it as two separate steps; the world can change in between. Our budget check is check-then-act, which is why the limit is *soft*.
*Analogy:* you check the cinema app — 1 seat left — but by the time you pay, someone else booked it.

**Interface** — A Go type that lists methods, not data. Our proxy depends on a `Budget` interface (`Allow`, `Record`), not on the meter itself, so tests or a future Redis meter can plug in.
*Analogy:* a phone charger port — any charger that fits the shape works.

**Sentinel error / `errors.Is`** — A named error value (`ErrOverBudget`) that callers can test for, even after it has been wrapped with more detail via `fmt.Errorf("...: %w", err)`.

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
