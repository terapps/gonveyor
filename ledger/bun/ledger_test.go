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
	bunledger "github.com/terapps/gonveyor/ledger/bun"
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
		fmt.Printf("skipping ledger/bun integration tests: PG unavailable (%v)\n", err)
		os.Exit(0)
	}

	testDB = db
	code := m.Run()
	_ = db.Close()
	os.Exit(code)
}

func newLedger() *bunledger.Ledger {
	return bunledger.New(testDB)
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
	_, err := newLedger().CreateBlueprint(context.Background(), manifest)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = testDB.ExecContext(ctx, `DELETE FROM node_heartbeats WHERE node_id IN (SELECT id FROM blueprint_nodes WHERE blueprint_id = $1)`, manifest.Blueprint.ID)
		_, _ = testDB.ExecContext(ctx, `DELETE FROM node_events WHERE blueprint_id = $1`, manifest.Blueprint.ID)
		_, _ = testDB.ExecContext(ctx, `DELETE FROM blueprint_node_dependencies WHERE node_id IN (SELECT id FROM blueprint_nodes WHERE blueprint_id = $1)`, manifest.Blueprint.ID)
		_, _ = testDB.ExecContext(ctx, `DELETE FROM blueprint_nodes WHERE blueprint_id = $1`, manifest.Blueprint.ID)
		_, _ = testDB.ExecContext(ctx, `DELETE FROM blueprints WHERE id = $1`, manifest.Blueprint.ID)
	})
}

func simpleManifest() ledger.BlueprintManifest {
	bpID := uuid()
	nodeID := uuid()
	return ledger.BlueprintManifest{
		Blueprint: ledger.Blueprint{ID: bpID, Name: "test"},
		Nodes: []ledger.Node{
			{ID: nodeID, BlueprintID: bpID, Key: "a", Payload: json.RawMessage(`{}`)},
		},
	}
}

func chainManifest() (ledger.BlueprintManifest, string, string) {
	bpID := uuid()
	n1ID, n2ID := uuid(), uuid()
	m := ledger.BlueprintManifest{
		Blueprint: ledger.Blueprint{ID: bpID, Name: "test"},
		Nodes: []ledger.Node{
			{ID: n1ID, BlueprintID: bpID, Key: "a", Payload: json.RawMessage(`{}`)},
			{ID: n2ID, BlueprintID: bpID, Key: "b", Payload: json.RawMessage(`{}`)},
		},
		NodeDependencies: []ledger.NodeDependency{{NodeID: n2ID, DependsOnID: n1ID}},
	}
	return m, n1ID, n2ID
}

func countEvents(t *testing.T, nodeID, eventType string) int {
	t.Helper()
	var n int
	err := testDB.NewSelect().
		TableExpr("node_events").
		ColumnExpr("COUNT(*)").
		Where("node_id = ?", nodeID).
		Where("type = ?", eventType).
		Scan(context.Background(), &n)
	require.NoError(t, err)
	return n
}

// --- CreateBlueprint ---

func TestCreateBlueprint_ReturnsRootNodes(t *testing.T) {
	m, n1ID, n2ID := chainManifest()
	nodes, err := newLedger().CreateBlueprint(context.Background(), m)
	require.NoError(t, err)

	defer func() {
		ctx := context.Background()
		_, _ = testDB.ExecContext(ctx, `DELETE FROM node_events WHERE blueprint_id = $1`, m.Blueprint.ID)
		_, _ = testDB.ExecContext(ctx, `DELETE FROM blueprint_node_dependencies WHERE node_id IN (SELECT id FROM blueprint_nodes WHERE blueprint_id = $1)`, m.Blueprint.ID)
		_, _ = testDB.ExecContext(ctx, `DELETE FROM blueprint_nodes WHERE blueprint_id = $1`, m.Blueprint.ID)
		_, _ = testDB.ExecContext(ctx, `DELETE FROM blueprints WHERE id = $1`, m.Blueprint.ID)
	}()

	require.Len(t, nodes, 1)
	assert.Equal(t, n1ID, nodes[0].ID)
	assert.Equal(t, 0, countEvents(t, n2ID, "node_dispatched"), "downstream node must not be dispatched yet")
	assert.Equal(t, 1, countEvents(t, n1ID, "node_dispatched"), "root node must have a dispatch event")
}

// --- Claim ---

func TestClaim_InsertsStartedEvent(t *testing.T) {
	m := simpleManifest()
	seed(t, m)

	_, ok, err := newLedger().Claim(context.Background(), m.Nodes[0].ID)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 1, countEvents(t, m.Nodes[0].ID, "node_started"))
}

func TestClaim_Keepalive_InsertsHeartbeat(t *testing.T) {
	m := simpleManifest()
	seed(t, m)
	ctx := context.Background()

	keepalive, ok, err := newLedger().Claim(ctx, m.Nodes[0].ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, keepalive())

	var n int
	err = testDB.NewSelect().
		TableExpr("node_heartbeats").
		ColumnExpr("COUNT(*)").
		Where("node_id = ?", m.Nodes[0].ID).
		Scan(ctx, &n)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}

// --- RecordCompleted ---

func TestRecordCompleted_ReturnsTrue_InsertsEvent(t *testing.T) {
	m := simpleManifest()
	seed(t, m)
	l := newLedger()
	ctx := context.Background()

	ok, nodes, err := l.RecordCompleted(ctx, m.Nodes[0].ID, map[string]string{"k": "v"})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Empty(t, nodes)
	assert.Equal(t, 1, countEvents(t, m.Nodes[0].ID, "node_completed"))
}

func TestRecordCompleted_Idempotent_ReturnsFalse(t *testing.T) {
	m := simpleManifest()
	seed(t, m)
	l := newLedger()
	ctx := context.Background()

	_, _, _ = l.RecordCompleted(ctx, m.Nodes[0].ID, nil)
	ok, nodes, err := l.RecordCompleted(ctx, m.Nodes[0].ID, nil)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, nodes)
}

func TestRecordCompleted_UnblocksDownstream(t *testing.T) {
	m, n1ID, n2ID := chainManifest()
	seed(t, m)
	l := newLedger()
	ctx := context.Background()

	ok, unblocked, err := l.RecordCompleted(ctx, n1ID, nil)
	require.NoError(t, err)
	assert.True(t, ok)
	require.Len(t, unblocked, 1)
	assert.Equal(t, n2ID, unblocked[0].ID)
	assert.Equal(t, 1, countEvents(t, n2ID, "node_dispatched"))
}

func TestRecordCompleted_AlreadyUnblockedByOtherDep_NoDoubleDispatch(t *testing.T) {
	// Diamond: a -> c, b -> c. Both a and b complete; c must be dispatched only once.
	bpID := uuid()
	nAID, nBID, nCID := uuid(), uuid(), uuid()
	m := ledger.BlueprintManifest{
		Blueprint: ledger.Blueprint{ID: bpID, Name: "test"},
		Nodes: []ledger.Node{
			{ID: nAID, BlueprintID: bpID, Key: "a", Payload: json.RawMessage(`{}`)},
			{ID: nBID, BlueprintID: bpID, Key: "b", Payload: json.RawMessage(`{}`)},
			{ID: nCID, BlueprintID: bpID, Key: "c", Payload: json.RawMessage(`{}`)},
		},
		NodeDependencies: []ledger.NodeDependency{
			{NodeID: nCID, DependsOnID: nAID},
			{NodeID: nCID, DependsOnID: nBID},
		},
	}
	seed(t, m)
	l := newLedger()
	ctx := context.Background()

	// Complete A: c still has 1 pending dep (b), not dispatched yet.
	ok, unblocked, err := l.RecordCompleted(ctx, nAID, nil)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Empty(t, unblocked)

	// Complete B: c is now unblocked.
	ok, unblocked, err = l.RecordCompleted(ctx, nBID, nil)
	require.NoError(t, err)
	assert.True(t, ok)
	require.Len(t, unblocked, 1)
	assert.Equal(t, nCID, unblocked[0].ID)
	assert.Equal(t, 1, countEvents(t, nCID, "node_dispatched"), "c must be dispatched exactly once")
}

// --- RecordFailed ---

func TestRecordFailed_InsertsEvent(t *testing.T) {
	m := simpleManifest()
	seed(t, m)

	require.NoError(t, newLedger().RecordFailed(context.Background(), m.Nodes[0].ID, fmt.Errorf("oops")))
	assert.Equal(t, 1, countEvents(t, m.Nodes[0].ID, "node_failed"))
}

// --- GatherDepResults ---

func TestGatherDepResults_ReturnsOutputByKey(t *testing.T) {
	m, n1ID, n2ID := chainManifest()
	seed(t, m)
	l := newLedger()
	ctx := context.Background()

	_, _, _ = l.RecordCompleted(ctx, n1ID, map[string]string{"x": "1"})

	results, err := l.GatherDepResults(ctx, n2ID)
	require.NoError(t, err)
	require.Contains(t, results, "a")
	assert.Len(t, results["a"], 1)
}

func TestGatherDepResults_MultiOutput_Split(t *testing.T) {
	bpID := uuid()
	n1aID, n1bID, n2ID := uuid(), uuid(), uuid()
	m := ledger.BlueprintManifest{
		Blueprint: ledger.Blueprint{ID: bpID, Name: "test"},
		Nodes: []ledger.Node{
			{ID: n1aID, BlueprintID: bpID, Key: "a", Payload: json.RawMessage(`{}`)},
			{ID: n1bID, BlueprintID: bpID, Key: "a", Payload: json.RawMessage(`{}`)},
			{ID: n2ID, BlueprintID: bpID, Key: "b", Payload: json.RawMessage(`{}`)},
		},
		NodeDependencies: []ledger.NodeDependency{
			{NodeID: n2ID, DependsOnID: n1aID},
			{NodeID: n2ID, DependsOnID: n1bID},
		},
	}
	seed(t, m)
	l := newLedger()
	ctx := context.Background()

	for _, id := range []string{n1aID, n1bID} {
		_, _, _ = l.RecordCompleted(ctx, id, map[string]string{"v": id})
	}

	results, err := l.GatherDepResults(ctx, n2ID)
	require.NoError(t, err)
	assert.Len(t, results["a"], 2)
}
