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
	"github.com/terapps/gonveyor/store"
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

func seed(t *testing.T, manifest store.BlueprintManifest) {
	t.Helper()
	require.NoError(t, newStore().CreateBlueprint(context.Background(), manifest))
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = testDB.ExecContext(ctx, `DELETE FROM blueprint_task_dependencies WHERE task_id IN (SELECT id FROM blueprint_tasks WHERE blueprint_id = $1)`, manifest.Blueprint.ID)
		_, _ = testDB.ExecContext(ctx, `DELETE FROM blueprint_tasks WHERE blueprint_id = $1`, manifest.Blueprint.ID)
		_, _ = testDB.ExecContext(ctx, `DELETE FROM blueprints WHERE id = $1`, manifest.Blueprint.ID)
	})
}

func simpleManifest() store.BlueprintManifest {
	bpID := uuid()
	taskID := uuid()
	return store.BlueprintManifest{
		Blueprint: store.Blueprint{ID: bpID, Name: "test"},
		Tasks: []store.Task{
			{ID: taskID, BlueprintID: bpID, Key: "a", Status: store.TaskStatusPending, Payload: json.RawMessage(`{}`)},
		},
	}
}

func chainManifest() (store.BlueprintManifest, string, string) {
	bpID := uuid()
	t1ID, t2ID := uuid(), uuid()
	m := store.BlueprintManifest{
		Blueprint: store.Blueprint{ID: bpID, Name: "test"},
		Tasks: []store.Task{
			{ID: t1ID, BlueprintID: bpID, Key: "a", Status: store.TaskStatusPending, Payload: json.RawMessage(`{}`)},
			{ID: t2ID, BlueprintID: bpID, Key: "b", Status: store.TaskStatusPending, Payload: json.RawMessage(`{}`)},
		},
		Dependencies: []store.TaskDependency{{TaskID: t2ID, DependsOnID: t1ID}},
	}
	return m, t1ID, t2ID
}

// --- SetDispatched ---

func TestSetDispatched_FromPending_ReturnsTrue(t *testing.T) {
	m := simpleManifest()
	seed(t, m)

	ok, err := newStore().SetDispatched(context.Background(), m.Tasks[0].ID)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestSetDispatched_Idempotent_ReturnsFalse(t *testing.T) {
	m := simpleManifest()
	seed(t, m)
	s := newStore()
	ctx := context.Background()

	_, _ = s.SetDispatched(ctx, m.Tasks[0].ID)
	ok, err := s.SetDispatched(ctx, m.Tasks[0].ID)
	require.NoError(t, err)
	assert.False(t, ok)
}

// --- SetRunning ---

func TestSetRunning_FromDispatched_ReturnsTrue(t *testing.T) {
	m := simpleManifest()
	seed(t, m)
	s := newStore()
	ctx := context.Background()

	_, _ = s.SetDispatched(ctx, m.Tasks[0].ID)
	ok, err := s.SetRunning(ctx, m.Tasks[0].ID)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestSetRunning_FromPending_ReturnsFalse(t *testing.T) {
	m := simpleManifest()
	seed(t, m)

	ok, err := newStore().SetRunning(context.Background(), m.Tasks[0].ID)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestSetRunning_Idempotent_ReturnsFalse(t *testing.T) {
	m := simpleManifest()
	seed(t, m)
	s := newStore()
	ctx := context.Background()

	_, _ = s.SetDispatched(ctx, m.Tasks[0].ID)
	_, _ = s.SetRunning(ctx, m.Tasks[0].ID)
	ok, err := s.SetRunning(ctx, m.Tasks[0].ID)
	require.NoError(t, err)
	assert.False(t, ok)
}

// --- SetSuccess ---

func TestSetSuccess_FromRunning_ReturnsTrue(t *testing.T) {
	m := simpleManifest()
	seed(t, m)
	s := newStore()
	ctx := context.Background()

	_, _ = s.SetDispatched(ctx, m.Tasks[0].ID)
	_, _ = s.SetRunning(ctx, m.Tasks[0].ID)
	ok, err := s.SetSuccess(ctx, m.Tasks[0].ID, map[string]string{"k": "v"})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestSetSuccess_Idempotent_ReturnsFalse(t *testing.T) {
	m := simpleManifest()
	seed(t, m)
	s := newStore()
	ctx := context.Background()

	_, _ = s.SetDispatched(ctx, m.Tasks[0].ID)
	_, _ = s.SetRunning(ctx, m.Tasks[0].ID)
	_, _ = s.SetSuccess(ctx, m.Tasks[0].ID, nil)
	ok, err := s.SetSuccess(ctx, m.Tasks[0].ID, nil)
	require.NoError(t, err)
	assert.False(t, ok)
}

// --- SetFailed ---

func TestSetFailed_FromRunning_Succeeds(t *testing.T) {
	m := simpleManifest()
	seed(t, m)
	s := newStore()
	ctx := context.Background()

	_, _ = s.SetDispatched(ctx, m.Tasks[0].ID)
	_, _ = s.SetRunning(ctx, m.Tasks[0].ID)
	require.NoError(t, s.SetFailed(ctx, m.Tasks[0].ID, fmt.Errorf("oops")))

	task, err := s.GetTask(ctx, m.Tasks[0].ID)
	require.NoError(t, err)
	assert.Equal(t, store.TaskStatusFailed, task.Status)
	assert.Equal(t, "oops", task.Error)
}

func TestSetFailed_FromPending_NoOp(t *testing.T) {
	m := simpleManifest()
	seed(t, m)
	s := newStore()
	ctx := context.Background()

	require.NoError(t, s.SetFailed(ctx, m.Tasks[0].ID, fmt.Errorf("oops")))

	task, err := s.GetTask(ctx, m.Tasks[0].ID)
	require.NoError(t, err)
	assert.Equal(t, store.TaskStatusPending, task.Status)
}

// --- RenewLock ---

func TestRenewLock_WhileRunning_UpdatesLockedUntil(t *testing.T) {
	m := simpleManifest()
	seed(t, m)
	s := newStore()
	ctx := context.Background()

	_, _ = s.SetDispatched(ctx, m.Tasks[0].ID)
	_, _ = s.SetRunning(ctx, m.Tasks[0].ID)
	require.NoError(t, s.RenewLock(ctx, m.Tasks[0].ID))

	var lockedUntil *string
	err := testDB.NewSelect().
		TableExpr("blueprint_tasks").
		ColumnExpr("locked_until::text AS locked_until").
		Where("id = ?", m.Tasks[0].ID).
		Scan(ctx, &lockedUntil)
	require.NoError(t, err)
	assert.NotNil(t, lockedUntil)
}

// --- Next ---

func TestNext_DepSuccess_UnblocksTask(t *testing.T) {
	m, t1ID, t2ID := chainManifest()
	seed(t, m)
	s := newStore()
	ctx := context.Background()

	_, _ = s.SetDispatched(ctx, t1ID)
	_, _ = s.SetRunning(ctx, t1ID)
	_, _ = s.SetSuccess(ctx, t1ID, nil)

	next, err := s.Next(ctx, t1ID)
	require.NoError(t, err)
	require.Len(t, next, 1)
	assert.Equal(t, t2ID, next[0].ID)
}

func TestNext_DepPending_DoesNotUnblock(t *testing.T) {
	m, t1ID, _ := chainManifest()
	seed(t, m)

	next, err := newStore().Next(context.Background(), t1ID)
	require.NoError(t, err)
	assert.Empty(t, next)
}

func TestNext_DepFailed_DoesNotUnblock(t *testing.T) {
	m, t1ID, _ := chainManifest()
	seed(t, m)
	s := newStore()
	ctx := context.Background()

	_, _ = s.SetDispatched(ctx, t1ID)
	_, _ = s.SetRunning(ctx, t1ID)
	_ = s.SetFailed(ctx, t1ID, fmt.Errorf("err"))

	next, err := s.Next(ctx, t1ID)
	require.NoError(t, err)
	assert.Empty(t, next)
}

// --- GatherDepResults ---

func TestGatherDepResults_ReturnsResultByKey(t *testing.T) {
	m, t1ID, t2ID := chainManifest()
	seed(t, m)
	s := newStore()
	ctx := context.Background()

	_, _ = s.SetDispatched(ctx, t1ID)
	_, _ = s.SetRunning(ctx, t1ID)
	_, _ = s.SetSuccess(ctx, t1ID, map[string]string{"x": "1"})

	results, err := s.GatherDepResults(ctx, t2ID)
	require.NoError(t, err)
	require.Contains(t, results, "a")
	assert.Len(t, results["a"], 1)
}

func TestGatherDepResults_MultiOutput_Split(t *testing.T) {
	bpID := uuid()
	t1aID, t1bID, t2ID := uuid(), uuid(), uuid()
	m := store.BlueprintManifest{
		Blueprint: store.Blueprint{ID: bpID, Name: "test"},
		Tasks: []store.Task{
			{ID: t1aID, BlueprintID: bpID, Key: "a", Status: store.TaskStatusPending, Payload: json.RawMessage(`{}`)},
			{ID: t1bID, BlueprintID: bpID, Key: "a", Status: store.TaskStatusPending, Payload: json.RawMessage(`{}`)},
			{ID: t2ID, BlueprintID: bpID, Key: "b", Status: store.TaskStatusPending, Payload: json.RawMessage(`{}`)},
		},
		Dependencies: []store.TaskDependency{
			{TaskID: t2ID, DependsOnID: t1aID},
			{TaskID: t2ID, DependsOnID: t1bID},
		},
	}
	seed(t, m)
	s := newStore()
	ctx := context.Background()

	for _, id := range []string{t1aID, t1bID} {
		_, _ = s.SetDispatched(ctx, id)
		_, _ = s.SetRunning(ctx, id)
		_, _ = s.SetSuccess(ctx, id, map[string]string{"v": id})
	}

	results, err := s.GatherDepResults(ctx, t2ID)
	require.NoError(t, err)
	assert.Len(t, results["a"], 2)
}
