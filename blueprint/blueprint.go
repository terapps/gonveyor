// Package blueprint provides the typed DSL for defining task workflows.
package blueprint

import (
	"encoding/json"
	"fmt"
	"strings"
)

// AnyDef is the minimal type-erased interface for a task node.
type AnyDef interface {
	Key() string
}

// AnyWiredStation is the type-erased interface for a Station with its dependency wiring.
// It is a node definition (schema/template) — distinct from ledger.Unit, which is a
// persisted runtime instance of that definition.
type AnyWiredStation interface {
	AnyDef
	// NodeType returns "signal" for gateway nodes, "" for regular task nodes.
	NodeType() string
	depList() []anyDep
	NeedsDepData() bool
	BuildInput(base json.RawMessage, outputs map[string][]json.RawMessage) (json.RawMessage, error)
}

// Station is a typed task node definition — input type I, output type O.
// Implements AnyWiredStation with no deps so it can be passed directly to Blueprint.New as a root node.
type Station[I, O any] struct {
	key      string
	nodeType string // "" = regular task, "signal" = gateway node activated by SendSignal
}

func (d *Station[I, O]) Key() string      { return d.key }
func (d *Station[I, O]) NodeType() string { return d.nodeType }
func (d *Station[I, O]) depList() []anyDep    { return nil }
func (d *Station[I, O]) NeedsDepData() bool   { return false }
func (d *Station[I, O]) BuildInput(base json.RawMessage, _ map[string][]json.RawMessage) (json.RawMessage, error) {
	if base != nil {
		return base, nil
	}
	var zero I
	return json.Marshal(zero)
}

// Define creates a new typed task node with the given key.
func Define[I, O any](key string) *Station[I, O] {
	return &Station[I, O]{key: key}
}

// Signal creates a gateway node that is never dispatched to a worker.
// It is activated by Gonductor.SendSignal(blueprintID, key, payload), which completes
// it and cascades to dispatch any successors. The type parameter T is the signal payload type;
// successors can receive it via Intake(signalStation, fn).
func Signal[T any](key string) *Station[struct{}, T] {
	return &Station[struct{}, T]{key: key, nodeType: "signal"}
}

// wiredNode wraps a Station with its blueprint-specific dependency wiring.
type wiredNode[I, O any] struct {
	station *Station[I, O]
	deps    []anyDep
}

func (w *wiredNode[I, O]) Key() string      { return w.station.key }
func (w *wiredNode[I, O]) NodeType() string { return w.station.nodeType }
func (w *wiredNode[I, O]) depList() []anyDep { return w.deps }

func (w *wiredNode[I, O]) NeedsDepData() bool {
	for _, dep := range w.deps {
		if _, ok := dep.(afterDep); !ok {
			return true
		}
	}
	return false
}

func (w *wiredNode[I, O]) BuildInput(base json.RawMessage, outputs map[string][]json.RawMessage) (json.RawMessage, error) {
	var input I
	if base != nil {
		if err := json.Unmarshal(base, &input); err != nil {
			return nil, fmt.Errorf("unmarshal base payload: %w", err)
		}
	}

	for _, dep := range w.deps {
		outs := outputs[dep.depKey()]
		if err := dep.apply(outs, &input); err != nil {
			return nil, fmt.Errorf("dep %s: %w", dep.depKey(), err)
		}
	}

	return json.Marshal(input)
}

// Wire creates a wired node — a Station with its blueprint-specific dependency declarations.
// Use inside Blueprint.New to declare how this task's input is built from upstream outputs.
func Wire[I, O any](def *Station[I, O], deps ...DepOption[I]) AnyWiredStation {
	dd := make([]anyDep, len(deps))
	for i, d := range deps {
		dd[i] = d
	}
	return &wiredNode[I, O]{station: def, deps: dd}
}

// Blueprint is a named workflow composed of wired task nodes.
type Blueprint struct {
	name  string
	tasks []AnyWiredStation
	index map[string]AnyWiredStation
}

// New creates a Blueprint from the given wired nodes.
// Pass bare *Station values for root nodes (no deps), or Wire(...) for nodes with deps.
// Panics if any dependency is missing from the task list or if the graph has a cycle.
func New(name string, tasks ...AnyWiredStation) *Blueprint {
	index := make(map[string]AnyWiredStation, len(tasks))
	for _, t := range tasks {
		index[t.Key()] = t
	}

	for _, t := range tasks {
		for _, dep := range t.depList() {
			if _, ok := index[dep.depKey()]; !ok {
				panic(fmt.Sprintf("blueprint %q: task %q depends on %q which is not in the blueprint", name, t.Key(), dep.depKey()))
			}
		}
	}

	if err := findCycle(tasks); err != nil {
		panic(fmt.Sprintf("blueprint %q: %s", name, err))
	}

	return &Blueprint{name: name, tasks: tasks, index: index}
}

// Name returns the blueprint name.
func (b *Blueprint) Name() string { return b.name }

// Node returns the wired node for the given task key, or nil if not found.
func (b *Blueprint) Node(key string) AnyWiredStation { return b.index[key] }

// Tasks returns the ordered task nodes in this blueprint.
func (b *Blueprint) Tasks() []AnyWiredStation { return b.tasks }

func findCycle(tasks []AnyWiredStation) error {
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

// DepOption declares a typed dependency for a task with input type I.
type DepOption[I any] interface {
	anyDep
}

type anyDep interface {
	depKey() string
	isAll() bool
	apply(outputs []json.RawMessage, inputPtr any) error
}

type afterDep struct{ key string }

func (a afterDep) depKey() string                          { return a.key }
func (a afterDep) isAll() bool                             { return false }
func (a afterDep) apply(_ []json.RawMessage, _ any) error { return nil }

// After declares a pure ordering dependency: the upstream must complete before this task
// is dispatched, but its output is not fetched from the store.
func After[I any](from AnyDef) DepOption[I] { return afterDep{key: from.Key()} }

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

// Intake declares a dependency on another task's output, mutating this task's input.
// The fn receives the upstream output and a pointer to the input to fill — only set the
// fields this dep owns. Multiple Intake/Merge deps must write to disjoint fields.
func Intake[DepI, DepO, I any](from *Station[DepI, DepO], fn func(DepO, *I)) DepOption[I] {
	return &singleDep[DepI, DepO, I]{from: from, fn: fn}
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

// Merge declares a fan-in dependency that receives all outputs from a split task as a slice.
// See Intake for field ownership constraints when combining multiple deps on the same input struct.
func Merge[DepI, DepO, I any](from *Station[DepI, DepO], fn func([]DepO, *I)) DepOption[I] {
	return &allDep[DepI, DepO, I]{from: from, fn: fn}
}
