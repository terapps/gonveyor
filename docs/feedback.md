# Feedback


## NTH — Reaper : recovery automatique des blueprints gelés

Les trois cas (dual-write gap, worker crash, OnComplete crash) sont détectables en DB (`status`, `locked_until`) et via DLQ — alerting + UI suffisent pour une recovery manuelle. Le reaper automatise ça mais n'est pas critique.

Si implémenté : exposer un `Reaper.RunOnce(ctx)` dans la lib, scheduling laissé à l'utilisateur (CronJob K8s, cron système, ticker in-process).



## ✓ After — ordering pur sans data fetch

`gonveyor.After[I](station)` — dépendance d'ordering sans transfert de données. `GatherDepResults` est skippé entièrement si toutes les deps d'une station sont des `After`. Tests : `TestAfter_*` + `TestHandler_After_*`.

---

## Données globales au blueprint sans pass-through

Quand une donnée est connue à la création du manifest (ex: `GameVersionID`) et nécessaire dans plusieurs stations non-adjacentes, la seule option aujourd'hui est de la faire transiter par les outputs intermédiaires même si ces stations n'en ont pas l'usage :

```
  ListFiles  Cleanup      ← reçoivent GameVersionID (input root)
       │        │
       └───┬────┘
           ▼
   ImportResource         ← doit inclure GameVersionID dans son output
           │                 pour que les downstream puissent le récupérer
      output: { ..., GameVersionID }
           │
      ┌────┴────┐
      ▼         ▼
ImportResearch  ImportSaveFile
  Intake(ImportResource) → GameVersionID
```

`ImportResource` joue le rôle de relais pour une donnée qui ne lui appartient pas.

## ✓ SplitWith — fan-out runtime avec payload par instance

`Split(station, N)` requiert N connu à la création du manifest. Résolu par `SplitWith` : N est `len(items)`, chaque instance reçoit un payload distinct baked au manifest. Les `Intake`/`Merge` deps s'appliquent par-dessus au dispatch.

```go
files, _ := repo.ListFiles(ctx, gameVersionID)
gonveyor.SplitWith(ProcessFile, files, func(f FileRef, in *ProcessInput) {
    in.FileID = f.ID
})
```

## NTH — Signal blueprints gelés par tâche failed

Un dep `failed` bloque ses descendants en `pending` indéfiniment (voulu : permet le retry). Pas de signal actif aujourd'hui — détectable via `SELECT blueprint_id, COUNT(*) FROM blueprint_tasks WHERE status='failed' GROUP BY blueprint_id`, à exposer côté UI ou monitoring si besoin.
