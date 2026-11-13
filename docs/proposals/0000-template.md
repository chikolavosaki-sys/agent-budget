# <NNNN>: <Proposal title>

- Status: draft | accepted | implemented | rejected
- Author: <name>
- Created: <YYYY-MM-DD>
- Milestone: <M#>

## Summary

One paragraph: what changes and why.

## Motivation

- The problem, with a concrete example (which agent, which team, what goes wrong today).
- Goals
- Non-goals (what this proposal deliberately does not do)

## Proposal

How it works, from the user's point of view: the YAML they write, the request an agent sends, what they see.

### API changes

Go types / CRD fields, with an example manifest.

### Request flow

Step by step, from the agent's request to the response. A diagram if it helps.

## Design details

### Data and storage

What is stored, where, and for how long.

### Concurrency and failure

- What happens when two requests arrive at the same moment?
- What happens when the proxy restarts, or the store is unreachable?
- Do we fail open or fail closed, and why?

### Performance

Expected extra latency on the request path, and how we will measure it.

### Security

Secrets, RBAC, what must never be logged.

## Alternatives considered

Each alternative with why we did not choose it. Include "use an existing tool" when one exists.

## Test plan

Unit, envtest and e2e tests that prove it works, including a `-race` concurrency test where state is shared.

## Prior art

Links to other projects that solve a similar problem and what we borrowed from them.
