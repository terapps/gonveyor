// Package blueprint provides the typed DSL for defining task workflows.
package blueprint

import (
	"encoding/json"
	"fmt"
	"strings"
)

// AnyDef is the type-erased interface for a task definition.
type AnyDef interface {
	Key() string
	depList() []anyDep
	BuildInput(outputs map[string][]json.RawMessage) (json.RawMessage, error)
}

// Station is a typed task definition with input type I and output type O.
type Station[I, O any] struct {
	key  string
	deps []anyDep
}

// Blueprint is a named workflow composed of typed task definitions.
type Blueprint struct {
	name  string
	tasks []AnyDef
}

// DepOption declares a typed dependency on a task definition's input.
type DepOption[I any] interface {
	anyDep
}

// Define creates a new typed task definition with the given key and dependencies.
func Define[I, O any](key string, opts ...DepOption[I]) *Station[I, O] {
	def := &Station[I, O]{key: key}
	for _, opt := range opts {
		def.deps = append(def.deps, opt)
	}

	return def
}

// New creates a new Blueprint with the given name and task definitions.
// Panics if any dependency is missing from the task list or if the graph has a cycle.
func New(name string, tasks ...AnyDef) *Blueprint {
	keys := make(map[string]struct{}, len(tasks))
	for _, t := range tasks {
		keys[t.Key()] = struct{}{}
	}

	for _, t := range tasks {
		for _, dep := range t.depList() {
			if _, ok := keys[dep.depKey()]; !ok {
				panic(fmt.Sprintf("blueprint %q: task %q depends on %q which is not in the blueprint", name, t.Key(), dep.depKey()))
			}
		}
	}

	if err := findCycle(tasks); err != nil {
		panic(fmt.Sprintf("blueprint %q: %s", name, err))
	}

	return &Blueprint{name: name, tasks: tasks}
}

func findCycle(tasks []AnyDef) error {
	adj := make(map[string][]string, len(tasks))
	for _, t := range tasks {
		for _, dep := range t.depList() {
			adj[dep.depKey()] = append(adj[dep.depKey()], t.Key())
		}
	}

	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)

	state := make(map[string]int, len(tasks))
	var path []string

	var dfs func(key string) bool
	dfs = func(key string) bool {
		state[key] = visiting
		path = append(path, key)

		for _, next := range adj[key] {
			switch state[next] {
			case visiting:
				path = append(path, next)
				return true
			case unvisited:
				if dfs(next) {
					return true
				}
			}
		}

		path = path[:len(path)-1]
		state[key] = visited
		return false
	}

	for _, t := range tasks {
		if state[t.Key()] == unvisited {
			if dfs(t.Key()) {
				return fmt.Errorf("cycle detected: %s", strings.Join(path, " → "))
			}
		}
	}

	return nil
}

// Intake declares a dependency on another task's output, mutating this task's input.
// The fn receives the upstream output and a pointer to the input to fill — only set the
// fields this dep owns. Multiple Intake/Merge deps must write to disjoint fields.
func Intake[DepI, DepO, I any](from *Station[DepI, DepO], fn func(DepO, *I)) DepOption[I] {
	return &singleDep[DepI, DepO, I]{from: from, fn: fn}
}

// Merge declares a fan-in dependency that receives all outputs from a split task as a slice.
// See Intake for field ownership constraints when combining multiple deps on the same input struct.
func Merge[DepI, DepO, I any](from *Station[DepI, DepO], fn func([]DepO, *I)) DepOption[I] {
	return &allDep[DepI, DepO, I]{from: from, fn: fn}
}

// Key returns the unique key identifying this task definition.
func (d *Station[I, O]) Key() string       { return d.key }
func (d *Station[I, O]) depList() []anyDep { return d.deps }

// BuildInput merges upstream outputs into the typed input for this task.
func (d *Station[I, O]) BuildInput(outputs map[string][]json.RawMessage) (json.RawMessage, error) {
	var input I

	for _, dep := range d.deps {
		outs := outputs[dep.depKey()]
		if err := dep.apply(outs, &input); err != nil {
			return nil, fmt.Errorf("dep %s: %w", dep.depKey(), err)
		}
	}

	return json.Marshal(input)
}

// Tasks returns the ordered task definitions in this blueprint.
func (b *Blueprint) Tasks() []AnyDef { return b.tasks }

// Name returns the blueprint name.
func (b *Blueprint) Name() string { return b.name }

type anyDep interface {
	depKey() string
	isAll() bool
	apply(outputs []json.RawMessage, inputPtr any) error
}

type singleDep[DepI, DepO, I any] struct {
	from *Station[DepI, DepO]
	fn   func(DepO, *I)
}

func (d *singleDep[DepI, DepO, I]) depKey() string { return d.from.key }
func (d *singleDep[DepI, DepO, I]) isAll() bool    { return false }
func (d *singleDep[DepI, DepO, I]) apply(outputs []json.RawMessage, inputPtr any) error {
	if len(outputs) == 0 {
		return fmt.Errorf("no output for dep %s", d.from.key)
	}

	if len(outputs) > 1 {
		return fmt.Errorf("dep %q has %d outputs — use Merge instead of Intake to collect all", d.from.key, len(outputs))
	}

	var out DepO
	if err := json.Unmarshal(outputs[0], &out); err != nil {
		return err
	}
	d.fn(out, inputPtr.(*I))
	return nil
}

type allDep[DepI, DepO, I any] struct {
	from *Station[DepI, DepO]
	fn   func([]DepO, *I)
}

func (d *allDep[DepI, DepO, I]) depKey() string { return d.from.key }
func (d *allDep[DepI, DepO, I]) isAll() bool    { return true }
func (d *allDep[DepI, DepO, I]) apply(outputs []json.RawMessage, inputPtr any) error {
	var outs []DepO

	for _, o := range outputs {
		var out DepO
		if err := json.Unmarshal(o, &out); err != nil {
			return err
		}
		outs = append(outs, out)
	}

	d.fn(outs, inputPtr.(*I))
	return nil
}
