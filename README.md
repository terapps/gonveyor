# gonveyor

[![CI](https://github.com/terapps/gonveyor/actions/workflows/main.yml/badge.svg)](https://github.com/terapps/gonveyor/actions/workflows/main.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/terapps/gonveyor.svg)](https://pkg.go.dev/github.com/terapps/gonveyor)
[![Latest tag](https://img.shields.io/github/v/tag/terapps/gonveyor)](https://github.com/terapps/gonveyor/releases)
[![Go version](https://img.shields.io/github/go-mod/go-version/terapps/gonveyor)](go.mod)
[![License](https://img.shields.io/github/license/terapps/gonveyor)](LICENSE)

A typed task orchestration framework for Go, built on AMQP and a relational store.

Define workflows as typed DAGs. Submit them. Let the conveyor run.

---

## Concepts

| Term | Role |
|------|------|
| **Blueprint** | A named workflow: the DAG of task definitions |
| **Station** | A typed task node — input type `I`, output type `O` |
| **Signal** | A gateway node activated by an external event (human approval, webhook…) |
| **Manifest** | A blueprint instantiated with concrete payloads — ready to persist and dispatch |
| **Gonveyor** | The worker side: consumes tasks, runs handlers, dispatches next tasks |
| **Gonductor** | The producer side: submits manifests and signals external events |

---

## Defining a workflow

Stations are pure typed nodes. Dependencies are declared in `blueprint.New` via `Wire` — so the same station can be reused across multiple blueprints with different wiring.

```go
import (
    "github.com/terapps/gonveyor"
    "github.com/terapps/gonveyor/blueprint"
)

// 1. Define typed task nodes — no deps here
var DownloadAsset  = blueprint.Define[DownloadInput, DownloadOutput]("download_asset")
var TranscodeVideo = blueprint.Define[TranscodeInput, TranscodeOutput]("transcode_video")
var GenerateThumb  = blueprint.Define[ThumbInput, ThumbOutput]("generate_thumb")
var ExtractAudio   = blueprint.Define[AudioInput, AudioOutput]("extract_audio")
var PackageBundle  = blueprint.Define[PackageInput, PackageOutput]("package_bundle")

// 2. Assemble the blueprint — wiring declared here
//                    ┌──> transcode_video ──┐
// download_asset ────┼──> generate_thumb   ──┼──> package_bundle
//                    └──> extract_audio   ──┘
var VideoProcessing = blueprint.New("video_processing",
    DownloadAsset,
    blueprint.Wire(TranscodeVideo,
        gonveyor.Intake(DownloadAsset, func(o DownloadOutput, in *TranscodeInput) {
            in.AssetID = o.AssetID
        }),
    ),
    blueprint.Wire(GenerateThumb,
        gonveyor.Intake(DownloadAsset, func(o DownloadOutput, in *ThumbInput) {
            in.AssetID = o.AssetID
        }),
    ),
    blueprint.Wire(ExtractAudio,
        gonveyor.Intake(DownloadAsset, func(o DownloadOutput, in *AudioInput) {
            in.AssetID = o.AssetID
        }),
    ),
    blueprint.Wire(PackageBundle,
        gonveyor.Intake(TranscodeVideo, func(o TranscodeOutput, in *PackageInput) {
            in.VideoURL = o.URL
        }),
        gonveyor.Intake(GenerateThumb, func(o ThumbOutput, in *PackageInput) {
            in.ThumbURL = o.URL
        }),
        gonveyor.Intake(ExtractAudio, func(o AudioOutput, in *PackageInput) {
            in.AudioURL = o.URL
        }),
    ),
)
```

Root nodes (no deps) are passed bare. `Wire` is only needed when declaring dependencies.

### Reusing a station across blueprints

Because wiring lives in `blueprint.New`, the same station can appear in multiple blueprints with different deps:

```go
var Notify = blueprint.Define[NotifyInput, NotifyOutput]("notify")

// Order confirmed — notify after payment clears
var OrderFlow = blueprint.New("order_flow",
    PlaceOrder, ChargePayment,
    blueprint.Wire(Notify,
        gonveyor.Intake(ChargePayment, func(o ChargeOutput, in *NotifyInput) {
            in.Template = "order_confirmed"
            in.UserID   = o.UserID
        }),
    ),
)

// Refund processed — notify when refund is done, no data needed
var RefundFlow = blueprint.New("refund_flow",
    ProcessRefund,
    blueprint.Wire(Notify,
        gonveyor.After[NotifyInput](ProcessRefund),
    ),
)

// One handler for both flows
g.RegisterBlueprint(OrderFlow)
g.RegisterBlueprint(RefundFlow)
g.RegisterHandler(Notify, gonveyor.Handle(Notify, notifyHandler))
```

### Fan-out with `Split`

Dispatch N parallel instances of a task at manifest time:

```go
manifest, _ := VideoProcessing.Manifest(
    gonveyor.Seed(DownloadAsset, DownloadInput{AssetID: "asset-1"}),
    gonveyor.Split(TranscodeVideo, 3), // transcode at 1080p, 720p, 480p in parallel
)
```

```
download_asset ──┬──> transcode_video/0 ──┐
                 ├──> transcode_video/1 ──┼──> package_bundle
                 └──> transcode_video/2 ──┘
```

Downstream tasks wait for **all** split instances before unblocking.

### Fan-out with `SplitWith`

When N is only known at runtime and each instance needs distinct input data:

```go
tracks, _ := repo.ListTracks(ctx, albumID)

manifest, _ := bp.Manifest(
    gonveyor.Seed(FetchAlbum, FetchAlbumInput{AlbumID: albumID}),
    gonveyor.SplitWith(EncodeTrack, tracks, func(t Track, in *EncodeInput) {
        in.TrackID = t.ID
        in.Format  = t.Format
    }),
)
```

N is `len(tracks)`. Each instance is seeded with its own payload at manifest creation. If `EncodeTrack` also has `Intake` deps, their results are merged on top of the seed at dispatch time.

### Fan-in with `Merge`

When a downstream task needs to collect N outputs into a slice:

```go
blueprint.Wire(PublishRelease,
    gonveyor.Merge(EncodeTrack, func(outputs []EncodeOutput, in *PublishInput) {
        urls := make([]string, len(outputs))
        for i, o := range outputs { urls[i] = o.URL }
        in.TrackURLs = urls
    }),
)
```

Use `gonveyor.Intake` when a single upstream output maps to specific fields of this task's input.
Use `gonveyor.Merge` when you need **all outputs as a slice** (fan-in aggregation).

> **Note:** when multiple `Intake`/`Merge` deps contribute to the same input struct, each must write to disjoint fields.

### Seeding a task with `Seed`

Every root task (no deps) must be explicitly seeded at manifest time. `Seed` also works on any downstream task that needs ambient context that doesn't flow naturally through the dep graph — fields set by `Seed` are overlaid by `Intake`/`Merge` at dispatch time:

```go
manifest, _ := bp.Manifest(
    gonveyor.Seed(DownloadAsset, DownloadInput{AssetID: "asset-1"}),
    gonveyor.Seed(PackageBundle, PackageInput{ReleaseID: "rel-42"}),
)
```

`Manifest` returns an error if any root task is missing a `Seed`.

### Ordering with `After`

When a task must run after another but doesn't need its output:

```go
blueprint.Wire(Cleanup,
    gonveyor.After[CleanupInput](TranscodeVideo),
    gonveyor.After[CleanupInput](ExtractAudio),
)
```

`Cleanup` is dispatched only after both complete. No result is fetched from the store — the upstream DB read is skipped entirely.

### Gateway nodes with `Signal`

When a downstream task must wait for an external event (human approval, webhook, third-party callback):

```go
var AwaitApproval = blueprint.Signal[ApprovalPayload]("await_approval")

var PublishFlow = blueprint.New("publish_flow",
    PrepareRelease,
    AwaitApproval, // gateway — never dispatched to the queue
    blueprint.Wire(PublishRelease,
        gonveyor.After[PublishInput](PrepareRelease),
        gonveyor.Intake(AwaitApproval, func(p ApprovalPayload, in *PublishInput) {
            in.ApprovedBy = p.UserID
        }),
    ),
)
```

`Signal[T]` is a station with `node_type = "signal"`. It is never dispatched to AMQP — it acts as a held gate in the DAG. Downstream nodes remain blocked until `SendSignal` is called.

On the producer side:

```go
gc.SendSignal(ctx, blueprintID, "await_approval", ApprovalPayload{UserID: "alice"})
```

This atomically completes the signal node, decrements `pending_deps` on its successors, and dispatches those that unblock — exactly like completing a regular task.

---

## Worker side

```go
import (
    bunledger "github.com/terapps/gonveyor/ledger/bun"
    // ...
)

sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN("postgres://...")))
db := bun.NewDB(sqldb, pgdialect.New())

conn, _ := amqp.Dial("amqp://...")
queue, _ := amqp.NewQueue("gonveyor", amqp.WithDeadLetter("gonveyor.dlx"))
dispatcher, _ := conn.NewDispatcher(queue)
worker, _ := conn.NewWorker(queue)

g := gonveyor.NewGonveyor(bunledger.New(db), dispatcher, worker)

// Register blueprint wiring so the worker knows how to build task inputs
g.RegisterBlueprint(VideoProcessing)

g.RegisterHandler(TranscodeVideo, gonveyor.Handle(TranscodeVideo,
    func(ctx context.Context, in TranscodeInput) (TranscodeOutput, error) {
        url, err := transcoder.Run(ctx, in.AssetID)
        return TranscodeOutput{URL: url}, err
    },
))

g.Listen(ctx)
```

When a handler completes, gonveyor automatically:
1. Persists the result as a `node_completed` event
2. Resolves which tasks are now unblocked
3. Builds their typed input from upstream outputs
4. Dispatches them to the queue

**Race safety:** claiming a task atomically inserts a `node_started` event. If two workers race on the same task, only one wins the insert — the other bails silently. A heartbeat goroutine upserts a `node_heartbeats` row every 15s while a task is running; staleness is detectable via `last_seen_at` for external monitoring.

**Handler context:** handlers receive a non-cancellable context. Shutdown signals do not interrupt a running task — the worker waits for the handler to return. Do not rely on `ctx.Done()` inside handlers.

---

## Producer side

```go
import (
    bunledger "github.com/terapps/gonveyor/ledger/bun"
    // ...
)

sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN("postgres://...")))
db := bun.NewDB(sqldb, pgdialect.New())

conn, _ := amqp.Dial("amqp://...")
queue, _ := amqp.NewQueue("gonveyor", amqp.WithDeadLetter("gonveyor.dlx"))
dispatcher, _ := conn.NewDispatcher(queue)

gc := gonveyor.NewGonductor(bunledger.New(db), dispatcher)

manifest, _ := VideoProcessing.Manifest(
    gonveyor.Seed(DownloadAsset, DownloadInput{AssetID: "asset-42", SourceURL: "s3://..."}),
)

gc.Launch(ctx, manifest)
```

`Launch` persists the manifest and dispatches the initial tasks atomically.

---

## Configuration

### Queue

```go
amqp.NewQueue("gonveyor",
    amqp.WithDeadLetter("gonveyor.dlx"),             // recommended — without this, failed messages are dropped
    amqp.WithExchange("events", amqp.ExchangeTopic), // named exchange (Direct or Topic)
    amqp.WithRoutingKey("tasks.#"),                  // required for Topic exchanges
)
```

| Option | Default | Description |
|--------|---------|-------------|
| `WithDeadLetter(exchange)` | — | Routes nacked messages to this exchange. Without it, RabbitMQ drops them permanently. |
| `WithExchange(name, type)` | direct, unnamed | Named exchange — `ExchangeDirect` or `ExchangeTopic` |
| `WithRoutingKey(key)` | — | Binding key, required for `ExchangeTopic` |

### Worker

```go
conn.NewWorker(queue,
    amqp.WithPrefetch(10),   // tune for your task duration — default of 1 is too conservative for production
    amqp.WithConcurrency(4),
    amqp.WithTag("worker-1"),
    amqp.WithRequeueFn(func(err error) bool {
        return errors.Is(err, ErrTransient)
    }),
    amqp.WithShutdownFn(func(ctx context.Context) (context.Context, context.CancelFunc) {
        return signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
    }),
)
```

| Option | Default | Description |
|--------|---------|-------------|
| `WithPrefetch(n)` | `1` | Messages prefetched per consumer. Default is safe but low-throughput — use `10`–`50` in production for short tasks. |
| `WithConcurrency(n)` | `1` | Goroutines processing messages in parallel. Should match or be lower than prefetch. |
| `WithTag(tag)` | — | AMQP consumer tag |
| `WithRequeueFn(fn)` | always false | Returns true if a failed message should be requeued |
| `WithRetryFn(fn)` | exponential backoff ×5 | Factory producing the reconnection retry strategy |
| `WithShutdownFn(fn)` | `SIGTERM` | Returns a context cancelled on shutdown signals |

---

## Project layout

```
gonveyor/
├── blueprint/         # Typed DAG DSL — Define, Wire, Signal, New, Station
│   ├── blueprint.go   # Type definitions, Wire, Intake, Merge, After
│   └── manifest.go    # Manifest building, Seed, Split, SplitWith
├── ledger/            # Persistence interface (ledger.Ledger) + domain types
│   └── bun/           # PostgreSQL implementation (bun ORM)
│       ├── ledger.go  # Ledger struct — orchestrates repo calls, owns transactions
│       ├── blueprint/ # Blueprint insert
│       ├── node/      # Node queries (insert, deps, unblocked)
│       └── event/     # Append-only node_events writes + GatherDepResults
├── transport/         # Transport interfaces + AMQP implementation
│   └── amqp/          # AMQP worker and dispatcher
├── examples/
│   ├── factory/       # Worker process — registers blueprints and handlers
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

Five tables: `blueprints`, `blueprint_nodes`, `blueprint_node_dependencies`, `node_events`, `node_heartbeats`.

Node state is event-sourced — there is no mutable status column. Events are appended:

| Event type | When |
|------------|------|
| `node_dispatched` | node sent to the queue (root nodes at `CreateBlueprint`, downstream nodes when unblocked) |
| `node_started` | worker claimed the node (`Claim`) — unique, used as a distributed lock |
| `node_completed` | handler returned successfully — unique via partial index, guarantees idempotency |
| `node_failed` | handler returned an error |

Heartbeats are upserted separately in `node_heartbeats` (one row per node, `last_seen_at` updated on each tick).

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

Integration tests (`ledger/bun`) require PostgreSQL — start it first:

```bash
docker compose up -d
make test
```
