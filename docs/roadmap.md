# Roadmap

## Done

- Typed DAG DSL — `Define`, `Intake`, `Merge`, `After`
- `Seed` — explicit payload injection at manifest time, works alongside dep-based injection
- `Fan` / `Seeds` — fan-out with static N or per-instance payloads
- Manifest validation — error if a root task has no `Seed`
- Heartbeat
- Event sourcing — append-only `node_events` log; task status is a projection
- Signals — external events (`Signal[T]`) that unblock waiting nodes via `SendSignal`

--- 

## Planned

### OpenTelemetry

Expose traces and metrics via the OpenTelemetry SDK. Each task execution becomes a span (dispatch → claim → complete/fail), blueprint launch is the root span. Metrics: task throughput, failure rate, claim latency, heartbeat lag.

Zero-config by default — picks up the ambient OTEL exporter if one is configured.

---

### Real time updates / Notification queue

Emit structured events (blueprint launched, task completed, task failed, blueprint completed) to a secondary queue so external consumers can react without polling Postgres. Backends: AMQP (fanout exchange), webhook, or direct Postgres `LISTEN/NOTIFY`.

---

### Sweeper / reaper

Detect tasks stuck in `started` state whose heartbeat has expired (last heartbeat > 2× lease duration ago) and mark them `failed`. Unlocks the node so it can be retried or surfaced as dead.

Currently the worker emits heartbeats to `node_heartbeats` every 15s but nothing consumes them to enforce the lease. Implementation TBD — could live in the scheduler.

---

### Scheduled blueprints

Launch a blueprint at a future time or on a cron expression.

```go
gc.Schedule(ctx, manifest, gonveyor.At(time.Now().Add(24*time.Hour)))
gc.Schedule(ctx, manifest, gonveyor.Cron("0 9 * * MON"))
```

Backed by a `scheduled_blueprints` table polled by a scheduler goroutine — no external dependency.

---

### Repeating blueprints

Re-launch a blueprint automatically after it completes, on a fixed interval or cron. Useful for periodic jobs that share the same DAG shape.

```go
gc.Schedule(ctx, manifest, gonveyor.Every(24*time.Hour))
```

---

### Sub-blueprints *(à évaluer)*

Compose blueprints by launching a child blueprint from within a task and waiting for its completion before continuing. Useful when a step is itself a complex workflow.

Pattern under evaluation — tradeoffs around error propagation, observability, and deadlock risk (child waiting on parent resource) need to be assessed before committing to a design.

---

### Per-station routing keys

Route tasks to different queues based on the station definition (e.g. `SendEmail` → `tasks.email`, `GenerateDocument` → `tasks.document`).

Routing key defined at the station level (compile-time), resolved at dispatch time from a `station_key → routing_key` registry built when registering blueprints — no schema change needed. Requires a topic exchange and one worker per queue.

### UI

Web dashboard for monitoring blueprint instances and task state — the gonveyor equivalent of Asynqmon or the Temporal Web UI.

Views:
- **Blueprint list** — active/completed/failed instances, submission time, progress
- **Blueprint detail** — DAG visualization with per-task status, payload and result inspection
- **Task detail** — status history, payload, result, error, heartbeat timestamps
- **Dead tasks** — failed tasks with retry controls

Stack TBD — likely a small Go HTTP server serving a single-page app. Reads from the existing Postgres store, no additional infra.

