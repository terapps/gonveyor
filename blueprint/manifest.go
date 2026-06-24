package blueprint

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/terapps/gonveyor/ledger"
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

// Fan creates n parallel instances of def in the manifest.
func Fan(def AnyDef, n int) ManifestOption {
	return splitOpt{key: def.Key(), n: n}
}

// Manifest builds a ledger.BlueprintManifest from the blueprint.
// Use Seed/Seeds to assign payloads, Fan for pure fan-out.
func (b *Blueprint) Manifest(opts ...ManifestOption) (ledger.BlueprintManifest, error) {
	cfg := &manifestCfg{splits: make(map[string]int), payloads: make(map[string][]json.RawMessage)}
	for _, opt := range opts {
		opt.applyManifest(cfg)
	}

	for _, def := range b.tasks {
		if def.NodeType() == "signal" {
			continue // signal units receive their payload via SendSignal, not Seed
		}
		if len(def.depList()) == 0 && cfg.payloads[def.Key()] == nil {
			return ledger.BlueprintManifest{}, fmt.Errorf("task %q has no dependencies and no Seed — use Seed() to provide its initial payload", def.Key())
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

	units := make([]ledger.Unit, 0)
	deps := make([]ledger.UnitDependency, 0)

	for _, def := range b.tasks {
		myIDs := taskIDs[def.Key()]

		for i, id := range myIDs {
			taskPayload := json.RawMessage(nil)
			if perInstance := cfg.payloads[def.Key()]; perInstance != nil {
				taskPayload = perInstance[i]
			}

			units = append(units, ledger.Unit{
				ID:            id,
				BlueprintID:   blueprintID,
				BlueprintName: b.name,
				Key:           def.Key(),
				UnitType:      def.NodeType(),
				Payload:       taskPayload,
			})
		}

		for _, d := range def.depList() {
			depIDs := taskIDs[d.depKey()]
			switch {
			case len(myIDs) == len(depIDs):
				for i, myID := range myIDs {
					deps = append(deps, ledger.UnitDependency{UnitID: myID, DependsOnID: depIDs[i]})
				}
			case len(depIDs) == 1:
				for _, myID := range myIDs {
					deps = append(deps, ledger.UnitDependency{UnitID: myID, DependsOnID: depIDs[0]})
				}
			default:
				for _, myID := range myIDs {
					for _, depID := range depIDs {
						deps = append(deps, ledger.UnitDependency{UnitID: myID, DependsOnID: depID})
					}
				}
			}
		}
	}

	return ledger.BlueprintManifest{
		Blueprint:        ledger.Blueprint{ID: blueprintID, Name: b.name},
		Units:            units,
		UnitDependencies: deps,
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

// Seeds creates N parallel instances of def, each seeded with a per-item payload.
func Seeds[I, O any, T any](def *Station[I, O], items []T, fn func(T, *I)) ManifestOption {
	payloads := make([]json.RawMessage, len(items))
	for i, item := range items {
		var input I
		fn(item, &input)
		raw, _ := json.Marshal(input)
		payloads[i] = raw
	}
	return splitWithOpt{key: def.Key(), payloads: payloads}
}
