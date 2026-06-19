package blueprint_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/terapps/gonveyor/blueprint"
	"github.com/terapps/gonveyor/store"
)

// --- helpers ---

type in1 struct{ A string }
type out1 struct{ A string }
type in2 struct{ A string }
type out2 struct{}
type in3 struct {
	A string
	B string
}
type out3 struct{}

func mustManifest(t *testing.T, bp *blueprint.Blueprint, input any, opts ...blueprint.ManifestOption) store.BlueprintManifest {
	t.Helper()
	m, err := bp.Manifest(input, opts...)
	require.NoError(t, err)
	return m
}

func taskByKey(m store.BlueprintManifest, key string) []store.Task {
	var out []store.Task
	for _, t := range m.Tasks {
		if t.Key == key {
			out = append(out, t)
		}
	}
	return out
}

// --- New / validation ---

func TestNew_Valid(t *testing.T) {
	a := blueprint.Define[in1, out1]("a")
	assert.NotPanics(t, func() {
		blueprint.New("bp", a)
	})
}

func TestNew_MissingDep(t *testing.T) {
	a := blueprint.Define[in1, out1]("a")
	b := blueprint.Define[in2, out2]("b",
		blueprint.Intake(a, func(o out1, in *in2) { in.A = o.A }),
	)
	assert.Panics(t, func() {
		blueprint.New("bp", b) // a manquant
	})
}

func TestNew_Linear(t *testing.T) {
	a := blueprint.Define[in1, out1]("a")
	b := blueprint.Define[in2, out2]("b",
		blueprint.Intake(a, func(o out1, in *in2) { in.A = o.A }),
	)

	assert.NotPanics(t, func() {
		blueprint.New("bp", a, b)
	})
}

func TestNew_Diamond(t *testing.T) {
	a := blueprint.Define[in1, out1]("a")
	b := blueprint.Define[in2, out2]("b",
		blueprint.Intake(a, func(o out1, in *in2) { in.A = o.A }),
	)
	c := blueprint.Define[in2, out2]("c",
		blueprint.Intake(a, func(o out1, in *in2) { in.A = o.A }),
	)
	d := blueprint.Define[in3, out3]("d",
		blueprint.Intake(b, func(o out2, in *in3) {}),
		blueprint.Intake(c, func(o out2, in *in3) {}),
	)
	assert.NotPanics(t, func() {
		blueprint.New("bp", a, b, c, d)
	})
}

// --- Manifest / PendingTasks ---

func TestManifest_RootIsInitial(t *testing.T) {
	a := blueprint.Define[in1, out1]("a")
	b := blueprint.Define[in2, out2]("b",
		blueprint.Intake(a, func(o out1, in *in2) { in.A = o.A }),
	)
	bp := blueprint.New("bp", a, b)
	m := mustManifest(t, bp, in1{A: "hello"})

	pending := m.PendingTasks()
	require.Len(t, pending, 1)
	assert.Equal(t, "a", pending[0].Key)
}

func TestManifest_DepTaskNotInitial(t *testing.T) {
	a := blueprint.Define[in1, out1]("a")
	b := blueprint.Define[in2, out2]("b",
		blueprint.Intake(a, func(o out1, in *in2) { in.A = o.A }),
	)
	bp := blueprint.New("bp", a, b)
	m := mustManifest(t, bp, in1{})

	ids := make(map[string]struct{})
	for _, t := range m.PendingTasks() {
		ids[t.ID] = struct{}{}
	}
	for _, task := range taskByKey(m, "b") {
		assert.NotContains(t, ids, task.ID)
	}
}

func TestManifest_RootPayload(t *testing.T) {
	a := blueprint.Define[in1, out1]("a")
	bp := blueprint.New("bp", a)
	m := mustManifest(t, bp, in1{A: "hello"})

	tasks := taskByKey(m, "a")
	require.Len(t, tasks, 1)

	var got in1
	require.NoError(t, json.Unmarshal(tasks[0].Payload, &got))
	assert.Equal(t, "hello", got.A)
}

func TestManifest_Split_CreatesNInstances(t *testing.T) {
	a := blueprint.Define[in1, out1]("a")
	b := blueprint.Define[in2, out2]("b",
		blueprint.Intake(a, func(o out1, in *in2) { in.A = o.A }),
	)
	bp := blueprint.New("bp", a, b)
	m := mustManifest(t, bp, in1{}, blueprint.Split(b, 3))

	assert.Len(t, taskByKey(m, "b"), 3)
}

func TestManifest_Split_DownstreamWaitsAll(t *testing.T) {
	a := blueprint.Define[in1, out1]("a")
	b := blueprint.Define[in2, out2]("b",
		blueprint.Intake(a, func(o out1, in *in2) { in.A = o.A }),
	)
	c := blueprint.Define[in3, out3]("c",
		blueprint.Merge(b, func(outs []out2, in *in3) {}),
	)
	bp := blueprint.New("bp", a, b, c)
	m := mustManifest(t, bp, in1{}, blueprint.Split(b, 3))

	cTasks := taskByKey(m, "c")
	require.Len(t, cTasks, 1)

	var cDeps []string
	for _, d := range m.Dependencies {
		if d.TaskID == cTasks[0].ID {
			cDeps = append(cDeps, d.DependsOnID)
		}
	}
	assert.Len(t, cDeps, 3)
}

func TestManifest_DepWiring(t *testing.T) {
	a := blueprint.Define[in1, out1]("a")
	b := blueprint.Define[in2, out2]("b",
		blueprint.Intake(a, func(o out1, in *in2) { in.A = o.A }),
	)
	bp := blueprint.New("bp", a, b)
	m := mustManifest(t, bp, in1{})

	aID := taskByKey(m, "a")[0].ID
	bID := taskByKey(m, "b")[0].ID

	var found bool
	for _, d := range m.Dependencies {
		if d.TaskID == bID && d.DependsOnID == aID {
			found = true
		}
	}
	assert.True(t, found)
}

// --- BuildInput / Intake ---

func TestBuildInput_Intake_SingleDep(t *testing.T) {
	a := blueprint.Define[in1, out1]("a")
	b := blueprint.Define[in2, out2]("b",
		blueprint.Intake(a, func(o out1, in *in2) { in.A = o.A }),
	)

	raw, err := json.Marshal(out1{A: "hello"})
	require.NoError(t, err)

	result, err := b.BuildInput(map[string][]json.RawMessage{"a": {raw}})
	require.NoError(t, err)

	var got in2
	require.NoError(t, json.Unmarshal(result, &got))
	assert.Equal(t, "hello", got.A)
}

func TestBuildInput_Intake_ZeroValue(t *testing.T) {
	type inBool struct{ Active bool }
	type outBool struct{ Active bool }

	a := blueprint.Define[inBool, outBool]("a")
	b := blueprint.Define[inBool, struct{}]("b",
		blueprint.Intake(a, func(o outBool, in *inBool) { in.Active = o.Active }),
	)

	raw, err := json.Marshal(outBool{Active: false})
	require.NoError(t, err)

	result, err := b.BuildInput(map[string][]json.RawMessage{"a": {raw}})
	require.NoError(t, err)

	var got inBool
	require.NoError(t, json.Unmarshal(result, &got))
	assert.False(t, got.Active)
}

func TestBuildInput_Intake_MultiDep_DisjointFields(t *testing.T) {
	a := blueprint.Define[in1, out1]("a")
	b := blueprint.Define[in2, out2]("b",
		blueprint.Intake(a, func(o out1, in *in2) {}),
	)
	c := blueprint.Define[in3, out3]("c",
		blueprint.Intake(a, func(o out1, in *in3) { in.A = o.A }),
		blueprint.Intake(b, func(o out2, in *in3) { in.B = "from_b" }),
	)

	rawA, _ := json.Marshal(out1{A: "val_a"})
	rawB, _ := json.Marshal(out2{})

	result, err := c.BuildInput(map[string][]json.RawMessage{"a": {rawA}, "b": {rawB}})
	require.NoError(t, err)

	var got in3
	require.NoError(t, json.Unmarshal(result, &got))
	assert.Equal(t, "val_a", got.A)
	assert.Equal(t, "from_b", got.B)
}

func TestBuildInput_Intake_MultipleOutputs_Error(t *testing.T) {
	a := blueprint.Define[in1, out1]("a")
	b := blueprint.Define[in2, out2]("b",
		blueprint.Intake(a, func(o out1, in *in2) { in.A = o.A }),
	)

	raw, _ := json.Marshal(out1{A: "x"})
	_, err := b.BuildInput(map[string][]json.RawMessage{"a": {raw, raw}})
	assert.ErrorContains(t, err, "Merge")
}

// --- BuildInput / Merge ---

func TestBuildInput_Merge_AggregatesAll(t *testing.T) {
	type inSlice struct{ Values []string }
	type outVal struct{ V string }

	a := blueprint.Define[in1, outVal]("a")
	b := blueprint.Define[inSlice, out3]("b",
		blueprint.Merge(a, func(outs []outVal, in *inSlice) {
			for _, o := range outs {
				in.Values = append(in.Values, o.V)
			}
		}),
	)

	r1, _ := json.Marshal(outVal{V: "x"})
	r2, _ := json.Marshal(outVal{V: "y"})
	r3, _ := json.Marshal(outVal{V: "z"})

	result, err := b.BuildInput(map[string][]json.RawMessage{"a": {r1, r2, r3}})
	require.NoError(t, err)

	var got inSlice
	require.NoError(t, json.Unmarshal(result, &got))
	assert.Equal(t, []string{"x", "y", "z"}, got.Values)
}

func TestBuildInput_Merge_EmptyOutputs(t *testing.T) {
	type inSlice struct{ Values []string }
	type outVal struct{ V string }

	a := blueprint.Define[in1, outVal]("a")
	b := blueprint.Define[inSlice, out3]("b",
		blueprint.Merge(a, func(outs []outVal, in *inSlice) {
			for _, o := range outs {
				in.Values = append(in.Values, o.V)
			}
		}),
	)

	result, err := b.BuildInput(map[string][]json.RawMessage{"a": {}})
	require.NoError(t, err)

	var got inSlice
	require.NoError(t, json.Unmarshal(result, &got))
	assert.Empty(t, got.Values)
}
