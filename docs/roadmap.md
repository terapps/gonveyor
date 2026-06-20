# Roadmap

## Done

- Typed DAG DSL — `Define`, `Intake`, `Merge`, `After`
- `Seed` — explicit payload injection at manifest time, works alongside dep-based injection
- `Split` / `SplitWith` — fan-out with static N or per-instance payloads
- Manifest validation — error if a root task has no `Seed`
- Heartbeat / distributed lock — 30s lease renewed every 15s
- Race safety — conditional UPDATEs prevent double-execution

---

## Planned

### Event Sourcing

Replace mutable task state with an append-only event log. Current task status becomes a projection of events rather than a directly mutated column.

**Events:** `task_dispatched`, `task_started`, `task_completed`, `task_failed`, `task_retried`, `blueprint_completed`

**What it unlocks — for free:**
- **Replay / time-travel debugging** — reconstruct blueprint state at any point in time
- **UI real-time updates** — Postgres `LISTEN/NOTIFY` on the events table, no polling
- **Metrics / alerting** — consumers read from the event log, no in-memory bus needed
- **Audit log** — durable, queryable, survives restarts
- **Distributed** — all workers emit to the same log, all consumers see the same events

**Approach:** pragmatic hybrid — event log runs in parallel with the existing `blueprint_tasks` projection. No full rewrite of the store; transitions (`SetRunning`, `SetSuccess`, `SetFailed`) append an event as a side effect. Pure event sourcing (state derived solely from events) is a future option once the log is established.

---

### Signals

External events that unblock a waiting task. Use case: human approval, webhook callback, external system confirmation.

A task declares it waits for a named signal; the signal is sent via an API call and unblocks dispatch.

```go
var AwaitApproval = blueprint.Signal[ApprovalPayload]("await_approval")

var Process = blueprint.Define[ProcessInput, ProcessOutput]("process",
    gonveyor.OnSignal(AwaitApproval, func(s ApprovalPayload, in *ProcessInput) {
        in.ApprovedBy = s.UserID
    }),
)
```

---

### Sleep / Durable Timers

A task that delays dispatch of downstream tasks for a fixed duration — survives process restarts.

---

### Child Workflows

Spawn a full blueprint from within a handler — the parent task can optionally wait for it to complete.
// Bof

---

### Task Versioning

Run multiple versions of the same task definition in parallel — useful for rolling deploys without draining the queue first.

```go
var ProcessFile = blueprint.Define[ProcessFileInput, ProcessFileOutput]("process_file@v2")
```

Routing: workers advertise which versions they handle; the dispatcher targets accordingly. Old in-flight tasks on `v1` finish on `v1` workers, new tasks go to `v2`.

---

### UI

Web dashboard for monitoring blueprint instances and task state — the gonveyor equivalent of Asynqmon or the Temporal Web UI.

Views:
- **Blueprint list** — active/completed/failed instances, submission time, progress
- **Blueprint detail** — DAG visualization with per-task status, payload and result inspection
- **Task detail** — status history, payload, result, error, heartbeat timestamps
- **Dead tasks** — failed tasks with retry controls

Stack TBD — likely a small Go HTTP server serving a single-page app. Reads from the existing Postgres store, no additional infra.

---

## Not planned

- **Saga / compensating transactions** — expressible today by adding rollback tasks to the DAG manually
