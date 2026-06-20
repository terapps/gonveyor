package bun

import (
	"context"
	"encoding/json"

	"github.com/terapps/gonveyor/store"
	bunblueprint "github.com/terapps/gonveyor/store/bun/blueprint"
	"github.com/terapps/gonveyor/store/bun/bunutil"
	buntask "github.com/terapps/gonveyor/store/bun/task"
	"github.com/uptrace/bun"
)

var _ store.Store = (*Store)(nil)

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

func (s *Store) CreateBlueprint(ctx context.Context, manifest store.BlueprintManifest) error {
	return bunutil.RunInTx(ctx, s.db, func(ctx context.Context) error {
		bp := &bunblueprint.Blueprint{
			ID:   manifest.Blueprint.ID,
			Name: manifest.Blueprint.Name,
		}
		if err := s.blueprintRepo.Insert(ctx, bp); err != nil {
			return err
		}

		tasks := make([]*buntask.Task, len(manifest.Tasks))
		for i, t := range manifest.Tasks {
			tasks[i] = &buntask.Task{
				ID:            t.ID,
				BlueprintID:   t.BlueprintID,
				BlueprintName: t.BlueprintName,
				Key:           t.Key,
				Status:        t.Status,
				Payload:       t.Payload,
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
		return s.taskRepo.InsertDependencies(ctx, deps)
	})
}

func (s *Store) GetBlueprint(ctx context.Context, blueprintID string) (store.BlueprintManifest, error) {
	bp, err := s.blueprintRepo.Get(ctx, blueprintID)
	if err != nil {
		return store.BlueprintManifest{}, err
	}

	tasks, err := s.taskRepo.AllByBlueprint(ctx, blueprintID)
	if err != nil {
		return store.BlueprintManifest{}, err
	}

	deps, err := s.taskRepo.DepsByBlueprint(ctx, blueprintID)
	if err != nil {
		return store.BlueprintManifest{}, err
	}

	return store.BlueprintManifest{
		Blueprint:    store.Blueprint{ID: bp.ID, Name: bp.Name, CreatedAt: bp.CreatedAt, UpdatedAt: bp.UpdatedAt, DispatchedAt: bp.DispatchedAt},
		Tasks:        tasks,
		Dependencies: deps,
	}, nil
}

func (s *Store) SetBlueprintDispatched(ctx context.Context, blueprintID string) error {
	return s.blueprintRepo.SetDispatched(ctx, blueprintID)
}

func (s *Store) ListBlueprints(ctx context.Context) ([]store.Blueprint, error) {
	bps, err := s.blueprintRepo.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]store.Blueprint, len(bps))
	for i, bp := range bps {
		out[i] = store.Blueprint{ID: bp.ID, Name: bp.Name, CreatedAt: bp.CreatedAt, UpdatedAt: bp.UpdatedAt, DispatchedAt: bp.DispatchedAt}
	}
	return out, nil
}

func (s *Store) GetTask(ctx context.Context, taskID string) (store.Task, error) {
	m, err := s.taskRepo.Get(ctx, taskID)
	if err != nil {
		return store.Task{}, err
	}
	return m.ToStore(), nil
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

func (s *Store) SetSuccess(ctx context.Context, taskID string, result any) (bool, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return false, err
	}
	return s.taskRepo.SetSuccess(ctx, taskID, raw)
}

func (s *Store) SetFailed(ctx context.Context, taskID string, taskErr error) error {
	return s.taskRepo.SetFailed(ctx, taskID, taskErr.Error())
}

func (s *Store) Pending(ctx context.Context, blueprintID string) ([]store.Task, error) {
	return s.taskRepo.Pending(ctx, blueprintID)
}

func (s *Store) Next(ctx context.Context, completedTaskID string) ([]store.Task, error) {
	return s.taskRepo.Next(ctx, completedTaskID)
}

func (s *Store) GatherDepResults(ctx context.Context, taskID string) (map[string][]json.RawMessage, error) {
	return s.taskRepo.GatherDepResults(ctx, taskID)
}
