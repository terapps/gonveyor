# Human Approval / Signals

## Use case

Un blueprint est créé et mis en attente d'une approbation manuelle avant d'être dispatché. Exemple : un programme Windows upload des fichiers de jeu vers un backend, un admin visualise le résultat dans une UI et approuve manuellement le lancement du job de traitement.

## Principe

La tâche en attente n'est **pas** envoyée sur AMQP immédiatement — elle reste en DB jusqu'à réception du signal. Aucun worker n'est bloqué.

Quand le signal arrive, il débloque la tâche et déclenche le dispatch vers AMQP. C'est un mécanisme purement **push/event-driven** — pas de scheduler, pas de polling.

## Flow

```
CreateBlueprint(manifest)
  → tâche racine créée, pending_deps = 0, non dispatchée

Admin approuve dans l'UI
  → POST /signals/{name}
  → ledger.SendSignal(ctx, signalName, payload)
      → INSERT signal event en DB
      → trouve les tâches en attente de ce signal
      → INSERT task_dispatched pour chacune          (même tx)
      → retourne les tâches débloquées
  → for each task: dispatcher.Publish(task)          (après commit)

Worker consomme
  → exécution normale
```

## Pattern identique à SetSuccess

`SendSignal` suit exactement le même pattern que `SetSuccess` :
- Tx DB atomique : event + dispatch des tâches débloquées
- Publish AMQP après commit, hors transaction

| Trigger | Débloque via |
|---|---|
| `SetSuccess` | `pending_deps = 0` |
| `SendSignal` | signal event correspondant |

## Ce que ce n'est pas

**Pas un durable timer.** Un timer ("dispatch dans 24h") nécessite un scheduler — un process qui réveille des tâches à un instant précis. C'est une feature distincte (voir roadmap : Sleep / Durable Timers).

Human approval = event-driven → pas de scheduler.
Sleep/timer = time-driven → scheduler nécessaire.

## Handler API

Le handler signal a besoin des deux :

```go
func (h *SignalHandler) Handle(ctx context.Context, name string, payload any) error {
    tasks, err := h.ledger.SendSignal(ctx, name, payload)
    if err != nil {
        return err
    }
    for _, t := range tasks {
        if err := h.dispatcher.Publish(ctx, t); err != nil {
            return err
        }
    }
    return nil
}
```

Même séparation que `OnComplete` : atomicité en DB, publish AMQP après commit.
