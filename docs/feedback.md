# Feedback


## NTH — Sweeper : recovery automatique des blueprints gelés

Les trois cas (dual-write gap, worker crash, OnComplete crash) sont détectables en DB (`status`, `locked_until`) et via DLQ — alerting + UI suffisent pour une recovery manuelle. Le sweeper automatise ça mais n'est pas critique.

Si implémenté : exposer un `Sweeper.RunOnce(ctx)` dans la lib, scheduling laissé à l'utilisateur (CronJob K8s, cron système, ticker in-process).


## NTH — Signal blueprints gelés par tâche failed

Un dep `failed` bloque ses descendants en `pending` indéfiniment (voulu : permet le retry). Pas de signal actif aujourd'hui — détectable via `SELECT blueprint_id, COUNT(*) FROM blueprint_tasks WHERE status='failed' GROUP BY blueprint_id`, à exposer côté UI ou monitoring si besoin.
