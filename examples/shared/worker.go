// Package shared provides common setup helpers for gonveyor examples.
package shared

import (
	"database/sql"
	"os"

	"github.com/terapps/gonveyor"
	"github.com/terapps/gonveyor/transport/amqp"
	bunstore "github.com/terapps/gonveyor/store/bun"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

const (
	defaultAMQPURL     = "amqp://gonveyor:gonveyor@localhost:5672/"
	defaultPostgresDSN = "postgres://gonveyor:gonveyor@localhost:5432/gonveyor?sslmode=disable"
)

// Config holds the connection parameters for a gonveyor worker.
type Config struct {
	QueueName string
	QueueOpts []amqp.QueueOption
}

// BuildGonductor wires up a bun store and AMQP dispatcher from the given config.
func BuildGonductor(cfg Config) (*gonveyor.Gonductor, func(), error) {
	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(envOr("POSTGRES_DSN", defaultPostgresDSN))))
	db := bun.NewDB(sqldb, pgdialect.New())

	queue, err := amqp.NewQueue(cfg.QueueName, cfg.QueueOpts...)
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}

	store := bunstore.New(db)

	conn, err := amqp.Dial(envOr("AMQP_URL", defaultAMQPURL))
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}

	dispatcher, err := conn.NewDispatcher(queue)
	if err != nil {
		_ = conn.Close()
		_ = db.Close()
		return nil, nil, err
	}

	cleanup := func() {
		_ = dispatcher.Close()
		_ = conn.Close()
		_ = db.Close()
	}

	return gonveyor.NewGonductor(store, dispatcher), cleanup, nil
}

// Build wires up a bun store, AMQP dispatcher, and AMQP worker from the given config.
func Build(cfg Config) (*gonveyor.Gonveyor, func(), error) {
	amqpURL := envOr("AMQP_URL", defaultAMQPURL)
	postgresDSN := envOr("POSTGRES_DSN", defaultPostgresDSN)

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(postgresDSN)))
	db := bun.NewDB(sqldb, pgdialect.New())

	queue, err := amqp.NewQueue(cfg.QueueName, cfg.QueueOpts...)
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}

	store := bunstore.New(db)

	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}

	dispatcher, err := conn.NewDispatcher(queue)
	if err != nil {
		_ = conn.Close()
		_ = db.Close()
		return nil, nil, err
	}

	worker, err := conn.NewWorker(queue)
	if err != nil {
		_ = dispatcher.Close()
		_ = conn.Close()
		_ = db.Close()
		return nil, nil, err
	}

	cleanup := func() {
		_ = worker.Close()
		_ = dispatcher.Close()
		_ = conn.Close()
		_ = db.Close()
	}

	return gonveyor.NewGonveyor(store, dispatcher, worker), cleanup, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}
