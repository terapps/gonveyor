package blueprint

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

type fakeAnyDep struct{ key string }

func (f fakeAnyDep) depKey() string                     { return f.key }
func (f fakeAnyDep) isAll() bool                        { return false }
func (f fakeAnyDep) apply([]json.RawMessage, any) error { return nil }

type fakeDef struct {
	key  string
	deps []anyDep
}

func (f *fakeDef) Key() string       { return f.key }
func (f *fakeDef) depList() []anyDep { return f.deps }
func (f *fakeDef) NeedsDepData() bool { return false }
func (f *fakeDef) BuildInput(_ json.RawMessage, _ map[string][]json.RawMessage) (json.RawMessage, error) {
	return nil, nil
}

func TestFindCycle_NoCycle(t *testing.T) {
	a := &fakeDef{key: "a"}
	b := &fakeDef{key: "b", deps: []anyDep{fakeAnyDep{"a"}}}
	assert.NoError(t, findCycle([]AnyWiredNode{a, b}))
}

func TestFindCycle_DirectCycle(t *testing.T) {
	a := &fakeDef{key: "a", deps: []anyDep{fakeAnyDep{"b"}}}
	b := &fakeDef{key: "b", deps: []anyDep{fakeAnyDep{"a"}}}
	assert.Error(t, findCycle([]AnyWiredNode{a, b}))
}

func TestFindCycle_IndirectCycle(t *testing.T) {
	a := &fakeDef{key: "a", deps: []anyDep{fakeAnyDep{"c"}}}
	b := &fakeDef{key: "b", deps: []anyDep{fakeAnyDep{"a"}}}
	c := &fakeDef{key: "c", deps: []anyDep{fakeAnyDep{"b"}}}
	assert.Error(t, findCycle([]AnyWiredNode{a, b, c}))
}
