package amqp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	amqp091 "github.com/rabbitmq/amqp091-go"
	"github.com/terapps/gonveyor"
	"github.com/terapps/gonveyor/ledger"
	"github.com/terapps/gonveyor/transport"
)

var _ transport.Worker = (*Worker)(nil)

// WorkerConfig holds tuning parameters for the AMQP worker.
type WorkerConfig struct {
	Prefetch    int
	Concurrency int
	Tag         string
}

// Worker is an AMQP consumer that dispatches tasks to a HandlerFunc.
type Worker struct {
	conn       *Connection
	ch         *amqp091.Channel
	queue      *Queue
	config     WorkerConfig
	requeueFn  func(error) bool
	retryFn    func() func(context.Context) error
	shutdownFn func(context.Context) (context.Context, context.CancelFunc)
}

// WorkerOption is a functional option for Worker.
type WorkerOption = func(*Worker)

// WithPrefetch sets the number of messages to prefetch from the broker.
func WithPrefetch(n int) WorkerOption { return func(w *Worker) { w.config.Prefetch = n } }

// WithConcurrency sets the number of goroutines processing messages concurrently.
func WithConcurrency(n int) WorkerOption { return func(w *Worker) { w.config.Concurrency = n } }

// WithTag sets the AMQP consumer tag.
func WithTag(tag string) WorkerOption { return func(w *Worker) { w.config.Tag = tag } }

// WithRequeueFn sets the function that decides whether to requeue a message after a handler failure.
func WithRequeueFn(fn func(error) bool) WorkerOption {
	return func(w *Worker) { w.requeueFn = fn }
}

// WithRetryFn sets the factory called to create a fresh retry closure on each reconnection attempt.
func WithRetryFn(fn func() func(context.Context) error) WorkerOption {
	return func(w *Worker) { w.retryFn = fn }
}

// WithShutdownFn sets the function that returns a context cancelled on shutdown signals.
func WithShutdownFn(fn func(context.Context) (context.Context, context.CancelFunc)) WorkerOption {
	return func(w *Worker) { w.shutdownFn = fn }
}

// Listen starts consuming messages and dispatches each to handler until ctx is cancelled.
func (w *Worker) Listen(ctx context.Context, handler transport.HandlerFunc) error {
	ctx, stop := w.shutdownFn(ctx)
	defer stop()

	for {
		msgs, err := w.ch.ConsumeWithContext(ctx, w.queue.Name, w.config.Tag, false, false, false, false, nil)
		if err != nil {
			if err := w.recover(ctx); err != nil {
				return err
			}
			continue
		}

		w.consume(ctx, msgs, handler)

		if ctx.Err() != nil {
			gonveyor.Logger.Info("shutting down...")
			return ctx.Err()
		}

		if err := w.recover(ctx); err != nil {
			return err
		}
	}
}

// Close shuts down the AMQP channel.
func (w *Worker) Close() error {
	return w.ch.Close()
}

func (w *Worker) openChannel() error {
	ch, err := w.conn.channel()
	if err != nil {
		return err
	}

	if err := ch.Qos(w.config.Prefetch, 0, false); err != nil {
		_ = ch.Close()
		return err
	}

	if err := w.declareQueue(ch); err != nil {
		_ = ch.Close()
		return err
	}

	w.ch = ch
	return nil
}

func (w *Worker) declareQueue(ch *amqp091.Channel) error {
	q := w.queue
	args := amqp091.Table{}

	if q.DeadLetter != "" {
		args["x-dead-letter-exchange"] = q.DeadLetter
		if err := ch.ExchangeDeclare(q.DeadLetter, amqp091.ExchangeDirect, q.Durable, false, false, false, nil); err != nil {
			return err
		}

		dlq := q.Name + ".dlq"
		if _, err := ch.QueueDeclare(dlq, q.Durable, false, false, false, nil); err != nil {
			return err
		}

		if err := ch.QueueBind(dlq, q.Name, q.DeadLetter, false, nil); err != nil {
			return err
		}
	}

	if _, err := ch.QueueDeclare(q.Name, q.Durable, q.AutoDelete, false, false, args); err != nil {
		return err
	}

	if q.ExchangeType == ExchangeTopic {
		if err := ch.ExchangeDeclare(q.Exchange, string(q.ExchangeType), q.Durable, false, false, false, nil); err != nil {
			return err
		}

		if err := ch.QueueBind(q.Name, q.RoutingKey, q.Exchange, false, nil); err != nil {
			return err
		}
	}

	return nil
}

func (w *Worker) recover(ctx context.Context) error {
	_ = w.ch.Close()

	retry := w.retryFn()

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err := w.conn.reconnect(); err != nil {
			gonveyor.Logger.Error("connection failed", "err", err)

			if err := retry(ctx); err != nil {
				return err
			}

			continue
		}

		if err := w.openChannel(); err != nil {
			gonveyor.Logger.Error("channel open failed", "err", err)

			if err := retry(ctx); err != nil {
				return err
			}

			continue
		}

		gonveyor.Logger.Info("reconnected to broker")
		return nil
	}
}

func (w *Worker) consume(ctx context.Context, msgs <-chan amqp091.Delivery, handler transport.HandlerFunc) {
	var wg sync.WaitGroup

	for range w.config.Concurrency {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for d := range msgs {
				w.handle(ctx, d, handler)
			}
		}()
	}

	wg.Wait()
}

func (w *Worker) handle(ctx context.Context, d amqp091.Delivery, handler transport.HandlerFunc) {
	if ctx.Err() != nil {
		_ = d.Nack(false, true)
		return
	}

	var task ledger.Task
	if err := json.Unmarshal(d.Body, &task); err != nil {
		gonveyor.Logger.Error("failed to unmarshal task", "err", err)
		_ = d.Nack(false, false)
		return
	}

	// ack is called by the handler after claiming the task (post-SetRunning).
	// Once called, we never NACK — crashes after this point are handled by the reaper.
	var acked bool
	ack := sync.OnceFunc(func() {
		acked = true
		_ = d.Ack(false)
	})

	if err := w.call(ctx, task, handler, ack); err != nil {
		if !acked {
			gonveyor.Logger.Error("handler failed before claim", "key", task.Key, "err", err)
			_ = d.Nack(false, w.requeueFn(err))
		}
		return
	}
	if !acked {
		_ = d.Ack(false)
	}
}

// call invokes the handler with a non-cancellable context so that shutdown signals
// do not interrupt a task mid-execution.
func (w *Worker) call(ctx context.Context, task ledger.Task, handler transport.HandlerFunc, ack func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	_, err = handler(context.WithoutCancel(ctx), task, ack)
	return
}
