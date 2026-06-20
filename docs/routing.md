# Routing

## Scénario 1 — Direct exchange, queue unique (défaut)

Un exchange direct, une queue, tous les workers consomment la même queue.

```
Dispatcher → [gonveyor] → Worker (tous les handlers enregistrés)
```

Configuration :

```go
queue, _ := amqp.NewQueue("gonveyor", amqp.WithDeadLetter("gonveyor.dlx"))
```

Simple, zéro config supplémentaire. Les tâches sont mélangées. On scale en ajoutant des workers qui consomment la même queue.

---

## Scénario 2 — Topic exchange, queues par type de tâche

Un exchange topic route les messages selon la routing key. Chaque type de tâche a sa propre queue et son propre pool de workers.

```
Dispatcher → exchange "gonveyor" (topic)
                ├── tasks.email    → [queue email]    → Worker email
                ├── tasks.document → [queue document] → Worker document
                └── tasks.#        → [queue default]  → Worker default
```

Configuration côté dispatcher :

```go
queue, _ := amqp.NewQueue("gonveyor",
    amqp.WithExchange("gonveyor", amqp.ExchangeTopic),
    amqp.WithRoutingKey("tasks.#"),
)
```

Configuration côté workers :

```go
// worker email
queue, _ := amqp.NewQueue("gonveyor.email",
    amqp.WithExchange("gonveyor", amqp.ExchangeTopic),
    amqp.WithRoutingKey("tasks.email"),
)

// worker document
queue, _ := amqp.NewQueue("gonveyor.document",
    amqp.WithExchange("gonveyor", amqp.ExchangeTopic),
    amqp.WithRoutingKey("tasks.document"),
)
```

Chaque pool scale indépendamment. Les workers email ne voient jamais les tâches document.

---

## Scénario 3 — Direct exchange nommé

Comme le scénario 2 mais sans patterns — chaque routing key doit matcher exactement. Utile si tu veux des queues dédiées sans la flexibilité du wildcard.

```go
queue, _ := amqp.NewQueue("gonveyor.email",
    amqp.WithExchange("gonveyor", amqp.ExchangeDirect),
    amqp.WithRoutingKey("tasks.email"),
)
```

---

## Support dans gonveyor

Aujourd'hui `Publish` envoie toutes les tâches sur la routing key configurée sur la queue — scénario 1 et 3 fonctionnent nativement.

Le scénario 2 (routing par station) est prévu : la routing key serait définie sur la station en Go, résolue au moment du `Publish` depuis un registre `station_key → routing_key` construit lors du `RegisterBlueprint` — sans changement de schema. Voir roadmap.
