# gonveyor

[![CI](https://github.com/terapps/gonveyor/actions/workflows/main.yml/badge.svg)](https://github.com/terapps/gonveyor/actions/workflows/main.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/terapps/gonveyor.svg)](https://pkg.go.dev/github.com/terapps/gonveyor)

A typed task orchestration framework for Go, built on AMQP and a relational store.

Define workflows as typed DAGs. Submit them. Let the conveyor run.

---

## Concepts

| Term | Role |
|------|------|
| **Blueprint** | A named workflow: the DAG of task definitions |
| **Station** | A typed task node — input type `I`, output type `O` |
| **Manifest** | A blueprint instantiated with a concrete input — ready to persist and dispatch |
| **Gonveyor** | The worker side: consumes tasks, runs handlers, dispatches next tasks |
| **Gonductor** | The producer side: submits manifests and dispatches initial tasks |

---

## Defining a workflow

```go
import (
    "github.com/terapps/gonveyor"
    "github.com/terapps/gonveyor/blueprint"
)

// 1. Define typed task nodes
var CutSteel = blueprint.Define[CutSteelInput, CutSteelOutput]("cut_steel")

var DrillHoles = blueprint.Define[DrillHolesInput, DrillHolesOutput]("drill_holes",
    gonveyor.Intake(CutSteel, func(o CutSteelOutput, in *DrillHolesInput) {
        in.SheetID = o.SheetID
    }),
)

var WeldAssembly = blueprint.Define[WeldAssemblyInput, WeldAssemblyOutput]("weld_assembly",
    gonveyor.Intake(DrillHoles, func(o DrillHolesOutput, in *WeldAssemblyInput) {
        in.HoleCount = o.HoleCount
    }),
    gonveyor.Intake(MillSurface, func(o MillSurfaceOutput, in *WeldAssemblyInput) {
        in.Roughness = o.Roughness
    }),
)

// 2. Assemble the blueprint
//                 ┌──> drill_holes ───┐
// cut_steel ──────┼──> mill_surface ──┼──> weld_assembly ──> ...
//                 └──> bend_frame ────┘
var SteelFrameDAG = blueprint.New("steel_frame_dag",
    CutSteel, DrillHoles, MillSurface, BendFrame, WeldAssembly,
)
```

### Fan-out with `Split`

Dispatch N parallel instances of a task at manifest time:

```go
manifest, _ := SteelFrameDAG.Manifest(
    CutSteelInput{OrderID: "order-1"},
    gonveyor.Split(DrillHoles, 3),
)
```

```
cut_steel ──┬──> drill_holes/0 ──┐
            ├──> drill_holes/1 ──┼──> weld_assembly
            └──> drill_holes/2 ──┘
```

Downstream tasks wait for **all** split instances before unblocking.

### Fan-in with `Merge`

When a downstream task needs to collect N outputs into a slice:

```go
var Inspect = blueprint.Define[InspectInput, InspectOutput]("inspect",
    gonveyor.Merge(DrillHoles, func(outputs []DrillHolesOutput, in *InspectInput) {
        ids := make([]string, len(outputs))
        for i, o := range outputs { ids[i] = o.HoleID }
        in.HoleIDs = ids
    }),
)
```

Use `gonveyor.Intake` when a single upstream output maps to specific fields of this task's input.
Use `gonveyor.Merge` when you need **all outputs as a slice** (fan-in aggregation).

> **Note:** when multiple `Intake`/`Merge` deps contribute to the same input struct, each must write to disjoint fields.

---

## Worker side

```go
sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN("postgres://...")))
db := bun.NewDB(sqldb, pgdialect.New())

conn, _ := amqp.Dial("amqp://...")
queue, _ := amqp.NewQueue("gonveyor", amqp.WithDeadLetter("gonveyor.dlx"))
dispatcher, _ := conn.NewDispatcher(queue)
worker, _ := conn.NewWorker(queue)

g := gonveyor.NewGonveyor(bunstore.New(db), dispatcher, worker)

g.RegisterHandler(DrillHoles, gonveyor.Handle(DrillHoles,
    func(ctx context.Context, in DrillHolesInput) (DrillHolesOutput, error) {
        return DrillHolesOutput{SheetID: in.SheetID, HoleCount: 4}, nil
    },
))

g.Listen(ctx)
```

When a handler completes, gonveyor automatically:
1. Persists the result
2. Resolves which tasks are now unblocked
3. Builds their typed input from upstream outputs
4. Dispatches them to the queue

**Race safety:** transitions are conditional UPDATEs (`WHERE status = 'pending'` / `WHERE status = 'dispatched'`). If two workers race on the same task, only one wins — the other bails silently. A heartbeat goroutine renews a 30s lease every 15s while a task is running; expired leases are detectable via the `locked_until` column for manual recovery.

**Handler context:** handlers receive a non-cancellable context (`context.WithoutCancel`). Shutdown signals do not interrupt a running task — the worker waits for the handler to return. Do not rely on `ctx.Done()` inside handlers.

---

## Producer side

```go
sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN("postgres://...")))
db := bun.NewDB(sqldb, pgdialect.New())

conn, _ := amqp.Dial("amqp://...")
queue, _ := amqp.NewQueue("gonveyor", amqp.WithDeadLetter("gonveyor.dlx"))
dispatcher, _ := conn.NewDispatcher(queue)

gc := gonveyor.NewGonductor(bunstore.New(db), dispatcher)

manifest, _ := SteelFrameDAG.Manifest(CutSteelInput{
    OrderID:   "order-42",
    SheetSize: "1200x800",
})

gc.Submit(ctx, manifest)
gc.Dispatch(ctx, manifest.PendingTasks())
```

`Submit` persists the full manifest. `Dispatch` publishes the initial tasks (those with no dependencies).

---

## Worker options

```go
amqp.WithPrefetch(1)             // messages fetched ahead
amqp.WithConcurrency(1)         // goroutines processing in parallel
amqp.WithTag("worker-1")        // consumer tag
amqp.WithRequeueFn(func(err error) bool {
    return errors.Is(err, ErrTransient)
})
```

---

## Project layout

```
gonveyor/
├── blueprint/         # Typed DAG DSL — Define, New, AnyDef, Station
│   ├── blueprint.go   # Type definitions, Intake, Merge
│   └── manifest.go    # Manifest building, Split
├── transport/         # Transport interfaces + AMQP implementation
│   └── amqp/          # AMQP worker and dispatcher
├── store/             # Persistence interfaces
│   └── bun/           # PostgreSQL implementation (bun ORM)
├── examples/
│   ├── factory/       # Worker process — registers handlers
│   └── publisher/     # Producer process — submits workflows
└── scripts/
    └── migrations/    # PostgreSQL schema
```

---

## Graph validation

`blueprint.New` panics at init time if:
- a dependency references a task not present in the blueprint
- the graph contains a cycle

Workflows are typically declared as package-level `var`, so invalid graphs are caught at startup.

---

## Schema

Three tables: `blueprints`, `blueprint_tasks`, `blueprint_task_dependencies`.

Run `scripts/migrations/001_init.sql` to initialise (auto-applied by docker compose on first start).

---

## Logger

gonveyor uses a package-level logger via `log/slog`. Plug in any `slog.Handler`:

```go
// stdlib
gonveyor.SetLogger(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

// zap (via zapslog bridge)
import "go.uber.org/zap/exp/zapslog"
gonveyor.SetLogger(slog.New(zapslog.NewHandler(zap.L().Core())))
```

Defaults to `slog.Default()`.

---

## Local development

```bash
docker compose up -d   # starts PostgreSQL and RabbitMQ
```

PostgreSQL is available at `localhost:5432`, RabbitMQ at `localhost:5672` (management UI at `localhost:15672`). Default credentials: `gonveyor / gonveyor`.

The migration runs automatically on first container start via `/docker-entrypoint-initdb.d`.

---

## Build & test

```bash
make build   # build all modules
make test    # unit tests (no external deps)
make lint    # golangci-lint across all modules
```

Integration tests (`store/bun`) require PostgreSQL — start it first:

```bash
docker compose up -d
make test
```
