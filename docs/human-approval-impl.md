# Human Approval / Signals — Implémentation

## Ce qui a été implémenté

Le pattern Signal décrit dans `human-approval.md` est maintenant opérationnel.

## Nouveaux types

### `blueprint.Signal[T](key string) *Station[struct{}, T]`

Crée une station gateway dans le DAG. Elle n'est jamais dispatchée sur AMQP — elle est activée par `SendSignal`. Les stations qui en dépendent via `After` ou `Intake` restent bloquées (`pending_deps > 0`) jusqu'à l'arrivée du signal.

```go
var AwaitApproval = blueprint.Signal[ApprovalPayload]("await_approval")

var Process = blueprint.Define[ProcessInput, ProcessOutput]("process")

var bp = blueprint.New("upload_pipeline",
    AwaitApproval,
    blueprint.Wire(Process, blueprint.Intake(AwaitApproval, func(p ApprovalPayload, in *ProcessInput) {
        in.ApprovedBy = p.UserID
    })),
)
```

### `Gonductor.SendSignal(ctx, blueprintID, signalKey string, payload any) error`

Complète le signal node et dispatche ses successeurs débloqués. Même séparation que `OnComplete` : atomicité en DB, publish AMQP après commit.

```go
err := gonductor.SendSignal(ctx, blueprintID, "await_approval", ApprovalPayload{
    UserID: "admin-42",
})
```

## Schéma

`blueprint_nodes` (ex `blueprint_tasks`) a une nouvelle colonne :

```sql
node_type TEXT NOT NULL DEFAULT 'task' CHECK (node_type IN ('task', 'signal'))
```

Les signal nodes ont `node_type = 'signal'`. Ils ne reçoivent jamais d'event `task_dispatched` à la création du blueprint — uniquement un `task_completed` via `SendSignal`.

## Flow complet

```
blueprint.New(...)  →  Manifest()
  → signal node créé avec node_type='signal', pending_deps=0
  → nodes successeurs créés avec pending_deps=1

Gonductor.Launch(ctx, manifest)
  → CreateBlueprint : signal nodes exclus du dispatch initial
  → aucun worker ne reçoit le signal node

[... attente d'un événement externe ...]

Gonductor.SendSignal(ctx, blueprintID, "await_approval", payload)
  → ledger.SendSignal : trouve le signal node par (blueprint_id, node_type='signal', key)
  → SetSuccess(signalNodeID, payload)
      → INSERT task_completed WITH output = payload JSON
      → pending_deps des successeurs décrémenté
      → successeurs à 0 → INSERT task_dispatched + retournés
  → dispatcher.Publish pour chaque successeur débloqué

Worker consomme les successeurs
  → GatherDepResults → récupère task_completed.output du signal node
  → BuildInput → Intake callback → champ injecté dans l'input
  → exécution normale
```

## Payload du signal

Le payload est stocké dans `task_completed.output` du signal node. Les successeurs qui font `Intake(signalStation, fn)` le reçoivent automatiquement via `GatherDepResults` → `BuildInput` — aucune mécanique supplémentaire.

## Ce qui n'est pas implémenté

- **Durable timers** : `SendSignal` est event-driven, pas time-driven. Un timer (`"dispatch dans 24h"`) nécessite un scheduler séparé — voir roadmap Sleep/Durable Timers.
- **Signal par instance** : un signal débloque tous les nodes de type signal avec ce key dans le blueprint. Si le même blueprint tourne plusieurs fois en parallèle, chaque instance a son propre `blueprint_id` → pas de collision.
