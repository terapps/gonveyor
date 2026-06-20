package blueprint

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/terapps/gonveyor/store"
)

// ManifestOption configures how Manifest builds its task graph.
type ManifestOption interface {
	applyManifest(cfg *manifestCfg)
}

// Seed sets the base payload for a task at manifest time.
// Fields set by Seed are overlaid by Intake/Merge deps at dispatch time,
// so Seed and dep-based injection can coexist on the same task.
func Seed[I, O any](def *Station[I, O], input I) ManifestOption {
	raw, _ := json.Marshal(input)
	return seedOpt{key: def.Key(), payload: raw}
}

type seedOpt struct {
	key     string
	payload json.RawMessage
}

func (s seedOpt) applyManifest(cfg *manifestCfg) {
	cfg.payloads[s.key] = []json.RawMessage{s.payload}
}

// Split creates n parallel instances of def in the manifest.
func Split(def AnyDef, n int) ManifestOption {
	return splitOpt{key: def.Key(), n: n}
}

// Manifest builds a store.BlueprintManifest from the blueprint.
// Use Seed to assign payloads to tasks, Split/SplitWith for fan-out.
func (b *Blueprint) Manifest(opts ...ManifestOption) (store.BlueprintManifest, error) {
	cfg := &manifestCfg{splits: make(map[string]int), payloads: make(map[string][]json.RawMessage)}
	for _, opt := range opts {
		opt.applyManifest(cfg)
	}

	for _, def := range b.tasks {
		if len(def.depList()) == 0 && cfg.payloads[def.Key()] == nil {
			return store.BlueprintManifest{}, fmt.Errorf("task %q has no dependencies and no Seed — use Seed() to provide its initial payload", def.Key())
		}
	}

	blueprintID := uuid.NewString()

	taskIDs := make(map[string][]string, len(b.tasks))
	for _, def := range b.tasks {
		count := max(cfg.splits[def.Key()], 1)

		ids := make([]string, count)
		for i := range count {
			ids[i] = uuid.NewString()
		}

		taskIDs[def.Key()] = ids
	}

	tasks := make([]store.Task, 0)
	deps := make([]store.TaskDependency, 0)

	for _, def := range b.tasks {
		myIDs := taskIDs[def.Key()]

		for i, id := range myIDs {
			taskPayload := json.RawMessage(nil)
			if perInstance := cfg.payloads[def.Key()]; perInstance != nil {
				taskPayload = perInstance[i]
			}

			tasks = append(tasks, store.Task{
				ID:            id,
				BlueprintID:   blueprintID,
				BlueprintName: b.name,
				Key:           def.Key(),
				Status:        store.TaskStatusPending,
				Payload:       taskPayload,
			})
		}

		for _, d := range def.depList() {
			depIDs := taskIDs[d.depKey()]
			switch {
			case len(myIDs) == len(depIDs):
				// paired: each instance depends on the same-index upstream
				for i, myID := range myIDs {
					deps = append(deps, store.TaskDependency{TaskID: myID, DependsOnID: depIDs[i]})
				}
			case len(depIDs) == 1:
				// broadcast: each of N instances depends on the single upstream
				for _, myID := range myIDs {
					deps = append(deps, store.TaskDependency{TaskID: myID, DependsOnID: depIDs[0]})
				}
			default:
				// gather: my instance(s) depend on all upstream instances
				for _, myID := range myIDs {
					for _, depID := range depIDs {
						deps = append(deps, store.TaskDependency{TaskID: myID, DependsOnID: depID})
					}
				}
			}
		}
	}

	return store.BlueprintManifest{
		Blueprint:    store.Blueprint{ID: blueprintID, Name: b.name},
		Tasks:        tasks,
		Dependencies: deps,
	}, nil
}

type manifestCfg struct {
	splits   map[string]int
	payloads map[string][]json.RawMessage
}

type splitOpt struct {
	key string
	n   int
}

func (s splitOpt) applyManifest(cfg *manifestCfg) {
	cfg.splits[s.key] = s.n
}

type splitWithOpt struct {
	key      string
	payloads []json.RawMessage
}

func (s splitWithOpt) applyManifest(cfg *manifestCfg) {
	cfg.splits[s.key] = len(s.payloads)
	cfg.payloads[s.key] = s.payloads
}

// SplitWith creates N parallel instances of def, each seeded with a per-item payload.
// The mapping fn receives one item from items and a pointer to the task input to fill.
// Unlike Split, N is len(items) and each instance starts with distinct payload data.
func SplitWith[I, O any, T any](def *Station[I, O], items []T, fn func(T, *I)) ManifestOption {
	payloads := make([]json.RawMessage, len(items))
	for i, item := range items {
		var input I
		fn(item, &input)
		raw, _ := json.Marshal(input)
		payloads[i] = raw
	}
	return splitWithOpt{key: def.Key(), payloads: payloads}
}
