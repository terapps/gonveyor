package gonveyor

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/terapps/gonveyor/blueprint"
	"github.com/terapps/gonveyor/ledger"
	"github.com/terapps/gonveyor/transport"
)

// --- mocks ---

type mockLedger struct {
	claim            func(ctx context.Context, taskID string) (func() error, bool, error)
	recordCompleted  func(ctx context.Context, taskID string, result any) (bool, []ledger.Node, error)
	recordFailed     func(ctx context.Context, taskID string, err error) error
	gatherDepResults func(ctx context.Context, taskID string) (map[string][]json.RawMessage, error)
	sendSignal       func(ctx context.Context, blueprintID, signalKey string, payload any) ([]ledger.Node, error)
}

func (m *mockLedger) Claim(ctx context.Context, taskID string) (func() error, bool, error) {
	if m.claim != nil {
		return m.claim(ctx, taskID)
	}
	return func() error { return nil }, true, nil
}
func (m *mockLedger) RecordCompleted(ctx context.Context, taskID string, result any) (bool, []ledger.Node, error) {
	return m.recordCompleted(ctx, taskID, result)
}
func (m *mockLedger) RecordFailed(ctx context.Context, taskID string, err error) error {
	return m.recordFailed(ctx, taskID, err)
}
func (m *mockLedger) GatherDepResults(ctx context.Context, taskID string) (map[string][]json.RawMessage, error) {
	return m.gatherDepResults(ctx, taskID)
}
func (m *mockLedger) CreateBlueprint(_ context.Context, _ ledger.BlueprintManifest) ([]ledger.Node, error) {
	return nil, nil
}
func (m *mockLedger) GetNode(_ context.Context, _ string) (ledger.Node, error) {
	return ledger.Node{}, nil
}
func (m *mockLedger) SendSignal(ctx context.Context, blueprintID, signalKey string, payload any) ([]ledger.Node, error) {
	if m.sendSignal != nil {
		return m.sendSignal(ctx, blueprintID, signalKey, payload)
	}
	return nil, nil
}

type mockDispatcher struct {
	publish func(ctx context.Context, task ledger.Node) error
}

func (m *mockDispatcher) Publish(ctx context.Context, task ledger.Node) error {
	if m.publish != nil {
		return m.publish(ctx, task)
	}
	return nil
}
func (m *mockDispatcher) Close() error { return nil }

type mockWorker struct{}

func (m *mockWorker) Listen(_ context.Context, _ transport.HandlerFunc) error { return nil }
func (m *mockWorker) Close() error                                            { return nil }

// --- helpers ---

type inA struct{}
type outA struct{}

var stationA = blueprint.Define[inA, outA]("a")

func newG(l ledger.Ledger, d transport.Dispatcher) *Gonveyor {
	return NewGonveyor(l, d, &mockWorker{})
}

func emptyPayload() []byte { b, _ := json.Marshal(inA{}); return b }

func invokeHandler(g *Gonveyor, task ledger.Node) error {
	_, err := g.handler()(context.Background(), task, func() {})
	return err
}

// --- handler() tests ---

func TestHandler_Claim_False_Bails(t *testing.T) {
	handlerCalled := false
	l := &mockLedger{
		claim: func(_ context.Context, _ string) (func() error, bool, error) {
			return nil, false, nil
		},
	}
	g := newG(l, &mockDispatcher{})
	g.RegisterHandler(stationA, Handle(stationA, func(_ context.Context, _ inA) (outA, error) {
		handlerCalled = true
		return outA{}, nil
	}))

	err := invokeHandler(g, ledger.Node{Key: "a", ID: "t1", Payload: emptyPayload()})
	assert.NoError(t, err)
	assert.False(t, handlerCalled)
}

func TestHandler_NoHandler_ReturnsError(t *testing.T) {
	g := newG(&mockLedger{}, &mockDispatcher{})

	err := invokeHandler(g, ledger.Node{Key: "unknown", ID: "t1"})
	assert.ErrorContains(t, err, "no handler registered")
}

func TestHandler_HandlerError_CallsRecordFailed(t *testing.T) {
	handlerErr := errors.New("boom")
	var failedID string
	var failedErr error

	l := &mockLedger{
		recordFailed: func(_ context.Context, taskID string, err error) error {
			failedID = taskID
			failedErr = err
			return nil
		},
	}
	g := newG(l, &mockDispatcher{})
	g.RegisterHandler(stationA, Handle(stationA, func(_ context.Context, _ inA) (outA, error) {
		return outA{}, handlerErr
	}))

	err := invokeHandler(g, ledger.Node{Key: "a", ID: "t1", Payload: emptyPayload()})
	assert.ErrorIs(t, err, handlerErr)
	assert.Equal(t, "t1", failedID)
	assert.ErrorIs(t, failedErr, handlerErr)
}

func TestHandler_RecordFailed_Fails_OriginalErrStillReturned(t *testing.T) {
	handlerErr := errors.New("handler error")

	l := &mockLedger{
		recordFailed: func(_ context.Context, _ string, _ error) error { return errors.New("db down") },
	}
	g := newG(l, &mockDispatcher{})
	g.RegisterHandler(stationA, Handle(stationA, func(_ context.Context, _ inA) (outA, error) {
		return outA{}, handlerErr
	}))

	err := invokeHandler(g, ledger.Node{Key: "a", ID: "t1", Payload: emptyPayload()})
	assert.ErrorIs(t, err, handlerErr)
}

func TestHandler_Success_PublishesNextTask(t *testing.T) {
	nextTask := ledger.Node{Key: "b", ID: "t2"}
	var published []ledger.Node

	l := &mockLedger{
		recordCompleted: func(_ context.Context, _ string, _ any) (bool, []ledger.Node, error) {
			return true, []ledger.Node{nextTask}, nil
		},
		gatherDepResults: func(_ context.Context, _ string) (map[string][]json.RawMessage, error) {
			return nil, nil
		},
	}
	d := &mockDispatcher{
		publish: func(_ context.Context, task ledger.Node) error {
			published = append(published, task)
			return nil
		},
	}
	g := newG(l, d)
	g.RegisterHandler(stationA, Handle(stationA, func(_ context.Context, _ inA) (outA, error) {
		return outA{}, nil
	}))

	err := invokeHandler(g, ledger.Node{Key: "a", ID: "t1", Payload: emptyPayload()})
	require.NoError(t, err)
	require.Len(t, published, 1)
	assert.Equal(t, "t2", published[0].ID)
}

// --- OnComplete tests ---

func TestOnComplete_RecordCompleted_False_Bails(t *testing.T) {
	published := false
	l := &mockLedger{
		recordCompleted: func(_ context.Context, _ string, _ any) (bool, []ledger.Node, error) {
			return false, nil, nil
		},
	}
	d := &mockDispatcher{
		publish: func(_ context.Context, _ ledger.Node) error {
			published = true
			return nil
		},
	}
	g := newG(l, d)

	err := g.OnComplete(context.Background(), "t1", nil)
	assert.NoError(t, err)
	assert.False(t, published)
}

func TestOnComplete_NoUnblockedTasks_NothingPublished(t *testing.T) {
	var published []ledger.Node
	l := &mockLedger{
		recordCompleted: func(_ context.Context, _ string, _ any) (bool, []ledger.Node, error) {
			return true, nil, nil
		},
	}
	d := &mockDispatcher{
		publish: func(_ context.Context, task ledger.Node) error {
			published = append(published, task)
			return nil
		},
	}
	g := newG(l, d)

	err := g.OnComplete(context.Background(), "t1", nil)
	assert.NoError(t, err)
	assert.Empty(t, published)
}

func TestOnComplete_MultipleUnblockedTasks_AllPublished(t *testing.T) {
	tasks := []ledger.Node{{Key: "b", ID: "t2"}, {Key: "c", ID: "t3"}}
	var published []ledger.Node

	l := &mockLedger{
		recordCompleted: func(_ context.Context, _ string, _ any) (bool, []ledger.Node, error) {
			return true, tasks, nil
		},
		gatherDepResults: func(_ context.Context, _ string) (map[string][]json.RawMessage, error) {
			return nil, nil
		},
	}
	d := &mockDispatcher{
		publish: func(_ context.Context, task ledger.Node) error {
			published = append(published, task)
			return nil
		},
	}
	g := newG(l, d)

	err := g.OnComplete(context.Background(), "t1", nil)
	require.NoError(t, err)
	assert.Len(t, published, 2)
}

func TestOnComplete_RecordCompleted_Error_Propagates(t *testing.T) {
	dbErr := errors.New("db error")
	l := &mockLedger{
		recordCompleted: func(_ context.Context, _ string, _ any) (bool, []ledger.Node, error) {
			return false, nil, dbErr
		},
	}
	g := newG(l, &mockDispatcher{})

	err := g.OnComplete(context.Background(), "t1", nil)
	assert.ErrorIs(t, err, dbErr)
}

// --- Race test ---

func TestHandler_Race_OnlyOneWins(t *testing.T) {
	var mu sync.Mutex
	handlerCalls := 0
	claimCalls := 0

	l := &mockLedger{
		claim: func(_ context.Context, _ string) (func() error, bool, error) {
			mu.Lock()
			defer mu.Unlock()
			claimCalls++
			return func() error { return nil }, claimCalls == 1, nil
		},
		recordCompleted: func(_ context.Context, _ string, _ any) (bool, []ledger.Node, error) {
			return true, nil, nil
		},
		gatherDepResults: func(_ context.Context, _ string) (map[string][]json.RawMessage, error) {
			return nil, nil
		},
	}
	g := newG(l, &mockDispatcher{})
	g.RegisterHandler(stationA, Handle(stationA, func(_ context.Context, _ inA) (outA, error) {
		mu.Lock()
		handlerCalls++
		mu.Unlock()
		return outA{}, nil
	}))

	task := ledger.Node{Key: "a", ID: "t1", Payload: emptyPayload()}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = invokeHandler(g, task)
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, handlerCalls)
}

func TestHandler_After_DoesNotCallGatherDepResults(t *testing.T) {
	stationB := blueprint.Define[inA, outA]("b")
	bp := blueprint.New("test_after", stationA, blueprint.Wire(stationB, blueprint.After[inA](stationA)))

	gatherCalled := false
	l := &mockLedger{
		recordCompleted: func(_ context.Context, _ string, _ any) (bool, []ledger.Node, error) {
			return true, []ledger.Node{{Key: "b", ID: "t2", BlueprintName: "test_after", Payload: emptyPayload()}}, nil
		},
		gatherDepResults: func(_ context.Context, _ string) (map[string][]json.RawMessage, error) {
			gatherCalled = true
			return nil, nil
		},
	}
	g := newG(l, &mockDispatcher{})
	g.RegisterBlueprint(bp)
	g.RegisterHandler(stationA, Handle(stationA, func(_ context.Context, _ inA) (outA, error) {
		return outA{}, nil
	}))
	g.RegisterHandler(stationB, Handle(stationB, func(_ context.Context, _ inA) (outA, error) {
		return outA{}, nil
	}))

	err := invokeHandler(g, ledger.Node{Key: "a", ID: "t1", BlueprintName: "test_after", Payload: emptyPayload()})
	require.NoError(t, err)
	assert.False(t, gatherCalled, "GatherDepResults should not be called for After-only deps")
}

func TestHandler_AfterAndIntake_CallsGatherDepResults(t *testing.T) {
	stationB := blueprint.Define[inA, outA]("b")
	bp := blueprint.New("test_after_intake", stationA,
		blueprint.Wire(stationB,
			blueprint.After[inA](stationA),
			blueprint.Intake(stationA, func(_ outA, _ *inA) {}),
		),
	)

	gatherCalled := false
	l := &mockLedger{
		recordCompleted: func(_ context.Context, _ string, _ any) (bool, []ledger.Node, error) {
			return true, []ledger.Node{{Key: "b", ID: "t2", BlueprintName: "test_after_intake", Payload: emptyPayload()}}, nil
		},
		gatherDepResults: func(_ context.Context, _ string) (map[string][]json.RawMessage, error) {
			gatherCalled = true
			raw, _ := json.Marshal(outA{})
			return map[string][]json.RawMessage{"a": {raw}}, nil
		},
	}
	g := newG(l, &mockDispatcher{})
	g.RegisterBlueprint(bp)
	g.RegisterHandler(stationA, Handle(stationA, func(_ context.Context, _ inA) (outA, error) {
		return outA{}, nil
	}))
	g.RegisterHandler(stationB, Handle(stationB, func(_ context.Context, _ inA) (outA, error) {
		return outA{}, nil
	}))

	err := invokeHandler(g, ledger.Node{Key: "a", ID: "t1", BlueprintName: "test_after_intake", Payload: emptyPayload()})
	require.NoError(t, err)
	assert.True(t, gatherCalled, "GatherDepResults should be called when Intake is present alongside After")
}

// --- Gonductor tests ---

func TestGonductor_SendSignal_PublishesUnblockedNodes(t *testing.T) {
	unblocked := []ledger.Node{{Key: "process", ID: "t2"}}
	var publishedKeys []string

	l := &mockLedger{}
	l.sendSignal = func(_ context.Context, blueprintID, signalKey string, payload any) ([]ledger.Node, error) {
		assert.Equal(t, "bp-1", blueprintID)
		assert.Equal(t, "await_approval", signalKey)
		return unblocked, nil
	}
	d := &mockDispatcher{
		publish: func(_ context.Context, n ledger.Node) error {
			publishedKeys = append(publishedKeys, n.Key)
			return nil
		},
	}

	c := NewGonductor(l, d)
	err := c.SendSignal(context.Background(), "bp-1", "await_approval", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"process"}, publishedKeys)
}

func TestGonductor_SendSignal_LedgerError_Propagates(t *testing.T) {
	l := &mockLedger{}
	l.sendSignal = func(_ context.Context, _, _ string, _ any) ([]ledger.Node, error) {
		return nil, errors.New("db error")
	}
	c := NewGonductor(l, &mockDispatcher{})
	err := c.SendSignal(context.Background(), "bp-1", "approve", nil)
	assert.ErrorContains(t, err, "db error")
}
