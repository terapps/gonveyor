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
	"github.com/terapps/gonveyor/store"
	"github.com/terapps/gonveyor/transport"
)

// --- mocks ---

type mockStore struct {
	setRunning       func(ctx context.Context, taskID string) (bool, error)
	setSuccess       func(ctx context.Context, taskID string, result any) (bool, error)
	setFailed        func(ctx context.Context, taskID string, err error) error
	setDispatched    func(ctx context.Context, taskID string) (bool, error)
	next             func(ctx context.Context, taskID string) ([]store.Task, error)
	gatherDepResults func(ctx context.Context, taskID string) (map[string][]json.RawMessage, error)
}

func (m *mockStore) SetRunning(ctx context.Context, taskID string) (bool, error) {
	return m.setRunning(ctx, taskID)
}
func (m *mockStore) SetSuccess(ctx context.Context, taskID string, result any) (bool, error) {
	return m.setSuccess(ctx, taskID, result)
}
func (m *mockStore) SetFailed(ctx context.Context, taskID string, err error) error {
	return m.setFailed(ctx, taskID, err)
}
func (m *mockStore) SetDispatched(ctx context.Context, taskID string) (bool, error) {
	return m.setDispatched(ctx, taskID)
}
func (m *mockStore) Next(ctx context.Context, taskID string) ([]store.Task, error) {
	return m.next(ctx, taskID)
}
func (m *mockStore) GatherDepResults(ctx context.Context, taskID string) (map[string][]json.RawMessage, error) {
	return m.gatherDepResults(ctx, taskID)
}
func (m *mockStore) RenewLock(_ context.Context, _ string) error                    { return nil }
func (m *mockStore) CreateBlueprint(_ context.Context, _ store.BlueprintManifest) error { return nil }
func (m *mockStore) GetBlueprint(_ context.Context, _ string) (store.BlueprintManifest, error) {
	return store.BlueprintManifest{}, nil
}
func (m *mockStore) GetTask(_ context.Context, _ string) (store.Task, error) { return store.Task{}, nil }
func (m *mockStore) Pending(_ context.Context, _ string) ([]store.Task, error) { return nil, nil }

type mockDispatcher struct {
	publish func(ctx context.Context, task store.Task) error
}

func (m *mockDispatcher) Publish(ctx context.Context, task store.Task) error {
	if m.publish != nil {
		return m.publish(ctx, task)
	}
	return nil
}
func (m *mockDispatcher) Close() error { return nil }

type mockWorker struct{}

func (m *mockWorker) Listen(_ context.Context, _ transport.HandlerFunc) error { return nil }
func (m *mockWorker) Close() error                                             { return nil }

// --- helpers ---

type inA struct{}
type outA struct{}

var stationA = blueprint.Define[inA, outA]("a")

func newG(s store.Store, d transport.Dispatcher) *Gonveyor {
	return NewGonveyor(s, d, &mockWorker{})
}

func emptyPayload() []byte { b, _ := json.Marshal(inA{}); return b }

func invokeHandler(g *Gonveyor, task store.Task) error {
	_, err := g.handler()(context.Background(), task)
	return err
}

// --- handler() tests ---

func TestHandler_SetRunning_False_Bails(t *testing.T) {
	handlerCalled := false
	s := &mockStore{
		setRunning: func(_ context.Context, _ string) (bool, error) { return false, nil },
	}
	g := newG(s, &mockDispatcher{})
	g.RegisterHandler(stationA, Handle(stationA, func(_ context.Context, _ inA) (outA, error) {
		handlerCalled = true
		return outA{}, nil
	}))

	err := invokeHandler(g, store.Task{Key: "a", ID: "t1", Payload: emptyPayload()})
	assert.NoError(t, err)
	assert.False(t, handlerCalled)
}

func TestHandler_NoHandler_ReturnsError(t *testing.T) {
	g := newG(&mockStore{}, &mockDispatcher{})

	err := invokeHandler(g, store.Task{Key: "unknown", ID: "t1"})
	assert.ErrorContains(t, err, "no handler registered")
}

func TestHandler_HandlerError_CallsSetFailed(t *testing.T) {
	handlerErr := errors.New("boom")
	var failedID string
	var failedErr error

	s := &mockStore{
		setRunning: func(_ context.Context, _ string) (bool, error) { return true, nil },
		setFailed: func(_ context.Context, taskID string, err error) error {
			failedID = taskID
			failedErr = err
			return nil
		},
	}
	g := newG(s, &mockDispatcher{})
	g.RegisterHandler(stationA, Handle(stationA, func(_ context.Context, _ inA) (outA, error) {
		return outA{}, handlerErr
	}))

	err := invokeHandler(g, store.Task{Key: "a", ID: "t1", Payload: emptyPayload()})
	assert.ErrorIs(t, err, handlerErr)
	assert.Equal(t, "t1", failedID)
	assert.ErrorIs(t, failedErr, handlerErr)
}

func TestHandler_SetFailed_Fails_OriginalErrStillReturned(t *testing.T) {
	handlerErr := errors.New("handler error")

	s := &mockStore{
		setRunning: func(_ context.Context, _ string) (bool, error) { return true, nil },
		setFailed:  func(_ context.Context, _ string, _ error) error { return errors.New("db down") },
	}
	g := newG(s, &mockDispatcher{})
	g.RegisterHandler(stationA, Handle(stationA, func(_ context.Context, _ inA) (outA, error) {
		return outA{}, handlerErr
	}))

	err := invokeHandler(g, store.Task{Key: "a", ID: "t1", Payload: emptyPayload()})
	assert.ErrorIs(t, err, handlerErr)
}

func TestHandler_Success_PublishesNextTask(t *testing.T) {
	nextTask := store.Task{Key: "b", ID: "t2"}
	var published []store.Task

	s := &mockStore{
		setRunning:       func(_ context.Context, _ string) (bool, error) { return true, nil },
		setSuccess:       func(_ context.Context, _ string, _ any) (bool, error) { return true, nil },
		next:             func(_ context.Context, _ string) ([]store.Task, error) { return []store.Task{nextTask}, nil },
		setDispatched:    func(_ context.Context, _ string) (bool, error) { return true, nil },
		gatherDepResults: func(_ context.Context, _ string) (map[string][]json.RawMessage, error) { return nil, nil },
	}
	d := &mockDispatcher{
		publish: func(_ context.Context, task store.Task) error {
			published = append(published, task)
			return nil
		},
	}
	g := newG(s, d)
	g.RegisterHandler(stationA, Handle(stationA, func(_ context.Context, _ inA) (outA, error) {
		return outA{}, nil
	}))

	err := invokeHandler(g, store.Task{Key: "a", ID: "t1", Payload: emptyPayload()})
	require.NoError(t, err)
	require.Len(t, published, 1)
	assert.Equal(t, "t2", published[0].ID)
}

// --- OnComplete tests ---

func TestOnComplete_SetSuccess_False_Bails(t *testing.T) {
	nextCalled := false
	s := &mockStore{
		setSuccess: func(_ context.Context, _ string, _ any) (bool, error) { return false, nil },
		next: func(_ context.Context, _ string) ([]store.Task, error) {
			nextCalled = true
			return nil, nil
		},
	}
	g := newG(s, &mockDispatcher{})

	err := g.OnComplete(context.Background(), "t1", nil)
	assert.NoError(t, err)
	assert.False(t, nextCalled)
}

func TestOnComplete_SetDispatched_False_SkipsPublish(t *testing.T) {
	var published []store.Task
	s := &mockStore{
		setSuccess:    func(_ context.Context, _ string, _ any) (bool, error) { return true, nil },
		next:          func(_ context.Context, _ string) ([]store.Task, error) { return []store.Task{{Key: "b", ID: "t2"}}, nil },
		setDispatched: func(_ context.Context, _ string) (bool, error) { return false, nil },
	}
	d := &mockDispatcher{
		publish: func(_ context.Context, task store.Task) error {
			published = append(published, task)
			return nil
		},
	}
	g := newG(s, d)

	err := g.OnComplete(context.Background(), "t1", nil)
	assert.NoError(t, err)
	assert.Empty(t, published)
}

// --- Race test ---

func TestHandler_Race_OnlyOneWins(t *testing.T) {
	var mu sync.Mutex
	handlerCalls := 0
	setRunningCalls := 0

	s := &mockStore{
		setRunning: func(_ context.Context, _ string) (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			setRunningCalls++
			return setRunningCalls == 1, nil
		},
		setSuccess:       func(_ context.Context, _ string, _ any) (bool, error) { return true, nil },
		next:             func(_ context.Context, _ string) ([]store.Task, error) { return nil, nil },
		setDispatched:    func(_ context.Context, _ string) (bool, error) { return true, nil },
		gatherDepResults: func(_ context.Context, _ string) (map[string][]json.RawMessage, error) { return nil, nil },
	}
	g := newG(s, &mockDispatcher{})
	g.RegisterHandler(stationA, Handle(stationA, func(_ context.Context, _ inA) (outA, error) {
		mu.Lock()
		handlerCalls++
		mu.Unlock()
		return outA{}, nil
	}))

	task := store.Task{Key: "a", ID: "t1", Payload: emptyPayload()}
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
