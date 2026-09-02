# Initial SLOs and capacity targets

Status: Phase 0 planning targets; measured at the TG service boundary over rolling
28 days, excluding documented maintenance and caller/downstream time beyond the
declared component budget. These are RC design inputs, not current production claims.

## Service objectives

| Signal | Target / budget |
|---|---|
| Authenticated discovery/lookup availability | 99.95%; p95 100 ms, p99 250 ms |
| Invocation acceptance/lookup availability (before downstream dependency outcome) | 99.9%; p95 150 ms, p99 400 ms |
| TG internal overhead excluding AG/GR/provider/downstream | p95 100 ms, p99 250 ms |
| AG authorization adapter overhead | p95 150 ms, p99 400 ms; hard default 1 s |
| GR pre or post evaluation overhead | each p95 250 ms, p99 750 ms; hard default 2 s |
| Credential resolution | cache p95 20 ms; provider p95 250 ms, p99 750 ms; hard default 2 s |
| Ready invocation queue-to-claim | p95 250 ms, p99 1 s under provisioned load |
| Transactional outbox lag | p95 <2 s, p99 <10 s; alert at 30 s sustained 5 min |
| Ambiguous item first reconciliation | p95 <30 s where automatic reconciliation exists; alert backlog oldest >5 min |

Availability never justifies bypassing authorization, approval, mandatory GR,
credential, fencing, or ambiguity controls. Fail-closed dependency outcomes are
classified separately from TG process availability and tracked by dependency.

## Initial single-deployment capacity envelope

- 1 MiB maximum uncompressed request and canonical argument bytes; tool limits may narrow.
- 4 MiB maximum buffered result and 16 MiB maximum explicitly streamed result;
  default tool result is 1 MiB. Oversize content is safely rejected/truncated only
  under an output contract, never silently accepted.
- Maximum JSON depth 64, object members 10,000, string 256 KiB, and schema 512 KiB.
  Schema compilation permits at most 10,000 JSON nodes and only local references;
  each process caches at most 128 compiled schemas by default and 1,024 under an
  explicit deployment configuration.
- Public request deadline 30 s; connector default 10 s; per-tool deadline may only
  narrow unless an administrator-reviewed async profile applies.
- 1,000 concurrent HTTP requests, 250 active connector sends, 50 per tenant, 20 per
  run, and 10 per connector instance as starting hard caps; deterministic `429` or
  queued backpressure applies before memory exhaustion.
- Initial sustained target 500 discovery RPS and 100 governed invocation RPS per
  three-replica deployment, with 2x 10-minute burst after load qualification.
- Outbox supports 1,000 events/s sustained and 1 million queued safe events without
  loss; storage alerts reserve headroom before the cap.
- Automatic retries default maximum 3 attempts inside the tool/provider window;
  reconcile/manual-review work has independent bounded workers and queues.

## Measurement and error budgets

Use server-side histograms with fixed buckets and low-cardinality outcome/tool-class
labels; tenant, run, resource, and call IDs are prohibited labels. Report dependency
latency separately so internal overhead is visible. Cancellation, caller validation,
policy denial, GR block, approval wait, and declared rate limit do not count as TG
availability failures, but are measured and alerted for abnormal rates.

The monthly error budget is 0.05% for discovery and 0.1% for invocation acceptance.
Burn alerts use 2% budget in 1 hour and 10% in 6 hours as initial thresholds.
Capacity/load tests must validate memory, database connections/locks, queue lag,
large valid results, cancellation, and graceful overload before these targets become
release claims. Any target change needs evidence and documented security impact.
