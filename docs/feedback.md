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

## `DefineMany` — fan-out runtime depuis une station

`Split(station, N)` requiert de connaître N à la création du manifest. Problème : quand N n'est connu qu'à runtime (ex: nombre de fichiers attachés à une game version), il faut faire une requête DB avant de créer le manifest et il n'existe pas de mécanisme pour que chaque instance sache quel item elle traite.

Proposition : `DefineMany[I, O]` — station dont le handler retourne `[]O`. Gonveyor persiste N résultats en une seule exécution. Les downstream utilisent `Merge` inchangé.

```go
var StationListFiles = blueprint.DefineMany[GameVersionInput, FileRef]("list_files")

// handler
func(ctx, in GameVersionInput) ([]FileRef, error) { return repo.ListFiles(ctx, in.GameVersionID) }

// downstream
gonveyor.Merge(StationListFiles, func(files []FileRef, in *ResearchInput) {
    // filtre par rôle
})
```

Manifest reste simple, pas de `Split(N)` à l'appel. `Merge` collecte les N outputs exactement comme pour un split statique.

## NTH — Signal blueprints gelés par tâche failed

Un dep `failed` bloque ses descendants en `pending` indéfiniment (voulu : permet le retry). Pas de signal actif aujourd'hui — détectable via `SELECT blueprint_id, COUNT(*) FROM blueprint_tasks WHERE status='failed' GROUP BY blueprint_id`, à exposer côté UI ou monitoring si besoin.
