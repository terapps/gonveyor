# Plan : Event Sourcing — Gonveyor

## Context

On remplace le modèle pull (Next() polling + claim race) par un modèle push event-sourcé pur :
- `task_events` : log append-only, source de vérité. Porte `blueprint_id` + `key` → self-describing.
- `task_heartbeats` : liveness opérationnelle, retention courte.
- `pending_deps` sur `blueprint_tasks` : seule projection maintenue, pour le push dispatch.
- `blueprint_tasks` perd `status`, `result`, `error`, `locked_until` — devient un registre de tâches.
- Seul `task_completed` est sous contrainte unique — `task_dispatched`, `task_started`, `task_failed` peuvent se répéter (retries via `task_retried`).

## Ordre d'implémentation

**Phase 1 — Schema** (aucun impact Go)
**Phase 2 — Modèles + types** (additif)
**Phase 3 — Interface + callers** (break compilé en une seule passe)
**Phase 4 — Repository internals**
**Phase 5 — Tests**

---

## Phase 1 — Réécriture de `scripts/migrations/001_init.sql`

`blueprint_tasks` : uniquement `id`, `blueprint_id`, `key`, `pending_deps`, `payload`, timestamps. `blueprint_name` supprimé — obtenu via JOIN sur `blueprints`.
`task_events` : porte `blueprint_id` + `key` → self-describing, pas besoin de joindre `blueprint_tasks`.

```sql
CREATE TABLE blueprints (
    id            UUID        PRIMARY KEY,
    name          TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    dispatched_at TIMESTAMPTZ
);

CREATE TABLE blueprint_tasks (
    id           UUID        PRIMARY KEY,
    blueprint_id UUID        NOT NULL REFERENCES blueprints (id),
    key          TEXT        NOT NULL,
    pending_deps INT         NOT NULL DEFAULT 0,
    payload      JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE blueprint_task_dependencies (
    task_id       UUID NOT NULL REFERENCES blueprint_tasks (id),
    depends_on_id UUID NOT NULL REFERENCES blueprint_tasks (id),
    PRIMARY KEY (task_id, depends_on_id)
);

CREATE TABLE task_events (
    id           BIGSERIAL   PRIMARY KEY,
    task_id      UUID        NOT NULL REFERENCES blueprint_tasks (id),
    blueprint_id UUID        NOT NULL REFERENCES blueprints (id),
    key          TEXT        NOT NULL,
    type         TEXT        NOT NULL CHECK (type IN (
                     'task_dispatched','task_started','task_completed','task_failed','task_retried'
                 )),
    output       JSONB,      -- task_completed uniquement
    error        TEXT,       -- task_failed uniquement
    emitted_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE task_heartbeats (
    task_id    UUID        NOT NULL REFERENCES blueprint_tasks (id),
    emitted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Indexes
CREATE INDEX idx_task_deps_depends_on_id         ON blueprint_task_dependencies (depends_on_id);
CREATE INDEX idx_blueprint_tasks_pending_dispatch ON blueprint_tasks (blueprint_id, pending_deps);

-- Idempotency guard : une tâche ne complète qu'une fois dans sa vie
-- task_dispatched / task_started / task_failed peuvent se répéter (retries via task_retried)
CREATE UNIQUE INDEX idx_task_events_idempotent ON task_events (task_id, type)
    WHERE type = 'task_completed';

CREATE INDEX idx_task_events_task_id      ON task_events (task_id, type);
CREATE INDEX idx_task_events_blueprint_id ON task_events (blueprint_id);
CREATE INDEX idx_task_heartbeats_task_id  ON task_heartbeats (task_id);
CREATE INDEX idx_task_heartbeats_emitted  ON task_heartbeats (emitted_at);
```

---

## Phase 2 — Modèles `store/bun/task/model.go`

`Task` perd `Status`, `Result`, `Error`, `LockedUntil`. Gagne `PendingDeps` (interne au repo, pas exposé dans `store.Task`).

```go
type Task struct {
    bun.BaseModel `bun:"table:blueprint_tasks"`
    ID          string          `bun:"id,pk,type:uuid"`
    BlueprintID string          `bun:"blueprint_id,notnull"`
    Key         string          `bun:"key,notnull"`
    PendingDeps int             `bun:"pending_deps,notnull"`
    Payload     json.RawMessage `bun:"payload,type:jsonb"`
    CreatedAt   time.Time       `bun:"created_at,notnull"`
    UpdatedAt   time.Time       `bun:"updated_at,notnull"`
}

type EventType string

const (
    EventTaskDispatched EventType = "task_dispatched"
    EventTaskStarted    EventType = "task_started"
    EventTaskCompleted  EventType = "task_completed"
    EventTaskFailed     EventType = "task_failed"
    EventTaskRetried    EventType = "task_retried"
)

// STI pattern : Output et Error sont mutuellement exclusifs selon Type.
// Output peuplé sur task_completed, Error sur task_failed, les autres vides.
type TaskEvent struct {
    bun.BaseModel `bun:"table:task_events"`
    ID          int64           `bun:"id,pk,autoincrement"`
    TaskID      string          `bun:"task_id,notnull,type:uuid"`
    BlueprintID string          `bun:"blueprint_id,notnull,type:uuid"`
    Key         string          `bun:"key,notnull"`
    Type        EventType       `bun:"type,notnull"`
    Output      json.RawMessage `bun:"output,type:jsonb"`
    Error       string          `bun:"error"`
    EmittedAt   time.Time       `bun:"emitted_at,notnull"`
}

type TaskHeartbeat struct {
    bun.BaseModel `bun:"table:task_heartbeats"`
    TaskID    string    `bun:"task_id,notnull,type:uuid"`
    EmittedAt time.Time `bun:"emitted_at,notnull"`
}
```

---

## Phase 3 — Interface + callers (break en une passe)

### `store/store.go` → renommé `ledger/ledger.go`

- Package `store` → `ledger`, interface `Store` → `Ledger`
- Supprimer `TaskStatus` et ses constantes
- `store.Task` : supprimer `Status`, `Result`, `Error`
- `CreateBlueprint` : `error` → `([]Task, error)` — retourne les root tasks déjà dispatchées
- `SetSuccess` : `(bool, error)` → `(bool, []Task, error)`
- Supprimer : `Next`, `Pending`, `SetBlueprintDispatched`
- `SetDispatched`, `SetRunning`, `SetFailed` : signatures inchangées

Interface résultante :
```go
type Ledger interface {
    CreateBlueprint(ctx, manifest) ([]Task, error)
    GetBlueprint(ctx, blueprintID) (BlueprintManifest, error)
    ListBlueprints(ctx) ([]Blueprint, error)

    GetTask(ctx, taskID) (Task, error)
    SetDispatched(ctx, taskID) (bool, error)
    SetRunning(ctx, taskID) (bool, error)
    SetSuccess(ctx, taskID, result) (bool, []Task, error)
    SetFailed(ctx, taskID, err) error
    RenewLock(ctx, taskID) error

    GatherDepResults(ctx, taskID) (map[string][]json.RawMessage, error)
}
```

### `store/bun/store.go`

- `CreateBlueprint` : dans la même tx —
  1. calculer `PendingDeps` par tâche
  2. INSERT blueprint + tasks + dependencies
  3. INSERT `task_dispatched` events pour les root tasks (`PendingDeps = 0`)
  4. retourner les root tasks
- `SetSuccess` : nouvelle signature, délègue + convertit `[]buntask.Task` → `[]Task`
- Supprimer : `Next`, `Pending`, `SetBlueprintDispatched`
- `ToStore()` sur le model : supprimer Status, Result, Error

### `gonveyor.go` — `Launch` + `OnComplete`

```go
func (o *Gonveyor) Launch(ctx context.Context, manifest ledger.BlueprintManifest) error {
    tasks, err := o.ledger.CreateBlueprint(ctx, manifest)
    if err != nil { return err }
    for _, t := range tasks {
        if err := o.dispatcher.Publish(ctx, t); err != nil { return err }
    }
    return nil
}

func (o *Gonveyor) OnComplete(ctx context.Context, taskID string, result any) error {
    ok, tasks, err := o.ledger.SetSuccess(ctx, taskID, result)
    if err != nil { return err }
    if !ok { return nil }

    for _, t := range tasks {
        if bp, ok := o.blueprints[t.BlueprintName]; ok {
            if node := bp.Node(t.Key); node != nil {
                var outputs map[string][]json.RawMessage
                if node.NeedsDepData() {
                    outputs, err = o.ledger.GatherDepResults(ctx, t.ID)
                    if err != nil { return err }
                }
                t.Payload, err = node.BuildInput(t.Payload, outputs)
                if err != nil { return err }
            }
        }
        if err := o.dispatcher.Publish(ctx, t); err != nil { return err }
    }
    return nil
}
```

---

## Phase 4 — Repository `store/bun/task/repository.go`

### Principe des Set* sans status

Chaque transition = INSERT dans `task_events` avec `blueprint_id` et `key` récupérés depuis `blueprint_tasks` dans le même statement :

```sql
INSERT INTO task_events (task_id, blueprint_id, key, type)
SELECT id, blueprint_id, key, 'task_started'
FROM blueprint_tasks WHERE id = $1
```

### `SetDispatched`

```sql
INSERT INTO task_events (task_id, blueprint_id, key, type)
SELECT id, blueprint_id, key, 'task_dispatched'
FROM blueprint_tasks WHERE id = $1
```
Retourne `(true, nil)` — plusieurs dispatches possibles (retries).

### `SetRunning`

Identique avec `'task_started'`. Le guard contre la double-exécution est assuré par AMQP (single-consumer par message) + `task_completed` unique en fin de chaîne.

### `SetFailed`

```sql
INSERT INTO task_events (task_id, blueprint_id, key, type, error)
SELECT id, blueprint_id, key, 'task_failed', $2
FROM blueprint_tasks WHERE id = $1
```
`$2` = `err.Error()`. Plusieurs `task_failed` possibles sur retry.

### `SetSuccess` — transaction atomique (pièce centrale)

```
BEGIN

1. INSERT INTO task_events (task_id, blueprint_id, key, type, output)
   SELECT id, blueprint_id, key, 'task_completed', $2
   FROM blueprint_tasks WHERE id = $1
   ON CONFLICT (task_id, type) WHERE type = 'task_completed' DO NOTHING
   → si 0 rows : ROLLBACK, return (false, nil, nil)  -- relivraison AMQP

2. UPDATE blueprint_tasks
   SET pending_deps = pending_deps - 1, updated_at = now()
   WHERE id IN (SELECT task_id FROM blueprint_task_dependencies WHERE depends_on_id = $1)

3. SELECT t.*, b.name AS blueprint_name FROM blueprint_tasks t
   JOIN blueprints b ON b.id = t.blueprint_id
   WHERE t.id IN (SELECT task_id FROM blueprint_task_dependencies WHERE depends_on_id = $1)
     AND t.pending_deps = 0
     AND NOT EXISTS (
       SELECT 1 FROM task_events
       WHERE task_id = t.id
         AND type IN ('task_dispatched', 'task_completed')
     )
   FOR UPDATE OF t
   ← FOR UPDATE : guard contre double-dispatch si deux deps complètent en parallèle

4. Si résultats non vides :
   INSERT INTO task_events (task_id, blueprint_id, key, type)
   SELECT id, blueprint_id, key, 'task_dispatched'
   FROM blueprint_tasks WHERE id = ANY($unblocked_ids)

COMMIT
→ return (true, unblockedTasks, nil)
```

Toutes les opérations via `bunutil.IDB(ctx, r.db)`.

### `GatherDepResults`

```sql
SELECT e.key, e.output
FROM blueprint_task_dependencies d
JOIN task_events e ON e.task_id = d.depends_on_id AND e.type = 'task_completed'
WHERE d.task_id = $1
```

Pas de JOIN sur `blueprint_tasks`. Le résultat vient de `task_events.output`.

### `RenewLock`

```go
func (r *Repository) RenewLock(ctx context.Context, taskID string) error {
    _, err := r.db.NewInsert().Model(&TaskHeartbeat{TaskID: taskID}).Exec(ctx)
    return err
}
```

### Supprimer : `Next`

---

## Phase 5 — Tests `gonveyor_test.go`

**`mockStore`** :
- `setSuccess func(...) (bool, []store.Task, error)` — nouvelle signature
- Supprimer `next`, `setDispatched` (dispatch fait dans SetSuccess)

**Tests à mettre à jour** :
- `TestHandler_Success_PublishesNextTask` : `setSuccess` retourne `(true, []store.Task{nextTask}, nil)`. Supprimer `next` et `setDispatched`.
- `TestOnComplete_SetDispatched_False_SkipsPublish` → `TestOnComplete_NoUnblockedTasks_NothingPublished` : `setSuccess` retourne `(true, nil, nil)`.
- `TestHandler_Race_OnlyOneWins` : `setSuccess` retourne `(true, nil, nil)`. Supprimer `next`.
- `TestHandler_After*` et `TestHandler_AfterAndIntake*` : tasks viennent de `setSuccess`.

**Nouveaux tests** :
- `TestOnComplete_MultipleUnblockedTasks_AllPublished`
- `TestOnComplete_SetSuccess_Error_Propagates`

---

## Vérification

```bash
go build ./...
go test ./...
```

Tester avec l'example `factory` : soumettre un blueprint, vérifier que `task_events` se peuple avec `blueprint_id` + `key`, que `pending_deps` décrémente, que `blueprint_tasks` ne porte plus de statut.

---

## Cycle de vie complet (avec retry)

```
task_dispatched → task_started → task_failed → task_retried → task_dispatched → task_started → task_completed
```
