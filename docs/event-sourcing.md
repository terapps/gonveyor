# Event Sourcing

## Tables

### `task_events` — source de vérité, append-only, retention permanente

Events : `task_dispatched`, `task_started`, `task_completed`, `task_failed`, `task_retried`

### `task_heartbeats` — liveness opérationnel, purge courte

```sql
CREATE TABLE task_heartbeats (
  task_id    TEXT NOT NULL,
  emitted_at TIMESTAMPTZ NOT NULL DEFAULT now()
)
```

### `blueprint_tasks` — projection de lecture

Deux colonnes clés :

- `pending_deps INT` — décrémenté à chaque `task_completed` d'une dep, rebuilable depuis le graphe + events si dérive
- `status` — rôle réduit au locking mid-execution (`WHERE status = dispatched`, `WHERE status = running`)

---

## Dispatch — push, plus de polling

Quand `pending_deps` tombe à 0 dans la même tx que `task_completed`, le worker dispatche directement le successeur. Plus de `Next()` polling, plus de race sur le claim — la transition `pending_deps 1 → 0` est atomique dans Postgres et appartient à exactement un worker.

---

## Cycle de vie d'une tâche

```
pending_deps = 0
  → task_dispatched event + dispatch AMQP          (même tx)
  → worker consomme
  → task_started event
  → task_alive toutes les 15s dans task_heartbeats  
  → task_completed event
      + décrémente pending_deps des successeurs     (même tx)
      + dispatche les successeurs à pending_deps = 0
    OU task_failed event
```

---

## Sweeper

Cherche les tâches avec `task_started` sans `task_heartbeat` récent et sans `task_completed`/`task_failed` → reclaim.

---

## ACK AMQP — persist first, ack after

AMQP et Postgres sont deux systèmes distincts — impossible de les inclure dans la même transaction. L'ordre est critique :

```
1. reçoit message AMQP
2. INSERT event → COMMIT Postgres
3. ACK AMQP
```

Si crash entre 2 et 3 : AMQP redistribue le message. Le worker essaie d'insérer le même event → contrainte unique → bail. Correct.

L'ordre inverse (ACK avant persist) est dangereux : si le process crash après l'ACK, le message est perdu définitivement — aucun sweeper ne récupère la tâche.

La contrainte unique sur `task_events` transforme la garantie at-least-once d'AMQP en effectively-once :

```sql
CREATE UNIQUE INDEX ON task_events (task_id, type)
WHERE type IN ('task_started', 'task_completed', 'task_failed');
```

---

## Invariants

- `task_events` est la source de vérité — tout état est reconstructible depuis le log
- `pending_deps` est une projection opérationnelle maintenue application-level dans la même tx que l'event (pas de trigger PG — l'interface `Store` doit rester implémentable par d'autres backends)
- `task_heartbeats` est purgeable — sa valeur expire après 30s, elle ne fait pas partie du domaine
- Pas de controller central — Postgres coordonne via les transitions atomiques, les workers drivent le dispatch
