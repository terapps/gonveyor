package bun

import (
	"context"
	"encoding/json"
	"time"

	"github.com/terapps/gonveyor/ledger"
	bunblueprint "github.com/terapps/gonveyor/store/bun/blueprint"
	"github.com/terapps/gonveyor/store/bun/bunutil"
	buntask "github.com/terapps/gonveyor/store/bun/task"
	"github.com/uptrace/bun"
)

var _ ledger.Ledger = (*Store)(nil)

type Store struct {
	db            *bun.DB
	blueprintRepo *bunblueprint.Repository
	taskRepo      *buntask.Repository
}

func New(db *bun.DB) *Store {
	return &Store{
		db:            db,
		blueprintRepo: bunblueprint.New(db),
		taskRepo:      buntask.New(db),
	}
}

// CreateBlueprint atomically persists the manifest, records task_dispatched
// events for root tasks, and returns those root tasks for publication.
func (s *Store) CreateBlueprint(ctx context.Context, manifest ledger.BlueprintManifest) ([]ledger.Task, error) {
	// Compute pending_deps per task from the dependency list.
	depCount := make(map[string]int, len(manifest.Tasks))
	for _, d := range manifest.Dependencies {
		depCount[d.TaskID]++
	}

	var rootTasks []ledger.Task

	err := bunutil.RunInTx(ctx, s.db, func(ctx context.Context) error {
		now := time.Now()
		bp := &bunblueprint.Blueprint{
			ID:           manifest.Blueprint.ID,
			Name:         manifest.Blueprint.Name,
			DispatchedAt: &now,
		}
		if err := s.blueprintRepo.Insert(ctx, bp); err != nil {
			return err
		}

		tasks := make([]*buntask.Task, len(manifest.Tasks))
		for i, t := range manifest.Tasks {
			tasks[i] = &buntask.Task{
				ID:          t.ID,
				BlueprintID: t.BlueprintID,
				Key:         t.Key,
				PendingDeps: depCount[t.ID],
				Payload:     t.Payload,
			}
		}
		if err := s.taskRepo.Insert(ctx, tasks); err != nil {
			return err
		}

		deps := make([]*buntask.Dependency, len(manifest.Dependencies))
		for i, d := range manifest.Dependencies {
			deps[i] = &buntask.Dependency{
				TaskID:      d.TaskID,
				DependsOnID: d.DependsOnID,
			}
		}
		if err := s.taskRepo.InsertDependencies(ctx, deps); err != nil {
			return err
		}

		// Dispatch root tasks (pending_deps == 0) atomically.
		rootEvents := make([]*buntask.TaskEvent, 0)
		for _, t := range manifest.Tasks {
			if depCount[t.ID] == 0 {
				rootEvents = append(rootEvents, &buntask.TaskEvent{
					TaskID:      t.ID,
					BlueprintID: t.BlueprintID,
					Key:         t.Key,
					Type:        buntask.EventTaskDispatched,
				})
				rootTasks = append(rootTasks, t)
			}
		}
		return s.taskRepo.InsertEvents(ctx, rootEvents)
	})
	if err != nil {
		return nil, err
	}

	return rootTasks, nil
}

func (s *Store) GetBlueprint(ctx context.Context, blueprintID string) (ledger.BlueprintManifest, error) {
	bp, err := s.blueprintRepo.Get(ctx, blueprintID)
	if err != nil {
		return ledger.BlueprintManifest{}, err
	}

	tasks, err := s.taskRepo.AllByBlueprint(ctx, blueprintID)
	if err != nil {
		return ledger.BlueprintManifest{}, err
	}

	deps, err := s.taskRepo.DepsByBlueprint(ctx, blueprintID)
	if err != nil {
		return ledger.BlueprintManifest{}, err
	}

	return ledger.BlueprintManifest{
		Blueprint: ledger.Blueprint{
			ID:           bp.ID,
			Name:         bp.Name,
			CreatedAt:    bp.CreatedAt,
			UpdatedAt:    bp.UpdatedAt,
			DispatchedAt: bp.DispatchedAt,
		},
		Tasks:        tasks,
		Dependencies: deps,
	}, nil
}

func (s *Store) ListBlueprints(ctx context.Context) ([]ledger.Blueprint, error) {
	bps, err := s.blueprintRepo.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ledger.Blueprint, len(bps))
	for i, bp := range bps {
		out[i] = ledger.Blueprint{
			ID:           bp.ID,
			Name:         bp.Name,
			CreatedAt:    bp.CreatedAt,
			UpdatedAt:    bp.UpdatedAt,
			DispatchedAt: bp.DispatchedAt,
		}
	}
	return out, nil
}

func (s *Store) GetTask(ctx context.Context, taskID string) (ledger.Task, error) {
	return s.taskRepo.Get(ctx, taskID)
}

func (s *Store) SetDispatched(ctx context.Context, taskID string) (bool, error) {
	return s.taskRepo.SetDispatched(ctx, taskID)
}

func (s *Store) SetRunning(ctx context.Context, taskID string) (bool, error) {
	return s.taskRepo.SetRunning(ctx, taskID)
}

func (s *Store) RenewLock(ctx context.Context, taskID string) error {
	return s.taskRepo.RenewLock(ctx, taskID)
}

func (s *Store) SetSuccess(ctx context.Context, taskID string, result any) (bool, []ledger.Task, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return false, nil, err
	}
	return s.taskRepo.SetSuccess(ctx, taskID, raw)
}

func (s *Store) SetFailed(ctx context.Context, taskID string, taskErr error) error {
	return s.taskRepo.SetFailed(ctx, taskID, taskErr.Error())
}

func (s *Store) GatherDepResults(ctx context.Context, taskID string) (map[string][]json.RawMessage, error) {
	return s.taskRepo.GatherDepResults(ctx, taskID)
}
