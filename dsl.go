package gonveyor

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/terapps/gonveyor/blueprint"
	"github.com/terapps/gonveyor/store"
	"github.com/terapps/gonveyor/transport"
)

// Handle wraps a typed handler function into a HandlerFunc.
func Handle[I, O any](_ *blueprint.Station[I, O], fn func(context.Context, I) (O, error)) transport.HandlerFunc {
	return func(ctx context.Context, task store.Task) (any, error) {
		var input I
		if err := json.Unmarshal(task.Payload, &input); err != nil {
			return nil, fmt.Errorf("unmarshal input: %w", err)
		}

		return fn(ctx, input)
	}
}

// Intake declares a dependency on another task's output, mutating this task's input.
func Intake[DepI, DepO, I any](from *blueprint.Station[DepI, DepO], fn func(DepO, *I)) blueprint.DepOption[I] {
	return blueprint.Intake(from, fn)
}

// Merge declares a fan-in dependency that receives all outputs from a split task as a slice.
func Merge[DepI, DepO, I any](from *blueprint.Station[DepI, DepO], fn func([]DepO, *I)) blueprint.DepOption[I] {
	return blueprint.Merge(from, fn)
}

// After declares a pure ordering dependency: the upstream task must complete before this one
// is dispatched, but its output is not fetched from the store.
func After[I any](from blueprint.AnyDef) blueprint.DepOption[I] {
	return blueprint.After[I](from)
}

// Split creates n parallel instances of def in the manifest.
func Split(def blueprint.AnyDef, n int) blueprint.ManifestOption {
	return blueprint.Split(def, n)
}

// SplitWith creates N parallel instances of def, each seeded with a per-item payload.
func SplitWith[I, O any, T any](def *blueprint.Station[I, O], items []T, fn func(T, *I)) blueprint.ManifestOption {
	return blueprint.SplitWith(def, items, fn)
}
