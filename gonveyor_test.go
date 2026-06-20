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
	setRunning       func(ctx context.Context, taskID string) (bool, error)
	setSuccess       func(ctx context.Context, taskID string, result any) (bool, []ledger.Task, error)
	setFailed        func(ctx context.Context, taskID string, err error) error
	gatherDepResults func(ctx context.Context, taskID string) (map[string][]json.RawMessage, error)
}

func (m *mockLedger) SetRunning(ctx context.Context, taskID string) (bool, error) {
	return m.setRunning(ctx, taskID)
}
func (m *mockLedger) SetSuccess(ctx context.Context, taskID string, result any) (bool, []ledger.Task, error) {
	return m.setSuccess(ctx, taskID, result)
}
func (m *mockLedger) SetFailed(ctx context.Context, taskID string, err error) error {
	return m.setFailed(ctx, taskID, err)
}
func (m *mockLedger) GatherDepResults(ctx context.Context, taskID string) (map[string][]json.RawMessage, error) {
	return m.gatherDepResults(ctx, taskID)
}
func (m *mockLedger) RenewLock(_ context.Context, _ string) error { return nil }
func (m *mockLedger) CreateBlueprint(_ context.Context, _ ledger.BlueprintManifest) ([]ledger.Task, error) {
	return nil, nil
}
func (m *mockLedger) GetBlueprint(_ context.Context, _ string) (ledger.BlueprintManifest, error) {
	return ledger.BlueprintManifest{}, nil
}
func (m *mockLedger) GetTask(_ context.Context, _ string) (ledger.Task, error) {
	return ledger.Task{}, nil
}
func (m *mockLedger) ListBlueprints(_ context.Context) ([]ledger.Blueprint, error) { return nil, nil }
func (m *mockLedger) SetDispatched(_ context.Context, _ string) (bool, error)      { return true, nil }

type mockDispatcher struct {
	publish func(ctx context.Context, task ledger.Task) error
}

func (m *mockDispatcher) Publish(ctx context.Context, task ledger.Task) error {
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

func invokeHandler(g *Gonveyor, task ledger.Task) error {
	_, err := g.handler()(context.Background(), task, func() {})
	return err
}

// --- handler() tests ---

func TestHandler_SetRunning_False_Bails(t *testing.T) {
	handlerCalled := false
	l := &mockLedger{
		setRunning: func(_ context.Context, _ string) (bool, error) { return false, nil },
	}
	g := newG(l, &mockDispatcher{})
	g.RegisterHandler(stationA, Handle(stationA, func(_ context.Context, _ inA) (outA, error) {
		handlerCalled = true
		return outA{}, nil
	}))

	err := invokeHandler(g, ledger.Task{Key: "a", ID: "t1", Payload: emptyPayload()})
	assert.NoError(t, err)
	assert.False(t, handlerCalled)
}

func TestHandler_NoHandler_ReturnsError(t *testing.T) {
	g := newG(&mockLedger{}, &mockDispatcher{})

	err := invokeHandler(g, ledger.Task{Key: "unknown", ID: "t1"})
	assert.ErrorContains(t, err, "no handler registered")
}

func TestHandler_HandlerError_CallsSetFailed(t *testing.T) {
	handlerErr := errors.New("boom")
	var failedID string
	var failedErr error

	l := &mockLedger{
		setRunning: func(_ context.Context, _ string) (bool, error) { return true, nil },
		setFailed: func(_ context.Context, taskID string, err error) error {
			failedID = taskID
			failedErr = err
			return nil
		},
	}
	g := newG(l, &mockDispatcher{})
	g.RegisterHandler(stationA, Handle(stationA, func(_ context.Context, _ inA) (outA, error) {
		return outA{}, handlerErr
	}))

	err := invokeHandler(g, ledger.Task{Key: "a", ID: "t1", Payload: emptyPayload()})
	assert.ErrorIs(t, err, handlerErr)
	assert.Equal(t, "t1", failedID)
	assert.ErrorIs(t, failedErr, handlerErr)
}

func TestHandler_SetFailed_Fails_OriginalErrStillReturned(t *testing.T) {
	handlerErr := errors.New("handler error")

	l := &mockLedger{
		setRunning: func(_ context.Context, _ string) (bool, error) { return true, nil },
		setFailed:  func(_ context.Context, _ string, _ error) error { return errors.New("db down") },
	}
	g := newG(l, &mockDispatcher{})
	g.RegisterHandler(stationA, Handle(stationA, func(_ context.Context, _ inA) (outA, error) {
		return outA{}, handlerErr
	}))

	err := invokeHandler(g, ledger.Task{Key: "a", ID: "t1", Payload: emptyPayload()})
	assert.ErrorIs(t, err, handlerErr)
}

func TestHandler_Success_PublishesNextTask(t *testing.T) {
	nextTask := ledger.Task{Key: "b", ID: "t2"}
	var published []ledger.Task

	l := &mockLedger{
		setRunning: func(_ context.Context, _ string) (bool, error) { return true, nil },
		setSuccess: func(_ context.Context, _ string, _ any) (bool, []ledger.Task, error) {
			return true, []ledger.Task{nextTask}, nil
		},
		gatherDepResults: func(_ context.Context, _ string) (map[string][]json.RawMessage, error) {
			return nil, nil
		},
	}
	d := &mockDispatcher{
		publish: func(_ context.Context, task ledger.Task) error {
			published = append(published, task)
			return nil
		},
	}
	g := newG(l, d)
	g.RegisterHandler(stationA, Handle(stationA, func(_ context.Context, _ inA) (outA, error) {
		return outA{}, nil
	}))

	err := invokeHandler(g, ledger.Task{Key: "a", ID: "t1", Payload: emptyPayload()})
	require.NoError(t, err)
	require.Len(t, published, 1)
	assert.Equal(t, "t2", published[0].ID)
}

// --- OnComplete tests ---

func TestOnComplete_SetSuccess_False_Bails(t *testing.T) {
	published := false
	l := &mockLedger{
		setSuccess: func(_ context.Context, _ string, _ any) (bool, []ledger.Task, error) {
			return false, nil, nil
		},
	}
	d := &mockDispatcher{
		publish: func(_ context.Context, _ ledger.Task) error {
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
	var published []ledger.Task
	l := &mockLedger{
		setSuccess: func(_ context.Context, _ string, _ any) (bool, []ledger.Task, error) {
			return true, nil, nil
		},
	}
	d := &mockDispatcher{
		publish: func(_ context.Context, task ledger.Task) error {
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
	tasks := []ledger.Task{{Key: "b", ID: "t2"}, {Key: "c", ID: "t3"}}
	var published []ledger.Task

	l := &mockLedger{
		setSuccess: func(_ context.Context, _ string, _ any) (bool, []ledger.Task, error) {
			return true, tasks, nil
		},
		gatherDepResults: func(_ context.Context, _ string) (map[string][]json.RawMessage, error) {
			return nil, nil
		},
	}
	d := &mockDispatcher{
		publish: func(_ context.Context, task ledger.Task) error {
			published = append(published, task)
			return nil
		},
	}
	g := newG(l, d)

	err := g.OnComplete(context.Background(), "t1", nil)
	require.NoError(t, err)
	assert.Len(t, published, 2)
}

func TestOnComplete_SetSuccess_Error_Propagates(t *testing.T) {
	dbErr := errors.New("db error")
	l := &mockLedger{
		setSuccess: func(_ context.Context, _ string, _ any) (bool, []ledger.Task, error) {
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
	setRunningCalls := 0

	l := &mockLedger{
		setRunning: func(_ context.Context, _ string) (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			setRunningCalls++
			return setRunningCalls == 1, nil
		},
		setSuccess: func(_ context.Context, _ string, _ any) (bool, []ledger.Task, error) {
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

	task := ledger.Task{Key: "a", ID: "t1", Payload: emptyPayload()}
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
		setRunning: func(_ context.Context, _ string) (bool, error) { return true, nil },
		setSuccess: func(_ context.Context, _ string, _ any) (bool, []ledger.Task, error) {
			return true, []ledger.Task{{Key: "b", ID: "t2", BlueprintName: "test_after", Payload: emptyPayload()}}, nil
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

	err := invokeHandler(g, ledger.Task{Key: "a", ID: "t1", BlueprintName: "test_after", Payload: emptyPayload()})
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
		setRunning: func(_ context.Context, _ string) (bool, error) { return true, nil },
		setSuccess: func(_ context.Context, _ string, _ any) (bool, []ledger.Task, error) {
			return true, []ledger.Task{{Key: "b", ID: "t2", BlueprintName: "test_after_intake", Payload: emptyPayload()}}, nil
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

	err := invokeHandler(g, ledger.Task{Key: "a", ID: "t1", BlueprintName: "test_after_intake", Payload: emptyPayload()})
	require.NoError(t, err)
	assert.True(t, gatherCalled, "GatherDepResults should be called when Intake is present alongside After")
}
