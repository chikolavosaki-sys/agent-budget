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

## D-001: Telemetry explains spend; it never enforces it (2026-11-13)
- Context: Traces (Jaeger) and metrics carry token counts, so they are tempting as the source of truth for budgets.
- Options considered: enforce from traces / enforce from our own counters on the request path.
- Decision: budgets are enforced only from counters updated on the request path; OpenTelemetry data is for explanation and dashboards.
- Why: traces are often sampled and arrive after the request; a budget must see every token before the next request.
- Status: accepted
