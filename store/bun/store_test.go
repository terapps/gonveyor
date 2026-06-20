package bun_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/terapps/gonveyor/ledger"
	bunstore "github.com/terapps/gonveyor/store/bun"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

const defaultDSN = "postgres://gonveyor:gonveyor@localhost:5432/gonveyor?sslmode=disable"

var testDB *bun.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		dsn = defaultDSN
	}

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn)))
	db := bun.NewDB(sqldb, pgdialect.New())

	if err := db.Ping(); err != nil {
		fmt.Printf("skipping store/bun integration tests: PG unavailable (%v)\n", err)
		os.Exit(0)
	}

	testDB = db
	code := m.Run()
	_ = db.Close()
	os.Exit(code)
}

func newStore() *bunstore.Store {
	return bunstore.New(testDB)
}

func uuid() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func seed(t *testing.T, manifest ledger.BlueprintManifest) {
	t.Helper()
	_, err := newStore().CreateBlueprint(context.Background(), manifest)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = testDB.ExecContext(ctx, `DELETE FROM task_heartbeats WHERE task_id IN (SELECT id FROM blueprint_tasks WHERE blueprint_id = $1)`, manifest.Blueprint.ID)
		_, _ = testDB.ExecContext(ctx, `DELETE FROM task_events WHERE blueprint_id = $1`, manifest.Blueprint.ID)
		_, _ = testDB.ExecContext(ctx, `DELETE FROM blueprint_task_dependencies WHERE task_id IN (SELECT id FROM blueprint_tasks WHERE blueprint_id = $1)`, manifest.Blueprint.ID)
		_, _ = testDB.ExecContext(ctx, `DELETE FROM blueprint_tasks WHERE blueprint_id = $1`, manifest.Blueprint.ID)
		_, _ = testDB.ExecContext(ctx, `DELETE FROM blueprints WHERE id = $1`, manifest.Blueprint.ID)
	})
}

func simpleManifest() ledger.BlueprintManifest {
	bpID := uuid()
	taskID := uuid()
	return ledger.BlueprintManifest{
		Blueprint: ledger.Blueprint{ID: bpID, Name: "test"},
		Tasks: []ledger.Task{
			{ID: taskID, BlueprintID: bpID, Key: "a", Payload: json.RawMessage(`{}`)},
		},
	}
}

func chainManifest() (ledger.BlueprintManifest, string, string) {
	bpID := uuid()
	t1ID, t2ID := uuid(), uuid()
	m := ledger.BlueprintManifest{
		Blueprint: ledger.Blueprint{ID: bpID, Name: "test"},
		Tasks: []ledger.Task{
			{ID: t1ID, BlueprintID: bpID, Key: "a", Payload: json.RawMessage(`{}`)},
			{ID: t2ID, BlueprintID: bpID, Key: "b", Payload: json.RawMessage(`{}`)},
		},
		Dependencies: []ledger.TaskDependency{{TaskID: t2ID, DependsOnID: t1ID}},
	}
	return m, t1ID, t2ID
}

func countEvents(t *testing.T, taskID, eventType string) int {
	t.Helper()
	var n int
	err := testDB.NewSelect().
		TableExpr("task_events").
		ColumnExpr("COUNT(*)").
		Where("task_id = ?", taskID).
		Where("type = ?", eventType).
		Scan(context.Background(), &n)
	require.NoError(t, err)
	return n
}

// --- CreateBlueprint ---

func TestCreateBlueprint_ReturnsRootTasks(t *testing.T) {
	m, t1ID, t2ID := chainManifest()
	tasks, err := newStore().CreateBlueprint(context.Background(), m)
	require.NoError(t, err)

	defer func() {
		ctx := context.Background()
		_, _ = testDB.ExecContext(ctx, `DELETE FROM task_events WHERE blueprint_id = $1`, m.Blueprint.ID)
		_, _ = testDB.ExecContext(ctx, `DELETE FROM blueprint_task_dependencies WHERE task_id IN (SELECT id FROM blueprint_tasks WHERE blueprint_id = $1)`, m.Blueprint.ID)
		_, _ = testDB.ExecContext(ctx, `DELETE FROM blueprint_tasks WHERE blueprint_id = $1`, m.Blueprint.ID)
		_, _ = testDB.ExecContext(ctx, `DELETE FROM blueprints WHERE id = $1`, m.Blueprint.ID)
	}()

	require.Len(t, tasks, 1)
	assert.Equal(t, t1ID, tasks[0].ID)
	assert.Equal(t, 0, countEvents(t, t2ID, "task_dispatched"), "downstream task must not be dispatched yet")
	assert.Equal(t, 1, countEvents(t, t1ID, "task_dispatched"), "root task must have a dispatch event")
}

// --- SetDispatched ---

func TestSetDispatched_InsertsEvent(t *testing.T) {
	m := simpleManifest()
	seed(t, m)

	ok, err := newStore().SetDispatched(context.Background(), m.Tasks[0].ID)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 2, countEvents(t, m.Tasks[0].ID, "task_dispatched"))
}

// --- SetRunning ---

func TestSetRunning_InsertsEvent(t *testing.T) {
	m := simpleManifest()
	seed(t, m)

	ok, err := newStore().SetRunning(context.Background(), m.Tasks[0].ID)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 1, countEvents(t, m.Tasks[0].ID, "task_started"))
}

// --- SetSuccess ---

func TestSetSuccess_ReturnsTrue_InsertsEvent(t *testing.T) {
	m := simpleManifest()
	seed(t, m)
	s := newStore()
	ctx := context.Background()

	ok, tasks, err := s.SetSuccess(ctx, m.Tasks[0].ID, map[string]string{"k": "v"})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Empty(t, tasks)
	assert.Equal(t, 1, countEvents(t, m.Tasks[0].ID, "task_completed"))
}

func TestSetSuccess_Idempotent_ReturnsFalse(t *testing.T) {
	m := simpleManifest()
	seed(t, m)
	s := newStore()
	ctx := context.Background()

	_, _, _ = s.SetSuccess(ctx, m.Tasks[0].ID, nil)
	ok, tasks, err := s.SetSuccess(ctx, m.Tasks[0].ID, nil)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, tasks)
}

func TestSetSuccess_UnblocksDownstream(t *testing.T) {
	m, t1ID, t2ID := chainManifest()
	seed(t, m)
	s := newStore()
	ctx := context.Background()

	ok, unblocked, err := s.SetSuccess(ctx, t1ID, nil)
	require.NoError(t, err)
	assert.True(t, ok)
	require.Len(t, unblocked, 1)
	assert.Equal(t, t2ID, unblocked[0].ID)
	assert.Equal(t, 1, countEvents(t, t2ID, "task_dispatched"))
}

func TestSetSuccess_AlreadyUnblockedByOtherDep_NoDoubleDispatch(t *testing.T) {
	// Diamond: a -> c, b -> c. Both a and b complete; c must be dispatched only once.
	bpID := uuid()
	tAID, tBID, tCID := uuid(), uuid(), uuid()
	m := ledger.BlueprintManifest{
		Blueprint: ledger.Blueprint{ID: bpID, Name: "test"},
		Tasks: []ledger.Task{
			{ID: tAID, BlueprintID: bpID, Key: "a", Payload: json.RawMessage(`{}`)},
			{ID: tBID, BlueprintID: bpID, Key: "b", Payload: json.RawMessage(`{}`)},
			{ID: tCID, BlueprintID: bpID, Key: "c", Payload: json.RawMessage(`{}`)},
		},
		Dependencies: []ledger.TaskDependency{
			{TaskID: tCID, DependsOnID: tAID},
			{TaskID: tCID, DependsOnID: tBID},
		},
	}
	seed(t, m)
	s := newStore()
	ctx := context.Background()

	// Complete A: c still has 1 pending dep (b), not dispatched yet.
	ok, unblocked, err := s.SetSuccess(ctx, tAID, nil)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Empty(t, unblocked)

	// Complete B: c is now unblocked.
	ok, unblocked, err = s.SetSuccess(ctx, tBID, nil)
	require.NoError(t, err)
	assert.True(t, ok)
	require.Len(t, unblocked, 1)
	assert.Equal(t, tCID, unblocked[0].ID)
	assert.Equal(t, 1, countEvents(t, tCID, "task_dispatched"), "c must be dispatched exactly once")
}

// --- SetFailed ---

func TestSetFailed_InsertsEvent(t *testing.T) {
	m := simpleManifest()
	seed(t, m)

	require.NoError(t, newStore().SetFailed(context.Background(), m.Tasks[0].ID, fmt.Errorf("oops")))
	assert.Equal(t, 1, countEvents(t, m.Tasks[0].ID, "task_failed"))
}

// --- RenewLock ---

func TestRenewLock_InsertsHeartbeat(t *testing.T) {
	m := simpleManifest()
	seed(t, m)
	ctx := context.Background()

	require.NoError(t, newStore().RenewLock(ctx, m.Tasks[0].ID))

	var n int
	err := testDB.NewSelect().
		TableExpr("task_heartbeats").
		ColumnExpr("COUNT(*)").
		Where("task_id = ?", m.Tasks[0].ID).
		Scan(ctx, &n)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}

// --- GatherDepResults ---

func TestGatherDepResults_ReturnsOutputByKey(t *testing.T) {
	m, t1ID, t2ID := chainManifest()
	seed(t, m)
	s := newStore()
	ctx := context.Background()

	_, _, _ = s.SetSuccess(ctx, t1ID, map[string]string{"x": "1"})

	results, err := s.GatherDepResults(ctx, t2ID)
	require.NoError(t, err)
	require.Contains(t, results, "a")
	assert.Len(t, results["a"], 1)
}

func TestGatherDepResults_MultiOutput_Split(t *testing.T) {
	bpID := uuid()
	t1aID, t1bID, t2ID := uuid(), uuid(), uuid()
	m := ledger.BlueprintManifest{
		Blueprint: ledger.Blueprint{ID: bpID, Name: "test"},
		Tasks: []ledger.Task{
			{ID: t1aID, BlueprintID: bpID, Key: "a", Payload: json.RawMessage(`{}`)},
			{ID: t1bID, BlueprintID: bpID, Key: "a", Payload: json.RawMessage(`{}`)},
			{ID: t2ID, BlueprintID: bpID, Key: "b", Payload: json.RawMessage(`{}`)},
		},
		Dependencies: []ledger.TaskDependency{
			{TaskID: t2ID, DependsOnID: t1aID},
			{TaskID: t2ID, DependsOnID: t1bID},
		},
	}
	seed(t, m)
	s := newStore()
	ctx := context.Background()

	for _, id := range []string{t1aID, t1bID} {
		_, _, _ = s.SetSuccess(ctx, id, map[string]string{"v": id})
	}

	results, err := s.GatherDepResults(ctx, t2ID)
	require.NoError(t, err)
	assert.Len(t, results["a"], 2)
}
